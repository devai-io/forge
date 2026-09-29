package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/usage"
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
	ChatSettings
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ChatSettings pick what answers a chat: the DeepSeek API loop on the
// server ("deepseek") or a Claude Code session on the master machine
// ("claude"), with a model and an effort ("" = the default). Edits lets a
// Claude Code session change files (acceptEdits instead of plan).
type ChatSettings struct {
	Engine string `json:"engine"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
	Edits  bool   `json:"edits"`
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
	// Reasoning is the model's thinking (DeepSeek), kept to send back.
	Reasoning string `json:"-"`
}

const chatCols = `id, title, busy, last_error, input_tokens, output_tokens, cached_tokens, created_at, updated_at,
	engine, model, effort, edits`

func scanChat(row interface{ Scan(...any) error }) (*Chat, error) {
	var c Chat
	if err := row.Scan(&c.ID, &c.Title, &c.Busy, &c.LastError, &c.Usage.InputTokens, &c.Usage.OutputTokens,
		&c.Usage.CachedTokens, &c.CreatedAt, &c.UpdatedAt, &c.Engine, &c.Model, &c.Effort, &c.Edits); err != nil {
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

func (s *Store) CreateChat(ctx context.Context, title string, set ChatSettings) (*Chat, error) {
	return scanChat(s.DB.QueryRow(ctx, `INSERT INTO chats (title, engine, model, effort, edits) VALUES ($1, $2, $3, $4, $5)
		RETURNING `+chatCols, chatTitle(title), set.Engine, set.Model, set.Effort, set.Edits))
}

// SetChatSettings changes what answers the chat from its next turn on.
func (s *Store) SetChatSettings(ctx context.Context, id int64, set ChatSettings) (*Chat, error) {
	return scanChat(s.DB.QueryRow(ctx, `UPDATE chats SET engine = $2, model = $3, effort = $4, edits = $5, updated_at = now()
		WHERE id = $1 RETURNING `+chatCols, id, set.Engine, set.Model, set.Effort, set.Edits))
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

// ResetBusyChats ends the turns a restart interrupted: those the server was
// running itself. A Claude Code turn goes on on its machine and ends when
// its run does.
func (s *Store) ResetBusyChats(ctx context.Context) error {
	if _, err := s.DB.Exec(ctx, `UPDATE chat_turns SET status = 'failed', error = 'interrupted by a server restart',
		finished_at = now() WHERE status = 'running' AND run_id IS NULL`); err != nil {
		return err
	}
	_, err := s.DB.Exec(ctx, `UPDATE chats SET busy = 0, last_error = 'interrupted by a server restart' WHERE busy = 1
		AND NOT EXISTS (SELECT 1 FROM chat_turns t WHERE t.chat_id = chats.id AND t.status = 'running')`)
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
		if _, err := tx.Exec(ctx, `INSERT INTO chat_messages (chat_id, seq, role, content, tool_calls, tool_call_id, tool_name, result, is_error, reasoning)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, chatID, seq, m.Role, m.Content, m.ToolCalls, m.ToolCallID,
			m.ToolName, result, m.IsError, m.Reasoning); err != nil {
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
	rows, err := s.DB.Query(ctx, `SELECT seq, role, content, tool_calls, tool_call_id, tool_name, result, is_error, created_at, reasoning
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
			&m.CreatedAt, &m.Reasoning); err != nil {
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

// ── Turns ─────────────────────────────────────────────────────────────────

// ChatTurn is one user message's processing: which engine answered, how it
// ended and what it spent, per API. Runs lists the runs it queued (and, on
// Claude Code, its own session run), filled in by ChatTurns.
type ChatTurn struct {
	ID         int64         `json:"id"`
	ChatID     int64         `json:"chat_id"`
	Seq        int           `json:"seq"` // the user message that started it
	Engine     string        `json:"engine"`
	Model      string        `json:"model"`
	Effort     string        `json:"effort"`
	Status     string        `json:"status"` // running | done | failed | stopped
	Error      string        `json:"error"`
	Usage      []usage.Entry `json:"usage"`
	RunID      *int64        `json:"run_id"` // the Claude Code session run
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt *time.Time    `json:"finished_at"`
	Runs       []Run         `json:"runs"`
}

const turnCols = `id, chat_id, seq, engine, model, effort, status, error, usage, run_id, started_at, finished_at`

func scanTurn(row interface{ Scan(...any) error }) (*ChatTurn, error) {
	var t ChatTurn
	var u string
	if err := row.Scan(&t.ID, &t.ChatID, &t.Seq, &t.Engine, &t.Model, &t.Effort, &t.Status, &t.Error, &u, &t.RunID,
		&t.StartedAt, &t.FinishedAt); err != nil {
		return nil, mapErr(err)
	}
	if json.Unmarshal([]byte(u), &t.Usage) != nil || t.Usage == nil {
		t.Usage = []usage.Entry{}
	}
	t.Runs = []Run{}
	return &t, nil
}

func (s *Store) CreateChatTurn(ctx context.Context, chatID int64, seq int, set ChatSettings) (*ChatTurn, error) {
	return scanTurn(s.DB.QueryRow(ctx, `INSERT INTO chat_turns (chat_id, seq, engine, model, effort) VALUES ($1, $2, $3, $4, $5)
		RETURNING `+turnCols, chatID, seq, set.Engine, set.Model, set.Effort))
}

func (s *Store) ChatTurnByID(ctx context.Context, id int64) (*ChatTurn, error) {
	return scanTurn(s.DB.QueryRow(ctx, `SELECT `+turnCols+` FROM chat_turns WHERE id = $1`, id))
}

func (s *Store) SetChatTurnRun(ctx context.Context, id, runID int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE chat_turns SET run_id = $2 WHERE id = $1`, id, runID)
	return err
}

// SetChatTurnUsage records what the turn has spent so far.
func (s *Store) SetChatTurnUsage(ctx context.Context, id int64, es []usage.Entry) error {
	if es == nil {
		es = []usage.Entry{}
	}
	raw, _ := json.Marshal(es)
	_, err := s.DB.Exec(ctx, `UPDATE chat_turns SET usage = $2 WHERE id = $1`, id, string(raw))
	return err
}

// FinishChatTurn ends a running turn; false when it had already ended.
func (s *Store) FinishChatTurn(ctx context.Context, id int64, status, errText string) (bool, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE chat_turns SET status = $2, error = $3, finished_at = now()
		WHERE id = $1 AND status = 'running'`, id, status, truncate(errText, 1000))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ChatTurns lists a chat's turns, oldest first, each with its runs.
func (s *Store) ChatTurns(ctx context.Context, chatID int64) ([]ChatTurn, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+turnCols+` FROM chat_turns WHERE chat_id = $1 ORDER BY id`, chatID)
	if err != nil {
		return nil, err
	}
	out := []ChatTurn{}
	idx := map[int64]int{}
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		idx[t.ID] = len(out)
		out = append(out, *t)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	runs, err := s.DB.Query(ctx, runSelect+` WHERE r.chat_turn_id IN (SELECT id FROM chat_turns WHERE chat_id = $1) ORDER BY r.id`, chatID)
	if err != nil {
		return nil, err
	}
	defer runs.Close()
	for runs.Next() {
		r, err := scanRun(runs)
		if err != nil {
			return nil, err
		}
		if i, ok := idx[*r.ChatTurnID]; ok {
			out[i].Runs = append(out[i].Runs, r.Public())
		}
	}
	return out, runs.Err()
}

// RunningChatTurn is the chat's unfinished turn, if any.
func (s *Store) RunningChatTurn(ctx context.Context, chatID int64) (*ChatTurn, error) {
	return scanTurn(s.DB.QueryRow(ctx, `SELECT `+turnCols+` FROM chat_turns WHERE chat_id = $1 AND status = 'running'
		ORDER BY id DESC LIMIT 1`, chatID))
}

// LastSessionRun is the chat's latest Claude Code session run that has a
// session to resume.
func (s *Store) LastSessionRun(ctx context.Context, chatID int64) (*Run, error) {
	return scanRun(s.DB.QueryRow(ctx, runSelect+` WHERE r.repo_id IS NULL AND r.session_id <> ''
		AND r.chat_turn_id IN (SELECT id FROM chat_turns WHERE chat_id = $1) ORDER BY r.id DESC LIMIT 1`, chatID))
}
