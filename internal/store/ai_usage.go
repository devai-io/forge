package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/devai-io/forge/internal/llm"
	"github.com/devai-io/forge/internal/usage"
)

// RecordUsage writes one call, or replaces an identified cumulative agent snapshot.
// Recording survives a cancelled request but is bounded and never fails the work.
func (s *Store) RecordUsage(ctx context.Context, source *string, scope usage.Scope, e usage.Entry, kind string, failed bool) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if scope.Feature == "" {
		scope.Feature = "provider"
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO ai_usage
 (source_key, feature, project_key, run_id, chat_id, api, model, calls, failed, input_tokens, cached_tokens, output_tokens, cost_usd, cost_kind)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
 ON CONFLICT(source_key) DO UPDATE SET calls=max(ai_usage.calls,excluded.calls), failed=max(ai_usage.failed,excluded.failed),
 input_tokens=max(ai_usage.input_tokens,excluded.input_tokens), cached_tokens=max(ai_usage.cached_tokens,excluded.cached_tokens), output_tokens=max(ai_usage.output_tokens,excluded.output_tokens),
 cost_usd=coalesce(max(ai_usage.cost_usd,excluded.cost_usd),excluded.cost_usd,ai_usage.cost_usd), cost_kind=excluded.cost_kind`, source, scope.Feature, scope.Project, scope.RunID, scope.ChatID,
		e.API, e.Model, e.Calls, failed, max(0, e.InputTokens), max(0, e.CachedTokens), max(0, e.OutputTokens), e.CostUSD, kind)
	if err != nil {
		slog.Warn("record AI usage", "err", err)
	}
}

func (s *Store) ObserveLLM(ctx context.Context, baseURL, model string, u llm.Usage, err error) {
	api := "provider"
	if parsed, e := url.Parse(baseURL); e == nil {
		api = parsed.Hostname()
	}
	e := usage.Entry{API: api, Model: model, Calls: 1, InputTokens: int64(max(0, u.InputTokens-u.CachedTokens)),
		CachedTokens: int64(u.CachedTokens), OutputTokens: int64(u.OutputTokens), CostUSD: u.CostUSD}
	kind := "provider"
	if e.CostUSD == nil {
		kind = "unknown"
		if err == nil {
			e.CostUSD = usage.DeepSeekCost(model, e.InputTokens, e.CachedTokens, e.OutputTokens, time.Now())
			if e.CostUSD != nil {
				kind = "estimate"
			}
		}
	}
	s.RecordUsage(ctx, nil, usage.ScopeOf(ctx), e, kind, err != nil)
}

// UsageGroup deliberately separates reported cost, estimates and unpriced usage.
type UsageGroup struct {
	Feature      string   `json:"feature"`
	Project      string   `json:"project"`
	API          string   `json:"api"`
	Model        string   `json:"model"`
	CostKind     string   `json:"cost_kind"`
	Calls        int64    `json:"calls"`
	Failed       int64    `json:"failed"`
	InputTokens  int64    `json:"input_tokens"`
	CachedTokens int64    `json:"cached_tokens"`
	OutputTokens int64    `json:"output_tokens"`
	CostUSD      *float64 `json:"cost_usd"`
}

func (s *Store) AIUsage(ctx context.Context, days int) ([]UsageGroup, error) {
	rows, err := s.DB.Query(ctx, `SELECT feature, project_key, api, model, cost_kind, sum(calls), sum(failed),
 sum(input_tokens), sum(cached_tokens), sum(output_tokens), sum(cost_usd)
 FROM ai_usage WHERE recorded_at >= ts_add(now(), $1)
 GROUP BY feature, project_key, api, model, cost_kind ORDER BY feature, project_key, model, cost_kind`, -days*86400)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsageGroup{}
	for rows.Next() {
		var g UsageGroup
		if err := rows.Scan(&g.Feature, &g.Project, &g.API, &g.Model, &g.CostKind, &g.Calls, &g.Failed, &g.InputTokens, &g.CachedTokens, &g.OutputTokens, &g.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) LastChatInputTokens(ctx context.Context, chatID int64) int {
	var n int
	_ = s.DB.QueryRow(ctx, `SELECT input_tokens + cached_tokens FROM ai_usage WHERE chat_id=$1 AND feature='assistant' AND failed=0 ORDER BY id DESC LIMIT 1`, chatID).Scan(&n)
	return n
}

// RecordAgentTranscript accounts for tmux Claude messages once, even when a hook
// retries or emits several content blocks for the same model message. Headless
// runs use the final modelUsage snapshot instead. Codex hooks lack token usage.
func (s *Store) RecordAgentTranscript(ctx context.Context, run *Run, events []EventInput) {
	for _, ev := range events {
		var v struct {
			Type    string `json:"type"`
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage *struct {
					Input   int64 `json:"input_tokens"`
					Cached  int64 `json:"cache_read_input_tokens"`
					Created int64 `json:"cache_creation_input_tokens"`
					Output  int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(ev.Data, &v) != nil || v.Type != "assistant" || v.Message.ID == "" || v.Message.Usage == nil {
			continue
		}
		u := v.Message.Usage
		e := usage.Entry{API: run.Engine, Model: v.Message.Model, Calls: 1, InputTokens: u.Input + u.Created, CachedTokens: u.Cached, OutputTokens: u.Output}
		key := fmt.Sprintf("message:%d:%s:%s", run.RunnerID, run.SessionID, v.Message.ID)
		s.RecordUsage(ctx, &key, usage.Scope{Feature: "agent", Project: run.ProjectKey, RunID: &run.ID}, e, "unknown", false)
	}
}

func (s *Store) RecordAgentRun(ctx context.Context, run *Run) {
	for _, e := range AgentUsage(run) {
		kind := "unknown"
		if e.CostUSD != nil {
			kind = "subscription_estimate"
			if run.Engine == "openrouter" {
				kind = "estimate"
			}
		}
		key := fmt.Sprintf("run:%d:%s", run.ID, e.Model)
		s.RecordUsage(ctx, &key, usage.Scope{Feature: "agent", Project: run.ProjectKey, RunID: &run.ID}, e, kind, run.Status == "failed")
	}
}

func AgentUsage(run *Run) []usage.Entry {
	if string(run.Usage) == "{}" && run.CostUSD == nil {
		return nil
	}
	if run.Engine == "codex" {
		var u struct {
			Model             string `json:"model"`
			InputTokens       int64  `json:"inputTokens"`
			CachedInputTokens int64  `json:"cachedInputTokens"`
			OutputTokens      int64  `json:"outputTokens"`
		}
		if json.Unmarshal(run.Usage, &u) != nil {
			return nil
		}
		model := u.Model
		if model == "" {
			model = run.Model
		}
		input := u.InputTokens - u.CachedInputTokens
		if input < 0 {
			input = 0
		}
		return []usage.Entry{{API: "codex", Model: model, Calls: 1, InputTokens: input, CachedTokens: u.CachedInputTokens, OutputTokens: u.OutputTokens, Note: "Codex on the machine’s login; billing follows its Codex authentication"}}
	}
	var models map[string]struct {
		InputTokens              int64    `json:"inputTokens"`
		OutputTokens             int64    `json:"outputTokens"`
		CacheReadInputTokens     int64    `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int64    `json:"cacheCreationInputTokens"`
		CostUSD                  *float64 `json:"costUSD"`
	}
	const note = "Claude Code on the machine's login; cost at API prices"
	out := []usage.Entry{}
	if json.Unmarshal(run.Usage, &models) == nil {
		for model, u := range models {
			out = append(out, usage.Entry{API: "claude-code", Model: model, Calls: 1,
				InputTokens: u.InputTokens + u.CacheCreationInputTokens, CachedTokens: u.CacheReadInputTokens,
				OutputTokens: u.OutputTokens, CostUSD: u.CostUSD, Note: note})
		}
	}
	if len(out) == 0 && run.CostUSD != nil {
		out = append(out, usage.Entry{API: "claude-code", Model: run.Model, Calls: 1, CostUSD: run.CostUSD, Note: note})
	}
	if run.Engine == "openrouter" {
		at := run.CreatedAt
		if run.FinishedAt != nil {
			at = *run.FinishedAt
		}
		for i := range out {
			e := &out[i]
			e.API = "openrouter"
			e.Note = "DeepSeek estimate at configured list prices"
			e.CostUSD = usage.DeepSeekCost(e.Model, e.InputTokens, e.CachedTokens, e.OutputTokens, at)
			if e.InputTokens+e.CachedTokens+e.OutputTokens == 0 {
				e.CostUSD = run.CostUSD
			}
		}
	}
	return out
}

func (s *Store) ObserveJev(ctx context.Context, e usage.Entry, err error) {
	scope := usage.ScopeOf(ctx)
	if scope.Feature == "" {
		scope.Feature = "jev"
	}
	kind := "unknown"
	if e.CostUSD != nil {
		kind = "provider"
	}
	s.RecordUsage(ctx, nil, scope, e, kind, err != nil)
}
