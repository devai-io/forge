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
}

func (s *Store) JevSettings(ctx context.Context) (JevSettings, error) {
	v := JevSettings{Routing: true, Context: true, Compaction: true}
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
