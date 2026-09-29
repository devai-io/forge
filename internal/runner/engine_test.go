package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeepSeekEnv(t *testing.T) {
	var none *engineSetup
	if _, err := none.env(""); err == nil {
		t.Fatal("a DeepSeek run without setup must be refused")
	}
	e := &engineSetup{Name: "deepseek", BaseURL: "https://api.deepseek.com/anthropic", APIKey: "k",
		Model: "deepseek-flash", HeavyModel: "deepseek-v4-pro"}
	if _, err := (&engineSetup{Name: "deepseek", BaseURL: "http://evil.example.com", APIKey: "k"}).env(""); err == nil {
		t.Fatal("plain http base URL accepted")
	}
	env, err := e.env("deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	for _, want := range []string{"ANTHROPIC_BASE_URL=https://api.deepseek.com/anthropic", "ANTHROPIC_AUTH_TOKEN=k",
		"ANTHROPIC_MODEL=deepseek-v4-pro[1m]", "ANTHROPIC_DEFAULT_HAIKU_MODEL=deepseek-flash"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in\n%s", want, joined)
		}
	}
	kept := withoutClaudeAuth([]string{"PATH=/bin", "ANTHROPIC_API_KEY=sk-ant", "CLAUDE_CODE_OAUTH_TOKEN=t", "HOME=/h"})
	if strings.Join(kept, " ") != "PATH=/bin HOME=/h" {
		t.Fatalf("kept = %v", kept)
	}
}

func TestDeepSeekCost(t *testing.T) {
	c := claudeResult{Usage: map[string]modelUsage{
		"deepseek-flash":  {InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadInputTokens: 1_000_000},
		"deepseek-v4-pro": {OutputTokens: 1_000_000},
	}}
	offPeak := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) // Saturday
	if got := *c.deepseekCost(offPeak); got < 2.7329 || got > 2.7331 {
		t.Fatalf("off-peak = %v", got)
	}
	peak := time.Date(2026, 9, 29, 7, 0, 0, 0, time.UTC) // Tuesday 07:00
	if got := *c.deepseekCost(peak); got < 5.4659 || got > 5.4661 {
		t.Fatalf("peak = %v", got)
	}
	if (&claudeResult{}).deepseekCost(peak) != nil {
		t.Fatal("no usage must give no cost")
	}
}

func TestSetupHooksReplaceOnlyForgeOnes(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	orig := `{"model":"opus","hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"other-tool"}]}],
		"SessionStart":[{"hooks":[{"type":"command","command":"/old/forge agent context --hook"}]}]}}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // running setup twice changes nothing more
		if err := addHook(path, "SessionStart", "/bin/forge agent context --hook", "context --hook"); err != nil {
			t.Fatal(err)
		}
		if err := addHook(path, "UserPromptSubmit", "/bin/forge agent match --hook", "match --hook"); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(path)
	s := string(raw)
	if strings.Count(s, "match --hook") != 1 || strings.Count(s, "context --hook") != 1 || !strings.Contains(s, "other-tool") ||
		strings.Contains(s, "/old/forge") || !strings.Contains(s, `"model": "opus"`) {
		t.Fatalf("settings = %s", s)
	}
}

func TestAssistantSessionRun(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	record := filepath.Join(t.TempDir(), "args")
	script := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
pwd > "`+record+`"
for a in "$@"; do echo "$a" >> "`+record+`"; done
echo '{"type":"system","subtype":"init","session_id":"s9"}'
echo '{"type":"result","subtype":"success","result":"ok","total_cost_usd":0.03,"modelUsage":{"claude-sonnet-5-5":{"inputTokens":5,"outputTokens":7,"cacheReadInputTokens":90,"costUSD":0.03}}}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(srv.Close)
	r := New(&Config{APIURL: srv.URL, Token: "t", ClaudePath: script, AllowedRoots: []string{root},
		PermissionModes: []string{"plan"}, MaxRunMinutes: 1, MaxConcurrent: 1, ExtraArgs: []string{"--verbose-extra"}})
	turn := int64(4)
	rep := r.execute(context.Background(), claimedRun{ID: 1, Kind: "agent", Prompt: "hi", PermissionMode: "plan",
		Effort: "high", Model: "sonnet", ChatTurnID: &turn, Assistant: &assistantSetup{AppendSystem: "be brief"}})
	if rep.Status != "succeeded" || rep.SessionID != "s9" || !strings.Contains(string(rep.Usage), "claude-sonnet-5-5") {
		t.Fatalf("report = %+v", rep)
	}
	raw, _ := os.ReadFile(record)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	got := strings.Join(lines[1:], " ")
	if lines[0] != root || !strings.Contains(got, "--effort high") || !strings.Contains(got, "--allowedTools mcp__forge") ||
		!strings.Contains(got, "--append-system-prompt be brief") || !strings.Contains(got, "--model sonnet") {
		t.Fatalf("cwd %s, args %s", lines[0], got)
	}
	// An ordinary run with no repo path is still refused.
	if rep := r.execute(context.Background(), claimedRun{ID: 2, Kind: "agent", Prompt: "hi", PermissionMode: "plan"}); rep.Status != "failed" {
		t.Fatalf("no repo, no assistant: %+v", rep)
	}
}

func TestMCPReadToolsAreMarkedReadOnly(t *testing.T) {
	for _, tl := range listedTools() {
		ro := tl["annotations"].(map[string]any)["readOnlyHint"].(bool)
		name := tl["name"].(string)
		if want := !strings.Contains(name, "create") && !strings.Contains(name, "update") && name != "forge_comment"; ro != want {
			t.Errorf("%s readOnlyHint = %v", name, ro)
		}
	}
}
