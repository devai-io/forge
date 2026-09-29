package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/devai-io/forge/internal/store"
)

// The engine an agent run's Claude Code talks to. DeepSeek serves an
// Anthropic-compatible API, so a "deepseek" run is the same claude binary
// with ANTHROPIC_BASE_URL and a DeepSeek key — a fraction of the price.
// Runs use the default engine (Settings → Agent engine) unless they pick one;
// Claude is then only used when someone explicitly asks for it.

const (
	deepseekTag        = "integration:deepseek"
	deepseekAnthropics = "https://api.deepseek.com/anthropic"
)

// deepseekKey is the vault item tagged integration:deepseek, else the
// Assistant's key when the Assistant is pointed at DeepSeek.
func (s *Server) deepseekKey(ctx context.Context) (string, error) {
	if !s.box.Available() {
		return "", errors.New("vault unavailable")
	}
	k, err := s.store.IntegrationSecret(ctx, s.box, deepseekTag)
	if err == nil && k != "" {
		return k, nil
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	set, err := s.store.AssistantSettings(ctx)
	if err != nil {
		return "", err
	}
	if u, err := url.Parse(set.BaseURL); err == nil && strings.HasSuffix(u.Hostname(), "deepseek.com") {
		return s.assistantKey(ctx)
	}
	return "", nil
}

// prepareRun settles an agent run's engine and model before it is stored:
// the default engine when none was asked for (falling back to Claude, with a
// note, only when there is no DeepSeek key), Jev's routing, then the
// engine's default model. Resumed runs keep their original engine (the store
// copies it).
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
		set = store.EngineSettings{Default: "deepseek", Model: "deepseek-flash", HeavyModel: "deepseek-v4-pro"}
	}
	if in.Engine == "" {
		in.Engine = set.Default
	}
	if in.Engine == "deepseek" {
		if k, _ := s.deepseekKey(ctx); k == "" {
			in.Engine = "claude"
			in.ModelNote = "no DeepSeek key in the vault — ran on Claude"
		}
	}
	s.jevRoute(ctx, in, set)
	if in.Engine == "deepseek" && in.Model == "" {
		in.Model = set.Model
	}
}

// runnerEngine is what a machine needs to run a claimed run on its engine:
// nothing for Claude, DeepSeek's endpoint and key for DeepSeek.
func (s *Server) runnerEngine(ctx context.Context, run *store.Run) map[string]any {
	if run.Engine != "deepseek" {
		return nil
	}
	set, _ := s.store.EngineSettings(ctx)
	k, _ := s.deepseekKey(ctx)
	return map[string]any{"name": "deepseek", "base_url": deepseekAnthropics, "api_key": k,
		"model": set.Model, "heavy_model": set.HeavyModel}
}

func (s *Server) engineStatus(ctx context.Context) (map[string]any, error) {
	set, err := s.store.EngineSettings(ctx)
	if err != nil {
		return nil, err
	}
	k, _ := s.deepseekKey(ctx)
	return map[string]any{"settings": set, "deepseek_key": k != ""}, nil
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
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Default != nil {
		if !store.OneOf(*in.Default, store.Engines) {
			writeErr(w, r, &store.ValidationError{Field: "default", Message: "must be deepseek or claude"})
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
	s.sec(r, "engine_settings", fmt.Sprintf("default=%s model=%s heavy=%s", set.Default, set.Model, set.HeavyModel))
	s.getEngine(w, r, u)
}

// setDeepseekKey stores the key agent runs use, separate from the
// Assistant's (which may point at another provider).
func (s *Server) setDeepseekKey(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		APIKey string `json:"api_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.APIKey = strings.TrimSpace(in.APIKey)
	if len(in.APIKey) < 16 || strings.ContainsAny(in.APIKey, " \t\n") {
		writeErr(w, r, &store.ValidationError{Field: "api_key", Message: "that does not look like an API key"})
		return
	}
	if err := s.store.SaveIntegrationSecret(r.Context(), s.box, "DeepSeek API key (agent runs)", deepseekTag, in.APIKey, s.clientIP(r)); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "deepseek_key", "DeepSeek API key replaced")
	s.getEngine(w, r, u)
}
