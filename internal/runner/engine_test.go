package runner

import (
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
