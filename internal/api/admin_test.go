package api

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

type adminList struct {
	Users    []store.AdminUser
	Machines []adminMachine
	Projects []adminProject
}

func (l adminList) user(name string) *store.AdminUser {
	for i := range l.Users {
		if l.Users[i].Username == name {
			return &l.Users[i]
		}
	}
	return nil
}

// TestAdminAccounts: an owner creates a member, sets what it may do, resets
// its password and deletes it — each change behind a password confirmation.
func TestAdminAccounts(t *testing.T) {
	h := setup(t)
	h.login()
	m := newFakeMachine(t, h)
	var list adminList
	expect(t, "list", h.do("GET", "/api/admin/users", nil, &list), 200)
	if len(list.Users) != 1 || list.Users[0].Role != "owner" || len(list.Machines) != 1 || len(list.Projects) == 0 {
		t.Fatalf("list = %+v", list)
	}
	me := list.Users[0]
	body := map[string]any{"username": "gus", "email": "gus@example.com", "display_name": "Gus F", "projects": []string{"SHOP"}}
	expect(t, "needs a password confirmation", h.do("POST", "/api/admin/users", body, nil), 403)
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)
	var created struct {
		User     store.AdminUser
		Password string
	}
	expect(t, "bad username", h.do("POST", "/api/admin/users", map[string]any{"username": "G us", "email": "x@example.com"}, nil), 422)
	expect(t, "unknown project", h.do("POST", "/api/admin/users", map[string]any{"username": "zed", "email": "zed@example.com", "projects": []string{"NOPE"}}, nil), 422)
	expect(t, "create", h.do("POST", "/api/admin/users", body, &created), 201)
	if u := created.User; u.Username != "gus" || u.Role != "member" || u.DisplayName != "Gus F" || len(u.Projects) != 1 || u.Projects[0] != "SHOP" ||
		!u.PasswordGenerated || len(created.Password) < 12 {
		t.Fatalf("created = %+v", created)
	}
	expect(t, "taken", h.do("POST", "/api/admin/users", body, nil), 422)
	gus := created.User.ID
	path := fmt.Sprintf("/api/admin/users/%d", gus)

	// The new account signs in with the password shown once.
	member := &harness{t: t, srv: h.srv, api: h.api, store: h.store, client: &http.Client{}}
	jar, _ := cookiejar.New(nil)
	member.client.Jar = jar
	expect(t, "member login", member.signIn("gus", created.Password), 200)
	expect(t, "admin is the owner's", member.do("GET", "/api/admin/users", nil, nil), 403)
	expect(t, "so is acting as someone", member.do("POST", "/api/admin/impersonate", map[string]any{"user_id": me.ID}, nil), 403)

	// What it may do: projects, a name, an agent of its own.
	var u store.AdminUser
	expect(t, "projects and name", h.do("PATCH", path, map[string]any{"display_name": "Gustavo", "projects": []string{"SHOP", "GAME"}}, &u), 200)
	if u.DisplayName != "Gustavo" || len(u.Projects) != 2 {
		t.Fatalf("updated = %+v", u)
	}
	expect(t, "an agent", h.do("PATCH", path, map[string]any{"agent": map[string]any{"machine": m.rn.Name, "model": "opus", "effort": "high"}}, &u), 200)
	if u.Agent == nil || u.Agent.RunnerName != m.rn.Name || u.Agent.Model != "opus" || u.Agent.Effort != "high" || u.Agent.PermissionMode != "acceptEdits" || len(u.Projects) != 2 {
		t.Fatalf("agent = %+v (%+v)", u.Agent, u)
	}
	expect(t, "never skipping permissions", h.do("PATCH", path, map[string]any{"agent": map[string]any{"machine": m.rn.Name, "permission_mode": "bypassPermissions"}}, nil), 422)
	expect(t, "no agent", h.do("PATCH", path, map[string]any{"agent": nil}, &u), 200)
	if u.Agent != nil {
		t.Fatalf("agent still there: %+v", u.Agent)
	}
	// Machines open to every member.
	expect(t, "open the machine", h.do("PATCH", fmt.Sprintf("/api/admin/machines/%d", m.rn.ID), map[string]any{"members_allowed": true}, &list), 200)
	if !list.Machines[0].MembersAllowed {
		t.Fatalf("machines = %+v", list.Machines)
	}

	// A new password: shown once, and it ends that account's sessions.
	var reset struct{ Password string }
	expect(t, "reset", h.do("POST", path+"/password", nil, &reset), 200)
	if reset.Password == created.Password || len(reset.Password) < 12 {
		t.Fatalf("reset = %+v", reset)
	}
	expect(t, "signed out", member.do("GET", "/api/auth/me", nil, nil), 401)
	expect(t, "old password", member.do("POST", "/api/auth/login", map[string]string{"username": "gus", "password": created.Password}, nil), 401)
	expect(t, "new password", member.signIn("gus", reset.Password), 200)

	// The owner's own account: not from here.
	own := fmt.Sprintf("/api/admin/users/%d", me.ID)
	expect(t, "own role", h.do("PATCH", own, map[string]any{"role": "member"}, nil), 422)
	expect(t, "own password", h.do("POST", own+"/password", nil, nil), 422)
	expect(t, "own account", h.do("DELETE", own, nil, nil), 422)
	// A second owner may be made — and demoted again.
	expect(t, "promote", h.do("PATCH", path, map[string]any{"role": "owner"}, &u), 200)
	if u.Role != "owner" {
		t.Fatalf("promoted = %+v", u)
	}
	expect(t, "an owner sees admin", member.do("GET", "/api/admin/users", nil, nil), 200)
	expect(t, "demote", h.do("PATCH", path, map[string]any{"role": "member"}, &u), 200)

	expect(t, "delete", h.do("DELETE", path, nil, nil), 204)
	expect(t, "gone", member.do("GET", "/api/auth/me", nil, nil), 401)
	expect(t, "list", h.do("GET", "/api/admin/users", nil, &list), 200)
	if list.user("gus") != nil {
		t.Fatalf("still listed: %+v", list.Users)
	}
}

// TestImpersonation: an owner acts as a member — sees what it sees, is held
// to its limits — and steps back out.
func TestImpersonation(t *testing.T) {
	h := setup(t)
	h.login()
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)
	var created struct {
		User     store.AdminUser
		Password string
	}
	expect(t, "create", h.do("POST", "/api/admin/users", map[string]any{"username": "gus", "email": "gus@example.com", "projects": []string{"SHOP"}}, &created), 201)
	var me struct {
		User         store.User
		Impersonator *struct {
			ID       int64
			Username string
		}
	}
	expect(t, "me", h.do("GET", "/api/auth/me", nil, &me), 200)
	owner := me.User
	expect(t, "not yourself", h.do("POST", "/api/admin/impersonate", map[string]any{"user_id": owner.ID}, nil), 422)
	expect(t, "nobody", h.do("POST", "/api/admin/impersonate", map[string]any{"user_id": 9999}, nil), 404)
	expect(t, "act as gus", h.do("POST", "/api/admin/impersonate", map[string]any{"user_id": created.User.ID}, nil), 200)

	me.Impersonator = nil
	expect(t, "me", h.do("GET", "/api/auth/me", nil, &me), 200)
	if me.User.Username != "gus" || me.User.Role != "member" || me.Impersonator == nil || me.Impersonator.ID != owner.ID {
		t.Fatalf("acting as = %+v (impersonator %+v)", me.User, me.Impersonator)
	}
	// Held to the member's limits.
	var ps struct{ Projects []store.Project }
	expect(t, "projects", h.do("GET", "/api/projects", nil, &ps), 200)
	if len(ps.Projects) != 1 || ps.Projects[0].Key != "SHOP" {
		t.Fatalf("projects as gus = %+v", ps.Projects)
	}
	expect(t, "no admin", h.do("GET", "/api/admin/users", nil, nil), 403)
	expect(t, "no terminals", h.do("GET", "/api/terminal/hosts", nil, nil), 403)
	expect(t, "not twice", h.do("POST", "/api/admin/impersonate", map[string]any{"user_id": owner.ID}, nil), 403)
	// What it does is its own: a task it makes is in its project.
	expect(t, "a task as gus", h.do("POST", "/api/tasks", map[string]any{"project_key": "SHOP", "title": "From gus"}, nil), 201)
	expect(t, "not in a project it lacks", h.do("POST", "/api/tasks", map[string]any{"project_key": "GAME", "title": "No"}, nil), 404)
	// Its sign-in settings stay its own.
	expect(t, "password", h.do("POST", "/api/auth/password", map[string]string{"current": "x", "new": "y"}, nil), 403)
	expect(t, "two-factor", h.do("POST", "/api/auth/totp/setup", nil, nil), 403)
	// The owner confirms with the owner's own password.
	expect(t, "elevate as the owner", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)

	expect(t, "stop", h.do("DELETE", "/api/admin/impersonate", nil, nil), 204)
	me.Impersonator = nil
	expect(t, "me", h.do("GET", "/api/auth/me", nil, &me), 200)
	if me.User.ID != owner.ID || me.Impersonator != nil {
		t.Fatalf("back = %+v (impersonator %+v)", me.User, me.Impersonator)
	}
	expect(t, "admin again", h.do("GET", "/api/admin/users", nil, nil), 200)
	expect(t, "stopping twice is fine", h.do("DELETE", "/api/admin/impersonate", nil, nil), 204)

	// The account goes away mid-impersonation: the session is the owner's again.
	expect(t, "act as gus", h.do("POST", "/api/admin/impersonate", map[string]any{"user_id": created.User.ID}, nil), 200)
	if err := h.store.DeleteAccount(t.Context(), created.User.ID); err != nil {
		t.Fatal(err)
	}
	expect(t, "me", h.do("GET", "/api/auth/me", nil, &me), 200)
	if me.User.ID != owner.ID {
		t.Fatalf("after the account was deleted = %+v", me.User)
	}
}
