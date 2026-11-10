package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/store"
)

// Project agents: each project has one agent — a Claude Code session on the
// master machine with Remote Control on, in a tmux session named F-<Project>
// (the same name Remote Control shows), in the project's folder: its one
// repo, or the folder all its repos share. It is started when the user asks
// for it (POST /api/project-sessions/{key}, with a model, an effort and
// maybe a first request), never by itself; an extra agent for the same
// project is F-<Project>-2, -3, … A request for a project whose agent is
// already open is typed into it.
//
// RunProjectSessions only reads, once a minute, the Remote Control link of
// each open session off its scrollback (once per session).

const (
	projectSessionPrefix = "F-"
	projectSessionTick   = time.Minute
	// projectSessionGrace is how long a just-started session may take to
	// show Claude before it counts as stopped (the tmux snapshot comes with
	// the machine's heartbeat, Claude takes a few seconds to start).
	projectSessionGrace = 3 * time.Minute
	// projectSessionSettle is how long after a start nothing is typed into
	// a session: Claude Code is still drawing its first screen.
	projectSessionSettle = 15 * time.Second
)

type projectSessionState struct {
	mu      sync.Mutex
	started map[string]time.Time // session name → when Forge last started it
	urls    map[string]string    // session name + created → its Remote Control link
	passMu  sync.Mutex
}

func (ps *projectSessionState) markStarted(name string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.started != nil {
		ps.started[name] = time.Now()
	}
}

func (ps *projectSessionState) startedAgo(name string) time.Duration {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	t, ok := ps.started[name]
	if !ok {
		return 24 * time.Hour
	}
	return time.Since(t)
}

// ProjectSession is one agent session of a project; State is running,
// starting, stopped (no Claude in it), missing or offline (the master is not
// connected).
type ProjectSession struct {
	ProjectKey    string  `json:"project_key"`
	ProjectName   string  `json:"project_name"`
	ProjectColor  string  `json:"project_color"`
	ProjectStatus string  `json:"project_status"`
	Name          string  `json:"name"`
	Dir           string  `json:"dir"`
	State         string  `json:"state"`
	URL           *string `json:"url"`
	Attached      int     `json:"attached"`
	Activity      *string `json:"activity"`
	// Extra is 0 for the project's agent and 2, 3, … for an extra one.
	Extra int `json:"extra"`
	// The conversation open in the session, if any: its run, that run's
	// status (queued, running, waiting), what it waits for and its request.
	RunID     *int64 `json:"run_id"`
	RunStatus string `json:"run_status"`
	Awaiting  string `json:"awaiting"`
	RunPrompt string `json:"run_prompt"`
}

type projectSessionHost struct {
	RunnerID        int64  `json:"runner_id"`
	RunnerName      string `json:"runner_name"`
	Online          bool   `json:"online"`
	SkipPermissions bool   `json:"skip_permissions"`
	// TmuxAgents: the machine's agent can take a first request (v0.1.44+).
	TmuxAgents bool `json:"tmux_agents"`
}

type projectSessionsView struct {
	Settings store.ProjectSessionsSettings `json:"settings"`
	Host     *projectSessionHost           `json:"host"`
	Sessions []ProjectSession              `json:"sessions"`

	cc   *controlConn
	tmux map[string]store.TmuxSession
}

// projectSessionName is F-<project name>, cut down to what tmux allows.
func projectSessionName(project string) string {
	name := projectSessionPrefix + strings.Trim(sessionUnsafe.ReplaceAllString(project, "-"), "-")
	if len(name) > 40 {
		name = strings.TrimRight(name[:40], "-.")
	}
	return name
}

// extraSuffix is the "-2" of an extra agent's session name.
var extraSuffix = regexp.MustCompile(`^(.+)-([1-9][0-9]?)$`)

// projectDir is where a project's session opens: its one repo, else the
// folder all its repos share — unless that folder also holds another
// project's repo (then the first repo).
func projectDir(repos []store.RepoWithProject, key string) string {
	var paths []string
	for _, r := range repos {
		if r.ProjectKey == key && r.Path != "" {
			paths = append(paths, filepath.Clean(r.Path))
		}
	}
	if len(paths) == 0 {
		return ""
	}
	common := paths[0]
	for _, p := range paths[1:] {
		for common != "/" && common != "." && p != common && !strings.HasPrefix(p, common+"/") {
			common = filepath.Dir(common)
		}
	}
	if common == "/" || common == "." {
		return paths[0]
	}
	for _, r := range repos {
		if r.ProjectKey != key && r.Path != "" {
			p := filepath.Clean(r.Path)
			if p == common || strings.HasPrefix(p, common+"/") || strings.HasPrefix(common, p+"/") {
				return paths[0]
			}
		}
	}
	return common
}

// projectRepo is the repo a project agent's runs are filed under: the one at
// dir, else the project's first with a path.
func projectRepo(repos []store.RepoWithProject, key, dir string) *store.RepoWithProject {
	var first *store.RepoWithProject
	for i := range repos {
		r := &repos[i]
		if r.ProjectKey != key || r.Path == "" {
			continue
		}
		if filepath.Clean(r.Path) == dir {
			return r
		}
		if first == nil {
			first = r
		}
	}
	return first
}

// projectSessionHost is the machine project agents live on: the master, with
// terminals on.
func (s *Server) projectSessionHost(ctx context.Context, v *projectSessionsView) error {
	hosts, err := s.store.TerminalHosts(ctx)
	if err != nil {
		return err
	}
	for _, h := range hosts {
		if h.Role == "master" && h.Terminal {
			v.cc = s.terminals.control(h.RunnerID)
			v.Host = &projectSessionHost{RunnerID: h.RunnerID, RunnerName: h.RunnerName,
				Online: h.Online && v.cc != nil, SkipPermissions: h.SkipPermissions}
			if rn, err := s.store.RunnerByID(ctx, h.RunnerID); err == nil {
				v.Host.TmuxAgents = rn.Capabilities.TmuxAgents
			}
			for _, ss := range h.Sessions {
				v.tmux[ss.Name] = ss
			}
			break
		}
	}
	return nil
}

func (s *Server) projectSessionsView(ctx context.Context) (*projectSessionsView, error) {
	set, err := s.store.ProjectSessionsSettings(ctx)
	if err != nil {
		return nil, err
	}
	v := &projectSessionsView{Settings: set, Sessions: []ProjectSession{}, tmux: map[string]store.TmuxSession{}}
	if err := s.projectSessionHost(ctx, v); err != nil {
		return nil, err
	}
	projects, err := s.store.ListProjects(ctx, store.TodayFor("UTC"), false)
	if err != nil {
		return nil, err
	}
	repos, err := s.store.AllRepos(ctx)
	if err != nil {
		return nil, err
	}
	runs := map[string]store.Run{}
	if v.Host != nil {
		if runs, err = s.store.OpenTmuxRuns(ctx, v.Host.RunnerID); err != nil {
			return nil, err
		}
	}
	primary := map[string]bool{}
	for _, p := range projects {
		primary[projectSessionName(p.Name)] = true
	}
	ps := &s.projectSessions
	ps.mu.Lock()
	defer ps.mu.Unlock()
	entry := func(p store.Project, dir, name string, extra int) ProjectSession {
		e := ProjectSession{ProjectKey: p.Key, ProjectName: p.Name, ProjectColor: p.Color, ProjectStatus: p.Status,
			Name: name, Dir: dir, Extra: extra}
		t, there := v.tmux[name]
		recent := time.Since(ps.started[name]) < projectSessionGrace
		switch {
		case v.Host == nil || !v.Host.Online:
			e.State = "offline"
		case there && (t.Claude || t.Codex):
			e.State = "running"
		case recent:
			e.State = "starting"
		case there:
			e.State = "stopped"
		default:
			e.State = "missing"
		}
		if there {
			e.Attached = t.Attached
			if t.Activity != "" {
				a := t.Activity
				e.Activity = &a
			}
			if u, ok := ps.urls[name+"@"+t.Created]; ok {
				e.URL = &u
			}
		}
		if run, ok := runs[name]; ok && run.ProjectKey == p.Key {
			id := run.ID
			e.RunID, e.RunStatus, e.Awaiting = &id, run.Status, run.Awaiting
			e.RunPrompt = clipText(run.Prompt, 240)
			if run.Status == "queued" && e.State != "offline" && e.State != "running" {
				e.State = "starting"
			}
		}
		return e
	}
	for _, p := range projects {
		dir := projectDir(repos, p.Key)
		if dir == "" {
			continue // nowhere to open it
		}
		base := projectSessionName(p.Name)
		v.Sessions = append(v.Sessions, entry(p, dir, base, 0))
		// Its extra agents: sessions that are there, or that a run is in.
		extras := map[int]string{}
		note := func(name string) {
			m := extraSuffix.FindStringSubmatch(name)
			if m == nil || m[1] != base || primary[name] {
				return
			}
			if n, _ := strconv.Atoi(m[2]); n >= 2 {
				extras[n] = name
			}
		}
		for name := range v.tmux {
			note(name)
		}
		for name := range runs {
			note(name)
		}
		for n := 2; n < 100 && len(extras) > 0; n++ {
			if name, ok := extras[n]; ok {
				delete(extras, n)
				v.Sessions = append(v.Sessions, entry(p, dir, name, n))
			}
		}
	}
	return v, nil
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 { // not in the middle of a character
		cut = cut[:len(cut)-1]
	}
	if len(cut) > 0 && cut[len(cut)-1] >= 0xC0 {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}

// RunProjectSessions reads the Remote Control links of open sessions until
// ctx ends.
func (s *Server) RunProjectSessions(ctx context.Context) {
	t := time.NewTicker(projectSessionTick)
	defer t.Stop()
	for {
		s.projectSessionsPass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// projectSessionsPass reads the Remote Control link of each running session
// that has none yet. It starts nothing: sessions open when the user asks.
func (s *Server) projectSessionsPass(ctx context.Context) {
	ps := &s.projectSessions
	if !ps.passMu.TryLock() {
		return // one pass at a time
	}
	defer ps.passMu.Unlock()
	v, err := s.projectSessionsView(ctx)
	if err != nil {
		slog.Warn("project sessions", "err", err)
		return
	}
	if v.Host == nil || !v.Host.Online {
		return
	}
	for _, e := range v.Sessions {
		if ctx.Err() != nil {
			return
		}
		if e.State != "running" || e.URL != nil {
			continue
		}
		res, err := v.cc.call(ctx, map[string]any{"type": "rc_url", "session": e.Name}, 10*time.Second)
		if err != nil || res.Text == "" {
			continue // an older agent, Remote Control off, or not printed yet
		}
		ps.mu.Lock()
		ps.urls[e.Name+"@"+v.tmux[e.Name].Created] = res.Text
		ps.mu.Unlock()
	}
}

func (s *Server) getProjectSessions(w http.ResponseWriter, r *http.Request, u *store.User) {
	v, err := s.projectSessionsView(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// updateProjectSessions sets how project agents start (skip_permissions).
func (s *Server) updateProjectSessions(w http.ResponseWriter, r *http.Request, u *store.User) {
	set, err := s.store.ProjectSessionsSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in struct {
		SkipPermissions *bool `json:"skip_permissions"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.SkipPermissions != nil {
		set.SkipPermissions = *in.SkipPermissions
	}
	if err := s.store.SetProjectSessionsSettings(r.Context(), set); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "project_sessions", map[bool]string{true: "skipping permissions", false: "asking for permissions"}[set.SkipPermissions])
	s.getProjectSessions(w, r, u)
}

// agentModel is what a project agent's model may be: an alias or a model id.
var agentModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,79}$`)

// openProjectSession is the project's button: it opens the project's agent
// (or an extra one) on the master with the model and effort asked for, and
// gives it the request, if there is one. An agent that is already open is
// left as it is — the request is typed into it.
func (s *Server) openProjectSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Model           string `json:"model"`
		Effort          string `json:"effort"`
		Prompt          string `json:"prompt"`
		Extra           bool   `json:"extra"`
		SkipPermissions *bool  `json:"skip_permissions"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := context.WithoutCancel(r.Context()) // a session must not be left half started
	in.Model, in.Prompt = strings.TrimSpace(in.Model), strings.TrimSpace(in.Prompt)
	if in.Model != "" && !agentModel.MatchString(in.Model) {
		writeErr(w, r, &store.ValidationError{Field: "model", Message: "not a model name"})
		return
	}
	if in.Effort != "" && !store.OneOf(in.Effort, store.ClaudeEfforts) {
		writeErr(w, r, &store.ValidationError{Field: "effort", Message: "must be one of " + strings.Join(store.ClaudeEfforts, ", ")})
		return
	}
	if len(in.Prompt) > 100000 {
		writeErr(w, r, &store.ValidationError{Field: "prompt", Message: "too long"})
		return
	}
	p, err := s.store.ProjectByKey(ctx, store.TodayFor("UTC"), r.PathValue("key"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	v := &projectSessionsView{tmux: map[string]store.TmuxSession{}}
	if err := s.projectSessionHost(ctx, v); err != nil {
		writeErr(w, r, err)
		return
	}
	if v.Host == nil {
		writeError(w, http.StatusConflict, "runner_offline", "project agents need a master machine with terminals on (\"terminal\": true in its agent.json)")
		return
	}
	if !v.Host.Online {
		writeError(w, http.StatusConflict, "runner_offline", v.Host.RunnerName+" is not connected")
		return
	}
	repos, err := s.store.AllRepos(ctx)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	dir := projectDir(repos, p.Key)
	repo := projectRepo(repos, p.Key, dir)
	if dir == "" || repo == nil {
		writeErr(w, r, &store.ValidationError{Field: "key", Message: p.Name + " has no repo with a path to open a session in"})
		return
	}
	set, err := s.store.ProjectSessionsSettings(ctx)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	skip := set.SkipPermissions
	if in.SkipPermissions != nil {
		skip = *in.SkipPermissions
	}
	skip = skip && v.Host.SkipPermissions
	runnerID := v.Host.RunnerID

	mu := s.tmux.lock(runnerID)
	mu.Lock()
	sessions := s.liveTmux(ctx, v.cc, runnerID)
	base := projectSessionName(p.Name)
	name := base
	if in.Extra {
		projects, _ := s.store.ListProjects(ctx, store.TodayFor("UTC"), true)
		live := claudeTaken(sessions)
		name, err = s.store.FreeTmuxName(ctx, runnerID, 0, base, 2, func(n string) bool {
			for _, other := range projects {
				if projectSessionName(other.Name) == n {
					return true // another project's own session
				}
			}
			return live(n)
		})
		if err != nil {
			mu.Unlock()
			writeErr(w, r, err)
			return
		}
	}
	exists, busy := store.ClaudeIn(sessions, name)
	settling := s.projectSessions.startedAgo(name) < projectSessionSettle
	var open *store.Run
	if cur, err := s.store.OpenRunInTmux(ctx, runnerID, name); err == nil {
		open = cur
	}
	result := func(run *store.Run, started bool) {
		out := map[string]any{"session": name, "runner_id": runnerID, "run": nil, "started": started}
		if run != nil {
			out["run"] = run.Public()
		}
		writeJSON(w, http.StatusOK, out)
	}

	// The agent has a conversation going: the request is one more message.
	if open != nil && (open.Status == "queued" || (open.Status == "running" && (busy || settling))) {
		mu.Unlock()
		if in.Prompt == "" {
			result(open, false)
			return
		}
		run, ok, err := s.sendToAgent(ctx, open, in.Prompt, false)
		if err != nil || !ok {
			if err == nil {
				err = &store.ValidationError{Field: "prompt", Message: "could not send that to " + name}
			}
			writeErr(w, r, err)
			return
		}
		s.dispatchTmux(ctx, runnerID)
		result(run, false)
		return
	}
	// Open, and nobody has asked it anything yet.
	if in.Prompt == "" && (busy || (exists && settling)) {
		mu.Unlock()
		result(nil, false)
		return
	}
	if in.Prompt != "" && exists && settling {
		mu.Unlock()
		writeError(w, http.StatusConflict, "starting", name+" is still starting — send that again in a few seconds")
		return
	}
	// A conversation that stopped with its session (Claude exited) makes
	// room: it stays in the history, and Continue picks it up again.
	if open != nil {
		if open.Status == "running" { // its Claude is gone; the machine's next report would say so
			_ = s.store.EndTmuxRun(ctx, open.ID, "Claude Code is no longer running in tmux session "+name)
		}
		if cur, err := s.store.RunByID(ctx, open.ID); err == nil && cur.Status == "waiting" {
			if _, err := s.store.FinishRunByUser(ctx, open.ID); err != nil {
				slog.Warn("project session: closing the previous run", "run", open.ID, "err", err)
			}
		}
	}

	if in.Prompt == "" {
		// Just the session: it becomes a run with its first request.
		msg := map[string]any{"type": "create", "session": name, "cwd": dir, "start": "claude", "skip_permissions": skip,
			"model": in.Model, "effort": in.Effort, "reuse": exists, "env": map[string]string{"FORGE_PROJECT": p.Key}}
		if as, err := s.store.AgentSettings(ctx); err != nil || as.RemoteControl {
			msg["remote_control"] = name
		}
		_, err := v.cc.call(ctx, msg, 30*time.Second)
		if err == nil {
			s.projectSessions.markStarted(name)
		}
		mu.Unlock()
		if err != nil {
			writeError(w, http.StatusBadGateway, "runner_error", err.Error())
			return
		}
		slog.Info("project session started", "session", name, "dir", dir, "model", in.Model, "effort", in.Effort, "ip", s.clientIP(r))
		s.sec(r, "terminal_create", name+" (project agent"+map[bool]string{true: ", skipping permissions", false: ""}[skip]+")")
		result(nil, true)
		return
	}

	// With a request: a run, started in the session (or typed into the one
	// that is open with no conversation).
	if !v.Host.TmuxAgents {
		mu.Unlock()
		writeError(w, http.StatusConflict, "agent_outdated", v.Host.RunnerName+" runs an older Forge agent that cannot take a request this way; upgrade it (forge update), or start the session without one")
		return
	}
	runIn := store.RunInput{RunnerID: runnerID, RepoID: repo.ID, Kind: "agent", Prompt: in.Prompt, Engine: "claude",
		Model: in.Model, Effort: in.Effort, Interactive: true, Fresh: true}
	if skip {
		runIn.PermissionMode = "bypassPermissions"
	} else {
		runIn.DefaultMode = true
	}
	if dir != filepath.Clean(repo.Path) {
		runIn.Cwd = dir
	}
	run, err := s.store.CreateRun(ctx, runIn)
	if err == nil {
		run, err = s.store.SetRunTmux(ctx, run.ID, name)
	}
	mu.Unlock()
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.dispatchTmux(ctx, runnerID)
	if cur, err := s.store.RunByID(ctx, run.ID); err == nil {
		run = cur
	}
	if run.Status == "failed" {
		writeError(w, http.StatusBadGateway, "runner_error", run.Error)
		return
	}
	slog.Info("project agent run", "run", run.ID, "session", name, "ip", s.clientIP(r))
	s.sec(r, "terminal_create", fmt.Sprintf("%s (project agent, run %d)", name, run.ID))
	result(run, !busy)
}

// closeProjectSession closes one of a project's agent sessions: its
// conversation is finished (which closes the tmux session), or, with none,
// the session is simply closed.
func (s *Server) closeProjectSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := context.WithoutCancel(r.Context())
	p, err := s.store.ProjectByKey(ctx, store.TodayFor("UTC"), r.PathValue("key"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	base := projectSessionName(p.Name)
	name := r.PathValue("name")
	if m := extraSuffix.FindStringSubmatch(name); name != base && (m == nil || m[1] != base) {
		writeError(w, http.StatusNotFound, "not_found", "no such session for "+p.Name)
		return
	}
	v := &projectSessionsView{tmux: map[string]store.TmuxSession{}}
	if err := s.projectSessionHost(ctx, v); err != nil {
		writeErr(w, r, err)
		return
	}
	if v.Host == nil || !v.Host.Online {
		writeError(w, http.StatusConflict, "runner_offline", "the master machine is not connected")
		return
	}
	if open, err := s.store.OpenRunInTmux(ctx, v.Host.RunnerID, name); err == nil && open.ProjectKey == p.Key {
		if _, err := s.store.FinishRunByUser(ctx, open.ID); err != nil {
			writeErr(w, r, err)
			return
		}
		s.inboxes.notify(open.ID)
		s.dispatchTmux(ctx, v.Host.RunnerID) // a running one closes its tmux session
	}
	// Whatever is left of the session (no conversation, or one that had stopped).
	_, _ = v.cc.call(ctx, map[string]any{"type": "kill", "session": name}, 10*time.Second)
	w.WriteHeader(http.StatusNoContent)
}
