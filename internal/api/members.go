package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/devai-io/forge/internal/store"
)

// A member is a limited account: it works only in the projects it was
// granted (tasks, agent prompts, runs). Everything not listed here is
// refused for it, so a new route is closed to members until added.
var memberRoutes = map[string]bool{
	"GET /api/auth/me":                      true,
	"POST /api/uploads":                     true,
	"GET /api/uploads/{token}":              true,
	"PATCH /api/auth/me":                    true,
	"POST /api/auth/password":               true,
	"GET /api/auth/sessions":                true,
	"POST /api/auth/sessions/revoke-others": true,
	"DELETE /api/auth/sessions/{id}":        true,
	"POST /api/auth/elevate":                true,
	"POST /api/auth/elevate/passkey/begin":  true,
	"POST /api/auth/elevate/passkey/finish": true,
	"GET /api/auth/passkeys":                true,
	"POST /api/auth/passkeys/begin":         true,
	"POST /api/auth/passkeys/finish":        true,
	"PATCH /api/auth/passkeys/{id}":         true,
	"DELETE /api/auth/passkeys/{id}":        true,
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
	"POST /api/runs/{id}/archive":              true,
	"GET /api/agent":                           true,
	"POST /api/agent":                          true,
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
			// machine: only on a machine open to this member, and only a
			// run it started itself — never a session somebody else
			// opened, which may run with permissions a member cannot ask
			// for.
			if r.Method != http.MethodGet {
				if !s.store.MemberMachineOpen(ctx, u.ID, run.RunnerID) {
					return closedMachine(w)
				}
				if run.UserID == nil || *run.UserID != u.ID {
					writeError(w, http.StatusForbidden, "forbidden", "that run is not yours to steer")
					return false
				}
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
		// The machine the prompt lands on: a resumed run's (its own runs
		// only), the task's current agent's when that is its own (the
		// prompt becomes a message to it), or the one asked for — its
		// agent's machine, when the owner gave it an agent.
		machine := b.RunnerID
		if a, err := s.store.MemberAgent(ctx, u.ID); err == nil {
			machine = a.RunnerID
		}
		if b.ResumeRunID != nil {
			prev, err := s.store.RunByID(ctx, *b.ResumeRunID)
			if err != nil || !s.store.IsProjectMember(ctx, u.ID, prev.ProjectID) || prev.UserID == nil || *prev.UserID != u.ID {
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
				if cur, err := s.store.TaskAgent(ctx, *b.TaskID); err == nil && cur.UserID != nil && *cur.UserID == u.ID {
					machine = cur.RunnerID
				}
			}
		}
		if !s.store.MemberMachineOpen(ctx, u.ID, machine) {
			return closedMachine(w)
		}
	}
	return true
}

func closedMachine(w http.ResponseWriter) bool {
	writeError(w, http.StatusForbidden, "forbidden", "that machine is not open to members")
	return false
}

// memberRunners is a member's view of the machines: only those open to it
// (its agent's machine, else the ones open to members), without hostnames.
func memberRunners(rs []store.Runner, agent *store.MemberAgent) []store.Runner {
	out := []store.Runner{}
	for _, rn := range rs {
		if (agent != nil && rn.ID == agent.RunnerID) || (agent == nil && rn.MembersAllowed) {
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

// ── A member's agent ──────────────────────────────────────────────────────
//
// The owner may give a member one agent (`forge member-agent USER MACHINE
// [MODEL] [EFFORT] [MODE]`): Claude Code on that machine, with that model,
// effort and permission mode, in one of the member's projects. The member
// gets an Agent page with that one agent: it asks, answers the agent's
// questions and permission prompts there, and starts over when it likes —
// it chooses nothing else. The agent runs headless (its prompts come to
// Forge: a member has no terminal), one conversation at a time.

type memberAgentProject struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type memberAgentView struct {
	Agent    *store.MemberAgent   `json:"agent"`
	Projects []memberAgentProject `json:"projects"`
	Run      *store.Run           `json:"run"`
	Recent   []store.Run          `json:"recent"`
}

// memberAgentProjects is the member's projects the agent can work in (those
// with a repo that has a path), with the repo its runs are filed under.
func (s *Server) memberAgentProjects(ctx context.Context, u *store.User) ([]memberAgentProject, map[string]int64, error) {
	keys, err := s.store.MemberProjectKeys(ctx, u.ID)
	if err != nil {
		return nil, nil, err
	}
	projects, err := s.store.ListProjectsOnly(ctx, store.TodayFor("UTC"), false, keys)
	if err != nil {
		return nil, nil, err
	}
	repos, err := s.store.AllRepos(ctx)
	if err != nil {
		return nil, nil, err
	}
	out, repoOf := []memberAgentProject{}, map[string]int64{}
	for _, p := range projects {
		if !store.OneOf(p.Key, keys) {
			continue
		}
		if repo := projectRepo(repos, p.Key, projectDir(repos, p.Key)); repo != nil {
			out = append(out, memberAgentProject{Key: p.Key, Name: p.Name, Color: p.Color})
			repoOf[p.Key] = repo.ID
		}
	}
	return out, repoOf, nil
}

func (s *Server) getMemberAgent(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	v := memberAgentView{Projects: []memberAgentProject{}, Recent: []store.Run{}}
	agent, err := s.store.MemberAgent(ctx, u.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, v) // no agent: the page is not offered
			return
		}
		writeErr(w, r, err)
		return
	}
	v.Agent = agent
	if v.Projects, _, err = s.memberAgentProjects(ctx, u); err != nil {
		writeErr(w, r, err)
		return
	}
	if cur, err := s.store.MemberAgentRun(ctx, u.ID); err == nil {
		pub := cur.Public()
		v.Run = &pub
	}
	runs, err := s.store.MemberRuns(ctx, u.ID, 15)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	for _, run := range runs {
		if v.Run == nil || run.ID != v.Run.ID {
			v.Recent = append(v.Recent, run.Public())
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// askMemberAgent gives the member's agent a request: one more message to
// the conversation it has open, or — with none, or with `fresh` — a new
// conversation (the open one is finished first), on the agent's machine with
// its model, effort and permission mode.
func (s *Server) askMemberAgent(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Prompt     string `json:"prompt"`
		ProjectKey string `json:"project_key"`
		// TaskID: the task this request is about (Run agent on a task, a
		// comment on it): a new conversation is linked to it and runs in
		// its project (and its repo, when it names one).
		TaskID *int64 `json:"task_id"`
		Fresh  bool   `json:"fresh"`
		// ContinueRunID picks one of the member's own earlier conversations
		// back up on its session (one that finished, failed or was cut off).
		ContinueRunID *int64 `json:"continue_run_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	agent, err := s.store.MemberAgent(ctx, u.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusForbidden, "forbidden", "this account has no agent")
			return
		}
		writeErr(w, r, err)
		return
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Prompt == "" {
		writeErr(w, r, &store.ValidationError{Field: "prompt", Message: "write what the agent should do"})
		return
	}
	var task *store.Task
	if in.TaskID != nil {
		if task, err = s.store.TaskByID(ctx, *in.TaskID); err != nil || !s.store.IsProjectMember(ctx, u.ID, task.ProjectID) {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
	}
	cur, err := s.store.MemberAgentRun(ctx, u.ID)
	if err != nil {
		cur = nil
	}
	if in.ContinueRunID != nil {
		prev, err := s.store.RunByID(ctx, *in.ContinueRunID)
		if err != nil || prev.UserID == nil || *prev.UserID != u.ID || prev.Kind != "agent" {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		if prev.RunnerID != agent.RunnerID || prev.SessionID == "" {
			writeErr(w, r, &store.ValidationError{Field: "continue_run_id", Message: "that conversation cannot be picked up again; start a new one"})
			return
		}
		if cur != nil && cur.ID != prev.ID { // one conversation at a time
			if _, err := s.store.FinishRunByUser(ctx, cur.ID); err == nil {
				s.inboxes.notify(cur.ID)
			}
		}
		run, ok, err := s.sendToAgent(ctx, prev, in.Prompt, false)
		if err != nil || !ok {
			if err == nil {
				err = &store.ValidationError{Field: "continue_run_id", Message: "that conversation cannot be picked up again; start a new one"}
			}
			writeErr(w, r, err)
			return
		}
		out := run.Public()
		out.Continued = true
		writeJSON(w, http.StatusOK, out)
		return
	}
	if cur != nil && !in.Fresh {
		run, ok, err := s.sendToAgent(ctx, cur, in.Prompt, false)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		if ok {
			out := run.Public()
			out.Continued = true
			writeJSON(w, http.StatusOK, out)
			return
		}
		// It cannot take another message (an older agent on that machine): start over.
	}
	// One agent, one conversation: whatever this member still has open ends here.
	if mine, err := s.store.MemberRuns(ctx, u.ID, 100); err == nil {
		for _, old := range mine {
			if store.OneOf(old.Status, store.RunOpenStatuses) {
				if _, err := s.store.FinishRunByUser(ctx, old.ID); err == nil {
					s.inboxes.notify(old.ID)
				}
			}
		}
	}
	projects, repoOf, err := s.memberAgentProjects(ctx, u)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	key := strings.ToUpper(strings.TrimSpace(in.ProjectKey))
	if task != nil {
		key = task.ProjectKey
	}
	if key == "" && len(projects) > 0 {
		key = projects[0].Key
	}
	repoID, ok := repoOf[key]
	if !ok {
		writeErr(w, r, &store.ValidationError{Field: "project_key", Message: "pick one of your projects (it needs a repo the agent can work in)"})
		return
	}
	if task != nil && task.RepoID != nil {
		// The task's own repo, when it has somewhere to run.
		if repo, err := s.store.RepoByID(ctx, *task.RepoID); err == nil && repo.Path != "" && repo.ProjectID == task.ProjectID {
			repoID = repo.ID
		}
	}
	rn, err := s.store.RunnerByID(ctx, agent.RunnerID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	run, err := s.store.CreateRun(ctx, store.RunInput{RunnerID: agent.RunnerID, RepoID: repoID, Kind: "agent", Prompt: in.Prompt,
		Engine: "claude", Model: agent.Model, Effort: agent.Effort, PermissionMode: agent.PermissionMode,
		Interactive: rn.LastSeenAt == nil || rn.Capabilities.Interactive, UserID: &u.ID, TaskID: in.TaskID})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.wakeups.notify(run.RunnerID)
	writeJSON(w, http.StatusCreated, run.Public())
}
