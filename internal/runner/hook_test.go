package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeLineForAgentRuns(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	up := " --add-dir '" + filepath.Join(cache, "forge", "uploads") + "'"
	r := &Runner{cfg: &Config{ClaudePath: "claude", PermissionModes: []string{"plan", "acceptEdits"}}}
	line, err := r.claudeLine(controlMsg{Session: "SHOP-12", SessionID: "0b6e3f4a-1c2d-4e5f-8a9b-0c1d2e3f4a5b",
		PermissionMode: "plan", Model: "opus", Effort: "high", Worktree: "forge-run-7", Prompt: "Fix the cart's rounding",
		Env: map[string]string{"FORGE_RUN_ID": "7"}}, "/srv/shop")
	if err != nil {
		t.Fatal(err)
	}
	want := "FORGE_RUN_ID='7' claude" + up + " --session-id 0b6e3f4a-1c2d-4e5f-8a9b-0c1d2e3f4a5b --permission-mode plan --model 'opus' --effort high --worktree forge-run-7 -- 'Fix the cart'\\''s rounding'"
	if line != want {
		t.Fatalf("line =\n%s\nwant\n%s", line, want)
	}
	if line, _ = r.claudeLine(controlMsg{Session: "SHOP-12", Resume: "0b6e3f4a-1c2d-4e5f-8a9b-0c1d2e3f4a5b", Prompt: "carry on"}, "/srv"); line != "claude"+up+" --resume 0b6e3f4a-1c2d-4e5f-8a9b-0c1d2e3f4a5b -- 'carry on'" {
		t.Fatalf("resume line = %s", line)
	}
	// What the machine does not allow never becomes a flag.
	for name, m := range map[string]controlMsg{
		"mode":    {PermissionMode: "bypassPermissions"},
		"env":     {Env: map[string]string{"PATH": "/tmp"}},
		"model":   {Model: "x; rm -rf ~"},
		"effort":  {Effort: "ludicrous"},
		"session": {SessionID: "--help"},
	} {
		if _, err := r.claudeLine(m, "/srv"); err == nil {
			t.Errorf("%s: accepted %+v", name, m)
		}
	}
}

func TestClaudeLineLongPromptGoesToAFile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	r := &Runner{cfg: &Config{ClaudePath: "claude"}}
	long := strings.Repeat("do the thing. ", 400)
	line, err := r.claudeLine(controlMsg{Session: "SHOP-1", Prompt: long}, "/srv")
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(line, "Your request is in ")
	if i < 0 || strings.Contains(line, "do the thing") {
		t.Fatalf("line = %.200s", line)
	}
	path := strings.Fields(line[i+len("Your request is in "):])[0]
	if b, err := os.ReadFile(path); err != nil || string(b) != strings.TrimSpace(long) {
		t.Fatalf("prompt file %s: %v", path, err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("prompt file mode %v", st.Mode())
	}
}

func TestReadTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"fix it"}}`,
		`{"type":"file-history-snapshot","snapshot":{}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Looking."},{"type":"tool_use","name":"Read"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Fixed it."}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"+`{"type":"assist`), 0o600); err != nil {
		t.Fatal(err)
	}
	events, last, next := readTranscript(path, 0)
	if len(events) != 3 || last != "Fixed it." {
		t.Fatalf("events = %d, last = %q", len(events), last)
	}
	// The half-written last line waits for the next read.
	if want := int64(len(strings.Join(lines, "\n")) + 1); next != want {
		t.Fatalf("next = %d, want %d", next, want)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`ant","message":{"role":"assistant","content":"Done."}}` + "\n")
	_ = f.Close()
	events, last, _ = readTranscript(path, next)
	if len(events) != 1 || last != "Done." {
		t.Fatalf("second read: %d events, last %q", len(events), last)
	}
}

func TestClipKeepsUTF8(t *testing.T) {
	if got := clip("ação", 2); got != "a…" {
		t.Fatalf("clip = %q", got)
	}
	if got := clip("short", 10); got != "short" {
		t.Fatalf("clip = %q", got)
	}
}

func TestCursorOn(t *testing.T) {
	dialog := " Quick safety check\n ❯ No, exit\n   Yes, I trust this folder\n Enter to confirm · Esc to cancel"
	if cursorOn(dialog, "Yes, I trust this folder") {
		t.Fatal("the cursor starts on No, exit")
	}
	moved := " Quick safety check\n   No, exit\n ❯ Yes, I trust this folder\n Enter to confirm · Esc to cancel"
	if !cursorOn(moved, "Yes, I trust this folder") || cursorOn(moved, "Yes, I accept") {
		t.Fatal("after Down the cursor is on Yes")
	}
	numbered := " ❯ 2. Yes, I accept\n   1. No, exit"
	if !cursorOn(numbered, "Yes, I accept") {
		t.Fatal("a numbered option counts too")
	}
}
