package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCLI writes an executable shell script named name into dir.
func fakeCLI(t *testing.T, dir, name, script string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func sessionByCLI(t *testing.T, all []Session, cli string) Session {
	t.Helper()
	for _, s := range all {
		if s.CLI == cli {
			return s
		}
	}
	t.Fatalf("no %s session in %+v", cli, all)
	return Session{}
}

func TestProbeSessionsFakeCLIs(t *testing.T) {
	dir := t.TempDir()
	fakeCLI(t, dir, "claude", `case "$1 $2" in
"--version ") echo "2.1.0 (Claude Code)";;
"auth status") cat <<'J'
{"loggedIn": true, "authMethod": "claude.ai", "email": "someone@example.com", "orgId": "00000000-0000-0000-0000-000000000000", "subscriptionType": "pro"}
J
;;
esac`)
	// Codex logs out loud and exits non-zero, as `codex login status` does.
	fakeCLI(t, dir, "codex", `case "$1 $2" in
"--version ") echo "codex-cli 0.50.0";;
"login status") echo "Not logged in"; exit 1;;
esac`)
	// cursor-agent only answers without a terminal and with CI set; it
	// reads stdin, which must be at EOF rather than waiting for a user.
	fakeCLI(t, dir, "cursor-agent", `case "$1" in
--version) echo "2026.01.28-abc1234";;
status) read x; [ "$CI" = 1 ] && [ ! -t 0 ] && printf '\033[2K\033[G Logged in as someone@example.com\n' || echo "Not logged in";;
esac`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := &Config{ClaudePath: "claude", CodexPath: "codex", CursorPath: "cursor-agent"}
	got := ProbeSessions(context.Background(), cfg)
	if len(got) != 3 {
		t.Fatalf("want 3 sessions, got %+v", got)
	}
	claude := sessionByCLI(t, got, "claude")
	if claude.State != SessionLoggedIn || claude.Version != "2.1.0" || claude.Detail != "claude.ai, pro" {
		t.Errorf("claude: %+v", claude)
	}
	codex := sessionByCLI(t, got, "codex")
	if codex.State != SessionLoggedOut || codex.Version != "0.50.0" {
		t.Errorf("codex: %+v", codex)
	}
	cursor := sessionByCLI(t, got, "cursor")
	if cursor.State != SessionLoggedIn || cursor.Version != "2026.01.28-abc1234" {
		t.Errorf("cursor: %+v", cursor)
	}
	raw, _ := json.Marshal(got)
	for _, secret := range []string{"someone@example.com", "00000000-0000"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("probe result leaks %q: %s", secret, raw)
		}
	}
}

func TestProbeSessionMissingAndLoggedOut(t *testing.T) {
	dir := t.TempDir()
	fakeCLI(t, dir, "claude", `[ "$1" = auth ] && echo '{"loggedIn": false}'; [ "$1" = --version ] && echo 2.0.1`)
	t.Setenv("PATH", dir)
	if s := probeSession(context.Background(), "codex", "codex"); s.State != SessionMissing || s.Version != "" {
		t.Errorf("missing codex: %+v", s)
	}
	if s := probeSession(context.Background(), "claude", "claude"); s.State != SessionLoggedOut {
		t.Errorf("logged-out claude: %+v", s)
	}
	// An absolute path in agent.json works without PATH.
	t.Setenv("PATH", "")
	if s := probeSession(context.Background(), "claude", filepath.Join(dir, "claude")); s.State != SessionLoggedOut || s.Version != "2.0.1" {
		t.Errorf("claude by path: %+v", s)
	}
}

func TestProbeSessionTimeoutKillsHangingStatus(t *testing.T) {
	oldV, oldS := versionTimeout, statusTimeout
	versionTimeout, statusTimeout = 500*time.Millisecond, 500*time.Millisecond
	t.Cleanup(func() { versionTimeout, statusTimeout = oldV, oldS })
	dir := t.TempDir()
	// A status that never returns, with a child holding the output open.
	fakeCLI(t, dir, "cursor-agent", `[ "$1" = --version ] && { echo 1.2.3; exit 0; }
sleep 30 & sleep 30`)
	start := time.Now()
	s := probeSession(context.Background(), "cursor", filepath.Join(dir, "cursor-agent"))
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("probe took %s", took)
	}
	if s.State != SessionUnknown || s.Detail != "status check timed out" || s.Version != "1.2.3" {
		t.Errorf("hanging cursor-agent: %+v", s)
	}
}

func TestTextLogin(t *testing.T) {
	for _, tc := range []struct {
		out   string
		fail  bool
		state string
	}{
		{"Logged in using ChatGPT", false, SessionLoggedIn},
		{" ✓ Login successful!\n Logged in (unable to fetch user details)", false, SessionLoggedIn},
		{"Not logged in", true, SessionLoggedOut},
		{"Not logged in", false, SessionLoggedOut},
		{"Logged in", true, SessionUnknown},
		{"something else", false, SessionUnknown},
	} {
		var err error
		if tc.fail {
			err = os.ErrInvalid
		}
		if got, _ := textLogin(tc.out, err); got != tc.state {
			t.Errorf("textLogin(%q, fail=%t) = %s, want %s", tc.out, tc.fail, got, tc.state)
		}
	}
}

func TestSafeWord(t *testing.T) {
	for in, want := range map[string]string{"claude.ai": "claude.ai", "max": "max", "a@example.com": "", "two words": "", "": ""} {
		if got := safeWord(in); got != want {
			t.Errorf("safeWord(%q) = %q, want %q", in, got, want)
		}
	}
}
