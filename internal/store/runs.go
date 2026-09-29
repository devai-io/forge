package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// A runner is online if it has heartbeated within this window. The runner
// heartbeats every 10 s (which is also how fast a cancel reaches it), so
// 90 s tolerates several lost beats.
const onlineWindow = "90" // seconds

// ── Runners ───────────────────────────────────────────────────────────────

const runnerSelect = `
SELECT rn.id, rn.name, rn.role, rn.hostname, rn.os, rn.version,
       coalesce(rn.last_seen_at > ts_add(now(), -` + onlineWindow + `), 0),
       rn.last_seen_at, rn.capabilities, rn.created_at, rn.pair_expires_at,
       (SELECT count(*) FROM runs r WHERE r.runner_id = rn.id AND r.status = 'running')
FROM runners rn`

func scanRunner(row interface{ Scan(...any) error }) (*Runner, error) {
	var r Runner
	var caps []byte
	if err := row.Scan(&r.ID, &r.Name, &r.Role, &r.Hostname, &r.OS, &r.Version, &r.Online, &r.LastSeenAt, &caps,
		&r.CreatedAt, &r.PairExpiresAt, &r.Running); err != nil {
		return nil, mapErr(err)
	}
	_ = json.Unmarshal(caps, &r.Capabilities)
	r.Capabilities.Commands = nonNil(r.Capabilities.Commands)
	r.Capabilities.PermissionModes = nonNil(r.Capabilities.PermissionModes)
	r.Capabilities.CommandDetails = nonNil(r.Capabilities.CommandDetails)
	for i := range r.Capabilities.CommandDetails {
		r.Capabilities.CommandDetails[i].Repos = nonNil(r.Capabilities.CommandDetails[i].Repos)
	}
	return &r, nil
}

func (s *Store) ListRunners(ctx context.Context) ([]Runner, error) {
	rows, err := s.DB.Query(ctx, runnerSelect+` ORDER BY (rn.role = 'master') DESC, rn.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Runner{}
	for rows.Next() {
		r, err := scanRunner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) RunnerByID(ctx context.Context, id int64) (*Runner, error) {
	return scanRunner(s.DB.QueryRow(ctx, runnerSelect+` WHERE rn.id = $1`, id))
}

func (s *Store) RunnerByToken(ctx context.Context, tokenHash []byte) (*Runner, error) {
	return scanRunner(s.DB.QueryRow(ctx, runnerSelect+` WHERE rn.token_hash = $1`, tokenHash))
}

func (s *Store) CreateRunner(ctx context.Context, name string, tokenHash []byte) (*Runner, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return nil, invalid("name", "must be 1-60 characters")
	}
	var id int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO runners (name, token_hash) VALUES ($1, $2) RETURNING id`,
		name, tokenHash).Scan(&id); err != nil {
		return nil, mapErr(err)
	}
	return s.RunnerByID(ctx, id)
}

func (s *Store) RotateRunnerToken(ctx context.Context, id int64, tokenHash []byte) error {
	tag, err := s.DB.Exec(ctx, `UPDATE runners SET token_hash = $2 WHERE id = $1`, id, tokenHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPairCode issues a pairing code for a runner (replacing any earlier
// one); it stays usable for ttl. The runner's current token keeps working
// until the code is claimed.
func (s *Store) SetPairCode(ctx context.Context, id int64, codeHash []byte, ttl time.Duration) (time.Time, error) {
	var until time.Time
	err := s.DB.QueryRow(ctx, `UPDATE runners SET pair_code_hash = $2, pair_expires_at = ts_add(now(), $3)
		WHERE id = $1 RETURNING pair_expires_at`, id, codeHash, ttl.Seconds()).Scan(&until)
	return until, mapErr(err)
}

// ClaimPairCode swaps a live pairing code for a new token: the code is
// burned, the token replaces the runner's old one. ErrNotFound for an
// unknown or expired code.
func (s *Store) ClaimPairCode(ctx context.Context, codeHash, tokenHash []byte, hostname, os string) (*Runner, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `UPDATE runners SET token_hash = $2, pair_code_hash = NULL, pair_expires_at = NULL,
		hostname = CASE WHEN $3 = '' THEN hostname ELSE $3 END, os = CASE WHEN $4 = '' THEN os ELSE $4 END
		WHERE pair_code_hash = $1 AND pair_expires_at > now() RETURNING id`,
		codeHash, tokenHash, truncate(hostname, 200), truncate(os, 100)).Scan(&id)
	if err != nil {
		return nil, mapErr(err)
	}
	return s.RunnerByID(ctx, id)
}

// SetRunnerRole changes a runner's role; making one the master demotes the
// previous master to worker in the same transaction (there is exactly one).
func (s *Store) SetRunnerRole(ctx context.Context, id int64, role string) (*Runner, error) {
	if !OneOf(role, RunnerRoles) {
		return nil, invalid("role", "must be master, ios or worker")
	}
	err := s.tx(ctx, func(tx pgxTx) error {
		if role == "master" {
			if _, err := tx.Exec(ctx, `UPDATE runners SET role = 'worker' WHERE role = 'master' AND id <> $1`, id); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE runners SET role = $2 WHERE id = $1`, id, role)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.RunnerByID(ctx, id)
}

// MasterRunner is the always-on primary machine, if one is elected.
func (s *Store) MasterRunner(ctx context.Context) (*Runner, error) {
	return scanRunner(s.DB.QueryRow(ctx, runnerSelect+` WHERE rn.role = 'master'`))
}

// RunnerTokenHash is the stored hash of a runner's token — also the shared
// secret forge-api presents to that runner's VS Code gateway (the runner can
// compute it from its token; nobody else has either).
func (s *Store) RunnerTokenHash(ctx context.Context, id int64) ([]byte, error) {
	var h []byte
	err := s.DB.QueryRow(ctx, `SELECT token_hash FROM runners WHERE id = $1`, id).Scan(&h)
	return h, mapErr(err)
}

// DeleteRunner removes a runner and cancels what was still waiting for it.
func (s *Store) DeleteRunner(ctx context.Context, id int64) error {
	return s.tx(ctx, func(tx pgxTx) error {
		if _, err := tx.Exec(ctx, `UPDATE runs SET status = 'cancelled', error = 'runner deleted', finished_at = now()
			WHERE runner_id = $1 AND status IN ('queued', 'running')`, id); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM runners WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

type HeartbeatInput struct {
	Hostname     string             `json:"hostname"`
	OS           string             `json:"os"`
	Version      string             `json:"version"`
	Capabilities RunnerCapabilities `json:"capabilities"`
	Running      []int64            `json:"running"`
	Tmux         []TmuxSession      `json:"tmux"`
}

// Heartbeat records the runner as alive and returns the runs it must kill.
//
// It also reconciles: a run the server thinks is running on this runner, but
// which the runner no longer reports, was lost (the runner restarted mid-run).
// The one-minute grace covers a run claimed between the runner building its
// list and this request arriving.
func (s *Store) Heartbeat(ctx context.Context, runnerID int64, in HeartbeatInput) ([]int64, error) {
	in.Capabilities.Commands = nonNil(in.Capabilities.Commands)
	in.Capabilities.PermissionModes = nonNil(in.Capabilities.PermissionModes)
	in.Capabilities.CommandDetails = nonNil(in.Capabilities.CommandDetails)
	running := nonNil(in.Running)
	if _, err := s.DB.Exec(ctx, `UPDATE runners SET hostname = $2, os = $3, version = $4, capabilities = $5,
		last_seen_at = now() WHERE id = $1`, runnerID, truncate(in.Hostname, 200), truncate(in.OS, 100),
		truncate(in.Version, 100), in.Capabilities); err != nil {
		return nil, err
	}
	if err := s.saveTmux(ctx, runnerID, in.Tmux); err != nil {
		return nil, err
	}
	if _, err := s.DB.Exec(ctx, `UPDATE runs SET status = 'failed', finished_at = now(),
		error = 'the runner restarted and lost this run'
		WHERE runner_id = $1 AND status = 'running' AND started_at < ts_add(now(), -60)
		  AND id NOT IN (SELECT value FROM json_each($2))`, runnerID, running); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT id FROM runs WHERE runner_id = $1 AND status = 'running' AND cancel_requested`, runnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cancel := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		cancel = append(cancel, id)
	}
	return cancel, rows.Err()
}

// FailOrphanedRuns ends runs whose runner has gone silent for ten minutes.
func (s *Store) FailOrphanedRuns(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `UPDATE runs SET status = 'failed', finished_at = now(), error = 'runner went offline'
		WHERE status = 'running' AND runner_id IN (
			SELECT id FROM runners WHERE last_seen_at IS NULL OR last_seen_at < ts_add(now(), -600))`)
	return err
}

// ── Runs ──────────────────────────────────────────────────────────────────

const runSelect = `
SELECT r.id, coalesce(r.runner_id, 0), coalesce(rn.name, r.runner_name), coalesce(r.project_id, 0),
       coalesce(p.key, ''), coalesce(p.color, ''), coalesce(r.repo_id, 0), coalesce(rp.name, ''), r.task_id,
       CASE WHEN t.id IS NULL THEN NULL ELSE tp.key || '-' || t.number END,
       r.kind, r.prompt, r.command, r.permission_mode, r.model, r.worktree, r.resume_run_id, r.status,
       r.cancel_requested, r.session_id, r.result, r.error, r.exit_code, r.cost_usd, r.num_turns, r.duration_ms,
       r.created_at, r.started_at, r.finished_at, coalesce(rp.path, ''), r.resume_session, r.model_note, r.engine,
       r.effort, r.chat_turn_id, r.usage, r.append_system
FROM runs r
LEFT JOIN projects p ON p.id = r.project_id
LEFT JOIN repos rp ON rp.id = r.repo_id
LEFT JOIN runners rn ON rn.id = r.runner_id
LEFT JOIN tasks t ON t.id = r.task_id
LEFT JOIN projects tp ON tp.id = t.project_id`

func scanRun(row interface{ Scan(...any) error }) (*Run, error) {
	var r Run
	var usage string
	if err := row.Scan(&r.ID, &r.RunnerID, &r.RunnerName, &r.ProjectID, &r.ProjectKey, &r.ProjectColor,
		&r.RepoID, &r.RepoName, &r.TaskID, &r.TaskRef, &r.Kind, &r.Prompt, &r.Command, &r.PermissionMode,
		&r.Model, &r.Worktree, &r.ResumeRunID, &r.Status, &r.CancelRequested, &r.SessionID, &r.Result, &r.Error,
		&r.ExitCode, &r.CostUSD, &r.NumTurns, &r.DurationMS, &r.CreatedAt, &r.StartedAt, &r.FinishedAt,
		&r.RepoPath, &r.ResumeSession, &r.ModelNote, &r.Engine, &r.Effort, &r.ChatTurnID, &usage, &r.AppendSystem); err != nil {
		return nil, mapErr(err)
	}
	r.Usage = json.RawMessage(usage)
	return &r, nil
}

// Public strips the fields only a runner needs.
func (r Run) Public() Run {
	r.RepoPath, r.ResumeSession, r.AppendSystem = "", "", ""
	return r
}

type RunFilter struct {
	ProjectKey string
	TaskID     int64
	Statuses   []string
	Finished   bool
	Limit      int
	// Assistant sessions (Claude Code turns of a chat, no repo) are left out
	// of run lists; they show in their chat.
}

func (s *Store) ListRuns(ctx context.Context, f RunFilter) ([]Run, error) {
	where := []string{"r.repo_id IS NOT NULL"}
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if f.ProjectKey != "" {
		where = append(where, "p.key = "+arg(strings.ToUpper(f.ProjectKey)))
	}
	if f.TaskID != 0 {
		where = append(where, "r.task_id = "+arg(f.TaskID))
	}
	if len(f.Statuses) > 0 {
		where = append(where, "r.status IN (SELECT value FROM json_each("+arg(f.Statuses)+"))")
	}
	if f.Finished {
		where = append(where, "r.finished_at IS NOT NULL")
	}
	q := runSelect + " WHERE " + strings.Join(where, " AND ")
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	order := "r.id DESC"
	if f.Finished {
		order = "r.finished_at DESC"
	}
	q += fmt.Sprintf(" ORDER BY %s LIMIT %d", order, f.Limit)
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r.Public())
	}
	return out, rows.Err()
}

func (s *Store) RunByID(ctx context.Context, id int64) (*Run, error) {
	return scanRun(s.DB.QueryRow(ctx, runSelect+` WHERE r.id = $1`, id))
}

type RunInput struct {
	RunnerID       int64  `json:"runner_id"`
	RepoID         int64  `json:"repo_id"`
	Kind           string `json:"kind"`
	Prompt         string `json:"prompt"`
	Command        string `json:"command"`
	PermissionMode string `json:"permission_mode"`
	Model          string `json:"model"`
	Worktree       bool   `json:"worktree"`
	TaskID         *int64 `json:"task_id"`
	ResumeRunID    *int64 `json:"resume_run_id"`
	Confirmed      bool   `json:"confirmed"`
	// Engine: "claude" or "deepseek" for an agent run; empty means the
	// server's default (settled before CreateRun, see api.prepareRun).
	Engine string `json:"engine"`
	// ModelNote says why Model was chosen, when Forge chose it (set by the
	// server, never by the client).
	ModelNote string `json:"-"`
	// Effort: claude --effort for an agent run ("" = Claude Code's default).
	Effort string `json:"effort"`
	// ChatTurnID links a run to the Assistant turn that queued it. A run with
	// a turn and no repo (RepoID 0) is that turn's Claude Code session, in the
	// machine's home folder. Set by the server only.
	ChatTurnID *int64 `json:"-"`
	// AppendSystem is added to Claude Code's system prompt (Assistant
	// sessions); set by the server only.
	AppendSystem string `json:"-"`
}

// CreateRun validates a run against what its runner says it can do, then
// queues it. Validation happens here rather than on the runner so a bad
// request fails in the browser, not minutes later on a machine elsewhere.
func (s *Store) CreateRun(ctx context.Context, in RunInput) (*Run, error) {
	var resumeSession string
	if in.ResumeRunID != nil {
		prev, err := s.RunByID(ctx, *in.ResumeRunID)
		if err != nil {
			return nil, invalid("resume_run_id", "no such run")
		}
		if prev.SessionID == "" {
			return nil, invalid("resume_run_id", "that run has no Claude session to continue")
		}
		if prev.RunnerID == 0 {
			return nil, invalid("resume_run_id", "that run's runner no longer exists")
		}
		in.RunnerID, in.RepoID, in.Kind = prev.RunnerID, prev.RepoID, "agent"
		in.Worktree = false // --resume reopens the session where it ran
		if in.TaskID == nil {
			in.TaskID = prev.TaskID
		}
		if in.PermissionMode == "" {
			in.PermissionMode = prev.PermissionMode
		}
		if in.Model == "" && in.ChatTurnID == nil { // a chat turn says its model itself ("" = default)
			in.Model = prev.Model
		}
		if in.Engine == "" {
			in.Engine = prev.Engine
		}
		resumeSession = prev.SessionID
	}

	runner, err := s.RunnerByID(ctx, in.RunnerID)
	if err != nil {
		return nil, invalid("runner_id", "no such runner")
	}
	// An Assistant session (a chat turn on Claude Code) runs in the machine's
	// home folder instead of a repo.
	home := in.RepoID == 0 && in.ChatTurnID != nil
	var repo *Repo
	var projectID, repoID *int64
	if !home {
		if repo, err = s.RepoByID(ctx, in.RepoID); err != nil {
			return nil, invalid("repo_id", "no such repo")
		}
		if repo.Path == "" {
			return nil, invalid("repo_id", "this repo has no local path to run in")
		}
		projectID, repoID = &repo.ProjectID, &repo.ID
	} else if in.Kind != "agent" {
		return nil, invalid("kind", "an assistant session is an agent run")
	}

	in.Prompt = strings.TrimSpace(in.Prompt)
	in.Model = strings.TrimSpace(in.Model)
	caps := runner.Capabilities
	switch in.Kind {
	case "agent":
		if in.Prompt == "" {
			return nil, invalid("prompt", "an agent run needs a prompt")
		}
		if len(in.Prompt) > 100000 {
			return nil, invalid("prompt", "too long")
		}
		if in.PermissionMode == "" {
			in.PermissionMode = "plan"
		}
		if !OneOf(in.PermissionMode, PermissionModes) {
			return nil, invalid("permission_mode", "must be one of %s", strings.Join(PermissionModes, ", "))
		}
		// A runner that has never heartbeated advertises nothing; let the run
		// queue and have the runner refuse it, rather than blocking setup.
		if runner.LastSeenAt != nil && !OneOf(in.PermissionMode, caps.PermissionModes) {
			return nil, invalid("permission_mode", "runner %s does not allow %s", runner.Name, in.PermissionMode)
		}
		if runner.LastSeenAt != nil && !caps.Claude {
			return nil, invalid("runner_id", "runner %s has no claude binary", runner.Name)
		}
		if in.Engine == "" {
			in.Engine = "claude"
		}
		if !OneOf(in.Engine, Engines) {
			return nil, invalid("engine", "must be one of %s", strings.Join(Engines, ", "))
		}
		if in.Effort != "" && !OneOf(in.Effort, ClaudeEfforts) {
			return nil, invalid("effort", "must be one of %s", strings.Join(ClaudeEfforts, ", "))
		}
		in.Command = ""
	case "command":
		in.Command = strings.TrimSpace(in.Command)
		if in.Command == "" {
			return nil, invalid("command", "pick a command")
		}
		if runner.LastSeenAt != nil {
			detail, ok := caps.Command(in.Command)
			if !ok {
				return nil, invalid("command", "runner %s has no command %q", runner.Name, in.Command)
			}
			if len(detail.Repos) > 0 && !OneOf(repo.Name, detail.Repos) {
				return nil, invalid("command", "%q only runs in %s", in.Command, strings.Join(detail.Repos, ", "))
			}
			if detail.Confirm && !in.Confirmed {
				return nil, invalid("confirmed", "%q needs explicit confirmation", in.Command)
			}
		}
		in.PermissionMode, in.Worktree, in.Model, in.Engine, in.Effort = "", false, "", "", ""
	default:
		return nil, invalid("kind", "must be agent or command")
	}
	if in.TaskID != nil {
		if _, err := s.TaskByID(ctx, *in.TaskID); err != nil {
			return nil, invalid("task_id", "no such task")
		}
	}

	var id int64
	err = s.tx(ctx, func(tx pgxTx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO runs (runner_id, runner_name, project_id, repo_id, task_id, kind, prompt, command,
			                  permission_mode, model, worktree, resume_run_id, resume_session, model_note, engine, effort, chat_turn_id,
			                  append_system)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18) RETURNING id`,
			runner.ID, runner.Name, projectID, repoID, in.TaskID, in.Kind, in.Prompt, in.Command,
			in.PermissionMode, in.Model, in.Worktree, in.ResumeRunID, resumeSession, in.ModelNote, in.Engine,
			in.Effort, in.ChatTurnID, in.AppendSystem).Scan(&id); err != nil {
			return mapErr(err)
		}
		if home {
			return nil // an assistant turn, not board activity
		}
		what := in.Command
		if in.Kind == "agent" {
			what = excerpt(in.Prompt, 80)
		}
		return logActivity(ctx, tx, ActivityInput{ProjectID: projectID, TaskID: in.TaskID, RunID: &id,
			Kind: "run.queued", Summary: fmt.Sprintf("Run #%d on %s (%s): %s", id, repo.Name, runner.Name, what)})
	})
	if err != nil {
		return nil, err
	}
	return s.RunByID(ctx, id)
}

// CancelRun cancels a queued run outright; a running one is flagged and the
// runner kills it on its next heartbeat.
func (s *Store) CancelRun(ctx context.Context, id int64) (*Run, error) {
	if _, err := s.DB.Exec(ctx, `UPDATE runs SET
		status = CASE WHEN status = 'queued' THEN 'cancelled' ELSE status END,
		finished_at = CASE WHEN status = 'queued' THEN now() ELSE finished_at END,
		cancel_requested = CASE WHEN status = 'running' THEN true ELSE cancel_requested END
		WHERE id = $1`, id); err != nil {
		return nil, err
	}
	return s.RunByID(ctx, id)
}

// ClaimRun hands the oldest queued run for this runner over to it.
func (s *Store) ClaimRun(ctx context.Context, runnerID int64) (*Run, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `
		UPDATE runs SET status = 'running', started_at = now()
		WHERE id = (SELECT id FROM runs WHERE runner_id = $1 AND status = 'queued'
		            ORDER BY id LIMIT 1)
		RETURNING id`, runnerID).Scan(&id)
	if err != nil {
		return nil, mapErr(err)
	}
	return s.RunByID(ctx, id)
}

type EventInput struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// maxEventBytes bounds one event; a larger one is replaced by a note saying
// so. Tool results (a whole file read) are the usual offenders.
const maxEventBytes = 256 << 10

// AppendEvents stores a batch in order. The counter on the run row assigns
// sequence numbers and, being a row lock, serialises concurrent batches.
func (s *Store) AppendEvents(ctx context.Context, runID, runnerID int64, events []EventInput) error {
	if len(events) == 0 {
		return nil
	}
	return s.tx(ctx, func(tx pgxTx) error {
		var last int
		err := tx.QueryRow(ctx, `UPDATE runs SET event_seq = event_seq + $3
			WHERE id = $1 AND runner_id = $2 RETURNING event_seq`, runID, runnerID, len(events)).Scan(&last)
		if err != nil {
			return mapErr(err)
		}
		seq := last - len(events)
		for _, e := range events {
			seq++
			kind, data := e.Kind, e.Data
			if !OneOf(kind, []string{"claude", "stdout", "stderr", "system"}) {
				kind = "system"
			}
			if len(data) == 0 || !json.Valid(data) {
				data, _ = json.Marshal(map[string]string{"text": string(data)})
			}
			if len(data) > maxEventBytes {
				data, _ = json.Marshal(map[string]string{"text": fmt.Sprintf("[event of %d bytes omitted]", len(data))})
				kind = "system"
			}
			if _, err := tx.Exec(ctx, `INSERT INTO run_events (run_id, seq, kind, data) VALUES ($1, $2, $3, $4)`,
				runID, seq, kind, string(data)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ListEvents(ctx context.Context, runID int64, after, limit int) ([]RunEvent, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := s.DB.Query(ctx, `SELECT seq, at, kind, data FROM run_events
		WHERE run_id = $1 AND seq > $2 ORDER BY seq LIMIT $3`, runID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunEvent{}
	for rows.Next() {
		var e RunEvent
		var data []byte
		if err := rows.Scan(&e.Seq, &e.At, &e.Kind, &data); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

type FinishInput struct {
	Status     string   `json:"status"`
	ExitCode   *int     `json:"exit_code"`
	SessionID  string   `json:"session_id"`
	Result     string   `json:"result"`
	Error      string   `json:"error"`
	CostUSD    *float64 `json:"cost_usd"`
	NumTurns   *int     `json:"num_turns"`
	DurationMS *int64   `json:"duration_ms"`
	// Usage: Claude Code's modelUsage (tokens and cost per model).
	Usage json.RawMessage `json:"usage"`
}

func (s *Store) FinishRun(ctx context.Context, runID, runnerID int64, in FinishInput) error {
	if !OneOf(in.Status, []string{"succeeded", "failed", "cancelled"}) {
		return invalid("status", "must be succeeded, failed or cancelled")
	}
	return s.tx(ctx, func(tx pgxTx) error {
		var projectID, taskID, repoID *int64
		var repoName string
		usage := "{}"
		if len(in.Usage) > 0 && json.Valid(in.Usage) && len(in.Usage) < 64<<10 {
			usage = string(in.Usage)
		}
		err := tx.QueryRow(ctx, `
			UPDATE runs SET status = $3, exit_code = $4, session_id = CASE WHEN $5 = '' THEN session_id ELSE $5 END,
			       result = $6, error = $7, cost_usd = $8, num_turns = $9, duration_ms = $10, finished_at = now(),
			       usage = $11
			WHERE id = $1 AND runner_id = $2 AND status = 'running'
			RETURNING project_id, task_id, repo_id`,
			runID, runnerID, in.Status, in.ExitCode, truncate(in.SessionID, 200), truncate(in.Result, 200000),
			truncate(in.Error, 5000), in.CostUSD, in.NumTurns, in.DurationMS, usage).Scan(&projectID, &taskID, &repoID)
		if err != nil {
			return mapErr(err)
		}
		if repoID == nil {
			return nil // an assistant session: its chat records it
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM repos WHERE id = $1`, repoID).Scan(&repoName); err != nil {
			return mapErr(err)
		}
		summary := fmt.Sprintf("Run #%d on %s %s", runID, repoName, in.Status)
		if in.CostUSD != nil {
			summary += fmt.Sprintf(" ($%.2f)", *in.CostUSD)
		}
		return logActivity(ctx, tx, ActivityInput{ProjectID: projectID, TaskID: taskID, RunID: &runID,
			Kind: "run.finished", Summary: summary})
	})
}
