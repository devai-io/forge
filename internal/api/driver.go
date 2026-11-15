package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/store"
)

// The work driver: every task in progress has an agent on it, and that agent
// keeps going until the task is done or blocked — nobody has to press
// Refresh. Every couple of minutes it looks at each task in progress (in a
// project that is neither paused nor archived) and its agent run:
//
//   - no agent yet          → one starts, on the master, in the task's repo,
//     unattended (the most permissive mode the machine allows)
//     — unless the task was set in progress by hand moments ago (it may be
//     worked on in a terminal Forge does not see)
//   - its turn ended         → one more message: carry on (a few minutes after
//     the turn, so a session someone is watching gets its say first)
//   - parked, or over        → picked up again on its session with that message
//   - working, queued, asking you something, ending, a member's → left alone
//
// So it cannot run away: at most driverMaxPerDay nudges a task a day, at
// least driverGap between two, and after driverMaxIdle nudges in a row that
// changed nothing on the task (status, update, comments) it pauses that task
// until the task changes. Its view of each task is in table task_driver
// (GET /api/driver) and shows in the dashboard's Working on.

const (
	driverTick      = 2 * time.Minute
	driverGap       = 15 * time.Minute
	driverTurnGrace = 3 * time.Minute
	driverHandGrace = 20 * time.Minute
	driverMaxPerDay = 10
	driverMaxIdle   = 3
)

type driverState struct{ mu sync.Mutex }

// RunDriver keeps the tasks in progress moving (see above).
func (s *Server) RunDriver(ctx context.Context) {
	t := time.NewTicker(driverTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.drivePass(ctx)
	}
}

// drivePass looks at every task in progress once.
func (s *Server) drivePass(ctx context.Context) {
	if !s.driver.mu.TryLock() {
		return // the last pass is still going
	}
	defer s.driver.mu.Unlock()
	set, err := s.store.AgentSettings(ctx)
	if err != nil || !set.Driver {
		return
	}
	owner, err := s.store.FirstOwner(ctx)
	if err != nil {
		return
	}
	tasks, err := s.store.DriverTasks(ctx)
	if err != nil {
		slog.Warn("driver: tasks", "err", err)
		return
	}
	states, err := s.store.DriverStates(ctx)
	if err != nil {
		slog.Warn("driver: state", "err", err)
		return
	}
	now := time.Now().UTC()
	for _, t := range tasks {
		s.driveTask(ctx, owner, t, states[t.ID], now)
	}
}

func (s *Server) driveTask(ctx context.Context, owner *store.User, t store.Task, st store.DriverState, now time.Time) {
	st.TaskID = t.ID
	if day := now.Format("2006-01-02"); st.Day != day {
		st.Day, st.Nudges = day, 0
	}
	snap := s.store.TaskSnapshot(ctx, t.ID)
	if st.State == "paused" && snap != st.Snapshot {
		st.State, st.Idle = "", 0 // the task moved: drive it again
	}
	save := func(state, note string) {
		st.State, st.Note = state, note
		if err := s.store.PutDriverState(ctx, st); err != nil {
			slog.Warn("driver: save", "task", t.Ref, "err", err)
		}
	}

	run, _ := s.store.TaskAgent(ctx, t.ID)
	if run != nil {
		id := run.ID
		st.RunID = &id
	}
	if run != nil && run.UserID != nil && *run.UserID != owner.ID {
		if u, err := s.store.UserByID(ctx, *run.UserID); err == nil && u.Role == store.RoleMember {
			save("member", u.Username+"'s agent is on it")
			return
		}
	}
	start := false
	switch {
	case run == nil || (run.SessionID == "" && !store.OneOf(run.Status, store.RunOpenStatuses)):
		if st.LastAt == nil && now.Sub(t.UpdatedAt) < driverHandGrace {
			save("grace", "set in progress just now: an agent starts if nothing picks it up")
			return
		}
		start = true
	case run.Status == "queued":
		save("starting", fmt.Sprintf("run #%d queued on %s", run.ID, run.RunnerName))
		return
	case run.CancelRequested || run.FinishRequested:
		save("ending", fmt.Sprintf("run #%d is ending", run.ID))
		return
	case run.Status == "running" && run.Awaiting == "answer":
		save("needs_you", fmt.Sprintf("run #%d is waiting for your answer", run.ID))
		return
	case run.Status == "running" && run.Awaiting == "":
		save("working", fmt.Sprintf("run #%d is working", run.ID))
		return
	case run.Handoff != "" && run.Status != "running":
		save("handed_off", "continued in "+run.Handoff)
		return
	case run.Status == "running" && now.Sub(s.store.RunLastActivity(ctx, run)) < driverTurnGrace:
		save("working", fmt.Sprintf("run #%d just ended a turn", run.ID))
		return
	}
	// It needs a nudge: start an agent, or tell the one it has to carry on.
	switch {
	case st.State == "paused":
		save("paused", st.Note)
		return
	case st.LastAt != nil && now.Sub(*st.LastAt) < driverGap:
		save("cooling", "nudged "+now.Sub(*st.LastAt).Round(time.Minute).String()+" ago: carries on at the next check")
		return
	case st.Nudges >= driverMaxPerDay:
		save("limit", fmt.Sprintf("%d nudges today: it carries on tomorrow, or when you write to it", driverMaxPerDay))
		return
	}
	if st.LastAt != nil && snap == st.Snapshot {
		st.Idle++
	} else {
		st.Idle = 0
	}
	if st.Idle >= driverMaxIdle {
		save("paused", fmt.Sprintf("%d nudges without any change on the task: check it — the driver picks it up again once the task changes", driverMaxIdle))
		return
	}
	var err error
	var what string
	if start {
		if run, err = s.driverStart(ctx, owner, t); err == nil {
			what = fmt.Sprintf("started run #%d on %s", run.ID, run.RunnerName)
		}
	} else {
		var ok bool
		if run, ok, err = s.sendToAgent(ctx, run, driverText(t), false); err == nil && !ok {
			err = errors.New("its run cannot take another message")
		}
		if err == nil {
			what = fmt.Sprintf("told run #%d to carry on", run.ID)
		}
	}
	nowCopy := now
	st.LastAt, st.Snapshot = &nowCopy, snap
	st.Nudges++
	if err != nil {
		slog.Warn("driver: nudge failed", "task", t.Ref, "err", err)
		save("error", err.Error())
		return
	}
	id := run.ID
	st.RunID = &id
	slog.Info("driver nudged", "task", t.Ref, "run", run.ID, "what", what)
	save("nudged", what)
}

// driverStart starts an agent for a task that has none: on the master, in
// the task's repo (else the project's), the engine's default, unattended.
func (s *Server) driverStart(ctx context.Context, owner *store.User, t store.Task) (*store.Run, error) {
	rn, err := s.store.MasterRunner(ctx)
	if err != nil {
		return nil, errors.New("no master machine to start an agent on")
	}
	repos, err := s.store.AllRepos(ctx)
	if err != nil {
		return nil, err
	}
	var repoID int64
	if t.RepoID != nil {
		for _, r := range repos {
			if r.ID == *t.RepoID && r.Path != "" {
				repoID = r.ID
			}
		}
	}
	if repoID == 0 {
		if r := projectRepo(repos, t.ProjectKey, projectDir(repos, t.ProjectKey)); r != nil {
			repoID = r.ID
		}
	}
	if repoID == 0 {
		return nil, errors.New(t.ProjectKey + " has no repo with a path to work in")
	}
	taskID := t.ID
	in := store.RunInput{RunnerID: rn.ID, RepoID: repoID, Kind: "agent", Prompt: driverStartText(t), TaskID: &taskID,
		Interactive: rn.LastSeenAt == nil || rn.Capabilities.Interactive, Fresh: true, UserID: &owner.ID}
	s.prepareRun(ctx, &in)
	in.PermissionMode = driverMode(in.Engine, rn.Capabilities.PermissionModes)
	run, err := s.store.CreateRun(ctx, in)
	if err != nil {
		return nil, err
	}
	run = s.placeInTmux(ctx, run, true)
	s.wakeups.notify(run.RunnerID)
	return run, nil
}

// driverMode is the permission mode an unattended run gets: the most
// permissive the machine allows for that engine.
func driverMode(engine string, allowed []string) string {
	order := []string{"bypassPermissions", "auto", "acceptEdits", "plan"}
	if engine == "codex" {
		order = []string{"bypassPermissions", "acceptEdits", "plan"}
	}
	for _, m := range order {
		if store.OneOf(m, allowed) {
			return m
		}
	}
	return "plan"
}

const driverUnattended = "Nobody is watching this session: do not stop to ask for confirmation — make reasonable decisions, note them in a forge_comment, and keep going until the task is done. " +
	"The project's change-control notes still hold: never deploy, push to a branch that deploys, or change production without the approval they ask for — when the next step needs it, set the task blocked and say exactly what to approve."

// driverStartText is the first request of an agent the driver starts.
func driverStartText(t store.Task) string {
	return strings.Join([]string{
		fmt.Sprintf("Work on %s %q — it is in progress in Forge.", t.Ref, t.Title),
		"Read it with forge_task (and its comments), check the repo for what has been done so far, and carry the work on from where it stands.",
		driverUnattended,
		"When it is finished, set it done with forge_update_task and forge_comment what you did (commits, decisions).",
		"If you truly cannot go on without me, set it blocked and say exactly what you need.",
	}, "\n")
}

// driverText carries an agent on.
func driverText(t store.Task) string {
	return strings.Join([]string{
		fmt.Sprintf("Forge driver: %s %q is still in progress — carry on with it.", t.Ref, t.Title),
		driverUnattended,
		"If it is finished, set it done with forge_update_task and forge_comment what you did (commits, decisions).",
		"If you truly cannot go on without me, set it blocked and say exactly what you need.",
		"Otherwise keep working on it now.",
	}, "\n")
}

// ── API ────────────────────────────────────────────────────────────────────

type driverTaskView struct {
	store.DriverState
	Ref   string `json:"ref"`
	Title string `json:"title"`
}

// getDriver is the driver's view: whether it is on, and per task in progress
// what it sees and did last.
func (s *Server) getDriver(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	set, err := s.store.AgentSettings(ctx)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	tasks, err := s.store.DriverTasks(ctx)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	states, err := s.store.DriverStates(ctx)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := []driverTaskView{}
	for _, t := range tasks {
		st, ok := states[t.ID]
		if !ok {
			st = store.DriverState{TaskID: t.ID, State: "pending", Note: "checked within a couple of minutes"}
		}
		out = append(out, driverTaskView{DriverState: st, Ref: t.Ref, Title: t.Title})
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": set.Driver, "tasks": out})
}

// driveNow runs a pass right away (the Working on widget's button).
func (s *Server) driveNow(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.drivePass(context.WithoutCancel(r.Context()))
	s.getDriver(w, r, u)
}
