package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/jev"
	"github.com/devai-io/forge/internal/llm"
	"github.com/devai-io/forge/internal/store"
)

// The AI provider: one OpenAI-compatible gateway (OpenRouter by default) and
// one key in the vault (tag integration:openrouter) for everything Forge
// pays for — the Assistant on DeepSeek, agent runs on DeepSeek, Jev. Claude
// (the Assistant's default, and agent runs' default) needs no key: it is
// Claude Code as logged in on the machine.

// providerKey is the provider key, "" when there is none.
func (s *Server) providerKey(ctx context.Context) (string, error) {
	if !s.box.Available() {
		return "", errors.New("vault unavailable")
	}
	k, err := s.store.IntegrationSecret(ctx, s.box, store.OpenRouterTag)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return k, err
}

// providerURL is the provider's OpenAI-compatible base URL.
func (s *Server) providerURL(ctx context.Context) string {
	p, _ := s.store.ProviderSettings(ctx)
	return p.BaseURL
}

// llmClient talks to the provider with the vault key, attributed to Forge.
func (s *Server) llmClient(ctx context.Context) *llm.Client {
	return llm.New(s.providerURL(ctx), s.providerKey).App("Forge", s.cfg.PublicURL)
}

func (s *Server) jevConfig(ctx context.Context) jev.Config {
	set, _ := s.store.JevSettings(ctx)
	return jev.Config{BaseURL: s.providerURL(ctx), Model: set.Model}
}

// providerModels is what the provider lists for the key, cached for a while
// (the Settings page and the run form ask on every load); nothing without a
// key or when it can't be reached.
func (s *Server) providerModels(ctx context.Context) []string {
	k, _ := s.providerKey(ctx)
	if k == "" {
		return []string{}
	}
	base := s.providerURL(ctx)
	c := &s.models
	c.mu.Lock()
	defer c.mu.Unlock()
	cacheKey := base + "|" + k
	if c.key == cacheKey && time.Since(c.at) < 10*time.Minute {
		return c.models
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ids, err := s.llmClient(ctx).Models(ctx)
	if err != nil { // try again in a minute
		c.key, c.at, c.models = cacheKey, time.Now().Add(-9*time.Minute), []string{}
		return c.models
	}
	c.key, c.at, c.models = cacheKey, time.Now(), ids
	return ids
}

func (s *Server) providerStatus(ctx context.Context) (map[string]any, error) {
	p, err := s.store.ProviderSettings(ctx)
	if err != nil {
		return nil, err
	}
	k, _ := s.providerKey(ctx)
	return map[string]any{"settings": p, "key_configured": k != "", "anthropic_url": p.AnthropicURL(),
		"default_url": store.DefaultProviderURL}, nil
}

func (s *Server) getProvider(w http.ResponseWriter, r *http.Request, u *store.User) {
	st, err := s.providerStatus(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) updateProvider(w http.ResponseWriter, r *http.Request, u *store.User) {
	p, err := s.store.ProviderSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in struct {
		BaseURL *string `json:"base_url"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.BaseURL != nil {
		v := strings.TrimRight(strings.TrimSpace(*in.BaseURL), "/")
		if v == "" {
			v = store.DefaultProviderURL
		}
		if pu, err := url.Parse(v); err != nil || pu.Scheme != "https" || pu.Host == "" {
			writeErr(w, r, &store.ValidationError{Field: "base_url", Message: "must be an https URL"})
			return
		}
		p.BaseURL = v
	}
	if err := s.store.SetProviderSettings(r.Context(), p); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "provider_settings", p.BaseURL)
	s.getProvider(w, r, u)
}

// setProviderKey stores the provider key as the vault item Forge reads it from.
func (s *Server) setProviderKey(w http.ResponseWriter, r *http.Request, u *store.User) {
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
	if err := s.store.SaveIntegrationSecret(r.Context(), s.box, "OpenRouter API key", store.OpenRouterTag, in.APIKey, s.clientIP(r)); err != nil {
		writeErr(w, r, err)
		return
	}
	s.models.mu.Lock()
	s.models.key = ""
	s.models.mu.Unlock()
	s.sec(r, "provider_key", "OpenRouter API key replaced")
	s.getProvider(w, r, u)
}

// testProvider makes one tiny call (a few tokens) so Settings can say the
// key works.
func (s *Server) testProvider(w http.ResponseWriter, r *http.Request, u *store.User) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	set, _ := s.store.EngineSettings(ctx)
	msg, use, err := s.llmClient(ctx).Chat(ctx, set.Model, "off", []llm.Message{{Role: "user", Content: "Reply with: ok"}}, nil)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	out := map[string]any{"ok": true, "ms": time.Since(start).Milliseconds(), "model": set.Model, "answer": msg.Content}
	if use.CostUSD != nil {
		out["cost_usd"] = *use.CostUSD
	}
	writeJSON(w, http.StatusOK, out)
}

// providerHint is how errors name what is missing.
func providerHint(what string) string {
	return fmt.Sprintf("no OpenRouter key in the vault — %s", what)
}
