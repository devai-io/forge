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

// TestProjectSessions plays the master machine: a pass starts the missing
// sessions with Remote Control, restarts one whose Claude exited, and reads
// the link of a running one.
func TestProjectSessions(t *testing.T) {
	h := setup(t)
	h.login()
	var rn store.Runner
	var token string
	expect(t, "runner", h.newRunner("desk", &rn, &token), 201)
	expect(t, "make master", h.do("PATCH", fmt.Sprintf("/api/runners/%d", rn.ID), map[string]string{"role": "master"}, nil), 200)
	bearer := []string{"Authorization", "Bearer " + token}
	machine := &harness{t: t, srv: h.srv, client: &http.Client{}}
	expect(t, "heartbeat", machine.do("POST", "/api/runner/heartbeat", map[string]any{
		"capabilities": map[string]any{"terminal": true, "permission_modes": []string{"bypassPermissions"}},
		"tmux": []map[string]any{
			{"name": "F-Shop", "windows": 1, "created": "2026-10-29T08:00:00Z", "command": "claude", "claude": true,
				"window_list": []map[string]any{{"index": 0, "command": "claude", "claude": true, "active": true}}},
			{"name": "F-Game", "windows": 1, "created": "2026-10-29T08:00:00Z", "command": "zsh",
				"window_list": []map[string]any{{"index": 0, "command": "zsh", "active": true}}},
		},
	}, nil, bearer...), 200)

	var view struct {
		Settings store.ProjectSessionsSettings
		Host     *projectSessionHost
		Sessions []ProjectSession
	}
	expect(t, "view", h.do("GET", "/api/project-sessions", nil, &view), 200)
	if view.Settings.Enabled || view.Host == nil || view.Host.Online || len(view.Sessions) != 3 || view.Sessions[0].State != "offline" {
		t.Fatalf("before the control socket: %+v", view)
	}

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
			got = append(got, m)
			mu.Unlock()
			res := map[string]any{"type": "result", "id": m["id"], "ok": true}
			if m["type"] == "rc_url" {
				res["text"] = "https://claude.ai/code/session_abc123"
			}
			raw, _ := json.Marshal(res)
			_ = control.Write(ctx, websocket.MessageText, raw)
		}
	}()
	time.Sleep(200 * time.Millisecond)

	// Off: a pass does nothing.
	h.api.projectSessionsPass(ctx)
	mu.Lock()
	if len(got) != 0 {
		t.Fatalf("off, yet sent %v", got)
	}
	mu.Unlock()

	expect(t, "turning on needs elevation", h.do("PATCH", "/api/project-sessions", map[string]any{"enabled": true}, nil), 403)
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)
	h.api.projectSessions.passMu.Lock() // keep the background pass out; this test runs its own
	expect(t, "on", h.do("PATCH", "/api/project-sessions", map[string]any{"enabled": true, "skip_permissions": true}, &view), 200)
	h.api.projectSessions.passMu.Unlock()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	got = nil
	mu.Unlock()
	h.api.projectSessionsPass(ctx)

	mu.Lock()
	defer mu.Unlock()
	byType := map[string][]map[string]any{}
	for _, m := range got {
		byType[m["type"].(string)] = append(byType[m["type"].(string)], m)
	}
	if len(byType["kill"]) != 1 || byType["kill"][0]["session"] != "F-Game" {
		t.Errorf("kill = %v", byType["kill"])
	}
	creates := map[string]map[string]any{}
	for _, m := range byType["create"] {
		creates[m["session"].(string)] = m
	}
	if g, b := creates["F-Game"], creates["F-Blog"]; len(creates) != 2 || g == nil || b == nil ||
		g["cwd"] != "/home/demo/dev/game" || b["cwd"] != "/home/demo/dev/blog" || b["remote_control"] != "F-Blog" ||
		b["start"] != "claude" || b["skip_permissions"] != true {
		t.Errorf("creates = %v", creates)
	}
	if len(byType["rc_url"]) != 1 || byType["rc_url"][0]["session"] != "F-Shop" {
		t.Errorf("rc_url = %v", byType["rc_url"])
	}

	expect(t, "view", h.do("GET", "/api/project-sessions", nil, &view), 200)
	states := map[string]ProjectSession{}
	for _, e := range view.Sessions {
		states[e.Name] = e
	}
	if s := states["F-Shop"]; s.State != "running" || s.URL == nil || *s.URL != "https://claude.ai/code/session_abc123" {
		t.Errorf("F-Shop = %+v", s)
	}
	if states["F-Game"].State != "starting" || states["F-Blog"].State != "starting" {
		t.Errorf("states = %+v", states)
	}
}
