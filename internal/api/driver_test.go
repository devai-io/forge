package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

type driverView struct {
	Enabled bool
	Tasks   []struct {
		TaskID int64 `json:"task_id"`
		Ref    string
		State  string
		Note   string
		Nudges int   `json:"nudges_today"`
		RunID  int64 `json:"run_id"`
	}
}

func (v driverView) state(id int64) (string, int, int64) {
	for _, t := range v.Tasks {
		if t.TaskID == id {
			return t.State, t.Nudges, t.RunID
		}
	}
	return "", 0, 0
}

// TestDriverKeepsTasksMoving: a task in progress gets an agent without
// anyone asking, its agent is told to carry on when its turn ends, and the
// driver stops on its own when nudges change nothing.
func TestDriverKeepsTasksMoving(t *testing.T) {
	h := setup(t)
	h.login()
	m := newFakeMachine(t, h)
	ctx := context.Background()
	expect(t, "make master", h.do("PATCH", fmt.Sprintf("/api/runners/%d", m.rn.ID), map[string]string{"role": "master"}, nil), 200)
	var tasks struct{ Tasks []store.Task }
	expect(t, "tasks", h.do("GET", "/api/tasks?project=SHOP", nil, &tasks), 200)
	task := tasks.Tasks[0]
	expect(t, "in progress", h.do("PATCH", fmt.Sprintf("/api/tasks/%d", task.ID), map[string]string{"status": "in_progress"}, nil), 200)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := h.store.DB.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	var v driverView
	pass := func() {
		t.Helper()
		expect(t, "drive", h.do("POST", "/api/driver/run", nil, &v), 200)
	}

	// Just set in progress by hand: it may be worked on in a terminal.
	pass()
	if st, _, _ := v.state(task.ID); st != "grace" || !v.Enabled {
		t.Fatalf("fresh task = %+v", v)
	}
	// Nothing picked it up: an agent starts, unattended, in the project's tmux session.
	exec(`UPDATE tasks SET updated_at = ts_add(now(), -3600) WHERE id = $1`, task.ID)
	pass()
	st, nudges, runID := v.state(task.ID)
	if st != "nudged" || nudges != 1 || runID == 0 {
		t.Fatalf("after start = %+v", v)
	}
	if c := m.wait("create", 1); c["prompt"] != driverStartText(task) || c["permission_mode"] != "acceptEdits" {
		t.Fatalf("create = %v", c)
	}
	run := m.run(runID)
	if run.TaskID == nil || *run.TaskID != task.ID || run.PermissionMode != "acceptEdits" || run.TmuxSession == "" || run.Status != "running" {
		t.Fatalf("driver run = %+v", run)
	}

	// Working: left alone.
	pass()
	if st, n, _ := v.state(task.ID); st != "working" || n != 1 {
		t.Fatalf("working = %+v", v)
	}
	// Its turn ended a while ago: it is told to carry on (typed into its session).
	const sid = "0b6e3f4a-1c2d-4e5f-8a9b-0c1d2e3f4a5b"
	m.hook(map[string]any{"event": "Stop", "session_id": sid, "run_id": runID, "tmux_session": run.TmuxSession, "last_message": "Step one done."})
	h.api.projectSessions.started = nil
	exec(`UPDATE run_events SET at = ts_add(now(), -600) WHERE run_id = $1`, runID)
	exec(`UPDATE task_driver SET last_at = ts_add(now(), -3600) WHERE task_id = $1`, task.ID)
	pass()
	if st, n, _ := v.state(task.ID); st != "nudged" || n != 2 {
		t.Fatalf("after the turn = %+v", v)
	}
	if p := m.wait("paste", 1); p["session"] != run.TmuxSession || p["text"] != driverText(task) {
		t.Fatalf("paste = %v", p)
	}
	// Too soon for another.
	m.hook(map[string]any{"event": "Stop", "session_id": sid, "run_id": runID, "tmux_session": run.TmuxSession, "last_message": "Still going."})
	exec(`UPDATE run_events SET at = ts_add(now(), -600) WHERE run_id = $1`, runID)
	pass()
	if st, _, _ := v.state(task.ID); st != "cooling" {
		t.Fatalf("cooling = %+v", v)
	}
	// Nudges that change nothing on the task: after three in a row it pauses.
	for i := 0; i < 3; i++ {
		m.hook(map[string]any{"event": "Stop", "session_id": sid, "run_id": runID, "tmux_session": run.TmuxSession, "last_message": "Nothing new."})
		exec(`UPDATE run_events SET at = ts_add(now(), -600) WHERE run_id = $1`, runID)
		exec(`UPDATE task_driver SET last_at = ts_add(now(), -3600) WHERE task_id = $1`, task.ID)
		pass()
	}
	if st, _, _ := v.state(task.ID); st != "paused" {
		t.Fatalf("idle = %+v", v)
	}
	// The task moves (a comment): driven again.
	expect(t, "comment", h.do("POST", fmt.Sprintf("/api/tasks/%d/comments", task.ID), map[string]string{"body": "progress"}, nil), 201)
	m.hook(map[string]any{"event": "Stop", "session_id": sid, "run_id": runID, "tmux_session": run.TmuxSession, "last_message": "Moved on."})
	exec(`UPDATE run_events SET at = ts_add(now(), -600) WHERE run_id = $1`, runID)
	exec(`UPDATE task_driver SET last_at = ts_add(now(), -3600) WHERE task_id = $1`, task.ID)
	pass()
	if st, _, _ := v.state(task.ID); st != "nudged" {
		t.Fatalf("after the task moved = %+v", v)
	}
	// Done: no longer driven. Off: nothing happens.
	expect(t, "done", h.do("PATCH", fmt.Sprintf("/api/tasks/%d", task.ID), map[string]string{"status": "done"}, nil), 200)
	pass()
	if st, _, _ := v.state(task.ID); st != "" {
		t.Fatalf("done task still driven: %+v", v)
	}
	expect(t, "off", h.do("PATCH", "/api/agent-settings", map[string]any{"driver": false}, nil), 200)
	exec(`UPDATE tasks SET updated_at = ts_add(now(), -3600) WHERE status = 'in_progress'`)
	var before, after struct{ Runs []store.Run }
	expect(t, "runs", h.do("GET", "/api/runs", nil, &before), 200)
	pass()
	expect(t, "runs", h.do("GET", "/api/runs", nil, &after), 200)
	if v.Enabled || len(after.Runs) != len(before.Runs) {
		t.Fatalf("driver off: enabled %v, runs %d → %d", v.Enabled, len(before.Runs), len(after.Runs))
	}
}

func TestDriverMode(t *testing.T) {
	if m := driverMode("claude", []string{"plan", "acceptEdits", "bypassPermissions"}); m != "bypassPermissions" {
		t.Fatal(m)
	}
	if m := driverMode("codex", []string{"plan", "auto", "acceptEdits"}); m != "acceptEdits" {
		t.Fatal(m)
	}
	if m := driverMode("claude", []string{"plan", "auto"}); m != "auto" {
		t.Fatal(m)
	}
}
