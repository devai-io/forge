package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

// AI provider: one OpenAI-compatible gateway (OpenRouter by default) and one
// key for everything Forge pays for itself — the Assistant's DeepSeek engine,
// agent runs on DeepSeek, and Jev. The key is the vault item tagged
// integration:openrouter.

const (
	// OpenRouterTag is the vault tag of the provider key.
	OpenRouterTag = "integration:openrouter"
	// DefaultProviderURL is OpenRouter's OpenAI-compatible API.
	DefaultProviderURL = "https://openrouter.ai/api/v1"
	// DefaultModel and DefaultHeavyModel are the DeepSeek models (OpenRouter ids).
	DefaultModel      = "deepseek/deepseek-v4-flash"
	DefaultHeavyModel = "deepseek/deepseek-v4-pro"
	// DefaultJevModel is TypeSafe's Jev router on OpenRouter.
	DefaultJevModel = "typesafe/jev-router"
)

// ProviderSettings say where the provider's API is. Claude Code (agent runs
// on DeepSeek) talks to the same gateway's Anthropic-compatible endpoint.
type ProviderSettings struct {
	BaseURL string `json:"base_url"`
}

// AnthropicURL is the base URL Claude Code gets as ANTHROPIC_BASE_URL: the
// OpenAI-compatible base without its /v1 (OpenRouter serves /api/v1/messages).
func (p ProviderSettings) AnthropicURL() string {
	return strings.TrimSuffix(strings.TrimRight(p.BaseURL, "/"), "/v1")
}

func (s *Store) ProviderSettings(ctx context.Context) (ProviderSettings, error) {
	v := ProviderSettings{BaseURL: DefaultProviderURL}
	_, err := s.getSetting(ctx, "provider", &v)
	if v.BaseURL == "" {
		v.BaseURL = DefaultProviderURL
	}
	return v, err
}

func (s *Store) SetProviderSettings(ctx context.Context, v ProviderSettings) error {
	return s.putSetting(ctx, "provider", v)
}

// ModelID maps the DeepSeek API's own model names, which settings and chats
// from before the switch to OpenRouter may still carry, to OpenRouter ids.
// Anything else is returned as it is.
func ModelID(m string) string {
	switch strings.TrimSpace(m) {
	case "deepseek-flash", "deepseek-chat", "deepseek-v4-flash":
		return DefaultModel
	case "deepseek-pro", "deepseek-reasoner", "deepseek-v4-pro":
		return DefaultHeavyModel
	}
	return m
}

// JevSettings says where Forge spends Jev calls to save frontier-model
// tokens. Nothing happens unless Enabled and the provider key is in the vault.
type JevSettings struct {
	Enabled bool `json:"enabled"`
	// Model is the Jev model asked through the provider.
	Model string `json:"model"`
	// Routing: an agent run queued without a model gets one picked by how
	// hard the prompt looks (haiku / sonnet / the machine's default).
	Routing bool `json:"routing"`
	// Context: the context a Claude session starts with lists only the open
	// tasks Jev judges relevant to the repo it was opened in.
	Context bool `json:"context"`
	// Match: each request typed into Claude Code is matched against the open
	// tasks, so the session knows which one it is working on.
	Match bool `json:"match"`
}

func (s *Store) JevSettings(ctx context.Context) (JevSettings, error) {
	v := JevSettings{Model: DefaultJevModel, Routing: true, Context: true, Match: true}
	_, err := s.getSetting(ctx, "jev", &v)
	if v.Model == "" || v.Model == "jev-latest" {
		v.Model = DefaultJevModel
	}
	return v, err
}

func (s *Store) SetJevSettings(ctx context.Context, v JevSettings) error {
	return s.putSetting(ctx, "jev", v)
}

// AssistantSettings configure the Assistant. Engine is what a new chat
// starts on: "claude" (a Claude Code session on the master machine, on the
// subscription logged in there) or "deepseek" (Model through the provider).
// BaseURL is the provider's, filled in when read.
type AssistantSettings struct {
	Enabled bool   `json:"enabled"`
	Engine  string `json:"engine"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

func (s *Store) AssistantSettings(ctx context.Context) (AssistantSettings, error) {
	v := AssistantSettings{Engine: "claude", Model: DefaultModel}
	if _, err := s.getSetting(ctx, "assistant", &v); err != nil {
		return v, err
	}
	if !OneOf(v.Engine, Engines) {
		v.Engine = "claude"
	}
	v.Model = ModelID(v.Model)
	p, err := s.ProviderSettings(ctx)
	v.BaseURL = p.BaseURL
	return v, err
}

func (s *Store) SetAssistantSettings(ctx context.Context, v AssistantSettings) error {
	v.BaseURL = "" // the provider's; see ProviderSettings
	return s.putSetting(ctx, "assistant", v)
}

// EngineSettings say which backend agent runs use when the run does not pick
// one. "claude" is Claude Code as logged in on the machine (the
// subscription); "deepseek" runs the same Claude Code against the provider's
// Anthropic-compatible endpoint with Model (HeavyModel when Jev judges the
// task heavy).
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
	v := EngineSettings{Default: "claude", Model: DefaultModel, HeavyModel: DefaultHeavyModel, MaxConcurrent: DefaultMaxConcurrent}
	_, err := s.getSetting(ctx, "engine", &v)
	if v.MaxConcurrent <= 0 {
		v.MaxConcurrent = DefaultMaxConcurrent
	}
	v.Model, v.HeavyModel = ModelID(v.Model), ModelID(v.HeavyModel)
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
