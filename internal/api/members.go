package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/devai-io/forge/internal/store"
)

// A member is a limited account: it works only in the projects it was
// granted (tasks, agent prompts, runs). Everything not listed here is
// refused for it, so a new route is closed to members until added.
var memberRoutes = map[string]bool{
	"GET /api/auth/me":                      true,
	"PATCH /api/auth/me":                    true,
	"POST /api/auth/password":               true,
	"GET /api/auth/sessions":                true,
	"POST /api/auth/sessions/revoke-others": true,
	"DELETE /api/auth/sessions/{id}":        true,
	"POST /api/auth/elevate":                true,
	"POST /api/auth/totp/setup":             true,
	"POST /api/auth/totp/enable":            true,
	"POST /api/auth/totp/disable":           true,

	"GET /api/projects":                        true,
	"GET /api/projects/{key}":                  true,
	"GET /api/projects/{key}/activity":         true,
	"GET /api/tasks":                           true,
	"POST /api/tasks":                          true,
	"GET /api/tasks/{id}":                      true,
	"PATCH /api/tasks/{id}":                    true,
	"POST /api/tasks/{id}/comments":            true,
	"GET /api/runners":                         true,
	"GET /api/runs":                            true,
	"POST /api/runs":                           true,
	"GET /api/runs/{id}":                       true,
	"GET /api/runs/{id}/events":                true,
	"POST /api/runs/{id}/cancel":               true,
	"POST /api/runs/{id}/prompts/{pid}/answer": true,
	"POST /api/runs/{id}/messages":             true,
	"POST /api/runs/{id}/end":                  true,
	"POST /api/runs/{id}/finish":               true,
}

func isMember(u *store.User) bool { return u.Role == store.RoleMember }

// onlyKeys is the project filter for list queries: nil (everything) for the
// owner, the granted keys for a member.
func (s *Server) onlyKeys(r *http.Request, u *store.User) ([]string, error) {
	if !isMember(u) {
		return nil, nil
	}
	return s.store.MemberProjectKeys(r.Context(), u.ID)
}

// memberGuard runs after authentication for a member: closed routes get 403,
// and routes that name a task, a run, a project, a repo or a resumed run are
// checked against the member's projects. Anything that does not resolve is a
// 404, so a member cannot tell a missing record from someone else's.
func (s *Server) memberGuard(w http.ResponseWriter, r *http.Request, u *store.User) bool {
	if !memberRoutes[r.Pattern] {
		writeError(w, http.StatusForbidden, "forbidden", "this account is limited to its own projects")
		return false
	}
	ctx := r.Context()
	deny := func() bool {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return false
	}
	switch r.Pattern {
	case "GET /api/projects/{key}", "GET /api/projects/{key}/activity":
		if !s.store.IsProjectKeyMember(ctx, u.ID, r.PathValue("key")) {
			return deny()
		}
	case "GET /api/tasks", "GET /api/runs":
		if k := r.URL.Query().Get("project"); k != "" && !s.store.IsProjectKeyMember(ctx, u.ID, k) {
			return deny()
		}
	}
	if id, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err == nil {
		switch {
		case len(r.Pattern) > 0 && contains(r.Pattern, "/api/tasks/"):
			t, err := s.store.TaskByID(ctx, id)
			if err != nil || !s.store.IsProjectMember(ctx, u.ID, t.ProjectID) {
				return deny()
			}
		case contains(r.Pattern, "/api/runs/"):
			run, err := s.store.RunByID(ctx, id)
			if err != nil || !s.store.IsProjectMember(ctx, u.ID, run.ProjectID) {
				return deny()
			}
			// Steering a run (messages, answers, end) types into its
			// machine: only on machines opened to members.
			if r.Method != http.MethodGet && !s.store.RunnerMembersAllowed(ctx, run.RunnerID) {
				return closedMachine(w)
			}
		}
	}
	switch r.Pattern {
	case "POST /api/tasks", "PATCH /api/tasks/{id}":
		var b struct {
			ProjectKey string `json:"project_key"`
			RepoID     *int64 `json:"repo_id"`
		}
		raw, ok := peekBody(w, r)
		if !ok {
			return false
		}
		_ = json.Unmarshal(raw, &b)
		if r.Method == http.MethodPost && !s.store.IsProjectKeyMember(ctx, u.ID, b.ProjectKey) {
			return deny()
		}
		if r.Method == http.MethodPatch && b.ProjectKey != "" {
			writeError(w, http.StatusForbidden, "forbidden", "a task cannot change project")
			return false
		}
	case "POST /api/runs":
		var b struct {
			RunnerID       int64  `json:"runner_id"`
			RepoID         int64  `json:"repo_id"`
			Kind           string `json:"kind"`
			TaskID         *int64 `json:"task_id"`
			ResumeRunID    *int64 `json:"resume_run_id"`
			Fresh          bool   `json:"fresh"`
			PermissionMode string `json:"permission_mode"`
		}
		raw, ok := peekBody(w, r)
		if !ok {
			return false
		}
		_ = json.Unmarshal(raw, &b)
		// No shell commands: a member prompts agents.
		if b.Kind != "agent" {
			writeError(w, http.StatusForbidden, "forbidden", "members can only start agent runs")
			return false
		}
		// Claude asks before acting; a member never turns that off.
		if b.PermissionMode == "bypassPermissions" {
			writeError(w, http.StatusForbidden, "forbidden", "members cannot skip permission prompts")
			return false
		}
		// The machine the prompt lands on: a resumed run's, the task's
		// current agent's (the prompt becomes a message to it), or the
		// one asked for.
		machine := b.RunnerID
		if b.ResumeRunID != nil {
			prev, err := s.store.RunByID(ctx, *b.ResumeRunID)
			if err != nil || !s.store.IsProjectMember(ctx, u.ID, prev.ProjectID) {
				return deny()
			}
			machine = prev.RunnerID
		} else {
			repo, err := s.store.RepoByID(ctx, b.RepoID)
			if err != nil || !s.store.IsProjectMember(ctx, u.ID, repo.ProjectID) {
				return deny()
			}
		}
		if b.TaskID != nil {
			t, err := s.store.TaskByID(ctx, *b.TaskID)
			if err != nil || !s.store.IsProjectMember(ctx, u.ID, t.ProjectID) {
				return deny()
			}
			if b.ResumeRunID == nil && !b.Fresh {
				if cur, err := s.store.TaskAgent(ctx, *b.TaskID); err == nil {
					machine = cur.RunnerID
				}
			}
		}
		if !s.store.RunnerMembersAllowed(ctx, machine) {
			return closedMachine(w)
		}
	}
	return true
}

func closedMachine(w http.ResponseWriter) bool {
	writeError(w, http.StatusForbidden, "forbidden", "that machine is not open to members")
	return false
}

// memberRunners is a member's view of the machines: only those open to
// members, without hostnames.
func memberRunners(rs []store.Runner) []store.Runner {
	out := []store.Runner{}
	for _, rn := range rs {
		if rn.MembersAllowed {
			rn.Hostname = ""
			rn.Capabilities.CommandDetails = []store.CommandDetail{}
			rn.Capabilities.Commands = []string{}
			out = append(out, rn)
		}
	}
	return out
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }

// peekBody reads the request body for inspection and puts it back.
func peekBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "request too large")
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, true
}

// scrubProject hides infrastructure from a member's view of a project.
func scrubProject(p *store.ProjectDetail) {
	p.InfraNotes = ""
	p.Servers = []store.ProjectServer{}
	p.Endpoints = []store.Endpoint{}
	for i := range p.Repos {
		p.Repos[i].Path = ""
		p.Repos[i].Deploy = ""
		p.Repos[i].Notes = ""
	}
}
