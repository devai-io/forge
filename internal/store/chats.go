package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Assistant chats: the conversation the agent loop reads and appends to.

type ChatUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CachedTokens int `json:"cached_tokens"`
}

type Chat struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Busy      bool      `json:"busy"`
	LastError string    `json:"last_error"`
	Usage     ChatUsage `json:"usage"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ChatToolCall is a tool call as stored and shown: arguments parsed.
type ChatToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ChatMessage struct {
	Seq        int             `json:"seq"`
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  []ChatToolCall  `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
	ToolName   string          `json:"tool_name"`
	Result     json.RawMessage `json:"result"`
	IsError    bool            `json:"is_error"`
	CreatedAt  time.Time       `json:"created_at"`
}

const chatCols = `id, title, busy, last_error, input_tokens, output_tokens, cached_tokens, created_at, updated_at`

func scanChat(row interface{ Scan(...any) error }) (*Chat, error) {
	var c Chat
	if err := row.Scan(&c.ID, &c.Title, &c.Busy, &c.LastError, &c.Usage.InputTokens, &c.Usage.OutputTokens,
		&c.Usage.CachedTokens, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &c, nil
}

func (s *Store) ListChats(ctx context.Context) ([]Chat, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+chatCols+` FROM chats ORDER BY updated_at DESC, id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Chat{}
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) ChatByID(ctx context.Context, id int64) (*Chat, error) {
	return scanChat(s.DB.QueryRow(ctx, `SELECT `+chatCols+` FROM chats WHERE id = $1`, id))
}

func (s *Store) CreateChat(ctx context.Context, title string) (*Chat, error) {
	return scanChat(s.DB.QueryRow(ctx, `INSERT INTO chats (title) VALUES ($1) RETURNING `+chatCols, chatTitle(title)))
}

// chatTitle is the first line of the first message, shortened.
func chatTitle(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > 60 {
		s = strings.TrimSpace(string(r[:57])) + "…"
	}
	if s == "" {
		s = "New chat"
	}
	return s
}

func (s *Store) RenameChat(ctx context.Context, id int64, title string) (*Chat, error) {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 120 {
		return nil, invalid("title", "must be 1-120 characters")
	}
	return scanChat(s.DB.QueryRow(ctx, `UPDATE chats SET title = $2, updated_at = now() WHERE id = $1 RETURNING `+chatCols, id, title))
}

func (s *Store) DeleteChat(ctx context.Context, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM chats WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimChat marks a chat busy; false if a turn is already running.
func (s *Store) ClaimChat(ctx context.Context, id int64) (bool, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE chats SET busy = 1, last_error = '', updated_at = now() WHERE id = $1 AND busy = 0`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) ReleaseChat(ctx context.Context, id int64, lastError string) error {
	_, err := s.DB.Exec(ctx, `UPDATE chats SET busy = 0, last_error = $2, updated_at = now() WHERE id = $1`, id, truncate(lastError, 1000))
	return err
}

// ResetBusyChats ends turns a restart interrupted.
func (s *Store) ResetBusyChats(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `UPDATE chats SET busy = 0, last_error = 'interrupted by a server restart' WHERE busy = 1`)
	return err
}

func (s *Store) AddChatUsage(ctx context.Context, id int64, u ChatUsage) error {
	_, err := s.DB.Exec(ctx, `UPDATE chats SET input_tokens = input_tokens + $2, output_tokens = output_tokens + $3,
		cached_tokens = cached_tokens + $4 WHERE id = $1`, id, u.InputTokens, u.OutputTokens, u.CachedTokens)
	return err
}

// AppendChatMessage adds a message at the next seq.
func (s *Store) AppendChatMessage(ctx context.Context, chatID int64, m ChatMessage) (*ChatMessage, error) {
	if m.ToolCalls == nil {
		m.ToolCalls = []ChatToolCall{}
	}
	var result any
	if len(m.Result) > 0 {
		result = string(m.Result)
	}
	var seq int
	err := s.tx(ctx, func(tx pgxTx) error {
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(seq), 0) + 1 FROM chat_messages WHERE chat_id = $1`, chatID).Scan(&seq); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO chat_messages (chat_id, seq, role, content, tool_calls, tool_call_id, tool_name, result, is_error)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, chatID, seq, m.Role, m.Content, m.ToolCalls, m.ToolCallID,
			m.ToolName, result, m.IsError); err != nil {
			return mapErr(err)
		}
		_, err := tx.Exec(ctx, `UPDATE chats SET updated_at = now() WHERE id = $1`, chatID)
		return err
	})
	if err != nil {
		return nil, err
	}
	m.Seq, m.CreatedAt = seq, time.Now().UTC()
	return &m, nil
}

func (s *Store) ChatMessages(ctx context.Context, chatID int64, after int) ([]ChatMessage, error) {
	rows, err := s.DB.Query(ctx, `SELECT seq, role, content, tool_calls, tool_call_id, tool_name, result, is_error, created_at
		FROM chat_messages WHERE chat_id = $1 AND seq > $2 ORDER BY seq`, chatID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatMessage{}
	for rows.Next() {
		var m ChatMessage
		var result *string
		if err := rows.Scan(&m.Seq, &m.Role, &m.Content, &m.ToolCalls, &m.ToolCallID, &m.ToolName, &result, &m.IsError,
			&m.CreatedAt); err != nil {
			return nil, err
		}
		if result != nil {
			m.Result = json.RawMessage(*result)
		} else {
			m.Result = json.RawMessage("null")
		}
		if m.ToolCalls == nil {
			m.ToolCalls = []ChatToolCall{}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
