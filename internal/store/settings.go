package store

import (
	"context"
	"encoding/json"
	"errors"
)

// Server-wide settings edited in the web app, one JSON value per key.

func (s *Store) getSetting(ctx context.Context, key string, v any) (bool, error) {
	var raw string
	err := s.DB.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&raw)
	if err != nil {
		if errors.Is(mapErr(err), ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), v)
}

func (s *Store) putSetting(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = now()`, key, string(raw))
	return err
}

// JevSettings says where Forge spends Jev calls to save frontier-model
// tokens. Nothing happens unless Enabled and a key is in the vault
// (tag integration:jev).
type JevSettings struct {
	Enabled bool `json:"enabled"`
	// Routing: an agent run queued without a model gets one picked by how
	// hard the prompt looks (haiku / sonnet / the machine's default).
	Routing bool `json:"routing"`
	// Context: the context a Claude session starts with lists only the open
	// tasks Jev judges relevant to the repo it was opened in.
	Context bool `json:"context"`
	// Compaction: every machine's Claude Code gets the Jev compaction plugin,
	// which drops tool results that no longer matter instead of summarising.
	Compaction bool `json:"compaction"`
	// Match: each request typed into Claude Code is matched against the open
	// tasks, so the session knows which one it is working on.
	Match bool `json:"match"`
}

func (s *Store) JevSettings(ctx context.Context) (JevSettings, error) {
	v := JevSettings{Routing: true, Context: true, Compaction: true, Match: true}
	_, err := s.getSetting(ctx, "jev", &v)
	return v, err
}

func (s *Store) SetJevSettings(ctx context.Context, v JevSettings) error {
	return s.putSetting(ctx, "jev", v)
}

// AssistantSettings configure the Assistant's LLM provider (any
// OpenAI-compatible chat-completions API). The key is the vault item tagged
// integration:assistant.
type AssistantSettings struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

func (s *Store) AssistantSettings(ctx context.Context) (AssistantSettings, error) {
	v := AssistantSettings{BaseURL: "https://api.deepseek.com", Model: "deepseek-flash"}
	_, err := s.getSetting(ctx, "assistant", &v)
	return v, err
}

func (s *Store) SetAssistantSettings(ctx context.Context, v AssistantSettings) error {
	return s.putSetting(ctx, "assistant", v)
}

// EngineSettings say which backend agent runs use when the run does not pick
// one. "deepseek" runs Claude Code against DeepSeek's Anthropic-compatible
// API with Model (HeavyModel when Jev judges the task heavy); "claude" is
// Anthropic. The DeepSeek key is the vault item tagged integration:deepseek,
// else the Assistant's key when the Assistant talks to DeepSeek.
type EngineSettings struct {
	Default    string `json:"default"`
	Model      string `json:"model"`
	HeavyModel string `json:"heavy_model"`
}

func (s *Store) EngineSettings(ctx context.Context) (EngineSettings, error) {
	v := EngineSettings{Default: "deepseek", Model: "deepseek-flash", HeavyModel: "deepseek-v4-pro"}
	_, err := s.getSetting(ctx, "engine", &v)
	return v, err
}

func (s *Store) SetEngineSettings(ctx context.Context, v EngineSettings) error {
	return s.putSetting(ctx, "engine", v)
}
