package store

import (
	"context"
	"encoding/json"
	"strings"
)

// TelegramLink says what a message the bot sent stands for: a run's prompt
// (buttons and replies answer it), a run (a reply is a follow-up message),
// or an Assistant chat (a reply continues it).
type TelegramLink struct {
	TgChat    int64  `json:"tg_chat"`
	MessageID int64  `json:"message_id"`
	Kind      string `json:"kind"` // prompt | run | chat
	RunID     *int64 `json:"run_id"`
	PromptID  *int64 `json:"prompt_id"`
	ChatID    *int64 `json:"chat_id"`
}

func (s *Store) AddTelegramLink(ctx context.Context, l TelegramLink) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO telegram_links (tg_chat, message_id, kind, run_id, prompt_id, chat_id)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (tg_chat, message_id) DO NOTHING`,
		l.TgChat, l.MessageID, l.Kind, l.RunID, l.PromptID, l.ChatID)
	return err
}

func (s *Store) TelegramLinkFor(ctx context.Context, tgChat, messageID int64) (*TelegramLink, error) {
	var l TelegramLink
	err := s.DB.QueryRow(ctx, `SELECT tg_chat, message_id, kind, run_id, prompt_id, chat_id FROM telegram_links
		WHERE tg_chat = $1 AND message_id = $2`, tgChat, messageID).
		Scan(&l.TgChat, &l.MessageID, &l.Kind, &l.RunID, &l.PromptID, &l.ChatID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &l, nil
}

// TelegramPromptSent reports whether a prompt was already sent (a machine
// may report the same prompt twice).
func (s *Store) TelegramPromptSent(ctx context.Context, promptID int64) bool {
	var n int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM telegram_links WHERE prompt_id = $1`, promptID).Scan(&n)
	return n > 0
}

// PromptByID is one prompt of any run.
func (s *Store) PromptByID(ctx context.Context, id int64) (*RunPrompt, error) {
	return scanPrompt(s.DB.QueryRow(ctx, promptSelect+` WHERE id = $1`, id))
}

// LastRunText is the newest thing Claude said in a run: the text of its last
// result, else of its last assistant message ("" when there is none).
func (s *Store) LastRunText(ctx context.Context, runID int64) string {
	rows, err := s.DB.Query(ctx, `SELECT data FROM run_events WHERE run_id = $1 AND kind IN ('claude', 'codex')
		ORDER BY seq DESC LIMIT 60`, runID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		if t := codexText(raw); t != "" {
			return t
		}
		if t := claudeText(raw); t != "" {
			return t
		}
	}
	return ""
}

// claudeText is the text of one stream-json line from Claude Code: a result's
// or an assistant message's.
func claudeText(raw string) string {
	var m struct {
		Type    string `json:"type"`
		Result  string `json:"result"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(raw), &m) != nil {
		return ""
	}
	switch m.Type {
	case "result":
		return strings.TrimSpace(m.Result)
	case "assistant":
		var parts []string
		for _, c := range m.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, strings.TrimSpace(c.Text))
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

func codexText(raw string) string {
	var m struct {
		Method string `json:"method"`
		Params struct {
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		} `json:"params"`
	}
	if json.Unmarshal([]byte(raw), &m) == nil && m.Method == "item/completed" && m.Params.Item.Type == "agentMessage" {
		return m.Params.Item.Text
	}
	return ""
}
