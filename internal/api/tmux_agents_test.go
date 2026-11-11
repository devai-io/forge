package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/devai-io/forge/internal/store"
)

// fakeMachine is a paired runner with a control channel that records what
// the server asks of tmux (create, paste, keys, kill) and says yes to all.
type fakeMachine struct {
	t      *testing.T
	h      *harness
	runner *harness
	rn     store.Runner
	bearer []string
	mu     sync.Mutex
	msgs   []map[string]any
	repoID int64
	path   string
}

func newFakeMachine(t *testing.T, h *harness) *fakeMachine {
	t.Helper()
	m := &fakeMachine{t: t, h: h, runner: &harness{t: t, srv: h.srv, client: &http.Client{}}}
	var token string
	expect(t, "runner", h.newRunner("desk", &m.rn, &token), 201)
	m.bearer = []string{"Authorization", "Bearer " + token}
	var hb struct{ Repos []store.RunnerRepo }
	m.beat(nil, &hb)
	for _, r := range hb.Repos {
		if strings.HasSuffix(r.Path, "/shop_api") {
			m.repoID, m.path = r.ID, r.Path
		}
	}
	if m.repoID == 0 {
		t.Fatal("no shop_api repo in the seed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wsBase := "ws" + strings.TrimPrefix(h.srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsBase+"/api/runner/control", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var msg map[string]any
			_ = json.Unmarshal(data, &msg)
			m.mu.Lock()
			m.msgs = append(m.msgs, msg)
			m.mu.Unlock()
			raw, _ := json.Marshal(map[string]any{"type": "result", "id": msg["id"], "ok": true})
			_ = conn.Write(ctx, websocket.MessageText, raw)
		}
	}()
	time.Sleep(200 * time.Millisecond)
	return m
}

// beat is a heartbeat from a machine that hosts tmux agents, with this tmux list.
func (m *fakeMachine) beat(tmux []map[string]any, out any) {
	m.t.Helper()
	if tmux == nil {
		tmux = []map[string]any{}
	}
	expect(m.t, "heartbeat", m.runner.do("POST", "/api/runner/heartbeat", map[string]any{
		"hostname": "desk", "os": "linux/amd64", "version": "test",
		"capabilities": map[string]any{"claude": true, "permission_modes": []string{"plan", "acceptEdits"}, "interactive": true,
			"resume_in_place": true, "terminal_resume": true, "terminal": true, "tmux_agents": true, "max_concurrent": 2},
		"tmux": tmux,
	}, out, m.bearer...), 200)
}

// wait returns the n-th request of a type the machine got, waiting up to 5 s.
func (m *fakeMachine) wait(typ string, n int) map[string]any {
	m.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		seen := 0
		for _, msg := range m.msgs {
			if msg["type"] == typ {
				seen++
				if seen == n {
					m.mu.Unlock()
					return msg
				}
			}
		}
		m.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	m.t.Fatalf("no %s request #%d (got %v)", typ, n, m.msgs)
	return nil
}

func (m *fakeMachine) hook(body map[string]any) {
	m.t.Helper()
	expect(m.t, "hook "+fmt.Sprint(body["event"]), m.runner.do("POST", "/api/runner/hook", body, nil, m.bearer...), 204)
}

func (m *fakeMachine) run(id int64) store.Run {
	m.t.Helper()
	var run store.Run
	expect(m.t, "run", m.h.do("GET", fmt.Sprintf("/api/runs/%d", id), nil, &run), 200)
	return run
}

func TestTmuxAgentLifecycle(t *testing.T) {
	h := setup(t)
	h.login()
	m := newFakeMachine(t, h)
	var tasks struct{ Tasks []store.Task }
	expect(t, "tasks", h.do("GET", "/api/tasks?project=SHOP", nil, &tasks), 200)
	task := tasks.Tasks[0]
	const name = "F-Shop" // the project's agent session

	// An agent run starts as Claude Code in the project's tmux session, Remote Control on.
	var run store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": m.rn.ID, "repo_id": m.repoID, "kind": "agent",
		"prompt": "Fix the cart", "task_id": task.ID, "permission_mode": "plan"}, &run), 201)
	if run.TmuxSession != name {
		t.Fatalf("tmux session = %q, want %q", run.TmuxSession, name)
	}
	create := m.wait("create", 1)
	sid, _ := create["session_id"].(string)
	env, _ := create["env"].(map[string]any)
	if create["session"] != name || create["start"] != "claude" || create["prompt"] != "Fix the cart" || !sessionIDRe.MatchString(sid) ||
		create["permission_mode"] != "plan" || create["cwd"] != m.path || env["FORGE_RUN_ID"] != fmt.Sprint(run.ID) ||
		create["remote_control"] != name {
		t.Fatalf("create = %v", create)
	}
	if run = m.run(run.ID); run.Status != "running" || run.SessionID != sid {
		t.Fatalf("started run = %+v", run)
	}
	// It is the server's to drive, never a claim for the machine's headless runner.
	expect(t, "nothing to claim", m.runner.do("POST", "/api/runner/claim", nil, nil, m.bearer...), 204)
	h.api.projectSessions.started = map[string]time.Time{} // the session has long drawn its first screen

	// The session's hooks report its turn.
	m.hook(map[string]any{"event": "UserPromptSubmit", "session_id": sid, "run_id": run.ID, "tmux_session": name, "prompt": "Fix the cart"})
	assistant, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant",
		"content": []map[string]any{{"type": "text", "text": "Fixed the cart."}}}})
	m.hook(map[string]any{"event": "Stop", "session_id": sid, "run_id": run.ID, "tmux_session": name,
		"last_message": "Fixed the cart.", "events": []map[string]any{{"kind": "claude", "data": json.RawMessage(assistant)}}})
	if run = m.run(run.ID); run.Awaiting != "reply" || run.Result != "Fixed the cart." || run.NumTurns == nil || *run.NumTurns != 1 {
		t.Fatalf("after the turn = %+v", run)
	}
	var ev struct{ Events []store.RunEvent }
	expect(t, "events", h.do("GET", fmt.Sprintf("/api/runs/%d/events", run.ID), nil, &ev), 200)
	if !strings.Contains(string(ev.Events[len(ev.Events)-1].Data), "Fixed the cart.") {
		t.Fatalf("events = %v", ev.Events)
	}

	// One project, one agent: another run for the project — any task, or none — is a
	// message, typed into the session.
	var again store.Run
	expect(t, "same project", h.do("POST", "/api/runs", map[string]any{"runner_id": m.rn.ID, "repo_id": m.repoID, "kind": "agent",
		"prompt": "Also the tests"}, &again), 200)
	if again.ID != run.ID || !again.Continued {
		t.Fatalf("continued = %+v", again)
	}
	paste := m.wait("paste", 1)
	if paste["session"] != name || paste["text"] != "Also the tests" || paste["enter"] != true {
		t.Fatalf("paste = %v", paste)
	}
	if run = m.run(run.ID); run.Awaiting != "" || run.InboxAfter == 0 {
		t.Fatalf("after the paste = %+v", run)
	}

	// Claude exits in tmux (gone from the machine's list): the run waits; a reply starts it again with --resume.
	if _, err := h.store.DB.Exec(context.Background(), `UPDATE runs SET started_at = ts_add(now(), -600) WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	m.beat([]map[string]any{{"name": name, "windows": 1, "command": "zsh", "claude": false}}, nil)
	if run = m.run(run.ID); run.Status != "waiting" || run.TmuxSession != name {
		t.Fatalf("after Claude exited = %+v", run)
	}
	expect(t, "reply", h.do("POST", fmt.Sprintf("/api/runs/%d/messages", run.ID), map[string]string{"text": "carry on"}, nil), 200)
	resume := m.wait("create", 2)
	if resume["session"] != name || resume["resume"] != sid || resume["prompt"] != "carry on" || resume["reuse"] != true || resume["session_id"] != nil {
		t.Fatalf("resume = %v", resume)
	}
	if run = m.run(run.ID); run.Status != "running" || run.SessionID != sid {
		t.Fatalf("resumed = %+v", run)
	}

	// Interrupt is Esc in the session; Finish closes the tmux session and the run.
	expect(t, "interrupt", h.do("POST", fmt.Sprintf("/api/runs/%d/cancel", run.ID), nil, nil), 200)
	if keys := m.wait("keys", 1); keys["text"] != "\x1b" || keys["session"] != name {
		t.Fatalf("keys = %v", keys)
	}
	if run = m.run(run.ID); run.Status != "running" {
		t.Fatalf("an interrupt must not end the run: %+v", run)
	}
	expect(t, "finish", h.do("POST", fmt.Sprintf("/api/runs/%d/finish", run.ID), nil, nil), 200)
	if kill := m.wait("kill", 1); kill["session"] != name {
		t.Fatalf("kill = %v", kill)
	}
	deadline := time.Now().Add(3 * time.Second)
	for run = m.run(run.ID); run.Status != "succeeded" && time.Now().Before(deadline); run = m.run(run.ID) {
		time.Sleep(50 * time.Millisecond)
	}
	if run.Status != "succeeded" {
		t.Fatalf("finished = %+v", run)
	}
	m.beat(nil, nil) // the tmux session is gone

	// A finished agent is still the task's agent: a new request for the task reopens it on its session.
	expect(t, "reopen", h.do("POST", "/api/runs", map[string]any{"runner_id": m.rn.ID, "repo_id": m.repoID, "kind": "agent",
		"prompt": "One more thing", "task_id": task.ID}, &again), 200)
	if again.ID != run.ID || !again.Continued {
		t.Fatalf("reopened = %+v", again)
	}
	if third := m.wait("create", 3); third["session"] != name || third["resume"] != sid || third["prompt"] != "One more thing" {
		t.Fatalf("reopen create = %v", third)
	}
	// Unless the user asks for a fresh agent: an extra session next to the project's.
	var fresh store.Run
	expect(t, "fresh", h.do("POST", "/api/runs", map[string]any{"runner_id": m.rn.ID, "repo_id": m.repoID, "kind": "agent",
		"prompt": "Start over", "task_id": task.ID, "fresh": true}, &fresh), 201)
	if fresh.ID == run.ID || fresh.TmuxSession != name+"-2" {
		t.Fatalf("fresh = %+v (an extra agent is %s-2)", fresh, name)
	}
	if extra := m.wait("create", 4); extra["session"] != name+"-2" || extra["remote_control"] != name+"-2" || extra["prompt"] != "Start over" {
		t.Fatalf("extra create = %v", extra)
	}

	// A conversation picked up again while its session holds another one comes back in a session of its own.
	expect(t, "finish the first", h.do("POST", fmt.Sprintf("/api/runs/%d/finish", run.ID), nil, nil), 200)
	m.wait("kill", 2)
	for run = m.run(run.ID); run.Status != "succeeded" && time.Now().Before(deadline.Add(5*time.Second)); run = m.run(run.ID) {
		time.Sleep(50 * time.Millisecond)
	}
	var other store.Run
	expect(t, "another conversation", h.do("POST", "/api/runs", map[string]any{"runner_id": m.rn.ID, "repo_id": m.repoID, "kind": "agent",
		"prompt": "Something else", "fresh": true}, &other), 201)
	if other.TmuxSession != name {
		t.Fatalf("the project's session was free: %+v", other)
	}
	m.wait("create", 5)
	m.beat([]map[string]any{{"name": name, "windows": 1, "command": "claude", "claude": true},
		{"name": name + "-2", "windows": 1, "command": "claude", "claude": true}}, nil)
	expect(t, "continue the first", h.do("POST", "/api/runs", map[string]any{"kind": "agent", "prompt": "Back to the cart", "resume_run_id": run.ID}, &again), 200)
	if back := m.wait("create", 6); back["session"] != name+"-3" || back["resume"] != sid || back["prompt"] != "Back to the cart" {
		t.Fatalf("picked up elsewhere = %v", back)
	}
	if run = m.run(run.ID); run.TmuxSession != name+"-3" || run.Status != "running" {
		t.Fatalf("picked up = %+v", run)
	}
}

func TestTmuxHookRegistersHandStartedSessions(t *testing.T) {
	h := setup(t)
	h.login()
	m := newFakeMachine(t, h)
	const sid = "0b6e3f4a-1c2d-4e5f-8a9b-0c1d2e3f4a5b"
	var list struct{ Runs []store.Run }
	runs := func() []store.Run {
		t.Helper()
		expect(t, "runs", h.do("GET", "/api/runs", nil, &list), 200)
		return list.Runs
	}

	// Outside every project: nothing to register.
	m.hook(map[string]any{"event": "UserPromptSubmit", "session_id": sid, "cwd": "/tmp/scratch", "tmux_session": "scratch", "prompt": "hello"})
	if len(runs()) != 0 {
		t.Fatalf("runs = %+v", list.Runs)
	}
	// In a repo, but nobody has asked it anything: an open session is not a run.
	m.hook(map[string]any{"event": "SessionStart", "session_id": sid, "cwd": m.path + "/pkg", "tmux_session": "work", "source": "startup"})
	m.hook(map[string]any{"event": "Notification", "session_id": sid, "tmux_session": "work", "notification_type": "idle_prompt"})
	if len(runs()) != 0 {
		t.Fatalf("an idle session became a run: %+v", list.Runs)
	}
	// Its first request makes it one.
	m.hook(map[string]any{"event": "UserPromptSubmit", "session_id": sid, "cwd": m.path + "/pkg", "tmux_session": "work", "prompt": "check the logs"})
	if len(runs()) != 1 {
		t.Fatalf("runs = %+v", list.Runs)
	}
	run := list.Runs[0]
	if run.Origin != "tmux" || run.TmuxSession != "work" || run.Status != "running" || run.ProjectKey != "SHOP" ||
		run.RepoName != "shop_api" || run.SessionID != sid || run.Awaiting != "" || run.Prompt != "check the logs" {
		t.Fatalf("registered = %+v", run)
	}
	m.hook(map[string]any{"event": "Notification", "session_id": sid, "tmux_session": "work",
		"notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"})
	if run = m.run(run.ID); run.Awaiting != "answer" {
		t.Fatalf("waiting for permission = %+v", run)
	}
	m.hook(map[string]any{"event": "Stop", "session_id": sid, "tmux_session": "work", "last_message": "The logs are clean."})
	if run = m.run(run.ID); run.Result != "The logs are clean." || run.Awaiting != "reply" {
		t.Fatalf("after the turn = %+v", run)
	}

	// The terminal list says which run each tmux session is.
	m.beat([]map[string]any{{"name": "work", "windows": 1, "command": "claude", "claude": true, "path": m.path}}, nil)
	var hosts struct{ Hosts []store.TerminalHost }
	expect(t, "hosts", h.do("GET", "/api/terminal/hosts", nil, &hosts), 200)
	if s := hosts.Hosts[0].Sessions[0]; s.RunID == nil || *s.RunID != run.ID {
		t.Fatalf("session = %+v", s)
	}

	// /clear in the same tmux session: the old conversation is over; the new one is a run with its first request.
	const sid2 = "1c7f4a5b-2d3e-4f60-9b0c-1d2e3f4a5b6c"
	m.hook(map[string]any{"event": "SessionStart", "session_id": sid2, "cwd": m.path, "tmux_session": "work", "source": "clear"})
	if old := m.run(run.ID); old.Status != "succeeded" {
		t.Fatalf("the cleared conversation = %+v", old)
	}
	expect(t, "runs", h.do("GET", "/api/runs?status=running", nil, &list), 200)
	if len(list.Runs) != 0 {
		t.Fatalf("after /clear, before a request = %+v", list.Runs)
	}
	m.hook(map[string]any{"event": "UserPromptSubmit", "session_id": sid2, "cwd": m.path, "tmux_session": "work", "prompt": "now the tests"})
	expect(t, "runs", h.do("GET", "/api/runs?status=running", nil, &list), 200)
	if len(list.Runs) != 1 || list.Runs[0].SessionID != sid2 || list.Runs[0].TmuxSession != "work" {
		t.Fatalf("after /clear = %+v", list.Runs)
	}
	// Exiting Claude ends a hand-started run.
	m.hook(map[string]any{"event": "SessionEnd", "session_id": sid2, "tmux_session": "work", "reason": "prompt_input_exit"})
	if r := m.run(list.Runs[0].ID); r.Status != "succeeded" {
		t.Fatalf("after exit = %+v", r)
	}

	// A session in the project's own folder (above its repos) is the project's too.
	const sid3 = "2d8a5b6c-3e4f-4a71-8c1d-2e3f4a5b6c7d"
	dir := m.path[:strings.LastIndex(m.path, "/")]
	m.hook(map[string]any{"event": "UserPromptSubmit", "session_id": sid3, "cwd": dir, "tmux_session": "F-Shop", "prompt": "what is next?"})
	expect(t, "runs", h.do("GET", "/api/runs?status=running", nil, &list), 200)
	if len(list.Runs) != 1 || list.Runs[0].ProjectKey != "SHOP" || list.Runs[0].TmuxSession != "F-Shop" || list.Runs[0].SessionID != sid3 {
		t.Fatalf("in the project folder = %+v", list.Runs)
	}
	// Opened again from Forge, it starts in that folder, in Claude Code's own permission mode.
	m.hook(map[string]any{"event": "SessionEnd", "session_id": sid3, "tmux_session": "F-Shop"})
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)
	expect(t, "open in terminal", h.do("POST", fmt.Sprintf("/api/runs/%d/handoff", list.Runs[0].ID), nil, nil), 200)
	if again := m.wait("create", 1); again["resume"] != sid3 || again["cwd"] != dir || again["permission_mode"] != "" {
		t.Fatalf("reopened = %v", again)
	}
}

func TestHeadlessRunsStayOutOfTmux(t *testing.T) {
	h := setup(t)
	h.login()
	m := newFakeMachine(t, h)
	off := store.AgentSettings{Tmux: false}
	expect(t, "setting", h.do("PATCH", "/api/agent-settings", off, nil), 200)
	var run store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": m.rn.ID, "repo_id": m.repoID, "kind": "agent",
		"prompt": "Look", "permission_mode": "plan"}, &run), 201)
	if run.TmuxSession != "" {
		t.Fatalf("with the setting off the run is headless: %+v", run)
	}
	var claim struct{ Run store.Run }
	expect(t, "claim", m.runner.do("POST", "/api/runner/claim", nil, &claim, m.bearer...), 200)
	// A headless run's heartbeat reconciliation still fails a lost run, and never a tmux one.
	if _, err := h.store.DB.Exec(context.Background(), `UPDATE runs SET started_at = ts_add(now(), -600) WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	m.beat(nil, nil)
	if r := m.run(run.ID); r.Status != "failed" {
		t.Fatalf("lost headless run = %+v", r)
	}
}

// A machine that was only out of reach (its connection died, not the agent)
// must not cost a run: failed as lost after ten silent minutes, the run is
// what the machine says once it reports again.
func TestOrphanedRunComesBackWithItsMachine(t *testing.T) {
	h := setup(t)
	h.login()
	m := newFakeMachine(t, h)
	ctx := context.Background()
	expect(t, "headless", h.do("PATCH", "/api/agent-settings", map[string]any{"tmux": false}, nil), 200)
	var run store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": m.rn.ID, "repo_id": m.repoID, "kind": "agent",
		"prompt": "Look", "permission_mode": "plan", "interactive": true}, &run), 201)
	var claim struct{ Run store.Run }
	expect(t, "claim", m.runner.do("POST", "/api/runner/claim", nil, &claim, m.bearer...), 200)
	orphan := func() {
		t.Helper()
		if _, err := h.store.DB.Exec(ctx, `UPDATE runners SET last_seen_at = ts_add(now(), -900) WHERE id = $1`, m.rn.ID); err != nil {
			t.Fatal(err)
		}
		if err := h.store.FailOrphanedRuns(ctx); err != nil {
			t.Fatal(err)
		}
		if r := m.run(run.ID); r.Status != "failed" || r.Error != "runner went offline" {
			t.Fatalf("silent for 15 minutes = %+v", r)
		}
	}
	beat := func(running []int64) {
		t.Helper()
		expect(t, "heartbeat", m.runner.do("POST", "/api/runner/heartbeat", map[string]any{"hostname": "desk", "version": "test", "running": running,
			"capabilities": map[string]any{"claude": true, "permission_modes": []string{"plan"}, "interactive": true, "resume_in_place": true}}, nil, m.bearer...), 200)
	}

	// Back, and still working on it: the run is running again.
	orphan()
	beat([]int64{run.ID})
	if r := m.run(run.ID); r.Status != "running" || r.Error != "" || r.FinishedAt != nil {
		t.Fatalf("the machine still runs it = %+v", r)
	}
	// Back with the run's end (it finished while out of reach): the report counts.
	orphan()
	expect(t, "late finish", m.runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", run.ID),
		map[string]any{"status": "waiting", "session_id": "0b6e3f4a-1c2d-4e5f-8a9b-0c1d2e3f4a5b", "result": "Looked."}, nil, m.bearer...), 204)
	if r := m.run(run.ID); r.Status != "waiting" || r.Error != "" || r.Result != "Looked." {
		t.Fatalf("after the late report = %+v", r)
	}
	// A run the machine no longer has is still lost.
	var other store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": m.rn.ID, "repo_id": m.repoID, "kind": "agent",
		"prompt": "Another", "permission_mode": "plan", "fresh": true}, &other), 201)
	expect(t, "claim", m.runner.do("POST", "/api/runner/claim", nil, &claim, m.bearer...), 200)
	run = other
	orphan()
	beat([]int64{})
	if r := m.run(run.ID); r.Status != "failed" {
		t.Fatalf("a run the machine lost = %+v", r)
	}
}
