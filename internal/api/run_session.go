package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/devai-io/forge/internal/store"
)

// An agent run the user started stays open after Claude answers: it waits
// for a reply (its session parked on the machine after a while) until the
// user finishes it here, or takes the session into Claude Code — a terminal
// on the machine (`claude --resume`), or `forge resume` in a shell there.

func (s *Server) finishRun(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, err := s.store.FinishRunByUser(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.inboxes.notify(id)
	writeJSON(w, http.StatusOK, run.Public())
}

// sessionID is what a Claude Code session id or Codex thread id looks like;
// it goes into a shell line, so nothing else passes.
var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{7,63}$`)

// parkForHandoff brings a run to a state where its session is free to be
// opened elsewhere: a live interactive session is parked (up to 45 s); a
// one-shot run still working, or a queued one, cannot be taken over yet.
func (s *Server) parkForHandoff(ctx context.Context, id int64) (*store.Run, error) {
	run, err := s.store.RunByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if run.Kind != "agent" || run.ChatTurnID != nil || !sessionIDRe.MatchString(run.SessionID) {
		return nil, &store.ValidationError{Field: "id", Message: "this run has no agent session to open"}
	}
	switch run.Status {
	case "queued":
		return nil, &store.ValidationError{Field: "id", Message: "the run is queued; open it once it has answered"}
	case "running":
		parked, err := s.store.RequestPark(ctx, id)
		if err != nil {
			return nil, err
		}
		if !parked {
			return nil, &store.ValidationError{Field: "id", Message: "Claude is still working on this one-shot run; open it once it has answered"}
		}
		s.inboxes.notify(id)
		deadline := time.Now().Add(45 * time.Second)
		for run.Status == "running" && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
			if run, err = s.store.RunByID(ctx, id); err != nil {
				return nil, err
			}
		}
		if run.Status == "running" {
			return nil, &store.ValidationError{Field: "id", Message: "the session did not stop in time (Claude may be mid-turn); try again when it has answered"}
		}
	}
	return run, nil
}

// handoffRun opens the run's session in Claude Code (or Codex) in a tmux
// session on its machine, then attaches the browser to it. The run itself
// is over from Forge's side: it says where the session went.
func (s *Server) handoffRun(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context()) // a parked session must not be left half handed over
	cur, err := s.store.RunByID(ctx, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	rn, err := s.store.RunnerByID(ctx, cur.RunnerID)
	if err != nil {
		writeErr(w, r, &store.ValidationError{Field: "id", Message: "the run's machine no longer exists"})
		return
	}
	if !rn.Capabilities.Terminal {
		writeError(w, http.StatusConflict, "terminal_disabled", "terminals are off on "+rn.Name+" (\"terminal\": true in its agent.json); run `forge resume "+fmt.Sprint(id)+"` there instead")
		return
	}
	if !rn.Capabilities.TerminalResume {
		writeError(w, http.StatusConflict, "agent_outdated", rn.Name+" runs an older Forge agent that cannot open a session by id; upgrade it, or copy the resume command")
		return
	}
	cc := s.terminals.control(rn.ID)
	if cc == nil {
		writeError(w, http.StatusConflict, "runner_offline", rn.Name+" is not connected")
		return
	}
	run, err := s.parkForHandoff(ctx, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	name := fmt.Sprintf("run-%d", run.ID)
	start := "claude"
	if run.Engine == "codex" {
		start = "codex"
	}
	skip := run.PermissionMode == "bypassPermissions" && store.OneOf("bypassPermissions", rn.Capabilities.PermissionModes)
	msg := map[string]any{"type": "create", "session": name, "cwd": run.SessionDir(), "start": start,
		"resume": run.SessionID, "skip_permissions": skip}
	if _, err := cc.call(ctx, msg, 20*time.Second); err != nil {
		writeError(w, http.StatusBadGateway, "runner_error", err.Error())
		return
	}
	run, err = s.store.HandOffRun(ctx, id, rn.Name+" · tmux "+name)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("run handed to a terminal", "run", id, "session", name, "runner", rn.Name, "ip", s.clientIP(r))
	s.sec(r, "terminal_create", name+" (resume run "+fmt.Sprint(id)+")")
	writeJSON(w, http.StatusOK, map[string]any{"run": run.Public(), "runner_id": rn.ID, "session": name})
}

// ── Machine side: `forge resume` ──────────────────────────────────────────

// runnerRuns lists the runs this machine can resume from a folder.
func (s *Server) runnerRuns(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	dir := r.URL.Query().Get("path")
	if dir == "" {
		writeErr(w, r, &store.ValidationError{Field: "path", Message: "which folder?"})
		return
	}
	runs, err := s.store.RunsForPath(r.Context(), rn.ID, dir, queryInt(r, "limit", 20))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		out = append(out, runnerRunSummary(run))
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

func runnerRunSummary(run store.Run) map[string]any {
	return map[string]any{"id": run.ID, "status": run.Status, "awaiting": run.Awaiting, "prompt": run.Prompt,
		"project_key": run.ProjectKey, "repo_name": run.RepoName, "engine": run.Engine,
		"permission_mode": run.PermissionMode, "session_id": run.SessionID, "dir": run.SessionDir(),
		"created_at": run.CreatedAt, "finished_at": run.FinishedAt, "handoff": run.Handoff}
}

// runnerHandoff is `forge resume <id>` on the machine: park the session if
// it is live, record that it continues in that shell, and say how to open it.
func (s *Server) runnerHandoff(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	cur, err := s.store.RunByID(ctx, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if cur.RunnerID != rn.ID {
		writeError(w, http.StatusConflict, "wrong_machine", fmt.Sprintf("run #%d ran on %s; resume it there", id, cur.RunnerName))
		return
	}
	run, err := s.parkForHandoff(ctx, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if run, err = s.store.HandOffRun(ctx, id, rn.Name+" · shell"); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": runnerRunSummary(*run)})
}
