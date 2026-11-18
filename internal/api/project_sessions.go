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

// Project agents: each project has one agent — a Claude Code (or, when asked
// for, Codex) session on the master machine with Remote Control on, in a tmux session named F-<Project>
// (the same name Remote Control shows), in the project's folder: its one
// repo, or the folder all its repos share. It is started when the user asks
// for it (POST /api/project-sessions/{key}, with a model, an effort and
// maybe a first request), never by itself; an extra agent for the same
// project is F-<Project>-2, -3, … A request for a project whose agent is
// already open is typed into it.
//
// RunProjectSessions reads, once a minute, the Remote Control link of each
// open session off its scrollback (once per session).
//
// Kept projects: a project on the keep list (opening its agent puts it
// there; PATCH /api/project-sessions/{key} {keep}) has two sessions open on
// the master at all times — its agent F-<Project>, for development, and its
// bot session F-<Project>-Bot, the project's assistant. The same pass starts
// whichever of them is gone (tmux crashed, the machine rebooted, Claude
// exited in it), resuming the conversation that last lived there. A session
// left with just a shell is reused, unless someone is attached to it.

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
	// keepBackoff is how long a kept session that would not start waits
	// before the next try, per failed try (at most keepBackoffMax).
	keepBackoff    = 5 * time.Minute
	keepBackoffMax = time.Hour
	botSuffix      = "-Bot"
)

// keepTry is how a kept session's restarts have gone.
type keepTry struct {
	n    int
	last time.Time
}

type projectSessionState struct {
	mu      sync.Mutex
	started map[string]time.Time // session name → when Forge last started it
	urls    map[string]string    // session name + created → its Remote Control link
	tries   map[string]keepTry   // kept session name → its restarts since it last ran
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
	// Bot: the project's bot session (F-<Project>-Bot).
	Bot bool `json:"bot"`
	// Kept: the project is on the keep list (its agent and bot session are
	// started again whenever they are gone).
	Kept bool `json:"kept"`
	// Engine is the agent in it: "claude", "codex", or "" when none is
	// open and no conversation says.
	Engine string `json:"engine"`
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
	// Codex: Codex can be the agent there (Codex installed, and an agent
	// that hosts Codex in tmux).
	Codex bool `json:"codex"`
}

type projectSessionsView struct {
	Settings store.ProjectSessionsSettings `json:"settings"`
	Host     *projectSessionHost           `json:"host"`
	Sessions []ProjectSession              `json:"sessions"`

	cc    *controlConn
	tmux  map[string]store.TmuxSession
	keeps map[int64]store.KeptAgent
	ids   map[string]int64 // project key → id
}

// projectSessionName is F-<project name>, cut down to what tmux allows.
func projectSessionName(project string) string {
	name := projectSessionPrefix + strings.Trim(sessionUnsafe.ReplaceAllString(project, "-"), "-")
	if len(name) > 40 {
		name = strings.TrimRight(name[:40], "-.")
	}
	return name
}

// projectBotSessionName is F-<project name>-Bot: the project's bot session.
func projectBotSessionName(project string) string {
	base := projectSessionName(project)
	if len(base) > 40-len(botSuffix) {
		base = strings.TrimRight(base[:40-len(botSuffix)], "-.")
	}
	return base + botSuffix
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
				v.Host.Codex = rn.Capabilities.Codex && rn.Capabilities.TmuxCodex
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
	v := &projectSessionsView{Settings: set, Sessions: []ProjectSession{}, tmux: map[string]store.TmuxSession{}, ids: map[string]int64{}}
	if err := s.projectSessionHost(ctx, v); err != nil {
		return nil, err
	}
	if v.keeps, err = s.store.KeptAgents(ctx); err != nil {
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
		primary[projectBotSessionName(p.Name)] = true
		v.ids[p.Key] = p.ID
	}
	ps := &s.projectSessions
	ps.mu.Lock()
	defer ps.mu.Unlock()
	entry := func(p store.Project, dir, name string, extra int) ProjectSession {
		_, kept := v.keeps[p.ID]
		e := ProjectSession{ProjectKey: p.Key, ProjectName: p.Name, ProjectColor: p.Color, ProjectStatus: p.Status,
			Name: name, Dir: dir, Extra: extra, Kept: kept}
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
			e.Engine = store.AgentEngineIn([]store.TmuxSession{t}, name)
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
			if e.Engine == "" {
				e.Engine = tmuxEngine(run.Engine)
			}
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
		// Its bot session: kept, or there anyway.
		bot := projectBotSessionName(p.Name)
		_, kept := v.keeps[p.ID]
		_, there := v.tmux[bot]
		if run, ok := runs[bot]; kept || there || (ok && run.ProjectKey == p.Key) {
			e := entry(p, dir, bot, 0)
			e.Bot = true
			v.Sessions = append(v.Sessions, e)
		}
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
	s.keepPass(ctx, v)
}

// shells are what a tmux pane runs when nothing else is running in it.
var shells = map[string]bool{"zsh": true, "bash": true, "fish": true, "sh": true, "dash": true, "nu": true}

// keepPass starts the kept sessions that are gone, or that are left with just
// a shell nobody is attached to; one that would not start is tried again
// after a while (keepBackoff, doubling up to keepBackoffMax).
func (s *Server) keepPass(ctx context.Context, v *projectSessionsView) {
	if len(v.keeps) == 0 || v.Host == nil || !v.Host.Online {
		return
	}
	ps := &s.projectSessions
	for _, e := range v.Sessions {
		if ctx.Err() != nil {
			return
		}
		if !e.Kept || e.Extra != 0 {
			continue
		}
		if e.State == "running" {
			ps.mu.Lock()
			delete(ps.tries, e.Name)
			ps.mu.Unlock()
			continue
		}
		if e.State != "stopped" && e.State != "missing" {
			continue // starting, or offline
		}
		if t, there := v.tmux[e.Name]; there && (t.Attached > 0 || !shells[t.Command]) {
			continue // someone is using that shell
		}
		ps.mu.Lock()
		try := ps.tries[e.Name]
		wait := keepBackoff * time.Duration(1<<min(try.n, 4))
		if try.n == 0 {
			wait = 0
		}
		if wait > keepBackoffMax {
			wait = keepBackoffMax
		}
		due := time.Since(try.last) >= wait
		if due {
			ps.tries[e.Name] = keepTry{n: try.n + 1, last: time.Now()}
		}
		ps.mu.Unlock()
		if !due {
			continue
		}
		if err := s.startKept(ctx, v, e); err != nil {
			slog.Warn("kept project session", "session", e.Name, "err", err)
		}
	}
}

// botSystem is what a project's bot session is told it is for.
func botSystem(project, agent, instructions string) string {
	out := "This is the " + project + " bot session: the project's assistant in Forge, not its development session. " +
		"Answer questions about the project, check its state (Forge MCP tools: forge_context, forge_tasks, forge_checkup, forge_recall), " +
		"plan and triage its work and keep its Forge tasks up to date. Development happens in the tmux session " + agent +
		": hand code changes over to it as Forge tasks (or ask the user to) rather than making them here, unless the user asks you to."
	if instructions = strings.TrimSpace(instructions); instructions != "" {
		out += " The user's standing orders for this bot: " + instructions
	}
	return out
}

// startKept starts a kept session again, in the project's folder, with the
// keep list's engine, model and effort, resuming the conversation that last
// lived in it.
func (s *Server) startKept(ctx context.Context, v *projectSessionsView, e ProjectSession) error {
	ctx = context.WithoutCancel(ctx)
	id := v.ids[e.ProjectKey]
	keep, ok := v.keeps[id]
	if !ok {
		return nil
	}
	engine := keep.Engine
	if engine == "" {
		engine = "claude"
	}
	if engine == "codex" && !v.Host.Codex {
		engine = "claude"
	}
	runnerID := v.Host.RunnerID
	mu := s.tmux.lock(runnerID)
	mu.Lock()
	defer mu.Unlock()
	sessions := s.liveTmux(ctx, v.cc, runnerID)
	exists, busy := store.ClaudeIn(sessions, e.Name)
	if busy {
		return nil // it came back meanwhile
	}
	for _, t := range sessions {
		if t.Name == e.Name && (t.Attached > 0 || !shells[t.Command]) {
			return nil
		}
	}
	if open, err := s.store.OpenRunInTmux(ctx, runnerID, e.Name); err == nil {
		if open.Status == "queued" {
			return nil // Forge is about to start it
		}
		if open.Status == "running" { // its agent is gone; the next report would say so
			_ = s.store.EndTmuxRun(ctx, open.ID, "the agent is no longer running in tmux session "+e.Name+"; Forge starts it again (kept project)")
		}
	}
	msg := map[string]any{"type": "create", "session": e.Name, "cwd": e.Dir, "start": engine,
		"skip_permissions": keep.SkipPermissions && v.Host.SkipPermissions,
		"model": keep.Model, "effort": keep.Effort, "reuse": exists, "env": map[string]string{"FORGE_PROJECT": e.ProjectKey}}
	if last, err := s.store.LastTmuxConversation(ctx, runnerID, e.Name); err == nil && tmuxEngine(last.Engine) == engine {
		msg["resume"] = last.SessionID
	}
	if engine == "claude" {
		if as, err := s.store.AgentSettings(ctx); err != nil || as.RemoteControl {
			msg["remote_control"] = e.Name
		}
		if e.Bot {
			var orders string
			if bs, err := s.store.BotSettingsFor(ctx, id); err == nil {
				orders = bs.Instructions
			}
			msg["append_system"] = botSystem(e.ProjectName, projectSessionName(e.ProjectName), orders)
		}
	}
	if _, err := v.cc.call(ctx, msg, 30*time.Second); err != nil {
		return err
	}
	s.projectSessions.markStarted(e.Name)
	slog.Info("kept project session started", "session", e.Name, "dir", e.Dir, "engine", engine, "resume", msg["resume"] != nil)
	return nil
}

// keepProjectSession puts a project on the keep list (its agent and bot
// session open from then on, started now) or takes it off (both closed).
func (s *Server) keepProjectSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Keep            *bool  `json:"keep"`
		Engine          string `json:"engine"`
		Model           string `json:"model"`
		Effort          string `json:"effort"`
		SkipPermissions *bool  `json:"skip_permissions"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Keep == nil {
		writeErr(w, r, &store.ValidationError{Field: "keep", Message: "required"})
		return
	}
	ctx := context.WithoutCancel(r.Context())
	p, err := s.store.ProjectByKey(ctx, store.TodayFor("UTC"), r.PathValue("key"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if !*in.Keep {
		if err := s.store.SetKeptAgent(ctx, p.ID, nil); err != nil {
			writeErr(w, r, err)
			return
		}
		v := &projectSessionsView{tmux: map[string]store.TmuxSession{}}
		if err := s.projectSessionHost(ctx, v); err == nil && v.Host != nil && v.Host.Online {
			for _, name := range []string{projectSessionName(p.Name), projectBotSessionName(p.Name)} {
				s.closeSession(ctx, v, p.Key, name)
			}
		}
		s.sec(r, "project_sessions", p.Key+": no longer kept")
		s.getProjectSessions(w, r, u)
		return
	}
	keep, err := s.keptAgentInput(ctx, in.Engine, in.Model, in.Effort, in.SkipPermissions)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	repos, err := s.store.AllRepos(ctx)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if projectDir(repos, p.Key) == "" {
		writeErr(w, r, &store.ValidationError{Field: "key", Message: p.Name + " has no repo with a path to open a session in"})
		return
	}
	if err := s.store.SetKeptAgent(ctx, p.ID, keep); err != nil {
		writeErr(w, r, err)
		return
	}
	s.projectSessions.mu.Lock()
	delete(s.projectSessions.tries, projectSessionName(p.Name))
	delete(s.projectSessions.tries, projectBotSessionName(p.Name))
	s.projectSessions.mu.Unlock()
	s.sec(r, "project_sessions", p.Key+": kept open")
	go s.projectSessionsPass(ctx) // start them now
	s.getProjectSessions(w, r, u)
}

// keptAgentInput checks a keep request's engine, model and effort.
func (s *Server) keptAgentInput(ctx context.Context, engine, model, effort string, skip *bool) (*store.KeptAgent, error) {
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" {
		engine = "claude"
	}
	if engine != "claude" && engine != "codex" {
		return nil, &store.ValidationError{Field: "engine", Message: "must be claude or codex"}
	}
	model = strings.TrimSpace(model)
	if model != "" && !agentModel.MatchString(model) {
		return nil, &store.ValidationError{Field: "model", Message: "not a model name"}
	}
	efforts := store.ClaudeEfforts
	if engine == "codex" {
		efforts = store.CodexEfforts
	}
	if effort != "" && !store.OneOf(effort, efforts) {
		return nil, &store.ValidationError{Field: "effort", Message: "must be one of " + strings.Join(efforts, ", ")}
	}
	set, err := s.store.ProjectSessionsSettings(ctx)
	if err != nil {
		return nil, err
	}
	k := &store.KeptAgent{Engine: engine, Model: model, Effort: effort, SkipPermissions: set.SkipPermissions,
		Since: time.Now().UTC().Format(time.RFC3339)}
	if skip != nil {
		k.SkipPermissions = *skip
	}
	return k, nil
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
		Engine          string `json:"engine"`
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
	in.Engine = strings.ToLower(strings.TrimSpace(in.Engine))
	if in.Engine == "" {
		in.Engine = "claude"
	}
	if in.Engine != "claude" && in.Engine != "codex" {
		writeErr(w, r, &store.ValidationError{Field: "engine", Message: "must be claude or codex"})
		return
	}
	efforts := store.ClaudeEfforts
	if in.Engine == "codex" {
		efforts = store.CodexEfforts
	}
	if in.Effort != "" && !store.OneOf(in.Effort, efforts) {
		writeErr(w, r, &store.ValidationError{Field: "effort", Message: "must be one of " + strings.Join(efforts, ", ")})
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
	if in.Engine == "codex" && !v.Host.Codex {
		writeError(w, http.StatusConflict, "agent_outdated", v.Host.RunnerName+" cannot run Codex agents: install Codex there, run forge update and forge agent setup-codex")
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
	// Opening a project's own agent keeps the project open from then on
	// (its agent and its bot session), with what it was opened with.
	keepIt := func() {
		if in.Extra {
			return
		}
		keeps, err := s.store.KeptAgents(ctx)
		if err != nil {
			return
		}
		if _, ok := keeps[p.ID]; ok {
			return
		}
		sk := skip
		if k, err := s.keptAgentInput(ctx, in.Engine, in.Model, in.Effort, &sk); err == nil {
			if err := s.store.SetKeptAgent(ctx, p.ID, k); err == nil {
				go s.projectSessionsPass(context.WithoutCancel(ctx)) // its bot session
			}
		}
	}

	mu := s.tmux.lock(runnerID)
	mu.Lock()
	sessions := s.liveTmux(ctx, v.cc, runnerID)
	base := projectSessionName(p.Name)
	name := base
	// The other agent open (or about to be) in the project's session: this
	// one is an extra agent next to it.
	otherAgent := func(n string) bool {
		if e := store.AgentEngineIn(sessions, n); e != "" && e != in.Engine {
			return true
		}
		cur, err := s.store.OpenRunInTmux(ctx, runnerID, n)
		return err == nil && cur.Status != "waiting" && tmuxEngine(cur.Engine) != in.Engine
	}
	if in.Extra || otherAgent(base) {
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
		msg := map[string]any{"type": "create", "session": name, "cwd": dir, "start": in.Engine, "skip_permissions": skip,
			"model": in.Model, "effort": in.Effort, "reuse": exists, "env": map[string]string{"FORGE_PROJECT": p.Key}}
		if as, err := s.store.AgentSettings(ctx); in.Engine == "claude" && (err != nil || as.RemoteControl) {
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
		if name == base {
			keepIt()
		}
		slog.Info("project session started", "session", name, "dir", dir, "engine", in.Engine, "model", in.Model, "effort", in.Effort, "ip", s.clientIP(r))
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
	runIn := store.RunInput{RunnerID: runnerID, RepoID: repo.ID, Kind: "agent", Prompt: in.Prompt, Engine: in.Engine,
		Model: in.Model, Effort: in.Effort, Interactive: true, Fresh: true}
	switch {
	case skip:
		runIn.PermissionMode = "bypassPermissions"
	case in.Engine == "codex":
		runIn.PermissionMode = "acceptEdits" // Codex asks in the terminal for anything outside the workspace
	default:
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
	if name == base {
		keepIt()
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
	bot := projectBotSessionName(p.Name)
	name := r.PathValue("name")
	if m := extraSuffix.FindStringSubmatch(name); name != base && name != bot && (m == nil || m[1] != base) {
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
	// Closing a kept project's agent or bot session takes the project off
	// the keep list: otherwise it would be started again within a minute.
	if name == base || name == bot {
		if keeps, err := s.store.KeptAgents(ctx); err == nil {
			if _, ok := keeps[p.ID]; ok {
				if err := s.store.SetKeptAgent(ctx, p.ID, nil); err != nil {
					writeErr(w, r, err)
					return
				}
				s.sec(r, "project_sessions", p.Key+": no longer kept ("+name+" closed)")
			}
		}
	}
	if err := s.closeSession(ctx, v, p.Key, name); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// closeSession finishes the conversation in one of a project's sessions
// (which closes it) and kills whatever is left of the session.
func (s *Server) closeSession(ctx context.Context, v *projectSessionsView, projectKey, name string) error {
	if open, err := s.store.OpenRunInTmux(ctx, v.Host.RunnerID, name); err == nil && open.ProjectKey == projectKey {
		if _, err := s.store.FinishRunByUser(ctx, open.ID); err != nil {
			return err
		}
		s.inboxes.notify(open.ID)
		s.dispatchTmux(ctx, v.Host.RunnerID) // a running one closes its tmux session
	}
	// Whatever is left of the session (no conversation, or one that had stopped).
	_, _ = v.cc.call(ctx, map[string]any{"type": "kill", "session": name}, 10*time.Second)
	return nil
}
