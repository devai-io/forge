package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// `forge resume` opens an agent run's session in Claude Code (or Codex) in
// this shell. Claude Code's own /resume picker leaves out sessions that
// began as `claude -p` — every Forge run — so this is how a run becomes one
// of "my Claude Code sessions": pick one of this folder's runs (or name it),
// Forge parks the session if it is still live and records where it went,
// then this process becomes `claude --resume <session>` in the run's folder.

type resumableRun struct {
	ID             int64      `json:"id"`
	Status         string     `json:"status"`
	Awaiting       string     `json:"awaiting"`
	Prompt         string     `json:"prompt"`
	ProjectKey     string     `json:"project_key"`
	RepoName       string     `json:"repo_name"`
	Engine         string     `json:"engine"`
	PermissionMode string     `json:"permission_mode"`
	SessionID      string     `json:"session_id"`
	Dir            string     `json:"dir"`
	CreatedAt      time.Time  `json:"created_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	Handoff        string     `json:"handoff"`
}

// Resume is `forge resume [--list] [RUN]`.
func Resume(cfg *Config, dir string, args []string, in io.Reader, out io.Writer) error {
	c := newClient(cfg.APIURL, cfg.Token)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	list, id := false, int64(0)
	for _, a := range args {
		switch {
		case a == "--list" || a == "-l":
			list = true
		default:
			n, err := strconv.ParseInt(strings.TrimPrefix(a, "#"), 10, 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("usage: forge resume [--list] [RUN-ID]")
			}
			id = n
		}
	}
	if id == 0 {
		var resp struct {
			Runs []resumableRun `json:"runs"`
		}
		if err := c.get(ctx, "/api/runner/runs?path="+url.QueryEscape(dir), &resp); err != nil {
			return err
		}
		if len(resp.Runs) == 0 {
			fmt.Fprintf(out, "No Forge runs with a session in %s on this machine.\n", dir)
			return nil
		}
		for i, r := range resp.Runs {
			fmt.Fprintf(out, "%2d) #%-5d %-10s %s/%s · %s · %s\n", i+1, r.ID, runLabel(r), r.ProjectKey, r.RepoName,
				ago(r.CreatedAt), oneLine(r.Prompt, 70))
		}
		if list {
			return nil
		}
		fmt.Fprint(out, "Resume which? [1] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		pick := 1
		if t := strings.TrimSpace(line); t != "" {
			n, err := strconv.Atoi(t)
			if err != nil || n < 1 || n > len(resp.Runs) {
				return errors.New("no such entry")
			}
			pick = n
		}
		id = resp.Runs[pick-1].ID
	} else if list {
		return errors.New("--list takes no run id")
	}

	var resp struct {
		Run resumableRun `json:"run"`
	}
	if _, err := c.post(ctx, fmt.Sprintf("/api/runner/runs/%d/handoff", id), map[string]any{}, &resp); err != nil {
		return err
	}
	r := resp.Run
	if !resumeID.MatchString(r.SessionID) {
		return fmt.Errorf("run #%d has no session to resume", id)
	}
	runDir, ok := cfg.Allowed(r.Dir)
	if !ok {
		return fmt.Errorf("%s is not inside this machine's allowed_roots", r.Dir)
	}
	bin, argv := cfg.ClaudePath, []string{"claude", "--resume", r.SessionID}
	if r.Engine == "codex" {
		bin, argv = cfg.CodexPath, []string{"codex", "resume", r.SessionID}
	}
	if r.PermissionMode == "bypassPermissions" && contains(cfg.PermissionModes, "bypassPermissions") {
		if r.Engine == "codex" {
			argv = append(argv, "--dangerously-bypass-approvals-and-sandbox")
		} else {
			argv = append(argv, "--dangerously-skip-permissions")
		}
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("%s: %w", bin, err)
	}
	if err := os.Chdir(runDir); err != nil {
		return err
	}
	fmt.Fprintf(out, "Run #%d continues here (Forge no longer runs it): %s in %s\n", id, strings.Join(argv, " "), runDir)
	return syscall.Exec(path, argv, os.Environ())
}

func runLabel(r resumableRun) string {
	if r.Status == "running" && r.Awaiting != "" {
		return "your turn"
	}
	if r.Handoff != "" {
		return "handed off"
	}
	return r.Status
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > n {
		return string([]rune(s)[:n-1]) + "…"
	}
	return s
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
