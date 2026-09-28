package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// claimedRun is the part of the server's Run the runner acts on.
type claimedRun struct {
	ID             int64   `json:"id"`
	Kind           string  `json:"kind"`
	Prompt         string  `json:"prompt"`
	Command        string  `json:"command"`
	PermissionMode string  `json:"permission_mode"`
	Model          string  `json:"model"`
	Worktree       bool    `json:"worktree"`
	RepoPath       string  `json:"repo_path"`
	RepoName       string  `json:"repo_name"`
	ProjectKey     string  `json:"project_key"`
	TaskRef        *string `json:"task_ref"`
	ResumeSession  string  `json:"resume_session"`
}

type event struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

func textEvent(kind, text string) event {
	data, _ := json.Marshal(map[string]string{"text": text})
	return event{Kind: kind, Data: data}
}

type finishReport struct {
	Status     string   `json:"status"`
	ExitCode   *int     `json:"exit_code"`
	SessionID  string   `json:"session_id"`
	Result     string   `json:"result"`
	Error      string   `json:"error"`
	CostUSD    *float64 `json:"cost_usd"`
	NumTurns   *int     `json:"num_turns"`
	DurationMS *int64   `json:"duration_ms"`
}

// shipper batches a run's events to the server: every 400 ms or 100 events,
// whichever comes first, so a chatty tool does not become 100 requests a
// second and a quiet one still streams promptly.
type shipper struct {
	c     *client
	runID int64
	in    chan event
	done  chan struct{}
}

func newShipper(c *client, runID int64) *shipper {
	s := &shipper{c: c, runID: runID, in: make(chan event, 1024), done: make(chan struct{})}
	go s.loop()
	return s
}

func (s *shipper) send(e event) { s.in <- e }

func (s *shipper) close() {
	close(s.in)
	<-s.done
}

func (s *shipper) loop() {
	defer close(s.done)
	var batch []event
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		body := map[string]any{"events": batch}
		for attempt := 0; attempt < 5; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			_, err := s.c.post(ctx, fmt.Sprintf("/api/runner/runs/%d/events", s.runID), body, nil)
			cancel()
			if err == nil {
				break
			}
			var ae *apiError
			if errors.As(err, &ae) && ae.status < 500 {
				slog.Error("events rejected", "run", s.runID, "err", err)
				break
			}
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
		batch = batch[:0]
	}
	for {
		select {
		case e, ok := <-s.in:
			if !ok {
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= 100 {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}

// execute runs one claimed run to completion and returns what to report.
func (r *Runner) execute(ctx context.Context, run claimedRun) finishReport {
	ship := newShipper(r.client, run.ID)
	defer ship.close()

	dir, ok := r.cfg.Allowed(run.RepoPath)
	if !ok {
		msg := fmt.Sprintf("refused: %s is not inside this runner's allowed_roots or does not exist here", run.RepoPath)
		ship.send(textEvent("system", msg))
		return finishReport{Status: "failed", Error: msg}
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.cfg.MaxRunMinutes)*time.Minute)
	defer cancel()

	switch run.Kind {
	case "agent":
		return r.runClaude(ctx, run, dir, ship)
	case "command":
		return r.runCommand(ctx, run, dir, ship)
	}
	return finishReport{Status: "failed", Error: "unknown run kind " + run.Kind}
}

func (r *Runner) runClaude(ctx context.Context, run claimedRun, dir string, ship *shipper) finishReport {
	if !contains(r.cfg.PermissionModes, run.PermissionMode) {
		msg := fmt.Sprintf("refused: permission mode %q is not enabled on this runner", run.PermissionMode)
		ship.send(textEvent("system", msg))
		return finishReport{Status: "failed", Error: msg}
	}
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", run.PermissionMode}
	if run.Model != "" {
		args = append(args, "--model", run.Model)
	}
	if run.ResumeSession != "" {
		args = append(args, "--resume", run.ResumeSession)
	}
	if run.Worktree {
		args = append(args, "--worktree", fmt.Sprintf("forge-run-%d", run.ID))
	}
	args = append(args, r.cfg.ExtraArgs...)

	cmd := exec.Command(r.cfg.ClaudePath, args...)
	cmd.Stdin = strings.NewReader(run.Prompt) // stdin, not argv: no length limit, not in `ps`
	ship.send(textEvent("system", fmt.Sprintf("%s on %s: claude %s in %s",
		r.hostname, run.ProjectKey, strings.Join(args, " "), dir)))

	var res claudeResult
	code, runErr := r.stream(ctx, cmd, dir, run, ship, func(line []byte) event {
		res.observe(line)
		return event{Kind: "claude", Data: shrink(line)}
	}, nil)

	rep := finishReport{ExitCode: code, SessionID: res.SessionID, Result: res.Result,
		CostUSD: res.CostUSD, NumTurns: res.NumTurns, DurationMS: res.DurationMS}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		rep.Status, rep.Error = "cancelled", "cancelled from Forge"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		rep.Status, rep.Error = "failed", fmt.Sprintf("timed out after %d minutes", r.cfg.MaxRunMinutes)
	case runErr != nil:
		rep.Status, rep.Error = "failed", runErr.Error()
	case res.IsError || (code != nil && *code != 0):
		rep.Status = "failed"
		rep.Error = res.Subtype
		if rep.Error == "" {
			rep.Error = fmt.Sprintf("claude exited with %d", derefInt(code))
		}
	default:
		rep.Status = "succeeded"
	}
	return rep
}

func (r *Runner) runCommand(ctx context.Context, run claimedRun, dir string, ship *shipper) finishReport {
	command, ok := r.cfg.Commands[run.Command]
	if !ok {
		msg := fmt.Sprintf("refused: no command %q in this runner's config", run.Command)
		ship.send(textEvent("system", msg))
		return finishReport{Status: "failed", Error: msg}
	}
	if len(command.Repos) > 0 && !contains(command.Repos, run.RepoName) {
		msg := fmt.Sprintf("refused: %q only runs in %s", run.Command, strings.Join(command.Repos, ", "))
		ship.send(textEvent("system", msg))
		return finishReport{Status: "failed", Error: msg}
	}
	line := command.Run
	ship.send(textEvent("system", fmt.Sprintf("%s: %s $ %s", r.hostname, dir, line)))
	cmd := exec.Command("sh", "-c", line)

	// Both streams feed the tail, under one lock: they are read concurrently.
	var tail tailBuffer
	var mu sync.Mutex
	keep := func(b []byte) { mu.Lock(); tail.add(string(b)); mu.Unlock() }
	code, runErr := r.stream(ctx, cmd, dir, run, ship, func(b []byte) event {
		keep(b)
		return textEvent("stdout", string(b))
	}, keep)
	rep := finishReport{ExitCode: code}
	if out := tail.String(); out != "" {
		rep.Result = "```\n" + out + "\n```"
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		rep.Status, rep.Error = "cancelled", "cancelled from Forge"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		rep.Status, rep.Error = "failed", fmt.Sprintf("timed out after %d minutes", r.cfg.MaxRunMinutes)
	case runErr != nil:
		rep.Status, rep.Error = "failed", runErr.Error()
	case code != nil && *code != 0:
		rep.Status, rep.Error = "failed", fmt.Sprintf("exit code %d", *code)
	default:
		rep.Status = "succeeded"
	}
	return rep
}

// stream runs cmd in its own process group (so cancelling kills whatever it
// spawned too), turning each stdout line into an event via onLine and each
// stderr line into a stderr event.
func (r *Runner) stream(ctx context.Context, cmd *exec.Cmd, dir string, run claimedRun, ship *shipper,
	onLine func([]byte) event, onErr func([]byte)) (*int, error) {
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FORGE_RUN_ID="+fmt.Sprint(run.ID), "FORGE_PROJECT="+run.ProjectKey,
		"FORGE_REPO="+run.RepoName, "GIT_TERMINAL_PROMPT=0")
	for k, v := range r.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		ship.send(textEvent("system", "could not start: "+err.Error()))
		return nil, err
	}

	// Kill the whole group on cancel: SIGTERM, then SIGKILL after 10 s.
	stopKill := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			pgid := -cmd.Process.Pid
			_ = syscall.Kill(pgid, syscall.SIGTERM)
			select {
			case <-time.After(10 * time.Second):
				_ = syscall.Kill(pgid, syscall.SIGKILL)
			case <-stopKill:
			}
		case <-stopKill:
		}
	}()
	defer close(stopKill)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		readLines(stdout, func(b []byte) { ship.send(onLine(b)) })
	}()
	go func() {
		defer wg.Done()
		readLines(stderr, func(b []byte) {
			if onErr != nil {
				onErr(b)
			}
			ship.send(textEvent("stderr", string(b)))
		})
	}()
	wg.Wait()

	err = cmd.Wait()
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		if code >= 0 {
			return &code, nil
		}
	}
	return nil, err
}

func readLines(r io.Reader, fn func([]byte)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 32<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		fn(cp)
	}
	if err := sc.Err(); err != nil {
		fn([]byte("[forge-runner: output read error: " + err.Error() + "]"))
		_, _ = io.Copy(io.Discard, r)
	}
}

// claudeResult accumulates what the stream says about the session.
type claudeResult struct {
	SessionID  string
	Result     string
	Subtype    string
	IsError    bool
	CostUSD    *float64
	NumTurns   *int
	DurationMS *int64
}

func (c *claudeResult) observe(line []byte) {
	var m struct {
		Type         string   `json:"type"`
		Subtype      string   `json:"subtype"`
		SessionID    string   `json:"session_id"`
		Result       string   `json:"result"`
		IsError      bool     `json:"is_error"`
		TotalCostUSD *float64 `json:"total_cost_usd"`
		NumTurns     *int     `json:"num_turns"`
		DurationMS   *int64   `json:"duration_ms"`
	}
	if json.Unmarshal(line, &m) != nil {
		return
	}
	if m.SessionID != "" {
		c.SessionID = m.SessionID
	}
	if m.Type == "result" {
		c.Result, c.Subtype, c.IsError = m.Result, m.Subtype, m.IsError
		c.CostUSD, c.NumTurns, c.DurationMS = m.TotalCostUSD, m.NumTurns, m.DurationMS
		if c.Subtype == "success" {
			c.Subtype = ""
		}
	}
}

// shrink keeps an event under the server's size cap by truncating long
// strings inside it (a tool result holding a whole file, usually) while
// keeping it valid JSON. Non-JSON output becomes a text event.
func shrink(line []byte) json.RawMessage {
	if !json.Valid(line) {
		data, _ := json.Marshal(map[string]string{"text": string(line)})
		return data
	}
	if len(line) <= 96<<10 {
		return line
	}
	var v any
	if json.Unmarshal(line, &v) != nil {
		return line
	}
	out, err := json.Marshal(truncateStrings(v, 8<<10))
	if err != nil {
		return line
	}
	return out
}

func truncateStrings(v any, max int) any {
	switch x := v.(type) {
	case string:
		if len(x) > max {
			return x[:max] + fmt.Sprintf("… [%d more bytes truncated by forge-runner]", len(x)-max)
		}
		return x
	case []any:
		for i := range x {
			x[i] = truncateStrings(x[i], max)
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = truncateStrings(x[k], max)
		}
		return x
	}
	return v
}

// tailBuffer keeps the last 40 lines of a command's output for its result.
type tailBuffer struct{ lines []string }

func (t *tailBuffer) add(s string) {
	t.lines = append(t.lines, s)
	if len(t.lines) > 40 {
		t.lines = t.lines[len(t.lines)-40:]
	}
}

func (t *tailBuffer) String() string { return strings.Join(t.lines, "\n") }

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func derefInt(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}
