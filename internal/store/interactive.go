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
// A prompt is answered once: a second answer is a conflict. A question left
// pending when its session parked (the run is waiting) can still be
// answered: the answer becomes the reply that resumes the session in place
// (requeued reports that; the caller wakes the machine).
func (s *Store) AnswerPrompt(ctx context.Context, runID, promptID int64, in PromptAnswer) (p *RunPrompt, requeued bool, err error) {
	run, err := s.RunByID(ctx, runID)
	if err != nil {
		return nil, false, err
	}
	parked := run.Status == "waiting"
	if parked && !s.resumesInPlace(ctx, run.RunnerID) {
		return nil, false, invalid("decision", "%s runs an older Forge agent that cannot pick this session back up; upgrade it, or use Continue", run.RunnerName)
	}
	if !parked {
		if _, err := s.interactiveRun(ctx, runID, 0); err != nil {
			return nil, false, err
		}
	}
	if p, err = scanPrompt(s.DB.QueryRow(ctx, promptSelect+` WHERE id = $1 AND run_id = $2`, promptID, runID)); err != nil {
		return nil, false, err
	}
	if !OneOf(in.Decision, PromptDecisions[p.Kind]) {
		return nil, false, invalid("decision", "must be one of %s", strings.Join(PromptDecisions[p.Kind], ", "))
	}
	in.Message = strings.TrimSpace(in.Message)
	if len(in.Message) > 20000 {
		return nil, false, invalid("message", "too long")
	}
	if in.Decision == "answer" {
		if len(in.Answers) == 0 {
			return nil, false, invalid("answers", "answer at least one question")
		}
		for q, a := range in.Answers {
			if strings.TrimSpace(a) == "" || len(q) > 2000 || len(a) > 5000 {
				return nil, false, invalid("answers", "every answer needs some text")
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
		if !parked {
			_, err = tx.Exec(ctx, `INSERT INTO run_inbox (run_id, kind, data) VALUES ($1, 'answer', $2)`, runID, string(item))
			return err
		}
		// The session that asked is gone: resume it with the answer as the reply.
		var last int64
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM run_inbox WHERE run_id = $1`, runID).Scan(&last); err != nil {
			return err
		}
		if last < run.InboxAfter {
			last = run.InboxAfter
		}
		if err := requeueTx(ctx, tx, runID, last); err != nil {
			return err
		}
		msg, _ := json.Marshal(map[string]string{"text": answerText(p, in)})
		_, err = tx.Exec(ctx, `INSERT INTO run_inbox (run_id, kind, data) VALUES ($1, 'message', $2)`, runID, string(msg))
		return err
	})
	if err != nil {
		return nil, false, err
	}
	p, err = scanPrompt(s.DB.QueryRow(ctx, promptSelect+` WHERE id = $1`, promptID))
	return p, parked && err == nil, err
}

// answerText says an answer in words, for a session resumed after it parked
// with the question still open (the tool call that asked is gone with it).
func answerText(p *RunPrompt, a PromptAnswer) string {
	note := ""
	if a.Message != "" {
		note = " " + a.Message
	}
	switch p.Kind {
	case "question":
		if a.Decision != "answer" {
			return "I'm not answering that question; continue with your best judgement." + note
		}
		var order []string
		var in struct {
			Questions []struct {
				Question string `json:"question"`
			} `json:"questions"`
		}
		_ = json.Unmarshal(p.Input, &in)
		for _, q := range in.Questions {
			order = append(order, q.Question)
		}
		for q := range a.Answers {
			if !OneOf(q, order) {
				order = append(order, q)
			}
		}
		var b strings.Builder
		b.WriteString("My answers to your questions (the session was paused while you asked):")
		for _, q := range order {
			if ans, ok := a.Answers[q]; ok {
				fmt.Fprintf(&b, "\n- %s → %s", q, ans)
			}
		}
		return b.String()
	case "plan":
		switch a.Decision {
		case "approve":
			return "Plan approved: go ahead (ask before edits)."
		case "approve_edits":
			return "Plan approved: go ahead and make the edits."
		}
		return "Keep planning; don't start yet." + note
	default:
		switch a.Decision {
		case "allow", "allow_always":
			return fmt.Sprintf("Yes, go ahead with %s (it was waiting for my approval when the session paused).", p.ToolName)
		}
		return fmt.Sprintf("No, don't use %s.", p.ToolName) + note
	}
}

// SendRunMessage queues a follow-up message for the session. A waiting run
// (its session parked) is queued again to resume in place on its machine;
// it reports whether that happened (the caller wakes the machine).
func (s *Store) SendRunMessage(ctx context.Context, runID int64, text string) (requeued bool, err error) {
	run, err := s.RunByID(ctx, runID)
	if err != nil {
		return false, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return false, invalid("text", "write a message")
	}
	if len(text) > 100000 {
		return false, invalid("text", "too long")
	}
	switch {
	case run.Status == "waiting":
		if !s.resumesInPlace(ctx, run.RunnerID) {
			return false, invalid("text", "%s runs an older Forge agent that cannot pick this session back up; upgrade it, or use Continue", run.RunnerName)
		}
	case run.Status == "queued" && run.Resumes > 0:
		// A resume already on its way: the message joins it.
	default:
		if _, err := s.interactiveRun(ctx, runID, 0); err != nil {
			return false, err
		}
	}
	data, _ := json.Marshal(map[string]string{"text": text})
	err = s.tx(ctx, func(tx pgxTx) error {
		if run.Status == "waiting" {
			var last int64
			if err := tx.QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM run_inbox WHERE run_id = $1`, runID).Scan(&last); err != nil {
				return err
			}
			if last < run.InboxAfter {
				last = run.InboxAfter
			}
			if err := requeueTx(ctx, tx, runID, last); err != nil {
				return err
			}
			requeued = true
		}
		if _, err := tx.Exec(ctx, `INSERT INTO run_inbox (run_id, kind, data) VALUES ($1, 'message', $2)`, runID, string(data)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE runs SET awaiting = '' WHERE id = $1`, runID)
		return err
	})
	return requeued, err
}

// EndRunSession is Finish from the session's own controls (the composer,
// Telegram): see FinishRunByUser.
func (s *Store) EndRunSession(ctx context.Context, runID int64) error {
	_, err := s.FinishRunByUser(ctx, runID)
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
