package runner

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/usage"
)

// engineSetup is how the server asks for a run on another backend than the
// machine's own Claude login: DeepSeek through the provider (OpenRouter),
// whose Anthropic-compatible endpoint lets the same claude binary run with a
// different base URL, key and model names.
type engineSetup struct {
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	APIKey     string `json:"api_key"`
	Model      string `json:"model"`
	HeavyModel string `json:"heavy_model"`
}

// env is what the claude process gets on top of the usual environment
// (which withoutClaudeAuth has cleared of Anthropic credentials). DeepSeek's
// models have a 1M-token window; "[1m]" tells Claude Code so, and
// compaction waits accordingly. Tool search stays off: it defers tools
// behind a server-side feature only Anthropic's endpoints implement.
func (e *engineSetup) env(runModel string) ([]string, error) {
	if e == nil || !isOpenRouter(e.Name) {
		return nil, errors.New("the server sent no DeepSeek setup for this run (update Forge?)")
	}
	if e.APIKey == "" {
		return nil, errors.New("no OpenRouter key: add one in Forge (Settings → AI provider)")
	}
	if u, err := url.Parse(e.BaseURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("the provider's base URL must be https")
	}
	model, heavy := orDefault(runModel, e.Model), orDefault(e.HeavyModel, e.Model)
	oneM := func(m string) string {
		if strings.Contains(m, "deepseek") && !strings.Contains(m, "[") {
			return m + "[1m]"
		}
		return m
	}
	return []string{
		"ANTHROPIC_BASE_URL=" + e.BaseURL,
		"ANTHROPIC_AUTH_TOKEN=" + e.APIKey, // gitleaks:allow (the variable name, not a key)
		"ANTHROPIC_API_KEY=",
		"ANTHROPIC_MODEL=" + oneM(model),
		"ANTHROPIC_DEFAULT_OPUS_MODEL=" + oneM(heavy),
		"ANTHROPIC_DEFAULT_SONNET_MODEL=" + oneM(e.Model),
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=" + e.Model,
		"CLAUDE_CODE_SUBAGENT_MODEL=" + e.Model,
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW=786432",
		"ENABLE_TOOL_SEARCH=false",
	}, nil
}

// deepseekArgs keep tool search off for a DeepSeek run even when the
// user's ~/.claude/settings.json turns it on ("env" there beats the process
// environment; --settings beats the user's settings). Nothing secret goes
// here: argv shows in ps.
var deepseekArgs = []string{"--settings", `{"env":{"ENABLE_TOOL_SEARCH":"false"}}`}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// withoutClaudeAuth drops whatever would send a DeepSeek run to Anthropic
// instead: API keys, an OAuth token, Bedrock/Vertex switches, a base URL.
func withoutClaudeAuth(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "ANTHROPIC_") || k == "CLAUDE_CODE_OAUTH_TOKEN" ||
			k == "CLAUDE_CODE_USE_BEDROCK" || k == "CLAUDE_CODE_USE_VERTEX" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

type modelUsage struct {
	InputTokens              int64 `json:"inputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
}

// deepseekCost estimates a run's cost from its token counts at the
// provider's list prices (see usage.DeepSeekCost; Claude Code would price
// them at Anthropic's). Nil when there is no usage.
func (c *claudeResult) deepseekCost(at time.Time) *float64 {
	if len(c.Usage) == 0 {
		return nil
	}
	total := 0.0
	for model, u := range c.Usage {
		if p := usage.DeepSeekCost(model, u.InputTokens+u.CacheCreationInputTokens, u.CacheReadInputTokens, u.OutputTokens, at); p != nil {
			total += *p
		}
	}
	return &total
}

// isOpenRouter: the OpenRouter engine, under its name since v0.1.30 or the
// "deepseek" it had before (a server that has not been updated sends that).
func isOpenRouter(engine string) bool { return engine == "openrouter" || engine == "deepseek" }
