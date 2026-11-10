package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/devai-io/forge/internal/auth"
	"github.com/devai-io/forge/internal/store"
)

func TestMemberSeesOnlyGrantedProjects(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	hash, _ := auth.HashPassword(testPassword)
	bob, err := h.store.CreateUser(ctx, "bob", "bob@example.com", "Bob", hash, "UTC", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetUserRole(ctx, bob.ID, store.RoleMember); err != nil {
		t.Fatal(err)
	}
	if err := h.store.GrantProject(ctx, bob.ID, "shop"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.GrantProject(ctx, bob.ID, "NOPE"); err == nil {
		t.Fatal("granting a missing project should fail")
	}

	// The owner makes data in both projects first.
	h.login()
	var own, other store.Task
	expect(t, "owner task shop", h.do("POST", "/api/tasks", map[string]any{"project_key": "SHOP", "title": "Mine"}, &own), 201)
	expect(t, "owner task game", h.do("POST", "/api/tasks", map[string]any{"project_key": "GAME", "title": "Secret"}, &other), 201)
	var shop, game store.ProjectDetail
	expect(t, "shop", h.do("GET", "/api/projects/SHOP", nil, &shop), 200)
	expect(t, "game", h.do("GET", "/api/projects/GAME", nil, &game), 200)
	if len(shop.Repos) == 0 || len(game.Repos) == 0 {
		t.Fatalf("seed has no repos: %d %d", len(shop.Repos), len(game.Repos))
	}

	jar, _ := cookiejar.New(nil)
	h.client.Jar = jar
	expect(t, "member login", h.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": testPassword}, nil), 200)

	var me struct{ User store.User }
	expect(t, "me", h.do("GET", "/api/auth/me", nil, &me), 200)
	if me.User.Role != "member" {
		t.Fatalf("role = %q", me.User.Role)
	}

	var ps struct{ Projects []store.Project }
	expect(t, "projects", h.do("GET", "/api/projects", nil, &ps), 200)
	if len(ps.Projects) != 1 || ps.Projects[0].Key != "SHOP" {
		t.Fatalf("projects = %+v", ps.Projects)
	}
	var got store.ProjectDetail
	expect(t, "own project", h.do("GET", "/api/projects/SHOP", nil, &got), 200)
	if len(got.Servers) != 0 || len(got.Endpoints) != 0 || got.InfraNotes != "" {
		t.Fatalf("infra leaked: %+v", got)
	}
	expect(t, "other project", h.do("GET", "/api/projects/GAME", nil, nil), 404)

	var ts struct{ Tasks []store.Task }
	expect(t, "tasks", h.do("GET", "/api/tasks", nil, &ts), 200)
	for _, tk := range ts.Tasks {
		if tk.ProjectKey != "SHOP" {
			t.Fatalf("saw %s", tk.Ref)
		}
	}
	expect(t, "tasks of other project", h.do("GET", "/api/tasks?project=GAME", nil, nil), 404)
	expect(t, "own task", h.do("GET", fmt.Sprintf("/api/tasks/%d", own.ID), nil, nil), 200)
	expect(t, "other task", h.do("GET", fmt.Sprintf("/api/tasks/%d", other.ID), nil, nil), 404)
	expect(t, "edit other task", h.do("PATCH", fmt.Sprintf("/api/tasks/%d", other.ID), map[string]string{"status": "done"}, nil), 404)
	expect(t, "comment other task", h.do("POST", fmt.Sprintf("/api/tasks/%d/comments", other.ID), map[string]string{"body": "hi"}, nil), 404)
	expect(t, "create in other project", h.do("POST", "/api/tasks", map[string]any{"project_key": "GAME", "title": "x"}, nil), 404)
	expect(t, "create in own project", h.do("POST", "/api/tasks", map[string]any{"project_key": "shop", "title": "From Bob"}, nil), 201)
	expect(t, "move task", h.do("PATCH", fmt.Sprintf("/api/tasks/%d", own.ID), map[string]any{"project_key": "GAME"}, nil), 403)

	// Runs: agents only, in granted projects only.
	expect(t, "shell run", h.do("POST", "/api/runs", map[string]any{"kind": "command", "command": "id", "repo_id": shop.Repos[0].ID}, nil), 403)
	expect(t, "agent run in other project", h.do("POST", "/api/runs", map[string]any{"kind": "agent", "prompt": "hi", "repo_id": game.Repos[0].ID, "runner_id": 1}, nil), 404)
	expect(t, "runs list", h.do("GET", "/api/runs", nil, nil), 200)
	expect(t, "runs of other project", h.do("GET", "/api/runs?project=GAME", nil, nil), 404)

	// Everything else is closed.
	for _, p := range []string{"/api/dashboard", "/api/vault", "/api/servers", "/api/endpoints", "/api/memories", "/api/chats", "/api/checkups", "/api/monitoring", "/api/provider", "/api/terminal/hosts"} {
		if code := h.do(http.MethodGet, p, nil, nil); code != 403 {
			t.Errorf("GET %s = %d, want 403", p, code)
		}
	}
	expect(t, "create project", h.do("POST", "/api/projects", map[string]any{"key": "ZZZ", "name": "z"}, nil), 403)
	expect(t, "delete task", h.do("DELETE", fmt.Sprintf("/api/tasks/%d", own.ID), nil, nil), 403)
}

func TestMemberRunsOnlyOnOpenMachines(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	h.login()
	var created struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "create runner", h.newRunner("desktop", &created.Runner, &created.Token), 201)
	var shop store.ProjectDetail
	expect(t, "shop", h.do("GET", "/api/projects/SHOP", nil, &shop), 200)
	var task store.Task
	expect(t, "task", h.do("POST", "/api/tasks", map[string]any{"project_key": "SHOP", "title": "Owner's agent"}, &task), 201)
	// The owner's agent on the task, on the closed machine.
	var ownerRun store.Run
	expect(t, "owner run", h.do("POST", "/api/runs", map[string]any{"runner_id": created.Runner.ID,
		"repo_id": shop.Repos[0].ID, "kind": "agent", "prompt": "hi", "task_id": task.ID}, &ownerRun), 201)

	hash, _ := auth.HashPassword(testPassword)
	bob, err := h.store.CreateUser(ctx, "bob", "bob@example.com", "Bob", hash, "UTC", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = h.store.SetUserRole(ctx, bob.ID, store.RoleMember)
	_ = h.store.GrantProject(ctx, bob.ID, "SHOP")
	jar, _ := cookiejar.New(nil)
	h.client.Jar = jar
	expect(t, "member login", h.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": testPassword}, nil), 200)

	run := map[string]any{"runner_id": created.Runner.ID, "repo_id": shop.Repos[0].ID, "kind": "agent", "prompt": "hi"}
	var rs struct{ Runners []store.Runner }
	expect(t, "runners", h.do("GET", "/api/runners", nil, &rs), 200)
	if len(rs.Runners) != 0 {
		t.Fatalf("closed machine listed: %+v", rs.Runners)
	}
	expect(t, "closed machine", h.do("POST", "/api/runs", run, nil), 403)
	expect(t, "message to agent on closed machine", h.do("POST", fmt.Sprintf("/api/runs/%d/messages", ownerRun.ID), map[string]string{"body": "x"}, nil), 403)
	expect(t, "see owner's run", h.do("GET", fmt.Sprintf("/api/runs/%d", ownerRun.ID), nil, nil), 200)

	if err := h.store.SetRunnerMembersAllowed(ctx, "desktop", true); err != nil {
		t.Fatal(err)
	}
	expect(t, "runners open", h.do("GET", "/api/runners", nil, &rs), 200)
	if len(rs.Runners) != 1 || rs.Runners[0].Hostname != "" {
		t.Fatalf("runners = %+v", rs.Runners)
	}
	expect(t, "skip permissions", h.do("POST", "/api/runs", map[string]any{"runner_id": created.Runner.ID, "repo_id": shop.Repos[0].ID,
		"kind": "agent", "prompt": "hi", "permission_mode": "bypassPermissions"}, nil), 403)
	expect(t, "open machine", h.do("POST", "/api/runs", run, nil), 201)
	_ = h.store.SetRunnerMembersAllowed(ctx, "desktop", false)
	// A task's agent stays on its machine: prompting that task again is a
	// message to it, so it is refused once the machine is closed.
	expect(t, "task agent on closed machine", h.do("POST", "/api/runs", map[string]any{"repo_id": shop.Repos[0].ID,
		"kind": "agent", "prompt": "more", "task_id": task.ID}, nil), 403)
}

// Project agents are the owner's: a member can neither see nor open them, and
// a member's request never lands in the project agent's conversation.
func TestMemberStaysOutOfProjectAgents(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	hash, _ := auth.HashPassword(testPassword)
	bob, err := h.store.CreateUser(ctx, "bob", "bob@example.com", "Bob", hash, "UTC", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetUserRole(ctx, bob.ID, store.RoleMember); err != nil {
		t.Fatal(err)
	}
	if err := h.store.GrantProject(ctx, bob.ID, "shop"); err != nil {
		t.Fatal(err)
	}
	h.login()
	m := newFakeMachine(t, h)
	if err := h.store.SetRunnerMembersAllowed(ctx, m.rn.Name, true); err != nil {
		t.Fatal(err)
	}
	// The owner's agent has a conversation open in the project's session.
	var own store.Run
	expect(t, "owner run", h.do("POST", "/api/runs", map[string]any{"runner_id": m.rn.ID, "repo_id": m.repoID, "kind": "agent",
		"prompt": "Fix the cart", "permission_mode": "plan"}, &own), 201)
	m.wait("create", 1)

	jar, _ := cookiejar.New(nil)
	h.client.Jar = jar
	expect(t, "member login", h.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": testPassword}, nil), 200)
	expect(t, "sessions", h.do("GET", "/api/project-sessions", nil, nil), 403)
	expect(t, "open", h.do("POST", "/api/project-sessions/SHOP", map[string]any{"prompt": "hello"}, nil), 403)
	expect(t, "close", h.do("DELETE", "/api/project-sessions/SHOP/F-Shop", nil, nil), 403)
	var mine store.Run
	expect(t, "member run", h.do("POST", "/api/runs", map[string]any{"runner_id": m.rn.ID, "repo_id": m.repoID, "kind": "agent",
		"prompt": "Look at the checkout", "permission_mode": "plan"}, &mine), 201)
	if mine.ID == own.ID || mine.Continued || mine.TmuxSession != "F-Shop-2" {
		t.Fatalf("a member's request went into the owner's session: %+v", mine)
	}
}
