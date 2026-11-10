package store

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Agents in tmux. A project's agent is one Claude Code session in a tmux
// session on a machine, named after the project (F-Shop; an extra one is
// F-Shop-2) with Remote Control on under the same name — the same session
// whether it is opened from Forge's terminal, `tmux attach` or claude.ai.
// The server drives it over the machine's control channel (api/tmux_agents.go):
// it starts the session, types requests into it, and learns what happens
// from Claude Code's hooks (`forge agent hook`) and the heartbeat's tmux list.
//
// A run is one conversation in such a session. It is "running" while its
// Claude Code session is open (Awaiting says whether it is working, waiting
// for a reply or for an answer in the terminal), "waiting" once Claude exited
// (a message starts it again with --resume), and finished only when the user
// finishes it. A conversation started by hand in tmux becomes a run with its
// first request (origin "tmux"); a session nobody has asked anything is just
// an open session, not a run.

// AgentSettings is how Forge runs agents.
type AgentSettings struct {
	// Tmux: agent runs on machines that support it live in tmux sessions
	// (default); off runs them headless (`claude -p`) as before.
	Tmux bool `json:"tmux"`
	// RemoteControl: an agent's Claude Code session starts with Remote
	// Control on under its tmux session's name (default), so it can be
	// picked up on claude.ai/code and in the Claude app.
	RemoteControl bool `json:"remote_control"`
}

func (s *Store) AgentSettings(ctx context.Context) (AgentSettings, error) {
	v := AgentSettings{Tmux: true, RemoteControl: true}
	_, err := s.getSetting(ctx, "agents", &v)
	return v, err
}

func (s *Store) PutAgentSettings(ctx context.Context, v AgentSettings) (AgentSettings, error) {
	return v, s.putSetting(ctx, "agents", v)
}

// tmuxNameRe is what Forge names a tmux session after (tmux refuses '.' and
// ':' in names; the rest is kept to what is safe in a shell word).
var tmuxNameRe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// ProjectNameByID is a project's name (its agent's tmux session is named after it).
func (s *Store) ProjectNameByID(ctx context.Context, id int64) (string, error) {
	var name string
	err := s.DB.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, id).Scan(&name)
	return name, mapErr(err)
}

// FreeTmuxName is base when no open run on the machine has it and taken does
// not list it, else the first free base-2, base-3, …: a project's extra agents.
// from is the first number tried (1 = base itself).
func (s *Store) FreeTmuxName(ctx context.Context, runnerID, exceptRun int64, base string, from int, taken func(string) bool) (string, error) {
	for n := max(from, 1); n < 100; n++ {
		name := base
		if n > 1 {
			suffix := fmt.Sprintf("-%d", n)
			if len(name)+len(suffix) > 40 {
				name = strings.TrimRight(name[:40-len(suffix)], "-")
			}
			name += suffix
		}
		inUse, err := s.TmuxNameInUse(ctx, runnerID, exceptRun, name)
		if err != nil {
			return "", err
		}
		if !inUse && (taken == nil || !taken(name)) {
			return name, nil
		}
	}
	return "", invalid("session", "too many sessions named %s", base)
}

// SetRunTmux puts a queued run into tmux under name; the server starts it.
func (s *Store) SetRunTmux(ctx context.Context, id int64, name string) (*Run, error) {
	if _, err := s.DB.Exec(ctx, `UPDATE runs SET tmux_session = $2, interactive = true, one_turn = false
		WHERE id = $1 AND status = 'queued'`, id, name); err != nil {
		return nil, err
	}
	return s.RunByID(ctx, id)
}

// AdoptTmuxSession gives a run that was typed into an already open Claude
// Code session (it has no session id yet) that session's id, as the
// session's hook reports it.
func (s *Store) AdoptTmuxSession(ctx context.Context, id int64, sessionID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE runs SET session_id = $2 WHERE id = $1 AND session_id = ''`, id, sessionID)
	return err
}

// TmuxNameInUse: another open run on this machine has the name.
func (s *Store) TmuxNameInUse(ctx context.Context, runnerID, exceptRun int64, name string) (bool, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM runs WHERE runner_id = $1 AND id <> $2 AND tmux_session = $3
		AND status IN ('queued', 'running', 'waiting')`, runnerID, exceptRun, name).Scan(&n)
	return n > 0, err
}

// TmuxToStart is what the server still has to start on this machine: queued
// tmux runs, new and picked up again (oldest first).
func (s *Store) TmuxToStart(ctx context.Context, runnerID int64) ([]Run, error) {
	return s.listRunsWhere(ctx, `r.runner_id = $1 AND r.status = 'queued' AND r.tmux_session <> '' ORDER BY r.id`, runnerID)
}

// TmuxPending is the messages written to a running tmux run that have not
// been typed into its session yet.
func (s *Store) TmuxPending(ctx context.Context, runnerID int64) (map[int64][]InboxItem, error) {
	rows, err := s.DB.Query(ctx, `SELECT i.run_id, i.id, i.kind, i.data FROM run_inbox i JOIN runs r ON r.id = i.run_id
		WHERE r.runner_id = $1 AND r.status = 'running' AND r.tmux_session <> '' AND i.id > r.inbox_after
		  AND i.kind IN ('message', 'end')
		ORDER BY i.id`, runnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]InboxItem{}
	for rows.Next() {
		var runID int64
		var it InboxItem
		var data string
		if err := rows.Scan(&runID, &it.ID, &it.Kind, &data); err != nil {
			return nil, err
		}
		it.Data = json.RawMessage(data)
		out[runID] = append(out[runID], it)
	}
	return out, rows.Err()
}

// InboxSince is a run's unread inbox (messages to start a resumed session with).
func (s *Store) InboxSince(ctx context.Context, runID, after int64) ([]InboxItem, error) {
	run, err := s.RunByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	items, _, err := s.Inbox(ctx, runID, run.RunnerID, after)
	return items, err
}

// MarkTmuxStarted: the session is up in tmux under its session id.
func (s *Store) MarkTmuxStarted(ctx context.Context, id int64, sessionID string, inboxAfter int64) (*Run, error) {
	if _, err := s.DB.Exec(ctx, `UPDATE runs SET status = 'running', started_at = coalesce(started_at, now()),
		session_id = $2, inbox_after = max(inbox_after, $3), awaiting = '', finished_at = NULL
		WHERE id = $1 AND status = 'queued'`, id, sessionID, inboxAfter); err != nil {
		return nil, err
	}
	return s.RunByID(ctx, id)
}

// MarkTmuxDelivered: the inbox up to inboxAfter has been typed into the session.
func (s *Store) MarkTmuxDelivered(ctx context.Context, id, inboxAfter int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE runs SET inbox_after = max(inbox_after, $2), awaiting = '' WHERE id = $1`, id, inboxAfter)
	return err
}

// RunBySession is the run (the latest, should there be more) whose Claude
// Code session this is, on this machine.
func (s *Store) RunBySession(ctx context.Context, runnerID int64, sessionID string) (*Run, error) {
	if sessionID == "" {
		return nil, ErrNotFound
	}
	runs, err := s.listRunsWhere(ctx, `r.runner_id = $1 AND r.session_id = $2 AND r.kind = 'agent' AND r.chat_turn_id IS NULL
		ORDER BY r.id DESC LIMIT 1`, runnerID, sessionID)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, ErrNotFound
	}
	return &runs[0], nil
}

// OpenRunInTmux is the open run living in this tmux session, if any.
func (s *Store) OpenRunInTmux(ctx context.Context, runnerID int64, tmuxSession string) (*Run, error) {
	runs, err := s.listRunsWhere(ctx, `r.runner_id = $1 AND r.tmux_session = $2 AND r.status IN ('queued', 'running', 'waiting')
		ORDER BY r.id DESC LIMIT 1`, runnerID, tmuxSession)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, ErrNotFound
	}
	return &runs[0], nil
}

// OpenTmuxRuns is the open run living in each tmux session of a machine (the
// latest, should a name have more than one).
func (s *Store) OpenTmuxRuns(ctx context.Context, runnerID int64) (map[string]Run, error) {
	runs, err := s.listRunsWhere(ctx, `r.runner_id = $1 AND r.tmux_session <> '' AND r.status IN ('queued', 'running', 'waiting')
		ORDER BY r.id`, runnerID)
	if err != nil {
		return nil, err
	}
	out := map[string]Run{}
	for _, r := range runs {
		out[r.TmuxSession] = r
	}
	return out, nil
}

// TaskAgent is the run that is a task's agent: its latest agent run (not an
// Assistant turn) that is open or has a session to pick up again.
func (s *Store) TaskAgent(ctx context.Context, taskID int64) (*Run, error) {
	runs, err := s.listRunsWhere(ctx, `r.task_id = $1 AND r.kind = 'agent' AND r.chat_turn_id IS NULL AND r.repo_id IS NOT NULL
		AND (r.status IN ('queued', 'running', 'waiting') OR r.session_id <> '')
		ORDER BY r.id DESC LIMIT 1`, taskID)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, ErrNotFound
	}
	return &runs[0], nil
}

// AppendToQueuedPrompt adds a request to a run that has not started yet.
func (s *Store) AppendToQueuedPrompt(ctx context.Context, id int64, text string) (bool, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE runs SET prompt = prompt || char(10) || char(10) || $2
		WHERE id = $1 AND status = 'queued' AND resumes = 0 AND length(prompt) + length($2) < 100000`, id, strings.TrimSpace(text))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// TmuxRegistration is a Claude Code session started by hand in tmux, as its
// SessionStart (or first prompt) hook reports it.
type TmuxRegistration struct {
	SessionID   string
	TmuxSession string
	Cwd         string
	Prompt      string
}

// firstRepoUnder is the project's first repo that lives inside dir (nil when
// none does).
func (s *Store) firstRepoUnder(ctx context.Context, projectKey, dir string) (*RepoWithProject, error) {
	repos, err := s.AllRepos(ctx)
	if err != nil {
		return nil, err
	}
	dir = strings.TrimRight(dir, "/")
	for i := range repos {
		if repos[i].ProjectKey == projectKey && repos[i].Path != "" && strings.HasPrefix(repos[i].Path, dir+"/") {
			return &repos[i], nil
		}
	}
	return nil, nil
}

// RegisterTmuxRun records a conversation started by hand in a Claude Code
// session in tmux as an agent run, in the repo its folder belongs to (a
// project's own folder: its first repo). It returns ErrNotFound when the
// folder is in no project (the session stays Forge-less). A tmux session
// that held another conversation before (/clear) hands its task on.
func (s *Store) RegisterTmuxRun(ctx context.Context, runnerID int64, in TmuxRegistration) (*Run, error) {
	if in.SessionID == "" || in.TmuxSession == "" {
		return nil, invalid("session_id", "a session id and a tmux session are needed")
	}
	key, repo, err := s.ProjectForPath(ctx, in.Cwd)
	if err != nil {
		return nil, err
	}
	if repo == nil && key != "" {
		// A project's own folder, above its repos (where its agent runs):
		// the conversation is filed under the project's first repo in there.
		if repo, err = s.firstRepoUnder(ctx, key, in.Cwd); err != nil {
			return nil, err
		}
	}
	if repo == nil {
		return nil, ErrNotFound
	}
	runner, err := s.RunnerByID(ctx, runnerID)
	if err != nil {
		return nil, err
	}
	var taskID *int64
	if prev, err := s.OpenRunInTmux(ctx, runnerID, in.TmuxSession); err == nil {
		taskID = prev.TaskID
		if err := s.EndTmuxRun(ctx, prev.ID, "the tmux session moved on to another conversation"); err != nil {
			return nil, err
		}
	}
	cwd := ""
	if in.Cwd != repo.Path {
		cwd = in.Cwd // Claude Code files the session under the folder it started in
	}
	var id int64
	err = s.tx(ctx, func(tx pgxTx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO runs (runner_id, runner_name, project_id, repo_id, task_id, kind, prompt,
			permission_mode, engine, interactive, status, started_at, session_id, tmux_session, origin, cwd)
			VALUES ($1, $2, $3, $4, $5, 'agent', $6, 'default', 'claude', true, 'running', now(), $7, $8, 'tmux', $9)
			RETURNING id`, runner.ID, runner.Name, repo.ProjectID, repo.ID, taskID, strings.TrimSpace(in.Prompt),
			in.SessionID, in.TmuxSession, cwd).Scan(&id); err != nil {
			return mapErr(err)
		}
		return logActivity(ctx, tx, ActivityInput{ProjectID: &repo.ProjectID, TaskID: taskID, RunID: &id, Kind: "run.queued",
			Summary: fmt.Sprintf("Run #%d: Claude Code session in tmux %s on %s (%s)", id, in.TmuxSession, runner.Name, repo.Name)})
	})
	if err != nil {
		return nil, err
	}
	return s.RunByID(ctx, id)
}

// BindTmux records that a run's session is (again) open in a tmux session:
// opened there by hand (`claude --resume`, `forge resume`), or started by
// Forge. A waiting or finished run is open again.
func (s *Store) BindTmux(ctx context.Context, id int64, tmuxSession string) error {
	_, err := s.DB.Exec(ctx, `UPDATE runs SET tmux_session = $2, status = 'running', interactive = true,
		started_at = coalesce(started_at, now()), finished_at = NULL, finish_requested = false, error = '', handoff = ''
		WHERE id = $1 AND status <> 'queued'`, id, tmuxSession)
	return err
}

// SetTmuxAwaiting is the terminal side of Awaiting: "" (working), "reply"
// (the turn ended) or "answer" (Claude asks something in the terminal).
func (s *Store) SetTmuxAwaiting(ctx context.Context, id int64, awaiting string) error {
	if !OneOf(awaiting, []string{"", "reply", "answer"}) {
		return invalid("awaiting", "must be reply, answer or empty")
	}
	_, err := s.DB.Exec(ctx, `UPDATE runs SET awaiting = $2 WHERE id = $1 AND status = 'running'`, id, awaiting)
	return err
}

// SetPromptIfEmpty gives a hand-started session its first request as prompt.
func (s *Store) SetPromptIfEmpty(ctx context.Context, id int64, prompt string) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil
	}
	if len(prompt) > 100000 {
		prompt = prompt[:100000]
	}
	_, err := s.DB.Exec(ctx, `UPDATE runs SET prompt = $2 WHERE id = $1 AND prompt = ''`, id, prompt)
	return err
}

// TmuxTurnDone: Claude answered in the terminal; result is its last message.
func (s *Store) TmuxTurnDone(ctx context.Context, id int64, result string) error {
	_, err := s.DB.Exec(ctx, `UPDATE runs SET awaiting = 'reply', num_turns = coalesce(num_turns, 0) + 1,
		result = CASE WHEN $2 = '' THEN result ELSE $2 END WHERE id = $1 AND status = 'running'`, id, truncate(result, 100000))
	return err
}

// EndTmuxRun: Claude Code exited in (or with) its tmux session. A run Forge
// started waits — a message starts it again on the same session; one started
// by hand is over (a message still picks it up again).
func (s *Store) EndTmuxRun(ctx context.Context, id int64, why string) error {
	run, err := s.RunByID(ctx, id)
	if err != nil {
		return err
	}
	if run.Status != "running" {
		return nil
	}
	if run.Origin == "tmux" || run.FinishRequested {
		_, err = s.DB.Exec(ctx, `UPDATE runs SET status = 'succeeded', awaiting = '', finished_at = now()
			WHERE id = $1 AND status = 'running'`, id)
	} else {
		_, err = s.DB.Exec(ctx, `UPDATE runs SET status = 'waiting', awaiting = '' WHERE id = $1 AND status = 'running'`, id)
	}
	if err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]string{"text": why})
	return s.AppendEvents(ctx, id, run.RunnerID, []EventInput{{Kind: "system", Data: data}})
}

// reconcileTmuxRuns ends the runs whose Claude Code session is gone from the
// machine's tmux list (the session was killed, or Claude exited and its
// window went back to a shell) — after a grace period for a session that is
// still starting.
func (s *Store) reconcileTmuxRuns(ctx context.Context, runnerID int64, sessions []TmuxSession) error {
	live := map[string]bool{}
	for _, se := range sessions {
		if se.Claude {
			live[se.Name] = true
		}
		for _, w := range se.WindowList {
			if w.Claude {
				live[se.Name] = true
			}
		}
	}
	runs, err := s.listRunsWhere(ctx, `r.runner_id = $1 AND r.status = 'running' AND r.tmux_session <> ''
		AND r.started_at < ts_add(now(), -120)`, runnerID)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if !live[run.TmuxSession] {
			if err := s.EndTmuxRun(ctx, run.ID, "Claude Code is no longer running in tmux session "+run.TmuxSession); err != nil {
				return err
			}
		}
	}
	return nil
}

// listRunsWhere runs runSelect with a condition (and its ORDER/LIMIT).
func (s *Store) listRunsWhere(ctx context.Context, where string, args ...any) ([]Run, error) {
	rows, err := s.DB.Query(ctx, runSelect+" WHERE "+where, args...)
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
		out = append(out, *r)
	}
	return out, rows.Err()
}

// RunnerTmux is the machine's tmux list as of its last heartbeat.
func (s *Store) RunnerTmux(ctx context.Context, runnerID int64) ([]TmuxSession, error) {
	var raw []byte
	if err := s.DB.QueryRow(ctx, `SELECT coalesce(tmux, '[]') FROM runners WHERE id = $1`, runnerID).Scan(&raw); err != nil {
		return nil, mapErr(err)
	}
	var out []TmuxSession
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out, nil
}

// ClaudeIn: Claude Code (or Codex) is running in the named tmux session.
func ClaudeIn(sessions []TmuxSession, name string) (exists, agent bool) {
	for _, se := range sessions {
		if se.Name != name {
			continue
		}
		if se.Claude || se.Codex {
			return true, true
		}
		for _, w := range se.WindowList {
			if w.Claude || w.Codex {
				return true, true
			}
		}
		return true, false
	}
	return false, false
}

// tmuxRunIDs maps each open tmux run's session name to the run, per machine.
func (s *Store) tmuxRunIDs(ctx context.Context) (map[int64]map[string]int64, error) {
	rows, err := s.DB.Query(ctx, `SELECT runner_id, tmux_session, max(id) FROM runs
		WHERE tmux_session <> '' AND status IN ('queued', 'running', 'waiting') AND runner_id IS NOT NULL
		GROUP BY runner_id, tmux_session`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]int64{}
	for rows.Next() {
		var runner, id int64
		var name string
		if err := rows.Scan(&runner, &name, &id); err != nil {
			return nil, err
		}
		if out[runner] == nil {
			out[runner] = map[string]int64{}
		}
		out[runner][name] = id
	}
	return out, rows.Err()
}

// ReopenTmux queues a tmux run that is not open in its session to start
// again there (claude --resume, no new message): Open in terminal on it.
func (s *Store) ReopenTmux(ctx context.Context, id int64) error {
	return s.tx(ctx, func(tx pgxTx) error {
		var last int64
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM run_inbox WHERE run_id = $1`, id).Scan(&last); err != nil {
			return err
		}
		var inboxAfter int64
		if err := tx.QueryRow(ctx, `SELECT inbox_after FROM runs WHERE id = $1`, id).Scan(&inboxAfter); err != nil {
			return mapErr(err)
		}
		if last < inboxAfter {
			last = inboxAfter
		}
		if err := requeueTx(ctx, tx, id, last); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE runs SET finish_requested = false WHERE id = $1`, id)
		return err
	})
}
