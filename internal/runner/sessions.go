package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Session is one coding-agent CLI on this machine and whether it is logged
// in: what Forge can run agent work on here. Only the state goes to the
// server — never a token, an e-mail address or an account id.
type Session struct {
	// CLI is "claude" (Claude Code), "codex" (OpenAI Codex CLI) or "cursor"
	// (Cursor's cursor-agent).
	CLI string `json:"cli"`
	// State: "logged_in", "logged_out", "unknown" (installed, but the check
	// could not tell) or "missing" (not installed).
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	// Detail is a short, non-identifying note: the auth method and plan
	// Claude Code reports, or why the check could not tell.
	Detail    string    `json:"detail,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

const (
	SessionLoggedIn  = "logged_in"
	SessionLoggedOut = "logged_out"
	SessionUnknown   = "unknown"
	SessionMissing   = "missing"
)

// SessionCLIs are the CLIs probed, in the order they are shown.
var SessionCLIs = []string{"claude", "codex", "cursor"}

// sessionsEvery is how often the probes run; the heartbeat carries the
// last result in between.
const sessionsEvery = 5 * time.Minute

// Probe timeouts: --version is quick everywhere; a status check may call
// home (cursor-agent does).
var (
	versionTimeout = 3 * time.Second
	statusTimeout  = 8 * time.Second
)

const maxProbeOutput = 64 << 10

// sessionPaths are the binaries probed (agent.json claude_path, codex_path,
// cursor_path; each resolves through PATH by default).
func (c *Config) sessionPaths() map[string]string {
	return map[string]string{"claude": c.ClaudePath, "codex": c.CodexPath, "cursor": c.CursorPath}
}

// ProbeSessions checks every CLI, in parallel.
func ProbeSessions(ctx context.Context, cfg *Config) []Session {
	paths := cfg.sessionPaths()
	out := make([]Session, len(SessionCLIs))
	var wg sync.WaitGroup
	for i, cli := range SessionCLIs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = probeSession(ctx, cli, paths[cli])
		}()
	}
	wg.Wait()
	return out
}

func probeSession(ctx context.Context, cli, bin string) Session {
	s := Session{CLI: cli, State: SessionMissing, CheckedAt: time.Now().UTC().Truncate(time.Second)}
	path, err := exec.LookPath(expandHome(bin))
	if err != nil {
		return s
	}
	s.State = SessionUnknown
	if v, _, err := probeRun(ctx, versionTimeout, path, "--version"); err == nil {
		s.Version = parseVersion(v)
	}
	switch cli {
	case "claude":
		s.State, s.Detail = claudeLogin(ctx, path)
	case "codex":
		s.State, s.Detail = statusLogin(ctx, path, "login", "status")
	case "cursor":
		s.State, s.Detail = statusLogin(ctx, path, "status")
	}
	return s
}

// claudeLogin reads `claude auth status` (JSON in Claude Code 2.x). Only
// loggedIn, the auth method and the plan are kept.
func claudeLogin(ctx context.Context, path string) (string, string) {
	out, _, err := probeRun(ctx, statusTimeout, path, "auth", "status")
	var st struct {
		LoggedIn         *bool  `json:"loggedIn"`
		AuthMethod       string `json:"authMethod"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if start := strings.IndexByte(out, '{'); start >= 0 && json.Unmarshal([]byte(out[start:]), &st) == nil && st.LoggedIn != nil {
		if !*st.LoggedIn {
			return SessionLoggedOut, ""
		}
		return SessionLoggedIn, joinDetail(safeWord(st.AuthMethod), safeWord(st.SubscriptionType))
	}
	if err != nil && isTimeout(err) {
		return SessionUnknown, "status check timed out"
	}
	return textLogin(out, err)
}

// statusLogin runs a status command that prints a sentence ("Logged in …",
// "Not logged in").
func statusLogin(ctx context.Context, path string, args ...string) (string, string) {
	out, _, err := probeRun(ctx, statusTimeout, path, args...)
	if err != nil && isTimeout(err) {
		return SessionUnknown, "status check timed out"
	}
	return textLogin(out, err)
}

var (
	loggedOutRe = regexp.MustCompile(`(?i)\b(not logged in|not authenticated|logged out|no credentials|please (log|sign) ?in|login required)\b`)
	loggedInRe  = regexp.MustCompile(`(?i)\b(logged in|authenticated as|signed in)\b`)
)

func textLogin(out string, err error) (string, string) {
	switch {
	case loggedOutRe.MatchString(out):
		return SessionLoggedOut, ""
	case loggedInRe.MatchString(out) && err == nil:
		return SessionLoggedIn, ""
	case err != nil:
		return SessionUnknown, "status check failed"
	}
	return SessionUnknown, "status not recognised"
}

var versionRe = regexp.MustCompile(`\d+\.\d+(\.\d+)?([-.+][0-9A-Za-z.-]+)?`)

func parseVersion(out string) string {
	return versionRe.FindString(firstNonEmptyLine(out))
}

func firstNonEmptyLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

var safeWordRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// safeWord keeps a short token like "claude.ai" or "max"; anything else
// (an address, a sentence) is dropped.
func safeWord(s string) string {
	if strings.Contains(s, "@") || !safeWordRe.MatchString(s) {
		return ""
	}
	return s
}

func joinDetail(parts ...string) string {
	keep := parts[:0]
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, ", ")
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// probeRun runs one check without a terminal: stdin is /dev/null, CI=1 and
// TERM=dumb keep CLIs from prompting or drawing, and the whole process group
// is killed at the timeout (a login helper must not outlive the probe).
func probeRun(ctx context.Context, timeout time.Duration, path string, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "CI=1", "NO_COLOR=1", "TERM=dumb", "FORCE_COLOR=0")
	cmd.Dir = os.TempDir()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	var buf limitedBuffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = errProbeTimeout
	}
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	out := strings.ReplaceAll(ansiRe.ReplaceAllString(buf.String(), ""), "\r", "\n")
	return out, code, err
}

type probeTimeoutError struct{}

func (probeTimeoutError) Error() string { return "timed out" }

var errProbeTimeout error = probeTimeoutError{}

func isTimeout(err error) bool { _, ok := err.(probeTimeoutError); return ok }

type limitedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := maxProbeOutput - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// sessionCache holds the last probe so the heartbeat can carry it.
type sessionCache struct {
	mu   sync.Mutex
	last []Session
	busy bool
}

func (c *sessionCache) get() []Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// refresh probes unless a probe is already running.
func (c *sessionCache) refresh(ctx context.Context, cfg *Config) {
	c.mu.Lock()
	if c.busy {
		c.mu.Unlock()
		return
	}
	c.busy = true
	c.mu.Unlock()
	s := ProbeSessions(ctx, cfg)
	c.mu.Lock()
	c.last, c.busy = s, false
	c.mu.Unlock()
}

// sessionsLoop probes at start and every sessionsEvery.
func (r *Runner) sessionsLoop(ctx context.Context) {
	t := time.NewTicker(sessionsEvery)
	defer t.Stop()
	for {
		r.sessions.refresh(ctx, r.cfg)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
