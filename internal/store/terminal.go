package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type TmuxWindow struct {
	Index        int     `json:"index"`
	Name         string  `json:"name"`
	Active       bool    `json:"active"`
	Command      string  `json:"command"`
	Path         string  `json:"path"`
	Claude       bool    `json:"claude"`
	ProjectKey   *string `json:"project_key"`
	ProjectColor *string `json:"project_color"`
	RepoID       *int64  `json:"repo_id"`
	RepoName     *string `json:"repo_name"`
}

type TmuxSession struct {
	Name         string       `json:"name"`
	Windows      int          `json:"windows"`
	Attached     int          `json:"attached"`
	Created      string       `json:"created"`
	Activity     string       `json:"activity"`
	Path         string       `json:"path"`
	Command      string       `json:"command"`
	Claude       bool         `json:"claude"`
	ProjectKey   *string      `json:"project_key"`
	ProjectColor *string      `json:"project_color"`
	RepoID       *int64       `json:"repo_id"`
	RepoName     *string      `json:"repo_name"`
	WindowList   []TmuxWindow `json:"window_list"`
}

type TerminalHost struct {
	RunnerID   int64         `json:"runner_id"`
	RunnerName string        `json:"runner_name"`
	Role       string        `json:"role"`
	Hostname   string        `json:"hostname"`
	Online     bool          `json:"online"`
	Terminal   bool          `json:"terminal"`
	Sessions   []TmuxSession `json:"sessions"`
	UpdatedAt  *time.Time    `json:"updated_at"`
}

func (s *Store) saveTmux(ctx context.Context, runnerID int64, sessions []TmuxSession) error {
	if sessions == nil {
		return nil // an old runner that does not report tmux
	}
	for i := range sessions { // the project fields are ours to fill, not the runner's
		sessions[i].ProjectKey, sessions[i].ProjectColor, sessions[i].RepoID, sessions[i].RepoName = nil, nil, nil, nil
		for j := range sessions[i].WindowList {
			w := &sessions[i].WindowList[j]
			w.ProjectKey, w.ProjectColor, w.RepoID, w.RepoName = nil, nil, nil, nil
		}
	}
	_, err := s.DB.Exec(ctx, `UPDATE runners SET tmux = $2, tmux_at = now() WHERE id = $1`, runnerID, sessions)
	return err
}

// pathIndex resolves working directories to repos and projects.
type pathIndex struct {
	repos    []RepoWithProject
	colors   map[string]string
	names    map[string]string // lower(name or key) -> key
	projects map[string]bool
}

func (s *Store) loadPathIndex(ctx context.Context) (*pathIndex, error) {
	repos, err := s.AllRepos(ctx)
	if err != nil {
		return nil, err
	}
	idx := &pathIndex{repos: repos, colors: map[string]string{}, names: map[string]string{}, projects: map[string]bool{}}
	rows, err := s.DB.Query(ctx, `SELECT key, name, color FROM projects WHERE status <> 'archived'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, name, color string
		if err := rows.Scan(&key, &name, &color); err != nil {
			return nil, err
		}
		idx.colors[key] = color
		idx.names[strings.ToLower(key)] = key
		idx.names[strings.ToLower(name)] = key
		idx.projects[key] = true
	}
	// Longest path first, so a nested repo wins over its parent.
	sort.SliceStable(idx.repos, func(i, j int) bool { return len(idx.repos[i].Path) > len(idx.repos[j].Path) })
	return idx, rows.Err()
}

// match finds the repo a directory is inside; failing that, the one project
// whose repos all live under it (a session opened at ~/dev/alpha); failing
// that, a project whose name or key is the session's name.
func (idx *pathIndex) match(path, sessionName string) (projectKey string, repo *RepoWithProject) {
	path = filepath.Clean(path)
	if path != "." && path != "/" {
		for i := range idx.repos {
			r := &idx.repos[i]
			if r.Path == "" || !idx.projects[r.ProjectKey] {
				continue
			}
			if path == r.Path || strings.HasPrefix(path, r.Path+"/") {
				return r.ProjectKey, r
			}
		}
		under := map[string]bool{}
		for _, r := range idx.repos {
			if r.Path != "" && idx.projects[r.ProjectKey] && strings.HasPrefix(r.Path, path+"/") {
				under[r.ProjectKey] = true
			}
		}
		if len(under) == 1 {
			for k := range under {
				return k, nil
			}
		}
	}
	if k, ok := idx.names[strings.ToLower(strings.TrimSpace(sessionName))]; ok {
		return k, nil
	}
	return "", nil
}

func (idx *pathIndex) fields(path, name string) (key, color *string, repoID *int64, repoName *string) {
	k, repo := idx.match(path, name)
	if k != "" {
		c := idx.colors[k]
		key, color = &k, &c
	}
	if repo != nil {
		id, n := repo.ID, repo.Name
		repoID, repoName = &id, &n
	}
	return
}

func (s *Store) TerminalHosts(ctx context.Context) ([]TerminalHost, error) {
	idx, err := s.loadPathIndex(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT id, name, role, hostname,
		coalesce(last_seen_at > ts_add(now(), -`+onlineWindow+`), 0),
		coalesce(capabilities->>'terminal', 0), tmux, tmux_at
		FROM runners ORDER BY (role = 'master') DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TerminalHost{}
	for rows.Next() {
		var h TerminalHost
		var raw []byte
		if err := rows.Scan(&h.RunnerID, &h.RunnerName, &h.Role, &h.Hostname, &h.Online, &h.Terminal, &raw, &h.UpdatedAt); err != nil {
			return nil, err
		}
		h.Sessions = []TmuxSession{}
		_ = json.Unmarshal(raw, &h.Sessions)
		for i := range h.Sessions {
			ss := &h.Sessions[i]
			ss.ProjectKey, ss.ProjectColor, ss.RepoID, ss.RepoName = idx.fields(ss.Path, ss.Name)
			if ss.WindowList == nil {
				ss.WindowList = []TmuxWindow{}
			}
			for j := range ss.WindowList {
				w := &ss.WindowList[j]
				w.ProjectKey, w.ProjectColor, w.RepoID, w.RepoName = idx.fields(w.Path, "")
			}
		}
		sort.SliceStable(h.Sessions, func(i, j int) bool {
			if h.Sessions[i].Claude != h.Sessions[j].Claude {
				return h.Sessions[i].Claude
			}
			return h.Sessions[i].Activity > h.Sessions[j].Activity
		})
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) ProjectKeyByID(ctx context.Context, id int64) (string, error) {
	var key string
	err := s.DB.QueryRow(ctx, `SELECT key FROM projects WHERE id = $1`, id).Scan(&key)
	return key, mapErr(err)
}

// TaskByRef resolves "ALPHA-12".
func (s *Store) TaskByRef(ctx context.Context, ref string) (*Task, error) {
	i := strings.LastIndexByte(ref, '-')
	if i <= 0 {
		return nil, ErrNotFound
	}
	var n int
	if _, err := fmt.Sscanf(ref[i+1:], "%d", &n); err != nil {
		return nil, ErrNotFound
	}
	return scanTask(s.DB.QueryRow(ctx, taskSelect+` WHERE p.key = $1 AND t.number = $2`,
		strings.ToUpper(ref[:i]), n))
}

// ── Claude context ────────────────────────────────────────────────────────

type PathContext struct {
	ProjectKey string  `json:"project_key"`
	RepoName   *string `json:"repo_name"`
	Markdown   string  `json:"markdown"`
}

// TaskTrimmer picks, from a project's open tasks (most important first), the
// ones worth telling a session opened in repo about. nil keeps them all.
type TaskTrimmer func(ctx context.Context, repo *Repo, tasks []Task) ([]Task, string)

// ContextForPath renders what a Claude session opened in `cwd` should know
// about its Forge project, compactly: it is injected at every session start.
func (s *Store) ContextForPath(ctx context.Context, cwd string, trim TaskTrimmer) (*PathContext, error) {
	idx, err := s.loadPathIndex(ctx)
	if err != nil {
		return nil, err
	}
	key, repo := idx.match(cwd, filepath.Base(cwd))
	if key == "" {
		return nil, ErrNotFound
	}
	p, err := s.ProjectByKey(ctx, TodayFor("UTC"), key)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w("# Forge project: %s (%s) — %s, priority P%d, %s\n", p.Name, p.Key, p.Status, p.Priority, p.Category)
	if p.Summary != "" {
		w("%s\n", p.Summary)
	}
	if p.TargetDate != nil {
		w("Next milestone: %s\n", *p.TargetDate)
	}
	w("Forge: %s/p/%s — tasks, infra and check-ups for this project live there.\n", s.PublicURL, p.Key)

	var out *PathContext
	if repo != nil {
		name := repo.Name
		out = &PathContext{ProjectKey: key, RepoName: &name}
		w("\n## This repo: %s (%s)\n", repo.Name, repo.Kind)
		if repo.Deploy != "" {
			w("Deploy: %s\n", repo.Deploy)
		}
		if g := repo.Git; g != nil {
			w("Git (last scan): branch %s, %d changed, %d untracked, %d ahead, %d behind", g.Branch, g.Dirty, g.Untracked, g.Ahead, g.Behind)
			if g.CI != nil {
				w("; CI %s %s (%s)", g.CI.Status, g.CI.Conclusion, g.CI.Workflow)
			}
			w("\n")
		}
		if repo.Notes != "" {
			w("Notes: %s\n", repo.Notes)
		}
	} else {
		out = &PathContext{ProjectKey: key}
	}
	var others []string
	for _, r := range p.Repos {
		if repo == nil || r.ID != repo.ID {
			others = append(others, fmt.Sprintf("%s (%s)", r.Name, r.Path))
		}
	}
	if len(others) > 0 {
		w("Other repos: %s\n", strings.Join(others, ", "))
	}

	if p.Description != "" {
		w("\n## About\n%s\n", clipLines(p.Description, 20, 1800))
	}
	if p.InfraNotes != "" {
		w("\n## Infra & change control\n%s\n", clipLines(p.InfraNotes, 25, 2200))
	}
	if len(p.Servers) > 0 {
		var sv []string
		for _, x := range p.Servers {
			tag := x.Environment
			if x.Critical {
				tag += ", CRITICAL"
			}
			sv = append(sv, fmt.Sprintf("%s (%s; %s)", x.Name, x.Role, tag))
		}
		w("Servers: %s\n", strings.Join(sv, "; "))
	}
	var down []string
	for _, e := range p.Endpoints {
		if e.LastStatus == "down" {
			down = append(down, e.Name+" ("+e.URL+")")
		}
	}
	if len(down) > 0 {
		w("**Down right now:** %s\n", strings.Join(down, ", "))
	}

	tasks, err := s.ListTasks(ctx, TaskFilter{ProjectKey: key, Open: true, Limit: 200})
	if err == nil && len(tasks) > 0 {
		rank := map[string]int{"in_progress": 0, "blocked": 1, "todo": 2, "backlog": 3}
		prio := map[string]int{"urgent": 0, "high": 1, "medium": 2, "low": 3}
		sort.SliceStable(tasks, func(i, j int) bool {
			if tasks[i].Focus != tasks[j].Focus {
				return tasks[i].Focus
			}
			if rank[tasks[i].Status] != rank[tasks[j].Status] {
				return rank[tasks[i].Status] < rank[tasks[j].Status]
			}
			return prio[tasks[i].Priority] < prio[tasks[j].Priority]
		})
		total, note := len(tasks), ""
		if trim != nil && repo != nil {
			tasks, note = trim(ctx, &repo.Repo, tasks)
		}
		if len(tasks) < total {
			w("\n## Open tasks (%d of %d — %s; forge_tasks lists all)\n", len(tasks), total, note)
		} else {
			w("\n## Open tasks (%d)\n", total)
		}
		for i, t := range tasks {
			if i == 20 {
				w("- …and %d more (forge_tasks)\n", len(tasks)-20)
				break
			}
			extra := ""
			if t.Focus {
				extra += " ★focus"
			}
			if t.DueDate != nil {
				extra += " due " + *t.DueDate
			}
			if t.RepoName != nil {
				extra += " [" + *t.RepoName + "]"
			}
			w("- %s %s/%s: %s%s\n", t.Ref, strings.ReplaceAll(t.Status, "_", " "), t.Priority, t.Title, extra)
		}
	}

	if c, err := s.LatestCheckup(ctx); err == nil {
		var lines []string
		for _, it := range c.Items {
			if it.Severity == "ok" || it.Done || it.ProjectKey == nil || *it.ProjectKey != key {
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s %s → %s", map[string]string{"fail": "✗", "warn": "!"}[it.Severity], it.Title, it.Action))
		}
		if len(lines) > 0 {
			w("\n## Open check-up actions (%s)\n%s\n", c.Date, strings.Join(lines, "\n"))
		}
	}
	w("\nUse the `forge` MCP tools to read or update this (forge_tasks, forge_update_task, forge_comment, forge_create_task, forge_checkup). Refer to tasks by ref (%s-N).\n", key)
	out.Markdown = b.String()
	return out, nil
}

func clipLines(s string, maxLines, maxChars int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	cut := false
	if len(lines) > maxLines {
		lines, cut = lines[:maxLines], true
	}
	out := strings.Join(lines, "\n")
	if len(out) > maxChars {
		out, cut = out[:maxChars], true
	}
	if cut {
		out += "\n…"
	}
	return out
}

// ProjectForPath is the project (and repo, when inside one) a working
// directory belongs to, as the session context resolves it; "" when none.
func (s *Store) ProjectForPath(ctx context.Context, cwd string) (string, *RepoWithProject, error) {
	idx, err := s.loadPathIndex(ctx)
	if err != nil {
		return "", nil, err
	}
	key, repo := idx.match(cwd, filepath.Base(cwd))
	return key, repo, nil
}
