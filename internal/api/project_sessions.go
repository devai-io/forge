package api

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/store"
)

// Project sessions: one long-running Claude Code session per project, kept
// on the master machine with Remote Control on, so every project can be
// picked up from claude.ai/code or the Claude app. Each is a tmux session
// named F-<Project> (the same name Remote Control shows) in the project's
// folder: its one repo, or the folder all its repos share.
//
// RunProjectSessions checks once a minute, when the setting is on: a missing
// session is started, one whose Claude has exited (and nobody is attached)
// is started again, and the Remote Control link of a running one is read off
// its scrollback once. The F- prefix is Forge's: a session by that name that
// is not running Claude gets replaced.

const (
	projectSessionPrefix = "F-"
	projectSessionTick   = time.Minute
	// projectSessionGrace is how long a just-started session may take to
	// show Claude before it counts as stopped (the tmux snapshot comes with
	// the machine's heartbeat, Claude takes a few seconds to start).
	projectSessionGrace = 3 * time.Minute
)

type projectSessionState struct {
	mu      sync.Mutex
	started map[string]time.Time // session name → when Forge last started it
	urls    map[string]string    // session name + created → its Remote Control link
	passMu  sync.Mutex
}

// ProjectSession is one project's entry; State is running, starting,
// stopped (no Claude in it), missing or offline (the master is not
// connected).
type ProjectSession struct {
	ProjectKey   string  `json:"project_key"`
	ProjectName  string  `json:"project_name"`
	ProjectColor string  `json:"project_color"`
	Name         string  `json:"name"`
	Dir          string  `json:"dir"`
	State        string  `json:"state"`
	URL          *string `json:"url"`
	Attached     int     `json:"attached"`
	Activity     *string `json:"activity"`
}

type projectSessionHost struct {
	RunnerID        int64  `json:"runner_id"`
	RunnerName      string `json:"runner_name"`
	Online          bool   `json:"online"`
	SkipPermissions bool   `json:"skip_permissions"`
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
		name = name[:40]
	}
	return name
}

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

func (s *Server) projectSessionsView(ctx context.Context) (*projectSessionsView, error) {
	set, err := s.store.ProjectSessionsSettings(ctx)
	if err != nil {
		return nil, err
	}
	v := &projectSessionsView{Settings: set, Sessions: []ProjectSession{}, tmux: map[string]store.TmuxSession{}}
	hosts, err := s.store.TerminalHosts(ctx)
	if err != nil {
		return nil, err
	}
	for _, h := range hosts {
		if h.Role == "master" && h.Terminal {
			v.cc = s.terminals.control(h.RunnerID)
			v.Host = &projectSessionHost{RunnerID: h.RunnerID, RunnerName: h.RunnerName,
				Online: h.Online && v.cc != nil, SkipPermissions: h.SkipPermissions}
			for _, ss := range h.Sessions {
				v.tmux[ss.Name] = ss
			}
			break
		}
	}
	projects, err := s.store.ListProjects(ctx, store.TodayFor("UTC"), false)
	if err != nil {
		return nil, err
	}
	repos, err := s.store.AllRepos(ctx)
	if err != nil {
		return nil, err
	}
	ps := &s.projectSessions
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, p := range projects {
		dir := projectDir(repos, p.Key)
		if dir == "" {
			continue // nowhere to open it
		}
		e := ProjectSession{ProjectKey: p.Key, ProjectName: p.Name, ProjectColor: p.Color,
			Name: projectSessionName(p.Name), Dir: dir}
		t, there := v.tmux[e.Name]
		recent := time.Since(ps.started[e.Name]) < projectSessionGrace
		switch {
		case v.Host == nil || !v.Host.Online:
			e.State = "offline"
		case there && t.Claude:
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
			if u, ok := ps.urls[e.Name+"@"+t.Created]; ok {
				e.URL = &u
			}
		}
		v.Sessions = append(v.Sessions, e)
	}
	return v, nil
}

// RunProjectSessions keeps the project sessions up until ctx ends.
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
	if !v.Settings.Enabled || v.Host == nil || !v.Host.Online {
		return
	}
	skip := v.Settings.SkipPermissions && v.Host.SkipPermissions
	for _, e := range v.Sessions {
		if ctx.Err() != nil {
			return
		}
		switch e.State {
		case "stopped":
			if e.Attached > 0 {
				continue // someone is in it: leave it to them
			}
			if _, err := v.cc.call(ctx, map[string]any{"type": "kill", "session": e.Name}, 10*time.Second); err != nil {
				slog.Warn("project session: kill failed", "session", e.Name, "err", err)
				continue
			}
			fallthrough
		case "missing":
			ps.mu.Lock()
			ps.started[e.Name] = time.Now()
			ps.mu.Unlock()
			if _, err := v.cc.call(ctx, map[string]any{"type": "create", "session": e.Name, "cwd": e.Dir,
				"start": "claude", "skip_permissions": skip, "remote_control": e.Name}, 20*time.Second); err != nil {
				slog.Warn("project session: start failed", "session", e.Name, "err", err)
				continue
			}
			slog.Info("project session started", "session", e.Name, "dir", e.Dir, "restart", e.State == "stopped")
			_ = s.store.LogSecurity(ctx, "terminal_create", e.Name+" (project session)", "", "forge", 0)
		case "running":
			if e.URL != nil {
				continue
			}
			res, err := v.cc.call(ctx, map[string]any{"type": "rc_url", "session": e.Name}, 10*time.Second)
			if err != nil || res.Text == "" {
				continue // an older agent, or not printed yet
			}
			ps.mu.Lock()
			ps.urls[e.Name+"@"+v.tmux[e.Name].Created] = res.Text
			ps.mu.Unlock()
		}
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

// updateProjectSessions turns the sessions on or off; turning them on starts
// the missing ones right away.
func (s *Server) updateProjectSessions(w http.ResponseWriter, r *http.Request, u *store.User) {
	set, err := s.store.ProjectSessionsSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in struct {
		Enabled         *bool `json:"enabled"`
		SkipPermissions *bool `json:"skip_permissions"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Enabled != nil {
		set.Enabled = *in.Enabled
	}
	if in.SkipPermissions != nil {
		set.SkipPermissions = *in.SkipPermissions
	}
	if err := s.store.SetProjectSessionsSettings(r.Context(), set); err != nil {
		writeErr(w, r, err)
		return
	}
	detail := "off"
	if set.Enabled {
		detail = "on"
		if set.SkipPermissions {
			detail += ", skipping permissions"
		}
	}
	s.sec(r, "project_sessions", detail)
	if set.Enabled {
		go func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
			defer cancel()
			s.projectSessionsPass(ctx)
		}()
	}
	s.getProjectSessions(w, r, u)
}
