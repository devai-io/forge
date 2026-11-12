package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/auth"
	"github.com/devai-io/forge/internal/store"
)

// decodeInto re-reads a patch into a typed struct, for handlers that need to
// look at a field before the store applies the patch.
func decodeInto(p store.Patch, v any) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, u *store.User) {
	d, err := s.store.Dashboard(r.Context(), u.Timezone)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// ── Projects ──────────────────────────────────────────────────────────────

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request, u *store.User) {
	only, err := s.onlyKeys(r, u)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	ps, err := s.store.ListProjectsOnly(r.Context(), store.TodayFor(u.Timezone), queryBool(r, "include_archived"), only)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": ps})
}

func (s *Server) respondProject(w http.ResponseWriter, r *http.Request, u *store.User, key string, status int) {
	p, err := s.store.ProjectByKey(r.Context(), store.TodayFor(u.Timezone), key)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if isMember(u) {
		scrubProject(p)
	}
	writeJSON(w, status, p)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in store.ProjectInput
	if !decode(w, r, &in) {
		return
	}
	key, err := s.store.CreateProject(r.Context(), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.respondProject(w, r, u, key, http.StatusCreated)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.respondProject(w, r, u, r.PathValue("key"), http.StatusOK)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request, u *store.User) {
	p, ok := decodePatch(w, r)
	if !ok {
		return
	}
	if err := s.store.UpdateProject(r.Context(), r.PathValue("key"), p); err != nil {
		writeErr(w, r, err)
		return
	}
	s.respondProject(w, r, u, r.PathValue("key"), http.StatusOK)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request, u *store.User) {
	key := strings.ToUpper(r.PathValue("key"))
	if err := s.store.DeleteProject(r.Context(), key); err != nil {
		writeErr(w, r, err)
		return
	}
	s.retireProjectFiles(key)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) projectActivity(w http.ResponseWriter, r *http.Request, u *store.User) {
	a, err := s.store.ListActivity(r.Context(), strings.ToUpper(r.PathValue("key")), queryInt(r, "limit", 50))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": a})
}

func (s *Server) setProjectServers(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Servers []store.ProjectServerInput `json:"servers"`
	}
	if !decode(w, r, &in) {
		return
	}
	links, err := s.store.SetProjectServers(r.Context(), r.PathValue("key"), in.Servers)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": links})
}

// ── Repos ─────────────────────────────────────────────────────────────────

func (s *Server) createRepo(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in store.RepoInput
	if !decode(w, r, &in) {
		return
	}
	repo, err := s.store.CreateRepo(r.Context(), r.PathValue("key"), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, repo)
}

func (s *Server) updateRepo(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, ok := decodePatch(w, r)
	if !ok {
		return
	}
	repo, err := s.store.UpdateRepo(r.Context(), id, p)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (s *Server) deleteRepo(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteRepo(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Endpoints ─────────────────────────────────────────────────────────────

func (s *Server) listEndpoints(w http.ResponseWriter, r *http.Request, u *store.User) {
	eps, err := s.store.ListEndpoints(r.Context(), 0)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": eps})
}

func (s *Server) createEndpoint(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in store.EndpointInput
	if !decode(w, r, &in) {
		return
	}
	e, err := s.store.CreateEndpoint(r.Context(), r.PathValue("key"), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	// Check straight away so a new endpoint does not sit at "unknown".
	if e.Enabled {
		if err := s.monitor.Check(r.Context(), *e); err == nil {
			if fresh, err := s.store.EndpointByID(r.Context(), e.ID); err == nil {
				e = fresh
			}
		}
	}
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) updateEndpoint(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, ok := decodePatch(w, r)
	if !ok {
		return
	}
	e, err := s.store.UpdateEndpoint(r.Context(), id, p)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) deleteEndpoint(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteEndpoint(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) checkEndpoint(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	e, err := s.store.EndpointByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.monitor.Check(r.Context(), *e); err != nil {
		writeErr(w, r, err)
		return
	}
	if e, err = s.store.EndpointByID(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) endpointChecks(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	hours := queryInt(r, "hours", 24)
	if hours < 1 || hours > 168 {
		hours = 24
	}
	checks, err := s.store.EndpointChecks(r.Context(), id, hours)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checks": checks})
}

// ── Servers ───────────────────────────────────────────────────────────────

func (s *Server) listServers(w http.ResponseWriter, r *http.Request, u *store.User) {
	servers, err := s.store.ListServers(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": servers})
}

func (s *Server) createServer(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in store.ServerInput
	if !decode(w, r, &in) {
		return
	}
	sv, err := s.store.CreateServer(r.Context(), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, sv)
}

func (s *Server) getServer(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sv, err := s.store.ServerByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sv)
}

func (s *Server) updateServer(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, ok := decodePatch(w, r)
	if !ok {
		return
	}
	sv, err := s.store.UpdateServer(r.Context(), id, p)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sv)
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteServer(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Tasks ─────────────────────────────────────────────────────────────────

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request, u *store.User) {
	q := r.URL.Query()
	f := store.TaskFilter{
		ProjectKey: q.Get("project"),
		Focus:      queryBool(r, "focus"),
		Query:      q.Get("q"),
		Priority:   q.Get("priority"),
		Type:       q.Get("type"),
		Label:      q.Get("label"),
		Overdue:    queryBool(r, "overdue"),
		Open:       queryBool(r, "open"),
		Today:      store.TodayFor(u.Timezone),
		Limit:      queryInt(r, "limit", 500),
	}
	if v := q.Get("status"); v != "" {
		for _, st := range strings.Split(v, ",") {
			if st = strings.TrimSpace(st); st != "" {
				f.Statuses = append(f.Statuses, st)
			}
		}
	}
	var err error
	if f.OnlyKeys, err = s.onlyKeys(r, u); err != nil {
		writeErr(w, r, err)
		return
	}
	tasks, err := s.store.ListTasks(r.Context(), f)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in store.TaskInput
	if !decode(w, r, &in) {
		return
	}
	t, err := s.store.CreateTask(r.Context(), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := s.store.TaskDetail(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, ok := decodePatch(w, r)
	if !ok {
		return
	}
	t, err := s.store.UpdateTask(r.Context(), id, p)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteTask(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createComment(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	c, err := s.store.CreateComment(r.Context(), id, in.Body)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) deleteComment(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteComment(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Runners & runs (browser side) ─────────────────────────────────────────

func (s *Server) listRunners(w http.ResponseWriter, r *http.Request, u *store.User) {
	rs, err := s.store.ListRunners(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if isMember(u) {
		agent, _ := s.store.MemberAgent(r.Context(), u.ID)
		rs = memberRunners(rs, agent)
	}
	writeJSON(w, http.StatusOK, map[string]any{"runners": rs})
}

func (s *Server) rotateRunner(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	token, hash := auth.Token("frg_")
	if err := s.store.RotateRunnerToken(r.Context(), id, hash); err != nil {
		writeErr(w, r, err)
		return
	}
	rn, err := s.store.RunnerByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "runner_rotated", rn.Name)
	writeJSON(w, http.StatusOK, map[string]any{"runner": rn, "token": token})
}

func (s *Server) updateRunner(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	rn, err := s.store.SetRunnerRole(r.Context(), id, in.Role)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rn)
}

func (s *Server) deleteRunner(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteRunner(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request, u *store.User) {
	f := store.RunFilter{
		ProjectKey: r.URL.Query().Get("project"),
		TaskID:     int64(queryInt(r, "task", 0)),
		Limit:      queryInt(r, "limit", 50),
	}
	if st := r.URL.Query().Get("status"); st != "" {
		f.Statuses = strings.Split(st, ",")
	}
	// Archived runs are off the lists, except a task's own history.
	switch r.URL.Query().Get("archived") {
	case "1", "only":
		f.Archived = "only"
	case "all":
		f.Archived = "all"
	default:
		if f.TaskID != 0 {
			f.Archived = "all"
		}
	}
	var err error
	if f.OnlyKeys, err = s.onlyKeys(r, u); err != nil {
		writeErr(w, r, err)
		return
	}
	runs, err := s.store.ListRuns(r.Context(), f)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in store.RunInput
	if !decode(w, r, &in) {
		return
	}
	in.UserID = &u.ID
	member := isMember(u)
	if member {
		// A member with an agent of its own runs everything as that agent:
		// its machine, model, effort and permission mode, whatever was asked.
		if a, err := s.store.MemberAgent(r.Context(), u.ID); err == nil && in.Kind == "agent" {
			in.Engine, in.Model, in.Effort, in.PermissionMode, in.Worktree = "claude", a.Model, a.Effort, a.PermissionMode, false
			if in.ResumeRunID == nil {
				in.RunnerID = a.RunnerID
			}
		}
	}
	// One project, one agent: a run for a project whose agent has a
	// conversation open (or for a task that has an agent, or Continue on a
	// run) goes to that agent as a message.
	if cont, ok, err := s.continueAgent(r.Context(), in, u); err != nil {
		writeErr(w, r, err)
		return
	} else if ok {
		out := cont.Public()
		out.Continued = true
		writeJSON(w, http.StatusOK, out)
		return
	}
	s.prepareRun(r.Context(), &in)
	run, err := s.store.CreateRun(r.Context(), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	// A member's run stays headless: what the agent asks (questions,
	// permissions) comes to Forge, where the member answers — it has no
	// terminal to answer in.
	if !member {
		run = s.placeInTmux(r.Context(), run, in.Fresh)
	}
	s.wakeups.notify(run.RunnerID)
	writeJSON(w, http.StatusCreated, run.Public())
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, err := s.store.RunByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run.Public())
}

func (s *Server) runEvents(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// Run first, then events: a run that finishes between the two reads is
	// reported as still running with all its events, never as finished with
	// some missing — so the poller always makes one more pass.
	run, err := s.store.RunByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	events, err := s.store.ListEvents(r.Context(), id, queryInt(r, "after", 0), queryInt(r, "limit", 500))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	prompts := []store.RunPrompt{}
	if run.Interactive {
		if prompts, err = s.store.ListPrompts(r.Context(), id); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "run": run.Public(), "prompts": prompts})
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if cur, err := s.store.RunByID(r.Context(), id); err == nil && cur.TmuxSession != "" && cur.Status == "running" {
		// A session in tmux is interrupted (Escape), not killed: it stays open
		// for what comes next, like pressing Esc in the terminal.
		cc := s.terminals.control(cur.RunnerID)
		if cc == nil {
			writeError(w, http.StatusConflict, "runner_offline", cur.RunnerName+" is not connected")
			return
		}
		if _, err := cc.call(r.Context(), map[string]any{"type": "keys", "session": cur.TmuxSession, "text": "\x1b"}, 15*time.Second); err != nil {
			writeError(w, http.StatusBadGateway, "runner_error", err.Error())
			return
		}
		s.appendSystem(r.Context(), cur, "interrupted from Forge (Esc)")
		_ = s.store.SetTmuxAwaiting(r.Context(), id, "reply")
		s.writeRun(w, r, id)
		return
	}
	run, err := s.store.CancelRun(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run.Public())
}
