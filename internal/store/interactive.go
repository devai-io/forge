package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Interactive runs. The machine keeps the Claude Code session open; what the
// session asks (a question, a tool permission, a plan to approve) becomes a
// RunPrompt the web app shows, and what the user says back (an answer, a
// follow-up message, "end the session") goes into the run's inbox, which the
// machine long-polls. The machine builds Claude Code's actual reply from an
// answer and its own rules, so the server never decides what is allowed.

// PromptKinds: AskUserQuestion, a tool permission, ExitPlanMode.
var PromptKinds = []string{"question", "permission", "plan"}

// PromptDecisions are the answers each kind accepts ("answer" carries the
// question's answers; the rest are buttons).
var PromptDecisions = map[string][]string{
	"question":   {"answer", "deny"},
	"permission": {"allow", "allow_always", "deny"},
	"plan":       {"approve", "approve_edits", "deny"},
}

type RunPrompt struct {
	ID          int64           `json:"id"`
	RunID       int64           `json:"run_id"`
	RequestID   string          `json:"request_id"`
	Kind        string          `json:"kind"`
	ToolName    string          `json:"tool_name"`
	Input       json.RawMessage `json:"input"`
	Suggestions json.RawMessage `json:"suggestions"`
	Description string          `json:"description"`
	Status      string          `json:"status"` // pending | answered | expired
	Answer      json.RawMessage `json:"answer"` // the PromptAnswer, once answered
	CreatedAt   time.Time       `json:"created_at"`
	AnsweredAt  *time.Time      `json:"answered_at"`
}

// PromptInput is what the machine reports when the session asks something.
type PromptInput struct {
	RequestID   string          `json:"request_id"`
	ToolName    string          `json:"tool_name"`
	Input       json.RawMessage `json:"input"`
	Suggestions json.RawMessage `json:"suggestions"`
	Description string          `json:"description"`
}

// PromptAnswer is the user's reply to a prompt.
type PromptAnswer struct {
	Decision string `json:"decision"`
	// Answers: question text → the chosen label(s) (comma-separated for a
	// multi-select) or the user's own words.
	Answers map[string]string `json:"answers,omitempty"`
	// Message goes back to the model: why a permission or plan was declined,
	// or what to change.
	Message string `json:"message,omitempty"`
}

// InboxItem is one thing for the machine: an answer (with the request it
// answers), a follow-up message, or "end".
type InboxItem struct {
	ID   int64           `json:"id"`
	Kind string          `json:"kind"` // answer | message | end
	Data json.RawMessage `json:"data"`
}

// PromptKind classifies a tool permission request by the tool asking.
func PromptKind(toolName string) string {
	switch toolName {
	case "AskUserQuestion":
		return "question"
	case "ExitPlanMode":
		return "plan"
	}
	return "permission"
}

const promptSelect = `SELECT id, run_id, request_id, kind, tool_name, input, suggestions, description, status, answer,
       created_at, answered_at FROM run_prompts`

func scanPrompt(row interface{ Scan(...any) error }) (*RunPrompt, error) {
	var p RunPrompt
	var input, suggestions, answer string
	if err := row.Scan(&p.ID, &p.RunID, &p.RequestID, &p.Kind, &p.ToolName, &input, &suggestions, &p.Description,
		&p.Status, &answer, &p.CreatedAt, &p.AnsweredAt); err != nil {
		return nil, mapErr(err)
	}
	p.Input, p.Suggestions, p.Answer = json.RawMessage(input), json.RawMessage(suggestions), json.RawMessage(answer)
	return &p, nil
}

// interactiveRun is a running interactive run of this runner (runnerID 0:
// any runner, for the browser side).
func (s *Store) interactiveRun(ctx context.Context, runID, runnerID int64) (*Run, error) {
	run, err := s.RunByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if runnerID != 0 && run.RunnerID != runnerID {
		return nil, ErrNotFound
	}
	if !run.Interactive {
		return nil, invalid("run", "this run is not interactive")
	}
	if run.Status != "running" {
		return nil, invalid("run", "this run is %s", run.Status)
	}
	return run, nil
}

// AddPrompt records what the session asks. The same request reported twice
// (a retry) is stored once.
func (s *Store) AddPrompt(ctx context.Context, runID, runnerID int64, in PromptInput) (*RunPrompt, error) {
	if _, err := s.interactiveRun(ctx, runID, runnerID); err != nil {
		return nil, err
	}
	in.RequestID = strings.TrimSpace(in.RequestID)
	if in.RequestID == "" || len(in.RequestID) > 200 {
		return nil, invalid("request_id", "is required")
	}
	if len(in.Input) == 0 || !json.Valid(in.Input) {
		in.Input = json.RawMessage("{}")
	}
	if len(in.Input) > maxEventBytes {
		return nil, invalid("input", "too large")
	}
	if len(in.Suggestions) == 0 || !json.Valid(in.Suggestions) || len(in.Suggestions) > 64<<10 {
		in.Suggestions = json.RawMessage("[]")
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO run_prompts (run_id, request_id, kind, tool_name, input, suggestions, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (run_id, request_id) DO NOTHING`,
		runID, in.RequestID, PromptKind(in.ToolName), truncate(in.ToolName, 200), string(in.Input),
		string(in.Suggestions), truncate(in.Description, 2000)); err != nil {
		return nil, err
	}
	return scanPrompt(s.DB.QueryRow(ctx, promptSelect+` WHERE run_id = $1 AND request_id = $2`, runID, in.RequestID))
}

// ListPrompts is every prompt of a run, oldest first.
func (s *Store) ListPrompts(ctx context.Context, runID int64) ([]RunPrompt, error) {
	rows, err := s.DB.Query(ctx, promptSelect+` WHERE run_id = $1 ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunPrompt{}
	for rows.Next() {
		p, err := scanPrompt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// AnswerPrompt records the user's answer and queues it for the machine.
// A prompt is answered once: a second answer is a conflict.
func (s *Store) AnswerPrompt(ctx context.Context, runID, promptID int64, in PromptAnswer) (*RunPrompt, error) {
	if _, err := s.interactiveRun(ctx, runID, 0); err != nil {
		return nil, err
	}
	p, err := scanPrompt(s.DB.QueryRow(ctx, promptSelect+` WHERE id = $1 AND run_id = $2`, promptID, runID))
	if err != nil {
		return nil, err
	}
	if !OneOf(in.Decision, PromptDecisions[p.Kind]) {
		return nil, invalid("decision", "must be one of %s", strings.Join(PromptDecisions[p.Kind], ", "))
	}
	in.Message = strings.TrimSpace(in.Message)
	if len(in.Message) > 20000 {
		return nil, invalid("message", "too long")
	}
	if in.Decision == "answer" {
		if len(in.Answers) == 0 {
			return nil, invalid("answers", "answer at least one question")
		}
		for q, a := range in.Answers {
			if strings.TrimSpace(a) == "" || len(q) > 2000 || len(a) > 5000 {
				return nil, invalid("answers", "every answer needs some text")
			}
		}
	} else {
		in.Answers = nil
	}
	answer, _ := json.Marshal(in)
	item, _ := json.Marshal(map[string]any{"request_id": p.RequestID, "answer": in})
	err = s.tx(ctx, func(tx pgxTx) error {
		tag, err := tx.Exec(ctx, `UPDATE run_prompts SET status = 'answered', answer = $2, answered_at = now()
			WHERE id = $1 AND status = 'pending'`, promptID, string(answer))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: this prompt was already answered", ErrConflict)
		}
		_, err = tx.Exec(ctx, `INSERT INTO run_inbox (run_id, kind, data) VALUES ($1, 'answer', $2)`, runID, string(item))
		return err
	})
	if err != nil {
		return nil, err
	}
	return scanPrompt(s.DB.QueryRow(ctx, promptSelect+` WHERE id = $1`, promptID))
}

// SendRunMessage queues a follow-up message for the session.
func (s *Store) SendRunMessage(ctx context.Context, runID int64, text string) error {
	if _, err := s.interactiveRun(ctx, runID, 0); err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return invalid("text", "write a message")
	}
	if len(text) > 100000 {
		return invalid("text", "too long")
	}
	data, _ := json.Marshal(map[string]string{"text": text})
	return s.tx(ctx, func(tx pgxTx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO run_inbox (run_id, kind, data) VALUES ($1, 'message', $2)`, runID, string(data)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE runs SET awaiting = '' WHERE id = $1`, runID)
		return err
	})
}

// EndRunSession asks the machine to close the session once the current
// turn is done; the run then finishes normally.
func (s *Store) EndRunSession(ctx context.Context, runID int64) error {
	if _, err := s.interactiveRun(ctx, runID, 0); err != nil {
		return err
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO run_inbox (run_id, kind) VALUES ($1, 'end')`, runID)
	return err
}

// Inbox is what the machine has not read yet (after the last id it holds).
// done is true once the run is no longer running: nothing more will come.
func (s *Store) Inbox(ctx context.Context, runID, runnerID, after int64) (items []InboxItem, done bool, err error) {
	run, err := s.RunByID(ctx, runID)
	if err != nil {
		return nil, false, err
	}
	if run.RunnerID != runnerID {
		return nil, false, ErrNotFound
	}
	rows, err := s.DB.Query(ctx, `SELECT id, kind, data FROM run_inbox WHERE run_id = $1 AND id > $2 ORDER BY id LIMIT 100`,
		runID, after)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items = []InboxItem{}
	for rows.Next() {
		var it InboxItem
		var data string
		if err := rows.Scan(&it.ID, &it.Kind, &data); err != nil {
			return nil, false, err
		}
		it.Data = json.RawMessage(data)
		items = append(items, it)
	}
	return items, run.Status != "running", rows.Err()
}

// SetAwaiting is the machine saying the session finished a turn ("reply")
// or is working again ("").
func (s *Store) SetAwaiting(ctx context.Context, runID, runnerID int64, awaiting string) error {
	if !OneOf(awaiting, []string{"", "reply"}) {
		return invalid("awaiting", "must be reply or empty")
	}
	tag, err := s.DB.Exec(ctx, `UPDATE runs SET awaiting = $3 WHERE id = $1 AND runner_id = $2 AND status = 'running'`,
		runID, runnerID, awaiting)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ExpirePrompt marks a prompt the session withdrew (it was interrupted).
func (s *Store) ExpirePrompt(ctx context.Context, runID, runnerID int64, requestID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE run_prompts SET status = 'expired'
		WHERE run_id = $1 AND request_id = $2 AND status = 'pending'
		  AND run_id IN (SELECT id FROM runs WHERE runner_id = $3)`, runID, requestID, runnerID)
	return err
}
