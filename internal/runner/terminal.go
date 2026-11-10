package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// Terminals: the runner keeps one control WebSocket to the API and, when the
// browser attaches to a tmux session, dials a second socket for that terminal
// and bridges it to `tmux attach` running in a PTY. Opt-in per machine with
// "terminal": true — a terminal is a shell as this user.

var tmuxName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,40}$`)

type tmuxWindow struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Active  bool   `json:"active"`
	Command string `json:"command"`
	Path    string `json:"path"`
	Claude  bool   `json:"claude"`
	Codex   bool   `json:"codex"`
}

type tmuxSession struct {
	Name       string       `json:"name"`
	Windows    int          `json:"windows"`
	Attached   int          `json:"attached"`
	Created    string       `json:"created"`
	Activity   string       `json:"activity"`
	Path       string       `json:"path"`
	Command    string       `json:"command"`
	Claude     bool         `json:"claude"`
	Codex      bool         `json:"codex"`
	WindowList []tmuxWindow `json:"window_list"`
}

// tmuxEnv makes tmux find the user's server from a systemd/launchd service,
// which does not inherit the login shell's TMUX_TMPDIR (NixOS points it at
// $XDG_RUNTIME_DIR).
func (r *Runner) tmuxEnv() []string {
	env := os.Environ()
	if os.Getenv("TMUX_TMPDIR") == "" {
		dir := r.cfg.TmuxTmpdir
		if dir == "" {
			if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
				if _, err := os.Stat(filepath.Join(xdg, fmt.Sprintf("tmux-%d", os.Getuid()))); err == nil {
					dir = xdg
				}
			}
		}
		if dir != "" {
			env = append(env, "TMUX_TMPDIR="+dir)
		}
	}
	out := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "TMUX=") { // never nest into a tmux the runner was started from
			out = append(out, e)
		}
	}
	return out
}

func (r *Runner) tmux(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.cfg.TmuxPath, args...)
	cmd.Env = r.tmuxEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func isClaude(cmd string) bool { return cmd == "claude" || strings.HasPrefix(cmd, "claude") }

// isCodex: the native binary shows as "codex"; the npm launcher shows as
// "node", so then the pane's processes are looked at (Linux /proc only).
func isCodex(cmd, panePID string) bool {
	if cmd == "codex" || strings.HasPrefix(cmd, "codex-") {
		return true
	}
	if cmd != "node" {
		return false
	}
	pid, err := strconv.Atoi(panePID)
	return err == nil && procTreeRuns(pid, "codex", 4)
}

// procTreeRuns reports whether pid or a descendant (up to depth levels down)
// is the named executable.
func procTreeRuns(pid int, name string, depth int) bool {
	if comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil && strings.TrimSpace(string(comm)) == name {
		return true
	}
	if depth == 0 {
		return false
	}
	// Each thread lists the children it started (node and Go spawn from any).
	tasks, _ := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", pid))
	for _, t := range tasks {
		children, _ := os.ReadFile(t)
		for _, f := range strings.Fields(string(children)) {
			if child, err := strconv.Atoi(f); err == nil && procTreeRuns(child, name, depth-1) {
				return true
			}
		}
	}
	return false
}

func unixTime(s string) string {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n == 0 {
		return ""
	}
	return time.Unix(n, 0).UTC().Format(time.RFC3339)
}

// listTmux reports every session with its windows. No tmux server is not an
// error: it is zero sessions.
func (r *Runner) listTmux(ctx context.Context) []tmuxSession {
	out, err := r.tmux(ctx, "list-sessions", "-F", "#{session_name}\t#{session_windows}\t#{session_attached}\t#{session_created}\t#{session_activity}")
	if err != nil {
		return []tmuxSession{}
	}
	sessions := []tmuxSession{}
	byName := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			continue
		}
		win, _ := strconv.Atoi(f[1])
		att, _ := strconv.Atoi(f[2])
		byName[f[0]] = len(sessions)
		sessions = append(sessions, tmuxSession{Name: f[0], Windows: win, Attached: att, Created: unixTime(f[3]),
			Activity: unixTime(f[4]), WindowList: []tmuxWindow{}})
	}
	panes, err := r.tmux(ctx, "list-panes", "-a", "-F",
		"#{session_name}\t#{window_index}\t#{window_name}\t#{window_active}\t#{pane_active}\t#{pane_current_command}\t#{pane_current_path}\t#{pane_pid}")
	if err != nil {
		return sessions
	}
	for _, line := range strings.Split(strings.TrimSpace(panes), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 8 {
			continue
		}
		i, ok := byName[f[0]]
		if !ok {
			continue
		}
		s := &sessions[i]
		idx, _ := strconv.Atoi(f[1])
		claude, codex := isClaude(f[5]), isCodex(f[5], f[7])
		s.Claude = s.Claude || claude
		s.Codex = s.Codex || codex
		// One entry per window, described by its active pane (a window with a
		// claude pane anywhere counts as a Claude window).
		var w *tmuxWindow
		for j := range s.WindowList {
			if s.WindowList[j].Index == idx {
				w = &s.WindowList[j]
			}
		}
		if w == nil {
			s.WindowList = append(s.WindowList, tmuxWindow{Index: idx, Name: f[2], Active: f[3] == "1"})
			w = &s.WindowList[len(s.WindowList)-1]
		}
		w.Claude = w.Claude || claude
		w.Codex = w.Codex || codex
		if f[4] == "1" {
			w.Command, w.Path = f[5], f[6]
			if w.Active {
				s.Command, s.Path = f[5], f[6]
			}
		}
	}
	return sessions
}

// ── Control connection ────────────────────────────────────────────────────

func wsURL(api, path string) string {
	u := strings.Replace(api, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1)
	return u + path
}

func (r *Runner) dial(ctx context.Context, path string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(r.cfg.APIURL, path), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + r.cfg.Token}, "User-Agent": {"forge-agent/" + Version}},
	})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(4 << 20)
	return c, nil
}

func (r *Runner) controlLoop(ctx context.Context) {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		c, err := r.dial(ctx, "/api/runner/control")
		if err != nil {
			slog.Warn("terminal control connect failed", "err", err)
			sleep(ctx, backoff)
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = 2 * time.Second
		slog.Info("terminal control connected")
		r.serveControl(ctx, c)
		slog.Warn("terminal control disconnected")
		sleep(ctx, 2*time.Second)
	}
}

type controlMsg struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Channel string `json:"channel"`
	Session string `json:"session"`
	Window  *int   `json:"window"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
	Lines   int    `json:"lines"`
	Text    string `json:"text"`
	Enter   bool   `json:"enter"`
	Cwd     string `json:"cwd"`
	Start   string `json:"start"`
	Prompt  string `json:"prompt"`
	// SkipPermissions starts Claude with --dangerously-skip-permissions.
	SkipPermissions bool `json:"skip_permissions"`
	// Resume starts Claude with --resume <session> (Codex: `codex resume`),
	// in Cwd: an agent run's session, taken over in a terminal.
	Resume string `json:"resume"`
	// RemoteControl starts Claude with --remote-control <name>: the session
	// can be driven from claude.ai/code and the Claude app under that name.
	RemoteControl string `json:"remote_control"`
	// An agent run in tmux (api/tmux_agents.go): SessionID starts a new
	// Claude Code session under that id (--session-id); PermissionMode,
	// Model, Effort and Worktree are claude's flags; Env is exported for the
	// session (FORGE_RUN_ID, which its hooks report). Reuse types the command
	// into the session when it already exists (its Claude has exited).
	SessionID      string            `json:"session_id"`
	PermissionMode string            `json:"permission_mode"`
	Model          string            `json:"model"`
	Effort         string            `json:"effort"`
	Worktree       string            `json:"worktree"`
	Env            map[string]string `json:"env"`
	Reuse          bool              `json:"reuse"`
	// SSH is the destination a Start "ssh" session connects to.
	SSH     string          `json:"ssh"`
	Name    string          `json:"name"`
	Folder  string          `json:"folder"`
	Folders json.RawMessage `json:"folders"`
}

func (r *Runner) serveControl(ctx context.Context, c *websocket.Conn) {
	defer c.CloseNow()
	var writeMu sync.Mutex
	reply := func(id string, text string, err error) {
		res := map[string]any{"type": "result", "id": id, "ok": err == nil, "text": text}
		if err != nil {
			res["error"] = err.Error()
		}
		raw, _ := json.Marshal(res)
		writeMu.Lock()
		defer writeMu.Unlock()
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = c.Write(wctx, websocket.MessageText, raw)
	}
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var m controlMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.Session != "" && !tmuxName.MatchString(m.Session) {
			reply(m.ID, "", errors.New("invalid session name"))
			continue
		}
		go func(m controlMsg) {
			switch m.Type {
			case "open":
				r.openTerminal(ctx, m)
			case "capture":
				text, err := r.capture(ctx, m)
				reply(m.ID, text, err)
			case "keys":
				reply(m.ID, "", r.sendKeys(ctx, m))
			case "paste":
				reply(m.ID, "", r.paste(ctx, m))
			case "create":
				reply(m.ID, "", r.createSession(ctx, m))
			case "rc_url":
				u, err := r.remoteControlURL(ctx, m)
				reply(m.ID, u, err)
			case "code_folder":
				p, err := r.codeFolder(m.Folder)
				reply(m.ID, p, err)
			case "code_workspace":
				p, err := r.codeWorkspace(m.Name, m.Folders)
				reply(m.ID, p, err)
			case "kill":
				_, err := r.tmux(ctx, "kill-session", "-t", "="+m.Session)
				reply(m.ID, "", err)
			default:
				reply(m.ID, "", fmt.Errorf("unknown request %q", m.Type))
			}
		}(m)
	}
}

// target is the pane a request aims at: the session's current window, or a
// given window.
func target(m controlMsg) string {
	t := "=" + m.Session + ":"
	if m.Window != nil {
		t += strconv.Itoa(*m.Window)
	}
	return t
}

func (r *Runner) capture(ctx context.Context, m controlMsg) (string, error) {
	lines := m.Lines
	if lines <= 0 || lines > 400 {
		lines = 60
	}
	out, err := r.tmux(ctx, "capture-pane", "-p", "-J", "-t", target(m), "-S", "-"+strconv.Itoa(lines))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n"), nil
}

func (r *Runner) sendKeys(ctx context.Context, m controlMsg) error {
	if m.Text != "" {
		if _, err := r.tmux(ctx, "send-keys", "-t", target(m), "-l", m.Text); err != nil {
			return err
		}
	}
	if m.Enter {
		_, err := r.tmux(ctx, "send-keys", "-t", target(m), "Enter")
		return err
	}
	return nil
}

// sshTargetRe matches the server's check: user@host, an address or an
// ssh-config alias; no spaces and no leading dash, so never an option.
var sshTargetRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@:\[\]-]*$`)

func validSSHTarget(t string) bool { return len(t) <= 200 && sshTargetRe.MatchString(t) }

// resumeID is a Claude Code session id or a Codex thread id: it goes into a
// shell line, so nothing else passes.
var resumeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{7,63}$`)

// shellQuote single-quotes s for sh/zsh/bash.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (r *Runner) createSession(ctx context.Context, m controlMsg) error {
	exists := false
	if _, err := r.tmux(ctx, "has-session", "-t", "="+m.Session); err == nil {
		if !m.Reuse || m.Start != "claude" {
			return nil // exists: attaching to it is what the caller wants
		}
		exists = true // an agent's session whose Claude exited: start it again in there
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if m.Cwd != "" {
		resolved, ok := r.cfg.Allowed(m.Cwd)
		if !ok {
			return fmt.Errorf("%s is not inside this runner's allowed_roots", m.Cwd)
		}
		dir = resolved
	}
	if m.Start == "ssh" && !validSSHTarget(m.SSH) {
		return errors.New("invalid ssh target")
	}
	agent := m.Start == "claude" || m.Start == "codex"
	if m.SkipPermissions && (!agent || !contains(r.cfg.PermissionModes, "bypassPermissions")) {
		return errors.New("skipping permissions is not enabled on this runner (bypassPermissions in its permission_modes)")
	}
	if !exists {
		if _, err := r.tmux(ctx, "new-session", "-d", "-s", m.Session, "-c", dir, "-x", "200", "-y", "50"); err != nil {
			return err
		}
	}
	if m.Start == "ssh" {
		time.Sleep(300 * time.Millisecond) // let the shell print its prompt
		return r.sendKeys(ctx, controlMsg{Session: m.Session, Text: "ssh " + shellQuote(m.SSH), Enter: true})
	}
	if !agent {
		return nil
	}
	// Typed into the session's own shell, so the user's rc files (PATH, env)
	// apply — exactly as if they had opened the window themselves. The
	// SessionStart hook (Claude's, or Codex's after setup-codex) then injects
	// the Forge context for this directory.
	if m.Resume != "" && !resumeID.MatchString(m.Resume) {
		return errors.New("invalid session id")
	}
	var line string
	if m.Start == "codex" {
		line = r.cfg.CodexPath
		if m.Resume != "" {
			line += " resume " + m.Resume
		}
		if m.SkipPermissions {
			line += " --dangerously-bypass-approvals-and-sandbox"
		}
		if strings.TrimSpace(m.Prompt) != "" && m.Resume == "" {
			line += " " + shellQuote(m.Prompt)
		}
	} else {
		var err error
		if line, err = r.claudeLine(m, dir); err != nil {
			return err
		}
	}
	if exists {
		line = "cd " + shellQuote(dir) + " && " + line
	}
	time.Sleep(300 * time.Millisecond) // let the shell print its prompt
	if err := r.sendKeys(ctx, controlMsg{Session: m.Session, Text: line, Enter: true}); err != nil {
		return err
	}
	if m.Start == "claude" && (m.SessionID != "" || len(m.Env) > 0) {
		// An agent run nobody is watching: answer Claude Code's first-run
		// questions the way its headless runs never have to.
		go r.answerStartup(context.WithoutCancel(ctx), m.Session, m.PermissionMode == "bypassPermissions" || m.SkipPermissions)
	}
	return nil
}

// startupPrompts are the questions interactive Claude Code may ask before it
// takes a prompt, and the option that lets the run go on: trusting the
// folder (already inside this machine's allowed_roots, where headless runs
// work without asking), and accepting --dangerously-skip-permissions (only
// when this machine's permission_modes allow bypassPermissions).
var startupPrompts = []struct {
	marker string
	bypass bool
}{
	{marker: "Yes, I trust this folder"},
	{marker: "Yes, I accept", bypass: true},
}

// answerStartup watches a just-started agent session for 20 seconds and
// picks the "yes" option (the second entry: Down, Enter) of each startup
// question it shows.
func (r *Runner) answerStartup(ctx context.Context, session string, bypass bool) {
	m := controlMsg{Session: session}
	answered := map[string]bool{}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		pane, err := r.capture(ctx, controlMsg{Session: session, Lines: 40})
		if err != nil {
			return
		}
		for _, p := range startupPrompts {
			// Only a dialog (it says how to confirm), never the same words in the conversation.
			if answered[p.marker] || !strings.Contains(pane, p.marker) || !strings.Contains(pane, "Enter to confirm") ||
				(p.bypass && !(bypass && contains(r.cfg.PermissionModes, "bypassPermissions"))) {
				continue
			}
			answered[p.marker] = true
			_, _ = r.tmux(ctx, "send-keys", "-t", target(m), "Down")
			time.Sleep(150 * time.Millisecond)
			_, _ = r.tmux(ctx, "send-keys", "-t", target(m), "Enter")
			slog.Info("answered a Claude Code startup question", "session", session, "answer", p.marker)
		}
	}
}

// envName is a variable name Forge may export into a session's command.
var envName = regexp.MustCompile(`^FORGE_[A-Z0-9_]{1,40}$`)

// modelName is what --model may carry (an alias or a model id).
var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,79}$`)

// maxInlinePrompt: a longer prompt goes to a file the session is pointed at,
// rather than through the shell (typed keys, `ps`).
const maxInlinePrompt = 2000

// claudeLine is the shell line that starts Claude Code in a terminal: a
// plain session, a resumed one, or an agent run's (with its own session id,
// permission mode, model, effort, worktree and first prompt). Everything the
// server sends is checked against this machine's config before it becomes a
// flag, the way a headless run's is.
func (r *Runner) claudeLine(m controlMsg, dir string) (string, error) {
	var b strings.Builder
	keys := make([]string, 0, len(m.Env))
	for k := range m.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !envName.MatchString(k) {
			return "", fmt.Errorf("invalid variable %q", k)
		}
		b.WriteString(k + "=" + shellQuote(m.Env[k]) + " ")
	}
	b.WriteString(r.cfg.ClaudePath)
	switch {
	case m.Resume != "":
		b.WriteString(" --resume " + m.Resume)
	case m.SessionID != "":
		if !resumeID.MatchString(m.SessionID) {
			return "", errors.New("invalid session id")
		}
		b.WriteString(" --session-id " + m.SessionID)
	}
	if m.PermissionMode != "" {
		if !contains(r.cfg.PermissionModes, m.PermissionMode) {
			return "", fmt.Errorf("permission mode %q is not enabled on this runner", m.PermissionMode)
		}
		b.WriteString(" " + strings.Join(permissionArgs(m.PermissionMode), " "))
	} else if m.SkipPermissions {
		b.WriteString(" --dangerously-skip-permissions")
	}
	if m.Model != "" {
		if !modelName.MatchString(m.Model) {
			return "", errors.New("invalid model")
		}
		b.WriteString(" --model " + shellQuote(m.Model))
	}
	if m.Effort != "" {
		if !contains(claudeEfforts, m.Effort) {
			return "", errors.New("invalid effort")
		}
		b.WriteString(" --effort " + m.Effort)
	}
	if m.Worktree != "" {
		if !tmuxName.MatchString(m.Worktree) {
			return "", errors.New("invalid worktree name")
		}
		b.WriteString(" --worktree " + m.Worktree)
	}
	if m.RemoteControl != "" {
		b.WriteString(" --remote-control " + shellQuote(m.RemoteControl))
	}
	if prompt := strings.TrimSpace(m.Prompt); prompt != "" {
		if len(prompt) > maxInlinePrompt {
			path, err := writePromptFile(m.Session, prompt)
			if err != nil {
				return "", err
			}
			prompt = "Your request is in " + path + " — read it and do what it says."
		}
		b.WriteString(" " + shellQuote(prompt))
	}
	return b.String(), nil
}

// writePromptFile keeps a long prompt in the user cache dir for the session
// to read (0600: it may say things that are nobody else's business).
func writePromptFile(session, prompt string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "forge", "prompts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.md", session, time.Now().UnixNano()))
	return path, os.WriteFile(path, []byte(prompt), 0o600)
}

func randomName() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// paste puts text into a session's pane as one bracketed paste (a message
// with line breaks stays one message in Claude Code), then presses Enter.
func (r *Runner) paste(ctx context.Context, m controlMsg) error {
	if m.Text == "" {
		return errors.New("nothing to paste")
	}
	buf := "forge-" + randomName()
	load := exec.CommandContext(ctx, r.cfg.TmuxPath, "load-buffer", "-b", buf, "-")
	load.Env = r.tmuxEnv()
	load.Stdin = strings.NewReader(m.Text)
	if out, err := load.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux load-buffer: %s", strings.TrimSpace(string(out)))
	}
	if _, err := r.tmux(ctx, "paste-buffer", "-p", "-d", "-b", buf, "-t", target(m)); err != nil {
		return err
	}
	if !m.Enter {
		return nil
	}
	time.Sleep(150 * time.Millisecond) // let the TUI take the paste before the key that sends it
	_, err := r.tmux(ctx, "send-keys", "-t", target(m), "Enter")
	return err
}

// rcURL is the session link Claude Code prints once Remote Control is on.
var rcURL = regexp.MustCompile(`https://claude\.ai/code/session_[A-Za-z0-9]+`)

// remoteControlURL finds the Remote Control link in the pane's scrollback:
// the first one, which Claude Code prints under its banner, not one that came
// up later in the conversation ("" while there is none yet).
func (r *Runner) remoteControlURL(ctx context.Context, m controlMsg) (string, error) {
	out, err := r.tmux(ctx, "capture-pane", "-p", "-J", "-t", target(m), "-S", "-", "-E", "-")
	if err != nil {
		return "", err
	}
	return rcURL.FindString(out), nil
}

// ── One terminal ──────────────────────────────────────────────────────────

func (r *Runner) openTerminal(ctx context.Context, m controlMsg) {
	c, err := r.dial(ctx, "/api/runner/tty/"+m.Channel)
	if err != nil {
		slog.Warn("terminal dial failed", "session", m.Session, "err", err)
		return
	}
	defer c.CloseNow()
	cols, rows := m.Cols, m.Rows
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	args := []string{"attach-session", "-t", "=" + m.Session}
	if m.Window != nil {
		args = []string{"attach-session", "-t", target(m)}
	}
	cmd := exec.Command(r.cfg.TmuxPath, args...)
	cmd.Env = append(r.tmuxEnv(), "TERM=xterm-256color", "COLORTERM=truecolor")
	if !hasLocale(cmd.Env) {
		cmd.Env = append(cmd.Env, "LANG=C.UTF-8")
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		msg, _ := json.Marshal(map[string]string{"type": "exit", "reason": err.Error()})
		_ = c.Write(ctx, websocket.MessageText, msg)
		return
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill() // detaches the client; the tmux session lives on
		}
		_ = cmd.Wait()
	}()
	slog.Info("terminal opened", "session", m.Session)

	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// pty → socket
	go func() {
		defer cancel()
		buf := make([]byte, 32<<10)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				wctx, wc := context.WithTimeout(tctx, 30*time.Second)
				werr := c.Write(wctx, websocket.MessageBinary, buf[:n])
				wc()
				if werr != nil {
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					slog.Debug("pty read", "err", err)
				}
				msg, _ := json.Marshal(map[string]string{"type": "exit", "reason": "the tmux client exited"})
				_ = c.Write(context.Background(), websocket.MessageText, msg)
				return
			}
		}
	}()
	// socket → pty
	for {
		typ, data, err := c.Read(tctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			if _, err := ptmx.Write(data); err != nil {
				return
			}
			continue
		}
		var ctl struct {
			Type string `json:"type"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}
		if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" && ctl.Cols > 0 && ctl.Rows > 0 {
			_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(ctl.Cols), Rows: uint16(ctl.Rows)})
		}
	}
}

func hasLocale(env []string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, "LANG=") || strings.HasPrefix(e, "LC_ALL=") {
			return true
		}
	}
	return false
}
