package runner

// Codex runs locally over app-server's stdio JSON-RPC transport. Credentials
// stay with Codex; Forge never reads or forwards the user's login tokens.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func SetupCodex(configPath string, out io.Writer) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	exe, err := executable()
	if err != nil {
		return err
	}
	args := append([]string{"mcp", "add", "forge", "--env", "FORGE_MCP_AGENT=codex", "--", exe}, agentArgs(cfg.configPath, "mcp")...)
	if output, err := exec.Command(cfg.CodexPath, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("codex mcp add: %w: %s", err, strings.TrimSpace(string(output)))
	}
	fmt.Fprintln(out, "Connected Forge's MCP tools to Codex.")
	// Codex reads Claude-style hooks (same JSON, same additionalContext output),
	// so a standalone Codex session gets the project context and task matching too.
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, _ := os.UserHomeDir()
		home = filepath.Join(userHome, ".codex")
	}
	path := filepath.Join(home, "hooks.json")
	hook := shellJoin(append([]string{exe}, agentArgs(cfg.configPath, "context", "--hook")...))
	if err := addHook(path, "SessionStart", hook, "context --hook"); err != nil {
		return err
	}
	match := shellJoin(append([]string{exe}, agentArgs(cfg.configPath, "match", "--hook")...))
	if err := addHook(path, "UserPromptSubmit", match, "match --hook"); err != nil {
		return err
	}
	fmt.Fprintf(out, "Added the SessionStart (context) and UserPromptSubmit (task matching) hooks to %s.\n", path)
	fmt.Fprintln(out, "Codex runs a new hook only once it is trusted: open codex, type /hooks and trust the two Forge hooks. Run codex login to use your ChatGPT plan, then restart the Forge agent to refresh its capabilities.")
	return nil
}

// codexPolicy deliberately has no approximation for Claude's auto/dontAsk modes.
func codexPolicy(mode string, approvals bool) (string, string, error) {
	policy := "never"
	if approvals {
		policy = "on-request"
	}
	switch mode {
	case "plan":
		return "read-only", "never", nil // no approval may widen a read-only run
	case "acceptEdits":
		return "workspace-write", policy, nil
	case "bypassPermissions":
		return "danger-full-access", "never", nil
	default:
		return "", "", fmt.Errorf("Codex does not support permission mode %q", mode)
	}
}

func (r *Runner) runCodex(ctx context.Context, run claimedRun, dir string, ship *shipper) finishReport {
	if !contains(r.cfg.PermissionModes, run.PermissionMode) {
		return finishReport{Status: "failed", Error: "refused: permission mode is not enabled on this runner"}
	}
	sandbox, policy, err := codexPolicy(run.PermissionMode, run.Interactive && r.cfg.Approvals && run.Assistant == nil)
	if err != nil {
		return finishReport{Status: "failed", Error: err.Error()}
	}
	originalDir := dir
	if run.Worktree && run.ResumeSession == "" {
		dir = filepath.Join(originalDir, ".forge", "worktrees", fmt.Sprintf("run-%d", run.ID))
		// Allowed resolves symlinks; check the existing parent before creating anything.
		if _, ok := r.cfg.Allowed(originalDir); !ok {
			return finishReport{Status: "failed", Error: "worktree outside allowed roots"}
		}
		for _, part := range []string{filepath.Join(originalDir, ".forge"), filepath.Dir(dir)} {
			if err := os.Mkdir(part, 0700); err != nil && !os.IsExist(err) {
				return finishReport{Status: "failed", Error: err.Error()}
			}
			if _, ok := r.cfg.Allowed(part); !ok {
				return finishReport{Status: "failed", Error: "worktree parent outside allowed roots"}
			}
		}
		if _, ok := r.cfg.Allowed(filepath.Dir(dir)); !ok {
			return finishReport{Status: "failed", Error: "worktree outside allowed roots"}
		}
		if _, err := git(ctx, originalDir, "worktree", "add", "-b", fmt.Sprintf("forge-run-%d", run.ID), dir, "HEAD"); err != nil {
			return finishReport{Status: "failed", Error: "create Codex worktree: " + err.Error()}
		}
		ship.send(textEvent("system", "Codex worktree: "+dir+" (kept for review)"))
	}
	exe, err := executable()
	if err != nil {
		return finishReport{Status: "failed", Error: err.Error()}
	}
	readOnlyTools := run.PermissionMode == "plan" && (run.Assistant == nil || !run.Assistant.ForgeWrites)
	quote := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	args := []string{"app-server", "--listen", "stdio://"}
	for _, v := range []string{
		"mcp_servers.forge.command=" + quote(exe),
		"mcp_servers.forge.args=" + quote(agentArgs(r.cfg.configPath, "mcp")),
		"mcp_servers.forge.required=true",
		"mcp_servers.forge.enabled=true",
		"mcp_servers.forge.env.FORGE_MCP_AGENT=\"codex\"",
		"mcp_servers.forge.env.FORGE_MCP_CWD=" + quote(originalDir),
		"mcp_servers.forge.env.FORGE_MCP_READ_ONLY=" + quote(map[bool]string{true: "1", false: "0"}[readOnlyTools]),
	} {
		args = append(args, "-c", v)
	}
	cmd := exec.Command(r.cfg.CodexPath, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return finishReport{Status: "failed", Error: err.Error()}
	}
	sess := newSession(ctx, r, run, ship, stdin)
	defer sess.end()
	c := &codexSession{s: sess, dir: dir, originalDir: originalDir, sandbox: sandbox, policy: policy, model: run.Model}
	sess.message = c.message
	sess.answer = c.answer
	if run.Setup != nil {
		var prev struct {
			ThreadTotal codexTokens `json:"threadTotal"`
		}
		_ = json.Unmarshal(run.Setup.UsageBaseline, &prev)
		c.baseline = prev.ThreadTotal
	}
	c.next.Store(10)
	// Pipe writes can wait until stream starts the child.
	go func() {
		_ = sess.write(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "forge", "title": "Forge", "version": Version}}})
	}()

	ship.send(textEvent("system", "Starting Codex using this machine's Codex login in "+dir))
	start := time.Now()
	code, runErr := r.stream(ctx, cmd, dir, run, ship, c.observe, nil)
	c.mu.Lock()
	defer c.mu.Unlock()
	duration := time.Since(start).Milliseconds()
	rep := finishReport{SessionID: c.thread, Result: c.result, Usage: c.reportUsage(), ExitCode: code, DurationMS: &duration, NumTurns: &c.turns,
		InboxAfter: sess.inboxRead()}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		rep.Status, rep.Error = "cancelled", "cancelled from Forge"
	case ctx.Err() != nil && sess.isWaiting() && c.failure == "":
		rep.Status = "waiting" // the time limit caught it between turns
	case ctx.Err() != nil:
		rep.Status, rep.Error = "failed", "Codex run timed out"
	case runErr != nil:
		rep.Status, rep.Error = "failed", runErr.Error()
	case c.failure != "":
		rep.Status, rep.Error = "failed", c.failure
	case !c.completed:
		rep.Status, rep.Error = "failed", "Codex exited before completing a turn"
	case sess.isParked():
		rep.Status = "waiting"
	case code != nil && *code != 0:
		rep.Status, rep.Error = "failed", fmt.Sprintf("Codex exited with %d", *code)
	default:
		rep.Status = "succeeded"
	}
	return rep
}

type codexSession struct {
	s    *session
	mu   sync.Mutex
	next atomic.Int64

	dir, originalDir       string
	sandbox, policy        string
	thread, turn           string
	model, result, failure string
	instructions           string
	usage                  json.RawMessage
	baseline               codexTokens
	completed              bool
	turns                  int
}

func (c *codexSession) request(method string, params any) error {
	return c.s.write(map[string]any{"id": c.next.Add(1), "method": method, "params": params})
}

func (c *codexSession) message(text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	input := []map[string]string{{"type": "text", "text": text}}
	if c.turn != "" && !c.completed {
		return c.request("turn/steer", map[string]any{"threadId": c.thread, "expectedTurnId": c.turn, "input": input})
	}
	c.completed = false
	c.result = ""
	params := map[string]any{"threadId": c.thread, "input": input}
	if c.s.run.Effort != "" {
		params["effort"] = c.s.run.Effort
	}
	return c.request("turn/start", params)
}

func (c *codexSession) fail(message string) {
	c.mu.Lock()
	c.failure = message
	c.mu.Unlock()
	c.s.closeInput()
}

func (c *codexSession) observe(line []byte) event {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(line, &m) != nil {
		return textEvent("stderr", string(line))
	}
	if m.Error != nil {
		c.fail("Codex: " + m.Error.Message)
		return textEvent("system", "Codex: "+m.Error.Message)
	}
	if len(m.ID) > 0 && m.Method != "" {
		c.onRequest(m.ID, m.Method, m.Params)
		return event{}
	}
	if m.Method == "" {
		switch string(m.ID) {
		case "1":
			_ = c.s.write(map[string]any{"method": "initialized", "params": map[string]any{}})
			// Context injection replaces Claude's SessionStart hook for Forge-launched Codex.
			ctx, cancel := context.WithTimeout(c.s.ctx, 20*time.Second)
			contextText, err := callTool(ctx, c.s.r.client, "forge_context", map[string]any{"cwd": c.originalDir})
			cancel()
			if err != nil {
				c.s.ship.send(textEvent("system", "Forge context unavailable: "+err.Error()))
			}
			c.instructions = "Use Forge's MCP tools for project context, tasks and memory. Call forge_context if context is missing or stale.\n" + contextText
			if c.s.run.PermissionMode == "plan" && (c.s.run.Assistant == nil || !c.s.run.Assistant.ForgeWrites) {
				c.instructions += "\nThis session is read-only: do not change files, tasks, or memory."
			}
			if c.s.run.Assistant != nil {
				c.instructions += "\n" + c.s.run.Assistant.AppendSystem
			}
			sandbox := c.sandbox
			params := map[string]any{"cwd": c.dir, "sandbox": sandbox, "approvalPolicy": c.policy, "approvalsReviewer": "user", "developerInstructions": c.instructions}
			if c.model != "" {
				params["model"] = c.model
			}
			method := "thread/start"
			if c.s.run.ResumeSession != "" {
				method = "thread/resume"
				params["threadId"] = c.s.run.ResumeSession
				// Resume in the thread's existing directory; validate the returned path before starting work.
				delete(params, "cwd")
			}
			_ = c.s.write(map[string]any{"id": 2, "method": method, "params": params})
		case "2":
			var res struct {
				Thread struct {
					ID  string `json:"id"`
					Cwd string `json:"cwd"`
				} `json:"thread"`
				Model string `json:"model"`
			}
			if json.Unmarshal(m.Result, &res) != nil || res.Thread.ID == "" {
				c.fail("Codex returned no thread ID")
				return event{}
			}
			if res.Thread.Cwd != "" {
				if _, ok := c.s.r.cfg.Allowed(res.Thread.Cwd); !ok {
					c.fail("refused: resumed Codex directory is outside allowed roots")
					return event{}
				}
			}
			c.mu.Lock()
			c.thread = res.Thread.ID
			if res.Model != "" {
				c.model = res.Model
			}
			c.mu.Unlock()
			if c.s.run.Resumes == 0 {
				if err := c.message(c.s.run.Prompt); err != nil {
					c.fail(err.Error())
				}
			} else {
				c.completed = true // nothing to finish before the next message
				c.s.ship.send(textEvent("system", "resuming the Codex thread"))
				c.s.idleWait()
			}
			if c.s.run.Interactive && c.s.run.Assistant == nil {
				go c.s.inboxLoop()
				go c.s.awaitLoop()
			}
		}
		return event{} // never forward authentication/configuration responses
	}
	switch m.Method {
	case "serverRequest/resolved":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		key := "codex-" + string(p.RequestID)
		c.s.mu.Lock()
		delete(c.s.pending, key)
		c.s.mu.Unlock()
		go c.s.post("prompts/expire", map[string]string{"request_id": key}, nil)
	case "turn/started":
		var p struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(m.Params, &p)
		c.mu.Lock()
		c.turn = p.Turn.ID
		c.mu.Unlock()
	case "item/completed":
		var p struct {
			Item struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Phase string `json:"phase"`
			} `json:"item"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if p.Item.Type == "agentMessage" {
			c.mu.Lock()
			c.result = p.Item.Text
			c.mu.Unlock()
		}
	case "thread/tokenUsage/updated":
		var p struct {
			TokenUsage struct {
				Total json.RawMessage `json:"total"`
			} `json:"tokenUsage"`
		}
		_ = json.Unmarshal(m.Params, &p)
		c.mu.Lock()
		c.usage = p.TokenUsage.Total
		c.mu.Unlock()
	case "turn/completed":
		var p struct {
			Turn struct {
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(m.Params, &p)
		c.mu.Lock()
		c.completed = p.Turn.Status == "completed"
		c.turns++
		c.turn = ""
		if !c.completed {
			c.failure = "Codex turn " + p.Turn.Status
			if p.Turn.Error != nil {
				c.failure += ": " + p.Turn.Error.Message
			}
		}
		result, failed := c.result, !c.completed
		c.mu.Unlock()
		data, _ := json.Marshal(map[string]any{"type": "result", "result": result, "is_error": failed})
		c.s.ship.send(event{Kind: "codex", Data: data})
		if c.s.run.Interactive && c.s.run.Assistant == nil && !failed {
			c.s.turnDone()
		} else {
			c.s.closeInput()
		}
	}
	// Only conversation events are persisted; account/auth notifications stay local.
	if strings.HasPrefix(m.Method, "item/") || strings.HasPrefix(m.Method, "turn/") || m.Method == "thread/started" {
		return event{Kind: "codex", Data: shrink(line)}
	}
	return event{}
}

func (c *codexSession) onRequest(id json.RawMessage, method string, params json.RawMessage) {
	key := "codex-" + string(id)
	question := method == "item/tool/requestUserInput"
	approval := method == "item/commandExecution/requestApproval" || method == "item/fileChange/requestApproval"
	if !question && !approval {
		// Unsupported requests must receive a response, rather than leaving Codex stuck.
		var result any
		switch method {
		case "item/permissions/requestApproval":
			result = map[string]any{"permissions": map[string]any{}}
		case "mcpServer/elicitation/request":
			result = map[string]any{"action": "decline", "content": nil}
		default:
			_ = c.s.write(map[string]any{"id": id, "error": map[string]any{"code": -32601, "message": "Forge does not support " + method}})
			return
		}
		_ = c.s.write(map[string]any{"id": id, "result": result})
		return
	}
	pending := pendingPrompt{kind: method, input: params, suggestions: id}
	if !c.s.run.Interactive || c.s.run.Assistant != nil || (!question && (!c.s.r.cfg.Approvals || c.s.run.PermissionMode == "plan")) {
		c.answer(key, pending, promptAnswer{Decision: "deny"})
		return
	}
	input := params
	toolName := "Codex command"
	if question {
		toolName = "AskUserQuestion"
	} else if method == "item/fileChange/requestApproval" {
		toolName = "Codex file change"
	}
	c.s.mu.Lock()
	c.s.pending[key] = pending
	c.s.mu.Unlock()
	go func() {
		if err := c.s.post("prompts", map[string]any{"request_id": key, "tool_name": toolName, "input": input, "suggestions": []any{}, "description": "Codex requests your approval or input"}, nil); err != nil {
			c.s.mu.Lock()
			delete(c.s.pending, key)
			c.s.mu.Unlock()
			c.answer(key, pending, promptAnswer{Decision: "deny"})
		}
	}()
}

func (c *codexSession) answer(_ string, p pendingPrompt, a promptAnswer) {
	var result any
	if p.kind == "item/tool/requestUserInput" {
		var params struct {
			Questions []struct {
				ID       string `json:"id"`
				Question string `json:"question"`
			} `json:"questions"`
		}
		_ = json.Unmarshal(p.input, &params)
		answers := map[string]any{}
		if a.Decision == "answer" {
			for _, q := range params.Questions {
				value := a.Answers[q.Question]
				if value == "" {
					value = a.Answers[q.ID]
				}
				if value != "" {
					answers[q.ID] = map[string]any{"answers": []string{value}}
				}
			}
		}
		result = map[string]any{"answers": answers}
	} else {
		decision := "decline"
		if c.s.r.cfg.Approvals && c.s.run.PermissionMode != "plan" {
			switch a.Decision {
			case "allow":
				decision = "accept"
			case "allow_always":
				decision = "acceptForSession"
			}
		}
		result = map[string]string{"decision": decision}
	}
	_ = c.s.write(map[string]any{"id": p.suggestions, "result": result})
	if a.Message != "" && c.s.run.Interactive {
		_ = c.message("The user’s note about the approval request: " + a.Message)
	}
}

// Store per-run usage and the thread total so a resumed run does not bill the
// whole conversation again. No dollar amount is inferred from a CLI login.
type codexTokens struct {
	Input  int64 `json:"inputTokens"`
	Cached int64 `json:"cachedInputTokens"`
	Output int64 `json:"outputTokens"`
}

func (c *codexSession) reportUsage() json.RawMessage {
	if len(c.usage) == 0 {
		return nil
	}
	var total codexTokens
	if json.Unmarshal(c.usage, &total) != nil {
		return nil
	}
	nonnegative := func(v int64) int64 {
		if v < 0 {
			return 0
		}
		return v
	}
	delta := codexTokens{nonnegative(total.Input - c.baseline.Input), nonnegative(total.Cached - c.baseline.Cached), nonnegative(total.Output - c.baseline.Output)}
	raw, _ := json.Marshal(struct {
		codexTokens
		ThreadTotal codexTokens `json:"threadTotal"`
		Model       string      `json:"model"`
	}{delta, total, c.model})
	return raw
}
