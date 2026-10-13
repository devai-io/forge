package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/devai-io/forge/internal/store"
)

// The engine an agent run's Claude Code talks to. "claude" is Claude Code
// as logged in on the machine (the subscription) and is the default.
// "deepseek" is the same claude binary pointed at the provider's
// Anthropic-compatible endpoint (OpenRouter's /api) with the provider key
// and DeepSeek's models — a fraction of the price, no subscription limits.

// prepareRun settles an agent run's engine and model before it is stored:
// the default engine when none was asked for (falling back to Claude, with a
// note, when DeepSeek is asked for but there is no provider key), Jev's
// routing, then the engine's default model. Resumed runs keep their
// original engine (the store copies it).
func (s *Server) prepareRun(ctx context.Context, in *store.RunInput) {
	if in.Kind != "agent" && in.ResumeRunID == nil {
		in.Engine = ""
		return
	}
	in.Engine = strings.ToLower(strings.TrimSpace(in.Engine))
	if in.ResumeRunID != nil {
		return
	}
	set, err := s.store.EngineSettings(ctx)
	if err != nil {
		set = store.EngineSettings{Default: "claude", Model: store.DefaultModel, HeavyModel: store.DefaultHeavyModel}
	}
	if in.Engine == "" {
		in.Engine = set.Default
	}
	if in.Engine == "deepseek" {
		if k, _ := s.providerKey(ctx); k == "" {
			in.Engine = "claude"
			in.ModelNote = providerHint("ran on Claude")
		}
	}
	s.jevRoute(ctx, in, set)
	if in.Engine == "deepseek" {
		in.Model = store.ModelID(in.Model)
		if in.Model == "" {
			in.Model = set.Model
		}
	}
}

// runnerEngine is what a machine needs to run a claimed run on its engine:
// nothing for Claude; for DeepSeek the provider's Anthropic endpoint, the
// key and the models. A run stored with one of DeepSeek's own model names
// (before the switch to OpenRouter) gets the OpenRouter id.
func (s *Server) runnerEngine(ctx context.Context, run *store.Run) map[string]any {
	if run.Engine != "deepseek" {
		return nil
	}
	run.Model = store.ModelID(run.Model)
	set, _ := s.store.EngineSettings(ctx)
	p, _ := s.store.ProviderSettings(ctx)
	k, _ := s.providerKey(ctx)
	return map[string]any{"name": "deepseek", "base_url": p.AnthropicURL(), "api_key": k,
		"model": set.Model, "heavy_model": set.HeavyModel}
}

func (s *Server) engineStatus(ctx context.Context) (map[string]any, error) {
	set, err := s.store.EngineSettings(ctx)
	if err != nil {
		return nil, err
	}
	k, _ := s.providerKey(ctx)
	deepseek := []string{}
	listed := []string{}
	for _, m := range s.providerModels(ctx) {
		if strings.HasPrefix(m, "deepseek/") {
			listed = append(listed, m)
		}
	}
	for _, m := range append(append([]string{set.Model, set.HeavyModel}, listed...), store.DeepSeekModels...) {
		if m != "" && !store.OneOf(m, deepseek) {
			deepseek = append(deepseek, m)
		}
	}
	return map[string]any{"settings": set, "provider_key": k != "",
		"models":  map[string]any{"deepseek": deepseek, "claude": store.ClaudeModels},
		"efforts": store.ClaudeEfforts}, nil
}

func (s *Server) getEngine(w http.ResponseWriter, r *http.Request, u *store.User) {
	st, err := s.engineStatus(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) updateEngine(w http.ResponseWriter, r *http.Request, u *store.User) {
	set, err := s.store.EngineSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in struct {
		Default    *string `json:"default"`
		Model      *string `json:"model"`
		HeavyModel *string `json:"heavy_model"`
		// MaxConcurrent: runs per machine at once.
		MaxConcurrent *int `json:"max_concurrent"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.MaxConcurrent != nil {
		if *in.MaxConcurrent < 1 || *in.MaxConcurrent > 16 {
			writeErr(w, r, &store.ValidationError{Field: "max_concurrent", Message: "must be between 1 and 16"})
			return
		}
		set.MaxConcurrent = *in.MaxConcurrent
	}
	if in.Default != nil {
		if !store.OneOf(*in.Default, store.Engines) {
			writeErr(w, r, &store.ValidationError{Field: "default", Message: "must be claude or deepseek"})
			return
		}
		set.Default = *in.Default
	}
	for field, p := range map[string]*string{"model": in.Model, "heavy_model": in.HeavyModel} {
		if p == nil {
			continue
		}
		v := strings.TrimSpace(*p)
		if v == "" || len(v) > 100 || strings.ContainsAny(v, " \t\n") {
			writeErr(w, r, &store.ValidationError{Field: field, Message: "must be a model name"})
			return
		}
		if field == "model" {
			set.Model = v
		} else {
			set.HeavyModel = v
		}
	}
	if err := s.store.SetEngineSettings(r.Context(), set); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "engine_settings", fmt.Sprintf("default=%s model=%s heavy=%s max_concurrent=%d", set.Default, set.Model, set.HeavyModel, set.MaxConcurrent))
	s.getEngine(w, r, u)
}
