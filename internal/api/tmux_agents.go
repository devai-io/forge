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

// Agents in tmux (see store/tmux_agents.go for the model). A project's agent
// is one Claude Code session in the tmux session F-<Project> on a machine
// (extra ones: F-<Project>-2, …), with Remote Control on. The server starts
// it over the control channel, types requests into it as one paste each, and
// ends it when the user finishes the run; the session's own hooks
// (POST /api/runner/hook) report its turns. All of it happens per machine
// under one lock, from three places: right after something changed
// (tmuxNudge), when the machine's control channel connects, and every 20
// seconds (RunTmuxAgents).

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
	if len(queued) > 0 {
		sessions := s.liveTmux(ctx, cc, runnerID)
		for i := range queued {
			s.startTmuxRun(ctx, cc, &queued[i], sessions)
			if len(queued) > i+1 {
				sessions = s.liveTmux(ctx, cc, runnerID) // what the last one started counts for the next
			}
		}
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

// liveTmux is the machine's tmux list right now (agents from v0.1.46 answer
// "list"), else as of its last heartbeat.
func (s *Server) liveTmux(ctx context.Context, cc *controlConn, runnerID int64) []store.TmuxSession {
	if cc != nil {
		if res, err := cc.call(ctx, map[string]any{"type": "list"}, 5*time.Second); err == nil && strings.HasPrefix(res.Text, "[") {
			var out []store.TmuxSession
			if json.Unmarshal([]byte(res.Text), &out) == nil {
				return out
			}
		}
	}
	sessions, _ := s.store.RunnerTmux(ctx, runnerID)
	return sessions
}

// agentSessionName is the tmux session a project's agent lives in ("" when
// the project is gone).
func (s *Server) agentSessionName(ctx context.Context, projectID int64) string {
	name, err := s.store.ProjectNameByID(ctx, projectID)
	if err != nil || name == "" {
		return ""
	}
	return projectSessionName(name)
}

// claudeTaken says whether Claude Code (or Codex) is open in a session of
// that name: a new conversation that wants a session of its own skips those.
func claudeTaken(sessions []store.TmuxSession) func(string) bool {
	return func(name string) bool {
		_, agent := store.ClaudeIn(sessions, name)
		return agent
	}
}

// startTmuxRun starts (or starts again) a run's Claude Code session in its
// tmux session: a new run under a fresh session id with its prompt (typed
// into the session instead, when Claude Code is already open in it with no
// conversation of its own); a run picked up again with --resume and the
// messages written since.
func (s *Server) startTmuxRun(ctx context.Context, cc *controlConn, run *store.Run, sessions []store.TmuxSession) {
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
		other, _ := s.store.TmuxNameInUse(ctx, run.RunnerID, run.ID, run.TmuxSession)
		switch {
		case busy && !other:
			// Claude is still open in there (the machine's list was behind):
			// the session is live, so the messages are typed into it.
			if _, err := s.store.MarkTmuxStarted(ctx, run.ID, resume, run.InboxAfter); err == nil {
				if cur, err := s.store.RunByID(ctx, run.ID); err == nil {
					s.deliverTmux(ctx, cc, cur, items)
				}
			}
			return
		case busy || !exists:
			// Its session went to another conversation, or is gone: it comes
			// back in the project's session when that is free, else in an
			// extra one.
			base := s.agentSessionName(ctx, run.ProjectID)
			if base == "" {
				base = run.TmuxSession
			}
			name, err := s.store.FreeTmuxName(ctx, run.RunnerID, run.ID, base, 1, claudeTaken(sessions))
			if err != nil {
				s.failTmuxStart(ctx, run, err)
				return
			}
			if name != run.TmuxSession {
				if _, err := s.store.SetRunTmux(ctx, run.ID, name); err != nil {
					return
				}
				run.TmuxSession = name
			}
			exists, _ = store.ClaudeIn(sessions, name)
		}
	} else if busy {
		// Claude Code is open in the session and has no conversation there
		// (a session nobody has asked anything yet): the request is typed
		// into it, and its hook says which session this run now is.
		if _, err := cc.call(ctx, map[string]any{"type": "paste", "session": run.TmuxSession, "text": prompt, "enter": true}, 15*time.Second); err != nil {
			if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "did not answer") {
				return // try again on the next pass
			}
			s.failTmuxStart(ctx, run, err)
			return
		}
		if _, err := s.store.MarkTmuxStarted(ctx, run.ID, "", inboxAfter); err != nil {
			slog.Warn("tmux run sent but not recorded", "run", run.ID, "err", err)
			return
		}
		s.appendSystem(ctx, run, fmt.Sprintf("Sent to the Claude Code session open in tmux session %s on %s (its model and permission mode stay as they are) — attach with `tmux attach -t %s`",
			run.TmuxSession, run.RunnerName, run.TmuxSession))
		slog.Info("tmux run sent to an open session", "run", run.ID, "session", run.TmuxSession, "runner", run.RunnerName)
		return
	}
	sessionID := resume
	mode := run.PermissionMode
	if mode == "default" {
		mode = "" // Claude Code's own default: it asks in the terminal
	}
	msg := map[string]any{"type": "create", "session": run.TmuxSession, "cwd": run.SessionDir(), "start": "claude",
		"prompt": prompt, "permission_mode": mode, "model": run.Model, "effort": run.Effort,
		"env": map[string]string{"FORGE_RUN_ID": fmt.Sprint(run.ID)}, "reuse": exists}
	if set, err := s.store.AgentSettings(ctx); err == nil && set.RemoteControl {
		msg["remote_control"] = run.TmuxSession // the same name on claude.ai/code and in the Claude app
	}
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
	s.projectSessions.markStarted(run.TmuxSession)
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
	if s.projectSessions.startedAgo(run.TmuxSession) < projectSessionSettle {
		return // Claude Code is still drawing its first screen: typed on the next pass
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
// keep their headless engines). It goes to the project's agent session
// (F-<Project>), or to an extra one (F-<Project>-2, …) when that one holds
// another conversation — or, for a fresh agent, has Claude Code open at all.
func (s *Server) placeInTmux(ctx context.Context, run *store.Run, fresh bool) *store.Run {
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
	base := s.agentSessionName(ctx, run.ProjectID)
	if base == "" {
		return run
	}
	var taken func(string) bool
	if fresh {
		sessions, _ := s.store.RunnerTmux(ctx, run.RunnerID)
		taken = claudeTaken(sessions)
	}
	name, err := s.store.FreeTmuxName(ctx, run.RunnerID, run.ID, base, 1, taken)
	if err != nil {
		return run
	}
	placed, err := s.store.SetRunTmux(ctx, run.ID, name)
	if err != nil {
		return run
	}
	s.tmuxNudge(run.ID)
	return placed
}

// projectAgentRun is the conversation open in the project's agent session on
// the machine a new run asks for (nil when there is none).
func (s *Server) projectAgentRun(ctx context.Context, in store.RunInput) *store.Run {
	if in.OneTurn || in.RepoID == 0 || in.RunnerID == 0 {
		return nil // a status check keeps to its own session
	}
	repo, err := s.store.RepoByID(ctx, in.RepoID)
	if err != nil {
		return nil
	}
	name := s.agentSessionName(ctx, repo.ProjectID)
	if name == "" {
		return nil
	}
	cur, err := s.store.OpenRunInTmux(ctx, in.RunnerID, name)
	if err != nil || cur.ProjectID != repo.ProjectID || cur.Kind != "agent" || cur.ChatTurnID != nil {
		return nil
	}
	return cur
}

// continueAgent is "one project, one agent": a new agent run for a project
// whose agent has a conversation open becomes a message to it; failing that,
// one for a task that already has an agent goes to that one (reopening it if
// it finished) — unless the user asked for a fresh agent. Continue on a run
// is the same thing for that run. A member's request goes only to a run that
// member started, never into the project agent's conversation. ok is false
// when there is nothing to continue.
func (s *Server) continueAgent(ctx context.Context, in store.RunInput, u *store.User) (*store.Run, bool, error) {
	member := isMember(u)
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
	default:
		if !member { // a member's request stays out of the project agent's conversation
			cur = s.projectAgentRun(ctx, in)
		}
		if cur == nil {
			if in.TaskID == nil {
				return nil, false, nil
			}
			if cur, err = s.store.TaskAgent(ctx, *in.TaskID); err != nil {
				return nil, false, nil
			}
		}
	}
	if in.Engine != "" && in.Engine != cur.Engine && in.ResumeRunID == nil {
		return nil, false, nil // another engine is another agent
	}
	if member && (cur.UserID == nil || *cur.UserID != u.ID) {
		return nil, false, nil // a member's request goes only to a run it started
	}
	return s.sendToAgent(ctx, cur, in.Prompt, in.OneTurn)
}

// sendToAgent gives an agent run one more request: added to a prompt that
// has not started, typed into an open session, or picking a closed one back
// up on its session.
func (s *Server) sendToAgent(ctx context.Context, cur *store.Run, prompt string, oneTurn bool) (*store.Run, bool, error) {
	if cur.Status == "queued" && cur.Resumes == 0 {
		if ok, err := s.store.AppendToQueuedPrompt(ctx, cur.ID, prompt); err != nil || !ok {
			return nil, false, err
		}
	} else {
		requeued, err := s.store.ReopenWithMessage(ctx, cur.ID, prompt, oneTurn)
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
// session id, else the request Forge typed into that tmux session),
// registers a conversation started by hand in tmux as a new run with its
// first request, and records what happened.
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
	if run != nil && run.SessionID != "" && run.SessionID != in.SessionID {
		// The session Forge started holds another conversation now.
		switch {
		case in.Event == "SessionStart" && in.Source == "clear" && run.Status == "running":
			_ = s.store.EndTmuxRun(ctx, run.ID, "/clear: the tmux session moved on to another conversation")
			run = nil
		case run.Status != "running":
			run = nil // that conversation is over; this one is its own run
		}
	}
	if run == nil {
		if cur, err := s.store.RunBySession(ctx, rn.ID, in.SessionID); err == nil {
			run = cur
		}
	}
	opening := in.Event == "SessionStart" || in.Event == "UserPromptSubmit"
	if run == nil && in.TmuxSession != "" && opening {
		if cur, err := s.store.OpenRunInTmux(ctx, rn.ID, in.TmuxSession); err == nil && cur.Status == "running" {
			switch {
			case cur.SessionID == "":
				// A request Forge typed into the open session: this is its session.
				if err := s.store.AdoptTmuxSession(ctx, cur.ID, in.SessionID); err == nil {
					run, _ = s.store.RunByID(ctx, cur.ID)
				}
			case in.Event == "SessionStart" && cur.SessionID != in.SessionID:
				// /clear, or Claude Code started again: that conversation is over.
				_ = s.store.EndTmuxRun(ctx, cur.ID, "the tmux session moved on to another conversation")
			}
		}
	}
	switch {
	case run == nil:
		// A conversation started by hand becomes a run with its first
		// request: a session nobody has asked anything is not a run.
		if in.TmuxSession == "" || in.Event != "UserPromptSubmit" || strings.TrimSpace(in.Prompt) == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		reg, err := s.store.RegisterTmuxRun(ctx, rn.ID, store.TmuxRegistration{SessionID: in.SessionID,
			TmuxSession: in.TmuxSession, Cwd: in.Cwd, Prompt: in.Prompt})
		if errors.Is(err, store.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent) // not in any project
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
	cur, err := s.store.AgentSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in struct {
		Tmux          *bool `json:"tmux"`
		RemoteControl *bool `json:"remote_control"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Tmux != nil {
		cur.Tmux = *in.Tmux
	}
	if in.RemoteControl != nil {
		cur.RemoteControl = *in.RemoteControl
	}
	v, err := s.store.PutAgentSettings(r.Context(), cur)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
