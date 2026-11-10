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

func TestProjectDir(t *testing.T) {
	repos := []store.RepoWithProject{
		{Repo: store.Repo{Path: "/home/demo/dev/shop/shop_api"}, ProjectKey: "SHOP"},
		{Repo: store.Repo{Path: "/home/demo/dev/shop/shop_ui"}, ProjectKey: "SHOP"},
		{Repo: store.Repo{Path: "/home/demo/dev/blog"}, ProjectKey: "BLOG"},
		{Repo: store.Repo{Path: "/home/demo/dev/game/app"}, ProjectKey: "GAME"},
		{Repo: store.Repo{Path: "/home/demo/dev/blog/game_site"}, ProjectKey: "GAME"},
		{Repo: store.Repo{Path: ""}, ProjectKey: "NONE"},
	}
	for key, want := range map[string]string{
		"SHOP": "/home/demo/dev/shop",     // the folder its repos share
		"BLOG": "/home/demo/dev/blog",     // its one repo
		"GAME": "/home/demo/dev/game/app", // the shared folder holds another project's repo
		"NONE": "",
	} {
		if got := projectDir(repos, key); got != want {
			t.Errorf("%s: %q, want %q", key, got, want)
		}
	}
	if got := projectSessionName("RC Grand Prix!"); got != "F-RC-Grand-Prix" {
		t.Errorf("name = %q", got)
	}
}

// TestProjectSessions plays the master machine: nothing starts by itself;
// the project's button opens its agent (with a model, an effort and maybe a
// first request), a request for an open agent is typed into it, and an extra
// agent gets a session of its own.
func TestProjectSessions(t *testing.T) {
	h := setup(t)
	h.login()
	var rn store.Runner
	var token string
	expect(t, "runner", h.newRunner("desk", &rn, &token), 201)
	expect(t, "make master", h.do("PATCH", fmt.Sprintf("/api/runners/%d", rn.ID), map[string]string{"role": "master"}, nil), 200)
	bearer := []string{"Authorization", "Bearer " + token}
	machine := &harness{t: t, srv: h.srv, client: &http.Client{}}
	beat := func(tmux []map[string]any) {
		t.Helper()
		expect(t, "heartbeat", machine.do("POST", "/api/runner/heartbeat", map[string]any{
			"capabilities": map[string]any{"claude": true, "terminal": true, "tmux_agents": true, "interactive": true, "resume_in_place": true,
				"permission_modes": []string{"plan", "bypassPermissions"}},
			"tmux": tmux,
		}, nil, bearer...), 200)
	}
	claude := func(name string) map[string]any {
		return map[string]any{"name": name, "windows": 1, "created": "2026-10-29T08:00:00Z", "command": "claude", "claude": true,
			"window_list": []map[string]any{{"index": 0, "command": "claude", "claude": true, "active": true}}}
	}
	shell := map[string]any{"name": "F-Game", "windows": 1, "created": "2026-10-29T08:00:00Z", "command": "zsh",
		"window_list": []map[string]any{{"index": 0, "command": "zsh", "active": true}}}
	beat([]map[string]any{claude("F-Shop"), shell})

	var view struct {
		Settings store.ProjectSessionsSettings
		Host     *projectSessionHost
		Sessions []ProjectSession
	}
	expect(t, "view", h.do("GET", "/api/project-sessions", nil, &view), 200)
	if view.Host == nil || view.Host.Online || len(view.Sessions) != 3 || view.Sessions[0].State != "offline" {
		t.Fatalf("before the control socket: %+v", view)
	}
	expect(t, "the master is not connected", h.do("POST", "/api/project-sessions/BLOG", map[string]any{}, nil), 409)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsBase := "ws" + strings.TrimPrefix(h.srv.URL, "http")
	control, _, err := websocket.Dial(ctx, wsBase+"/api/runner/control", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer control.CloseNow()
	var mu sync.Mutex
	var got []map[string]any
	go func() {
		for {
			_, data, err := control.Read(ctx)
			if err != nil {
				return
			}
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			mu.Lock()
			if m["type"] != "list" {
				got = append(got, m)
			}
			mu.Unlock()
			res := map[string]any{"type": "result", "id": m["id"], "ok": true}
			switch m["type"] {
			case "rc_url":
				res["text"] = "https://claude.ai/code/session_abc123"
			case "list": // an agent that cannot say: the heartbeat's list is used
				res["ok"], res["error"] = false, `unknown request "list"`
			}
			raw, _ := json.Marshal(res)
			_ = control.Write(ctx, websocket.MessageText, raw)
		}
	}()
	time.Sleep(200 * time.Millisecond)
	sent := func(typ string) []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		var out []map[string]any
		for _, m := range got {
			if m["type"] == typ {
				out = append(out, m)
			}
		}
		return out
	}

	// A pass starts nothing and restarts nothing: it only reads the link of the open session.
	h.api.projectSessionsPass(ctx)
	if n := len(sent("create")) + len(sent("kill")); n != 0 {
		t.Fatalf("a pass started or closed sessions: %v", got)
	}
	if rc := sent("rc_url"); len(rc) != 1 || rc[0]["session"] != "F-Shop" {
		t.Fatalf("rc_url = %v", rc)
	}
	expect(t, "view", h.do("GET", "/api/project-sessions", nil, &view), 200)
	states := func() map[string]ProjectSession {
		out := map[string]ProjectSession{}
		for _, e := range view.Sessions {
			out[e.Name] = e
		}
		return out
	}
	if st := states(); st["F-Shop"].State != "running" || st["F-Shop"].URL == nil || *st["F-Shop"].URL != "https://claude.ai/code/session_abc123" ||
		st["F-Shop"].RunID != nil || st["F-Game"].State != "stopped" || st["F-Blog"].State != "missing" || st["F-Blog"].ProjectStatus == "" {
		t.Fatalf("states = %+v", st)
	}

	// The button, no request: the session starts with the model and effort asked for, Remote Control on.
	expect(t, "settings need elevation", h.do("PATCH", "/api/project-sessions", map[string]any{"skip_permissions": true}, nil), 403)
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)
	expect(t, "skip permissions", h.do("PATCH", "/api/project-sessions", map[string]any{"skip_permissions": true}, nil), 200)
	var res struct {
		Session  string
		RunnerID int64 `json:"runner_id"`
		Run      *store.Run
		Started  bool
	}
	expect(t, "bad effort", h.do("POST", "/api/project-sessions/BLOG", map[string]any{"effort": "extreme"}, nil), 422)
	expect(t, "open BLOG", h.do("POST", "/api/project-sessions/BLOG", map[string]any{"model": "opus", "effort": "high"}, &res), 200)
	creates := sent("create")
	if c := creates[len(creates)-1]; len(creates) != 1 || res.Session != "F-Blog" || !res.Started || res.Run != nil || res.RunnerID != rn.ID ||
		c["session"] != "F-Blog" || c["cwd"] != "/home/demo/dev/blog" || c["remote_control"] != "F-Blog" || c["start"] != "claude" ||
		c["model"] != "opus" || c["effort"] != "high" || c["skip_permissions"] != true || c["prompt"] != nil {
		t.Fatalf("open BLOG = %+v, create = %v", res, c)
	}
	// Still starting: a second click starts nothing; a request has to wait for Claude Code's first screen.
	beat([]map[string]any{claude("F-Shop"), shell, {"name": "F-Blog", "windows": 1, "command": "zsh"}})
	expect(t, "again", h.do("POST", "/api/project-sessions/BLOG", map[string]any{}, &res), 200)
	if res.Started || len(sent("create")) != 1 {
		t.Fatalf("started twice: %+v", res)
	}
	expect(t, "too early for a request", h.do("POST", "/api/project-sessions/BLOG", map[string]any{"prompt": "hello"}, nil), 409)
	expect(t, "view", h.do("GET", "/api/project-sessions", nil, &view), 200)
	if st := states(); st["F-Blog"].State != "starting" {
		t.Fatalf("F-Blog = %+v", st["F-Blog"])
	}

	// A session whose Claude exited (a shell is left) starts again in place, asking for permissions when told to.
	no := false
	expect(t, "open GAME", h.do("POST", "/api/project-sessions/GAME", map[string]any{"skip_permissions": no}, &res), 200)
	creates = sent("create")
	if c := creates[len(creates)-1]; len(creates) != 2 || c["session"] != "F-Game" || c["reuse"] != true || c["skip_permissions"] != false ||
		c["cwd"] != "/home/demo/dev/game" {
		t.Fatalf("open GAME: create = %v", c)
	}

	// A request for an agent that is open with no conversation is typed into it, and is a run from then on.
	expect(t, "ask SHOP", h.do("POST", "/api/project-sessions/SHOP", map[string]any{"prompt": "Fix the cart", "model": "sonnet"}, &res), 200)
	pastes := sent("paste")
	if res.Run == nil || res.Started || res.Run.TmuxSession != "F-Shop" || res.Run.Status != "running" || res.Run.SessionID != "" ||
		len(pastes) != 1 || pastes[0]["session"] != "F-Shop" || pastes[0]["text"] != "Fix the cart" || pastes[0]["enter"] != true || len(sent("create")) != 2 {
		t.Fatalf("ask SHOP = %+v, pastes = %v", res.Run, pastes)
	}
	runID := res.Run.ID
	const sid = "0b6e3f4a-1c2d-4e5f-8a9b-0c1d2e3f4a5b"
	expect(t, "its hook", machine.do("POST", "/api/runner/hook", map[string]any{"event": "UserPromptSubmit", "session_id": sid,
		"cwd": "/home/demo/dev/shop", "tmux_session": "F-Shop", "prompt": "Fix the cart"}, nil, bearer...), 204)
	var run store.Run
	expect(t, "run", h.do("GET", fmt.Sprintf("/api/runs/%d", runID), nil, &run), 200)
	if run.SessionID != sid || run.Origin != "forge" || run.PermissionMode != "bypassPermissions" || run.Model != "sonnet" || run.ProjectKey != "SHOP" {
		t.Fatalf("adopted run = %+v", run)
	}
	var list struct{ Runs []store.Run }
	expect(t, "runs", h.do("GET", "/api/runs?project=SHOP", nil, &list), 200)
	if len(list.Runs) != 1 {
		t.Fatalf("the hook made another run: %+v", list.Runs)
	}
	// The next request is one more message to the same conversation.
	expect(t, "ask SHOP again", h.do("POST", "/api/project-sessions/SHOP", map[string]any{"prompt": "Also the tests"}, &res), 200)
	if pastes = sent("paste"); res.Run == nil || res.Run.ID != runID || len(pastes) != 2 || pastes[1]["text"] != "Also the tests" {
		t.Fatalf("second request = %+v, pastes = %v", res.Run, pastes)
	}
	// And the button alone just says where it is.
	expect(t, "open SHOP", h.do("POST", "/api/project-sessions/SHOP", map[string]any{}, &res), 200)
	if res.Started || res.Run == nil || res.Run.ID != runID || res.Session != "F-Shop" {
		t.Fatalf("open SHOP = %+v", res)
	}

	// An extra agent: its own session, started with its request, in the project's folder.
	expect(t, "extra", h.do("POST", "/api/project-sessions/SHOP", map[string]any{"prompt": "Review the checkout", "extra": true, "effort": "low", "skip_permissions": no}, &res), 200)
	creates = sent("create")
	c := creates[len(creates)-1]
	env, _ := c["env"].(map[string]any)
	if len(creates) != 3 || res.Session != "F-Shop-2" || !res.Started || res.Run == nil || res.Run.ID == runID ||
		c["session"] != "F-Shop-2" || c["remote_control"] != "F-Shop-2" || c["prompt"] != "Review the checkout" || c["effort"] != "low" ||
		c["cwd"] != "/home/demo/dev/shop" || c["permission_mode"] != "" || !sessionIDRe.MatchString(fmt.Sprint(c["session_id"])) ||
		env["FORGE_RUN_ID"] != fmt.Sprint(res.Run.ID) {
		t.Fatalf("extra = %+v, create = %v", res, c)
	}
	if res.Run.PermissionMode != "default" {
		t.Fatalf("extra run = %+v", res.Run)
	}
	beat([]map[string]any{claude("F-Shop"), claude("F-Shop-2"), shell})
	expect(t, "view", h.do("GET", "/api/project-sessions", nil, &view), 200)
	st := states()
	if e := st["F-Shop"]; e.RunID == nil || *e.RunID != runID || e.RunStatus != "running" || e.Extra != 0 || e.RunPrompt != "Fix the cart" {
		t.Fatalf("F-Shop = %+v", e)
	}
	if e := st["F-Shop-2"]; e.Extra != 2 || e.ProjectKey != "SHOP" || e.State != "running" || e.RunID == nil || *e.RunID != res.Run.ID {
		t.Fatalf("F-Shop-2 = %+v", e)
	}

	// Closing one finishes its conversation and its tmux session.
	expect(t, "not this project's", h.do("DELETE", "/api/project-sessions/BLOG/F-Shop-2", nil, nil), 404)
	expect(t, "close the extra", h.do("DELETE", "/api/project-sessions/SHOP/F-Shop-2", nil, nil), 204)
	if kills := sent("kill"); len(kills) == 0 || kills[0]["session"] != "F-Shop-2" {
		t.Fatalf("kill = %v", kills)
	}
	expect(t, "extra run", h.do("GET", fmt.Sprintf("/api/runs/%d", res.Run.ID), nil, &run), 200)
	if run.Status != "succeeded" {
		t.Fatalf("closed extra = %+v", run)
	}
}
