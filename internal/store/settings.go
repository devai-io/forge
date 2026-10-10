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
//
// MaxConcurrent is how many runs each machine works on at once (an
// interactive run holds its slot while it waits for a reply); a machine's
// agent.json max_concurrent can only lower it.
type EngineSettings struct {
	Default       string `json:"default"`
	Model         string `json:"model"`
	HeavyModel    string `json:"heavy_model"`
	MaxConcurrent int    `json:"max_concurrent"`
}

// DefaultMaxConcurrent is the per-machine run limit until Settings change it.
const DefaultMaxConcurrent = 4

func (s *Store) EngineSettings(ctx context.Context) (EngineSettings, error) {
	v := EngineSettings{Default: "deepseek", Model: "deepseek-flash", HeavyModel: "deepseek-v4-pro", MaxConcurrent: DefaultMaxConcurrent}
	_, err := s.getSetting(ctx, "engine", &v)
	if v.MaxConcurrent <= 0 {
		v.MaxConcurrent = DefaultMaxConcurrent
	}
	return v, err
}

func (s *Store) SetEngineSettings(ctx context.Context, v EngineSettings) error {
	return s.putSetting(ctx, "engine", v)
}

// TelegramSettings connect Forge to a Telegram bot (the token is the vault
// item tagged integration:telegram). Only ChatID is ever answered or written
// to; each switch turns one kind of traffic on.
type TelegramSettings struct {
	Enabled bool  `json:"enabled"`
	ChatID  int64 `json:"chat_id"`
	// Prompts: what an interactive run asks (questions, permissions, plans)
	// is sent with buttons, and answered from Telegram.
	Prompts bool `json:"prompts"`
	// Runs: a run that finishes, or an interactive one that waits for a reply.
	Runs bool `json:"runs"`
	// Checkup: the scheduled daily check-up.
	Checkup bool `json:"checkup"`
	// Assistant: other messages go to the Assistant (one chat, /new starts another).
	Assistant bool `json:"assistant"`
}

func (s *Store) TelegramSettings(ctx context.Context) (TelegramSettings, error) {
	v := TelegramSettings{Prompts: true, Runs: true, Checkup: true, Assistant: true}
	_, err := s.getSetting(ctx, "telegram", &v)
	return v, err
}

func (s *Store) SetTelegramSettings(ctx context.Context, v TelegramSettings) error {
	return s.putSetting(ctx, "telegram", v)
}

// TelegramChat is the Assistant chat Telegram messages currently go to (0:
// the next message starts one).
func (s *Store) TelegramChat(ctx context.Context) int64 {
	var v struct {
		ChatID int64 `json:"chat_id"`
	}
	_, _ = s.getSetting(ctx, "telegram_chat", &v)
	return v.ChatID
}

func (s *Store) SetTelegramChat(ctx context.Context, chatID int64) error {
	return s.putSetting(ctx, "telegram_chat", map[string]int64{"chat_id": chatID})
}
