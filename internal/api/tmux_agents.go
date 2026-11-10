package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/store"
)

// Agents in tmux (see store/tmux_agents.go for the model). The server starts
// a tmux run's Claude Code session on its machine over the control channel,
// types follow-ups into it as one paste each, and ends it when the user
// finishes the run; the session's own hooks (POST /api/runner/hook) report
// its turns. All of it happens per machine under one lock, from three
// places: right after something changed (tmuxNudge), when the machine's
// control channel connects, and every 20 seconds (RunTmuxAgents).

const tmuxTick = 20 * time.Second

type tmuxState struct {
	mu    sync.Mutex
	locks map[int64]*sync.Mutex
}

func (t *tmuxState) lock(runnerID int64) *sync.Mutex {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.locks == nil {
		t.locks = map[int64]*sync.Mutex{}
	}
	if t.locks[runnerID] == nil {
		t.locks[runnerID] = &sync.Mutex{}
	}
	return t.locks[runnerID]
}

// RunTmuxAgents keeps every connected machine's tmux runs moving.
func (s *Server) RunTmuxAgents(ctx context.Context) {
	t := time.NewTicker(tmuxTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		runners, err := s.store.ListRunners(ctx)
		if err != nil {
			continue
		}
		for _, rn := range runners {
			if rn.Capabilities.TmuxAgents && s.terminals.control(rn.ID) != nil {
				s.dispatchTmux(ctx, rn.ID)
			}
		}
	}
}

// tmuxNudge moves a run's machine along now, in the background.
func (s *Server) tmuxNudge(runID int64) {
	run, err := s.store.RunByID(context.Background(), runID)
	if err != nil || run.TmuxSession == "" || run.RunnerID == 0 {
		return
	}
	go s.dispatchTmux(context.Background(), run.RunnerID)
}

// dispatchTmux starts what is queued on a machine and delivers what was
// written to its live sessions.
func (s *Server) dispatchTmux(ctx context.Context, runnerID int64) {
	cc := s.terminals.control(runnerID)
	if cc == nil {
		return
	}
	mu := s.tmux.lock(runnerID)
	mu.Lock()
	defer mu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()

	queued, err := s.store.TmuxToStart(ctx, runnerID)
	if err != nil {
		slog.Warn("tmux runs", "err", err)
		return
	}
	for i := range queued {
		s.startTmuxRun(ctx, cc, &queued[i])
	}
	pending, err := s.store.TmuxPending(ctx, runnerID)
	if err != nil {
		slog.Warn("tmux inbox", "err", err)
		return
	}
	for runID, items := range pending {
		run, err := s.store.RunByID(ctx, runID)
		if err != nil {
			continue
		}
		s.deliverTmux(ctx, cc, run, items)
	}
}

// startTmuxRun starts (or starts again) a run's Claude Code session in its
// tmux session: a new run under a fresh session id with its prompt; a run
// picked up again with --resume and the messages written since.
func (s *Server) startTmuxRun(ctx context.Context, cc *controlConn, run *store.Run) {
	sessions, err := s.store.RunnerTmux(ctx, run.RunnerID)
	if err != nil {
		return
	}
	exists, busy := store.ClaudeIn(sessions, run.TmuxSession)
	prompt, inboxAfter := run.Prompt, run.InboxAfter
	resume := ""
	if run.Resumes > 0 {
		resume = run.ResumeSession
		if resume == "" {
			resume = run.SessionID
		}
		items, err := s.store.InboxSince(ctx, run.ID, run.InboxAfter)
		if err != nil {
			return
		}
		var msgs []string
		for _, it := range items {
			if t := inboxText(it); t != "" {
				msgs = append(msgs, t)
			}
			inboxAfter = it.ID
		}
		prompt = strings.Join(msgs, "\n\n")
		if busy {
			// Claude is still open in there (the machine's list was behind):
			// the session is live, so the messages are typed into it.
			if _, err := s.store.MarkTmuxStarted(ctx, run.ID, resume, run.InboxAfter); err == nil {
				if cur, err := s.store.RunByID(ctx, run.ID); err == nil {
					s.deliverTmux(ctx, cc, cur, items)
				}
			}
			return
		}
	} else if busy {
		// The name is taken by a Claude session that is not this run's: use another.
		name := fmt.Sprintf("%s-%d", run.TmuxSession, run.ID)
		if _, err := s.store.SetRunTmux(ctx, run.ID, name); err != nil {
			return
		}
		run.TmuxSession, exists = name, false
	}
	sessionID := resume
	msg := map[string]any{"type": "create", "session": run.TmuxSession, "cwd": run.SessionDir(), "start": "claude",
		"prompt": prompt, "permission_mode": run.PermissionMode, "model": run.Model, "effort": run.Effort,
		"env": map[string]string{"FORGE_RUN_ID": fmt.Sprint(run.ID)}, "reuse": exists}
	if resume != "" {
		msg["resume"] = resume
	} else {
		sessionID = newSessionID()
		msg["session_id"] = sessionID
		if run.Worktree {
			msg["worktree"] = fmt.Sprintf("forge-run-%d", run.ID)
		}
	}
	if _, err := cc.call(ctx, msg, 30*time.Second); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "did not answer") {
			return // try again on the next pass
		}
		s.failTmuxStart(ctx, run, err)
		return
	}
	if _, err := s.store.MarkTmuxStarted(ctx, run.ID, sessionID, inboxAfter); err != nil {
		slog.Warn("tmux run started but not recorded", "run", run.ID, "err", err)
		return
	}
	s.appendSystem(ctx, run, fmt.Sprintf("Claude Code %s in tmux session %s on %s — attach with `tmux attach -t %s`",
		map[bool]string{true: "resumed", false: "started"}[resume != ""], run.TmuxSession, run.RunnerName, run.TmuxSession))
	slog.Info("tmux run started", "run", run.ID, "session", run.TmuxSession, "runner", run.RunnerName)
}

// failTmuxStart: the machine refused to start the session (a permission
// mode it does not allow, a folder outside its roots…).
func (s *Server) failTmuxStart(ctx context.Context, run *store.Run, err error) {
	s.appendSystem(ctx, run, "could not start Claude Code in tmux: "+err.Error())
	_, _ = s.store.DB.Exec(ctx, `UPDATE runs SET status = 'failed', error = $2, finished_at = now() WHERE id = $1 AND status = 'queued'`,
		run.ID, truncateErr(err.Error()))
}

// deliverTmux types what was written to a live tmux run into its session;
// Finish ends the session.
func (s *Server) deliverTmux(ctx context.Context, cc *controlConn, run *store.Run, items []store.InboxItem) {
	var msgs []string
	var last int64
	for _, it := range items {
		if it.Kind == "end" {
			_, _ = cc.call(ctx, map[string]any{"type": "kill", "session": run.TmuxSession}, 15*time.Second)
			_ = s.store.MarkTmuxDelivered(ctx, run.ID, it.ID)
			_ = s.store.EndTmuxRun(ctx, run.ID, "finished from Forge: tmux session "+run.TmuxSession+" closed")
			return
		}
		if t := inboxText(it); t != "" {
			msgs = append(msgs, t)
		}
		last = it.ID
	}
	if len(msgs) == 0 {
		if last > 0 {
			_ = s.store.MarkTmuxDelivered(ctx, run.ID, last)
		}
		return
	}
	text := strings.Join(msgs, "\n\n")
	if _, err := cc.call(ctx, map[string]any{"type": "paste", "session": run.TmuxSession, "text": text, "enter": true}, 15*time.Second); err != nil {
		slog.Warn("tmux message not delivered", "run", run.ID, "err", err)
		return // stays in the inbox for the next pass
	}
	_ = s.store.MarkTmuxDelivered(ctx, run.ID, last)
}

func inboxText(it store.InboxItem) string {
	if it.Kind != "message" {
		return ""
	}
	var m struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(it.Data, &m) != nil {
		return ""
	}
	return strings.TrimSpace(m.Text)
}

func (s *Server) appendSystem(ctx context.Context, run *store.Run, text string) {
	data, _ := json.Marshal(map[string]string{"text": text})
	_ = s.store.AppendEvents(ctx, run.ID, run.RunnerID, []store.EventInput{{Kind: "system", Data: data}})
}

func truncateErr(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

// newSessionID is a random UUID (v4), the form Claude Code's --session-id takes.
func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ── Where a new run goes ──────────────────────────────────────────────────

// placeInTmux puts a freshly queued run into tmux when the machine can host
// it there and the setting is on: a user's Claude Code agent run (not an
// Assistant turn, not a one-turn status check, not Codex or DeepSeek, which
// keep their headless engines).
func (s *Server) placeInTmux(ctx context.Context, run *store.Run) *store.Run {
	if run.Kind != "agent" || run.Engine != "claude" || run.ChatTurnID != nil || run.OneTurn || run.RepoID == 0 {
		return run
	}
	set, err := s.store.AgentSettings(ctx)
	if err != nil || !set.Tmux {
		return run
	}
	rn, err := s.store.RunnerByID(ctx, run.RunnerID)
	if err != nil || !rn.Capabilities.TmuxAgents {
		return run
	}
	name := store.TmuxName(run)
	sessions, _ := s.store.RunnerTmux(ctx, run.RunnerID)
	inUse, _ := s.store.TmuxNameInUse(ctx, run.RunnerID, run.ID, name)
	if exists, _ := store.ClaudeIn(sessions, name); exists || inUse {
		name = fmt.Sprintf("%s-%d", name, run.ID)
	}
	placed, err := s.store.SetRunTmux(ctx, run.ID, name)
	if err != nil {
		return run
	}
	s.tmuxNudge(run.ID)
	return placed
}

// continueTaskAgent is "one task, one agent": a new agent run for a task
// that already has one becomes a message to that one (reopening it if it
// finished), unless the user asked for a fresh agent. Continue on a run is
// the same thing for that run. ok is false when there is nothing to continue.
func (s *Server) continueTaskAgent(ctx context.Context, in store.RunInput) (*store.Run, bool, error) {
	if in.Kind != "agent" || in.Fresh || in.ChatTurnID != nil {
		return nil, false, nil
	}
	var cur *store.Run
	var err error
	switch {
	case in.ResumeRunID != nil:
		if cur, err = s.store.RunByID(ctx, *in.ResumeRunID); err != nil || cur.ChatTurnID != nil {
			return nil, false, nil
		}
	case in.TaskID != nil:
		if cur, err = s.store.TaskAgent(ctx, *in.TaskID); err != nil {
			return nil, false, nil
		}
	default:
		return nil, false, nil
	}
	if in.Engine != "" && in.Engine != cur.Engine && in.ResumeRunID == nil {
		return nil, false, nil // another engine is another agent
	}
	if cur.Status == "queued" && cur.Resumes == 0 {
		if ok, err := s.store.AppendToQueuedPrompt(ctx, cur.ID, in.Prompt); err != nil || !ok {
			return nil, false, err
		}
	} else {
		requeued, err := s.store.ReopenWithMessage(ctx, cur.ID, in.Prompt, in.OneTurn)
		if err != nil {
			return nil, false, nil // e.g. an old agent that cannot resume in place: a new run on the session, as before
		}
		s.inboxes.notify(cur.ID)
		if requeued {
			s.wakeups.notify(cur.RunnerID)
		}
	}
	s.tmuxNudge(cur.ID)
	run, err := s.store.RunByID(ctx, cur.ID)
	return run, err == nil, err
}

// ── Claude Code's hooks ───────────────────────────────────────────────────

type hookReport struct {
	Event            string             `json:"event"`
	SessionID        string             `json:"session_id"`
	Cwd              string             `json:"cwd"`
	TmuxSession      string             `json:"tmux_session"`
	RunID            int64              `json:"run_id"`
	Source           string             `json:"source"`
	Prompt           string             `json:"prompt"`
	Message          string             `json:"message"`
	NotificationType string             `json:"notification_type"`
	Reason           string             `json:"reason"`
	LastMessage      string             `json:"last_message"`
	Events           []store.EventInput `json:"events"`
}

// runnerHook is `forge agent hook --report` from a Claude Code session on the
// machine: it finds the session's run (by the run id Forge gave it, else by
// session id), registers a hand-started session in tmux as a new run, and
// records what happened.
func (s *Server) runnerHook(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	var in hookReport
	if !decode16(w, r, &in) {
		return
	}
	ctx := r.Context()
	if !sessionIDRe.MatchString(in.SessionID) {
		writeErr(w, r, &store.ValidationError{Field: "session_id", Message: "not a session id"})
		return
	}
	var run *store.Run
	if in.RunID > 0 {
		if cur, err := s.store.RunByID(ctx, in.RunID); err == nil && cur.RunnerID == rn.ID {
			run = cur
		}
	}
	if run == nil {
		if cur, err := s.store.RunBySession(ctx, rn.ID, in.SessionID); err == nil {
			run = cur
		}
	}
	opening := in.Event == "SessionStart" || in.Event == "UserPromptSubmit"
	switch {
	case run == nil:
		if in.TmuxSession == "" || !opening {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		reg, err := s.store.RegisterTmuxRun(ctx, rn.ID, store.TmuxRegistration{SessionID: in.SessionID,
			TmuxSession: in.TmuxSession, Cwd: in.Cwd, Prompt: in.Prompt})
		if errors.Is(err, store.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent) // not in any project's repo
			return
		}
		if err != nil {
			writeErr(w, r, err)
			return
		}
		run = reg
	case in.TmuxSession != "" && opening && (run.TmuxSession != in.TmuxSession || run.Status != "running"):
		// The run's session opened in tmux: Forge started it, or it was taken
		// there by hand (`claude --resume`, Open in terminal).
		if other, err := s.store.OpenRunInTmux(ctx, rn.ID, in.TmuxSession); err == nil && other.ID != run.ID {
			_ = s.store.EndTmuxRun(ctx, other.ID, "the tmux session moved on to another conversation")
		}
		if run.SessionID == "" {
			_, _ = s.store.DB.Exec(ctx, `UPDATE runs SET session_id = $2 WHERE id = $1`, run.ID, in.SessionID)
		}
		if run.Status != "queued" {
			if err := s.store.BindTmux(ctx, run.ID, in.TmuxSession); err != nil {
				writeErr(w, r, err)
				return
			}
		}
	}
	if len(in.Events) > 0 {
		_ = s.store.AppendEvents(ctx, run.ID, rn.ID, in.Events)
	}
	switch in.Event {
	case "SessionStart":
		_ = s.store.SetTmuxAwaiting(ctx, run.ID, "reply")
	case "UserPromptSubmit":
		_ = s.store.SetTmuxAwaiting(ctx, run.ID, "")
		_ = s.store.SetPromptIfEmpty(ctx, run.ID, in.Prompt)
	case "Stop":
		// Telegram hears about runs Forge started; a session started by hand
		// has its user right there at the terminal.
		if err := s.store.TmuxTurnDone(ctx, run.ID, in.LastMessage); err == nil && run.Origin != "tmux" {
			s.tgNotifyAwaiting(run.ID)
		}
	case "Notification":
		switch {
		case in.NotificationType == "permission_prompt" || in.NotificationType == "elicitation_dialog" ||
			(in.NotificationType == "" && strings.Contains(strings.ToLower(in.Message), "permission")):
			_ = s.store.SetTmuxAwaiting(ctx, run.ID, "answer")
			if in.Message != "" {
				s.appendSystem(ctx, run, "waiting for you in tmux: "+in.Message)
			}
		case in.NotificationType == "idle_prompt":
			_ = s.store.SetTmuxAwaiting(ctx, run.ID, "reply")
		}
	case "SessionEnd":
		why := "Claude Code exited"
		if in.Reason != "" {
			why += " (" + in.Reason + ")"
		}
		_ = s.store.EndTmuxRun(ctx, run.ID, why)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Settings ──────────────────────────────────────────────────────────────

func (s *Server) getAgentSettings(w http.ResponseWriter, r *http.Request, u *store.User) {
	v, err := s.store.AgentSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) putAgentSettings(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in store.AgentSettings
	if !decode(w, r, &in) {
		return
	}
	v, err := s.store.PutAgentSettings(r.Context(), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
