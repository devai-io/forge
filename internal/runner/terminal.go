package runner

import (
	"context"
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
		"#{session_name}\t#{window_index}\t#{window_name}\t#{window_active}\t#{pane_active}\t#{pane_current_command}\t#{pane_current_path}")
	if err != nil {
		return sessions
	}
	for _, line := range strings.Split(strings.TrimSpace(panes), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			continue
		}
		i, ok := byName[f[0]]
		if !ok {
			continue
		}
		s := &sessions[i]
		idx, _ := strconv.Atoi(f[1])
		claude := isClaude(f[5])
		if claude {
			s.Claude = true
		}
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
		if claude {
			w.Claude = true
		}
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
		HTTPHeader: http.Header{"Authorization": {"Bearer " + r.cfg.Token}, "User-Agent": {"forge-runner/" + Version}},
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
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Channel string          `json:"channel"`
	Session string          `json:"session"`
	Window  *int            `json:"window"`
	Cols    int             `json:"cols"`
	Rows    int             `json:"rows"`
	Lines   int             `json:"lines"`
	Text    string          `json:"text"`
	Enter   bool            `json:"enter"`
	Cwd     string          `json:"cwd"`
	Start   string          `json:"start"`
	Prompt  string          `json:"prompt"`
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
			case "create":
				reply(m.ID, "", r.createSession(ctx, m))
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

// shellQuote single-quotes s for sh/zsh/bash.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (r *Runner) createSession(ctx context.Context, m controlMsg) error {
	if _, err := r.tmux(ctx, "has-session", "-t", "="+m.Session); err == nil {
		return nil // exists: attaching to it is what the caller wants
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
	if _, err := r.tmux(ctx, "new-session", "-d", "-s", m.Session, "-c", dir, "-x", "200", "-y", "50"); err != nil {
		return err
	}
	if m.Start != "claude" {
		return nil
	}
	// Typed into the session's own shell, so the user's rc files (PATH, env)
	// apply — exactly as if they had opened the window themselves. Claude's
	// SessionStart hook then injects the Forge context for this directory.
	line := r.cfg.ClaudePath
	if strings.TrimSpace(m.Prompt) != "" {
		line += " " + shellQuote(m.Prompt)
	}
	time.Sleep(300 * time.Millisecond) // let the shell print its prompt
	return r.sendKeys(ctx, controlMsg{Session: m.Session, Text: line, Enter: true})
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
