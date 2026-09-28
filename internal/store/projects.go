package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// projectSelect returns every project with its stats in one pass. $1 is the
// viewer's "today" (YYYY-MM-DD in their timezone), which decides "overdue".
const projectSelect = `
WITH ts AS (
	SELECT project_id,
	       count(*)                                                        AS total,
	       count(*) FILTER (WHERE status = 'backlog')                      AS backlog,
	       count(*) FILTER (WHERE status = 'todo')                         AS todo,
	       count(*) FILTER (WHERE status = 'in_progress')                  AS in_progress,
	       count(*) FILTER (WHERE status = 'blocked')                      AS blocked,
	       count(*) FILTER (WHERE status = 'done')                         AS done,
	       count(*) FILTER (WHERE status <> 'done' AND due_date < $1) AS overdue,
	       count(*) FILTER (WHERE completed_at >= ts_add(now(), -604800)) AS done_7d,
	       count(*) FILTER (WHERE created_at >= ts_add(now(), -604800))  AS created_7d
	FROM tasks GROUP BY project_id
), rs AS (
	SELECT project_id,
	       coalesce(sum(CAST(git->>'commits_7d' AS INTEGER)), 0)          AS commits_7d,
	       count(*) FILTER (WHERE CAST(git->>'dirty' AS INTEGER) > 0)      AS dirty,
	       max(ts_norm(git->>'last_commit_at'))                            AS last_commit
	FROM repos GROUP BY project_id
), es AS (
	SELECT project_id,
	       count(*) FILTER (WHERE enabled)                                 AS total,
	       count(*) FILTER (WHERE enabled AND last_status = 'up')          AS up,
	       count(*) FILTER (WHERE enabled AND last_status = 'down')        AS down
	FROM endpoints GROUP BY project_id
), ru AS (
	SELECT project_id, count(*) AS active
	FROM runs WHERE status IN ('queued', 'running') GROUP BY project_id
), ac AS (
	SELECT project_id, max(created_at) AS last FROM activity
	WHERE kind NOT LIKE 'endpoint.%' GROUP BY project_id
)
SELECT p.id, p.key, p.name, p.category, p.status, p.priority, p.color, p.summary, p.description,
       p.infra_notes, p.target_date, p.links, p.created_at, p.updated_at,
       coalesce(ts.total, 0), coalesce(ts.backlog, 0), coalesce(ts.todo, 0), coalesce(ts.in_progress, 0),
       coalesce(ts.blocked, 0), coalesce(ts.done, 0), coalesce(ts.overdue, 0), coalesce(ts.done_7d, 0),
       coalesce(ts.created_7d, 0), coalesce(rs.commits_7d, 0), coalesce(rs.dirty, 0),
       coalesce(es.total, 0), coalesce(es.up, 0), coalesce(es.down, 0), coalesce(ru.active, 0),
       CASE WHEN ac.last IS NULL THEN rs.last_commit WHEN rs.last_commit IS NULL THEN ac.last
            ELSE max(ac.last, rs.last_commit) END
FROM projects p
LEFT JOIN ts ON ts.project_id = p.id
LEFT JOIN rs ON rs.project_id = p.id
LEFT JOIN es ON es.project_id = p.id
LEFT JOIN ru ON ru.project_id = p.id
LEFT JOIN ac ON ac.project_id = p.id
`

func scanProject(row interface{ Scan(...any) error }) (*Project, error) {
	var p Project
	var links []byte
	st := &p.Stats
	if err := row.Scan(&p.ID, &p.Key, &p.Name, &p.Category, &p.Status, &p.Priority, &p.Color, &p.Summary,
		&p.Description, &p.InfraNotes, &p.TargetDate, &links, &p.CreatedAt, &p.UpdatedAt,
		&st.Total, &st.Backlog, &st.Todo, &st.InProgress, &st.Blocked, &st.Done, &st.Overdue, &st.Done7d,
		&st.Created7d, &st.Commits7d, &st.DirtyRepos, &st.EndpointsTotal, &st.EndpointsUp, &st.EndpointsDown,
		&st.ActiveRuns, &st.LastActivityAt); err != nil {
		return nil, mapErr(err)
	}
	p.Links = []Link{}
	_ = json.Unmarshal(links, &p.Links)
	if st.Total > 0 {
		st.Progress = float64(st.Done) / float64(st.Total)
	}
	return &p, nil
}

func (s *Store) ListProjects(ctx context.Context, today string, includeArchived bool) ([]Project, error) {
	q := projectSelect
	if !includeArchived {
		q += ` WHERE p.status <> 'archived'`
	}
	q += ` ORDER BY p.priority, lower(p.name)`
	rows, err := s.DB.Query(ctx, q, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) ProjectByKey(ctx context.Context, today, key string) (*ProjectDetail, error) {
	p, err := scanProject(s.DB.QueryRow(ctx, projectSelect+` WHERE p.key = $2`, today, strings.ToUpper(key)))
	if err != nil {
		return nil, err
	}
	d := &ProjectDetail{Project: *p}
	if d.Repos, err = s.ListRepos(ctx, p.ID); err != nil {
		return nil, err
	}
	if d.Servers, err = s.projectServers(ctx, p.ID); err != nil {
		return nil, err
	}
	if d.Endpoints, err = s.ListEndpoints(ctx, p.ID); err != nil {
		return nil, err
	}
	return d, nil
}

// ProjectIDByKey resolves a project key (any case).
func (s *Store) ProjectIDByKey(ctx context.Context, key string) (int64, error) {
	return s.projectID(ctx, s.DB, key)
}

func (s *Store) projectID(ctx context.Context, q querier, key string) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `SELECT id FROM projects WHERE key = $1`, strings.ToUpper(key)).Scan(&id)
	return id, mapErr(err)
}

type ProjectInput struct {
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	Status      string  `json:"status"`
	Priority    int     `json:"priority"`
	Color       string  `json:"color"`
	Summary     string  `json:"summary"`
	Description string  `json:"description"`
	InfraNotes  string  `json:"infra_notes"`
	TargetDate  *string `json:"target_date"`
	Links       []Link  `json:"links"`
}

func (in *ProjectInput) normalize() error {
	in.Key = strings.ToUpper(strings.TrimSpace(in.Key))
	in.Name = strings.TrimSpace(in.Name)
	if !IsProjectKey(in.Key) {
		return invalid("key", "2-10 uppercase letters or digits, starting with a letter")
	}
	if in.Name == "" || len(in.Name) > 100 {
		return invalid("name", "must be 1-100 characters")
	}
	if in.Category == "" {
		in.Category = "personal"
	}
	if !OneOf(in.Category, Categories) {
		return invalid("category", "must be work or personal")
	}
	if in.Status == "" {
		in.Status = "building"
	}
	if !OneOf(in.Status, ProjectStatuses) {
		return invalid("status", "must be one of %s", strings.Join(ProjectStatuses, ", "))
	}
	if in.Priority == 0 {
		in.Priority = 3
	}
	if in.Priority < 1 || in.Priority > 5 {
		return invalid("priority", "must be between 1 and 5")
	}
	if in.Color == "" {
		in.Color = "#6366f1"
	}
	if !IsColor(in.Color) {
		return invalid("color", "must be a #rrggbb colour")
	}
	in.Color = strings.ToLower(in.Color)
	if in.TargetDate != nil && !IsDate(*in.TargetDate) {
		return invalid("target_date", "must be a YYYY-MM-DD date")
	}
	links, err := CleanLinks(in.Links)
	if err != nil {
		return err
	}
	in.Links = links
	return nil
}

func (s *Store) CreateProject(ctx context.Context, in ProjectInput) (string, error) {
	if err := in.normalize(); err != nil {
		return "", err
	}
	return in.Key, s.tx(ctx, func(tx pgxTx) error {
		var id int64
		err := tx.QueryRow(ctx, `
			INSERT INTO projects (key, name, category, status, priority, color, summary, description,
			                      infra_notes, target_date, links)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id`,
			in.Key, in.Name, in.Category, in.Status, in.Priority, in.Color, in.Summary, in.Description,
			in.InfraNotes, in.TargetDate, in.Links).Scan(&id)
		if err != nil {
			return mapErr(err)
		}
		return logActivity(ctx, tx, ActivityInput{ProjectID: &id, Kind: "project.created",
			Summary: fmt.Sprintf("Created project %s", in.Name)})
	})
}

var projectFields = map[string]field{
	"name":        {"name", pString(100, true)},
	"category":    {"category", pEnum(Categories)},
	"status":      {"status", pEnum(ProjectStatuses)},
	"priority":    {"priority", pInt(1, 5)},
	"color":       {"color", pColor},
	"summary":     {"summary", pString(300, false)},
	"description": {"description", pString(50000, false)},
	"infra_notes": {"infra_notes", pString(50000, false)},
	"target_date": {"target_date", pNullableDate},
	"links":       {"links", pLinks},
}

var projectReadOnly = []string{"id", "key", "created_at", "updated_at", "stats", "repos", "servers", "endpoints"}

func (s *Store) UpdateProject(ctx context.Context, key string, p Patch) error {
	set, args, err := updateSet(p, projectFields, projectReadOnly, 2)
	if err != nil || set == "" {
		return err
	}
	return s.tx(ctx, func(tx pgxTx) error {
		var id int64
		var name, status string
		err := tx.QueryRow(ctx, `UPDATE projects SET `+set+`, updated_at = now() WHERE key = $1
			RETURNING id, name, status`, append([]any{strings.ToUpper(key)}, args...)...).Scan(&id, &name, &status)
		if err != nil {
			return mapErr(err)
		}
		summary := fmt.Sprintf("Updated %s", name)
		if p.Has("status") {
			summary = fmt.Sprintf("%s is now %s", name, status)
		}
		return logActivity(ctx, tx, ActivityInput{ProjectID: &id, Kind: "project.updated", Summary: summary})
	})
}

func (s *Store) DeleteProject(ctx context.Context, key string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM projects WHERE key = $1`, strings.ToUpper(key))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Repos ─────────────────────────────────────────────────────────────────

const repoCols = `id, project_id, name, path, remote_url, default_branch, kind, deploy, notes, sort_order, git`

func scanRepo(row interface{ Scan(...any) error }) (*Repo, error) {
	var r Repo
	var git []byte
	if err := row.Scan(&r.ID, &r.ProjectID, &r.Name, &r.Path, &r.RemoteURL, &r.DefaultBranch, &r.Kind,
		&r.Deploy, &r.Notes, &r.SortOrder, &git); err != nil {
		return nil, mapErr(err)
	}
	if len(git) > 0 && string(git) != "null" {
		r.Git = &GitStatus{}
		if err := json.Unmarshal(git, r.Git); err != nil {
			r.Git = nil
		}
	}
	return &r, nil
}

func (s *Store) ListRepos(ctx context.Context, projectID int64) ([]Repo, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+repoCols+` FROM repos WHERE project_id = $1
		ORDER BY sort_order, lower(name)`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Repo{}
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) RepoByID(ctx context.Context, id int64) (*Repo, error) {
	return scanRepo(s.DB.QueryRow(ctx, `SELECT `+repoCols+` FROM repos WHERE id = $1`, id))
}

type RepoInput struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	RemoteURL     string `json:"remote_url"`
	DefaultBranch string `json:"default_branch"`
	Kind          string `json:"kind"`
	Deploy        string `json:"deploy"`
	Notes         string `json:"notes"`
}

func (s *Store) CreateRepo(ctx context.Context, projectKey string, in RepoInput) (*Repo, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Path = strings.TrimSpace(in.Path)
	if in.Name == "" || len(in.Name) > 100 {
		return nil, invalid("name", "must be 1-100 characters")
	}
	if in.Path != "" && !strings.HasPrefix(in.Path, "/") {
		return nil, invalid("path", "must be an absolute path")
	}
	if in.DefaultBranch == "" {
		in.DefaultBranch = "main"
	}
	if in.Kind == "" {
		in.Kind = "other"
	}
	if !OneOf(in.Kind, RepoKinds) {
		return nil, invalid("kind", "must be one of %s", strings.Join(RepoKinds, ", "))
	}
	pid, err := s.projectID(ctx, s.DB, projectKey)
	if err != nil {
		return nil, err
	}
	return scanRepo(s.DB.QueryRow(ctx, `
		INSERT INTO repos (project_id, name, path, remote_url, default_branch, kind, deploy, notes, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
		        (SELECT coalesce(max(sort_order), 0) + 1 FROM repos WHERE project_id = $1))
		RETURNING `+repoCols,
		pid, in.Name, in.Path, in.RemoteURL, in.DefaultBranch, in.Kind, in.Deploy, in.Notes))
}

func pAbsPath(key string, raw json.RawMessage) (any, error) {
	v, err := pString(500, false)(key, raw)
	if err != nil {
		return nil, err
	}
	if p := v.(string); p != "" && !strings.HasPrefix(p, "/") {
		return nil, invalid(key, "must be an absolute path")
	}
	return v, nil
}

var repoFields = map[string]field{
	"name":           {"name", pString(100, true)},
	"path":           {"path", pAbsPath},
	"remote_url":     {"remote_url", pString(500, false)},
	"default_branch": {"default_branch", pString(100, true)},
	"kind":           {"kind", pEnum(RepoKinds)},
	"deploy":         {"deploy", pString(500, false)},
	"notes":          {"notes", pString(10000, false)},
	"sort_order":     {"sort_order", pInt(-100000, 100000)},
}

func (s *Store) UpdateRepo(ctx context.Context, id int64, p Patch) (*Repo, error) {
	set, args, err := updateSet(p, repoFields, []string{"id", "project_id", "git"}, 2)
	if err != nil {
		return nil, err
	}
	if set == "" {
		return s.RepoByID(ctx, id)
	}
	return scanRepo(s.DB.QueryRow(ctx, `UPDATE repos SET `+set+`, updated_at = now() WHERE id = $1
		RETURNING `+repoCols, append([]any{id}, args...)...))
}

func (s *Store) DeleteRepo(ctx context.Context, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM repos WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RunnerRepo is what a runner is told to scan and may run in.
type RunnerRepo struct {
	ID            int64  `json:"id"`
	ProjectKey    string `json:"project_key"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	DefaultBranch string `json:"default_branch"`
}

func (s *Store) RunnerRepos(ctx context.Context) ([]RunnerRepo, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT r.id, p.key, r.name, r.path, r.default_branch
		FROM repos r JOIN projects p ON p.id = r.project_id
		WHERE r.path <> '' AND p.status <> 'archived'
		ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunnerRepo{}
	for rows.Next() {
		var r RunnerRepo
		if err := rows.Scan(&r.ID, &r.ProjectKey, &r.Name, &r.Path, &r.DefaultBranch); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RepoScan is one repo's git state as a runner saw it.
type RepoScan struct {
	RepoID int64 `json:"repo_id"`
	GitStatus
}

// SaveRepoScans stores a runner's view of its repos — only the master's, when
// one is elected: two machines with the same checkout in different states
// would otherwise overwrite each other every few minutes.
func (s *Store) SaveRepoScans(ctx context.Context, runnerName string, scans []RepoScan) error {
	var accept bool
	if err := s.DB.QueryRow(ctx, `SELECT NOT exists(SELECT 1 FROM runners WHERE role = 'master')
		OR exists(SELECT 1 FROM runners WHERE role = 'master' AND name = $1)`, runnerName).Scan(&accept); err != nil {
		return err
	}
	if !accept {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, sc := range scans {
		g := sc.GitStatus
		g.RunnerName = runnerName
		g.ScannedAt = now
		if g.LastCommitAt != nil && *g.LastCommitAt == "" {
			g.LastCommitAt = nil
		}
		if _, err := s.DB.Exec(ctx, `UPDATE repos SET git = $2 WHERE id = $1`, sc.RepoID, g); err != nil {
			return err
		}
	}
	return nil
}

// ── Project ↔ server links ────────────────────────────────────────────────

func (s *Store) projectServers(ctx context.Context, projectID int64) ([]ProjectServer, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT sv.id, sv.name, ps.role, sv.environment, sv.critical
		FROM project_servers ps JOIN servers sv ON sv.id = ps.server_id
		WHERE ps.project_id = $1 ORDER BY sv.critical DESC, sv.name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectServer{}
	for rows.Next() {
		var x ProjectServer
		if err := rows.Scan(&x.ServerID, &x.Name, &x.Role, &x.Environment, &x.Critical); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type ProjectServerInput struct {
	ServerID int64  `json:"server_id"`
	Role     string `json:"role"`
}

func (s *Store) SetProjectServers(ctx context.Context, key string, links []ProjectServerInput) ([]ProjectServer, error) {
	var pid int64
	err := s.tx(ctx, func(tx pgxTx) error {
		var err error
		if pid, err = s.projectID(ctx, tx, key); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM project_servers WHERE project_id = $1`, pid); err != nil {
			return err
		}
		for _, l := range links {
			if _, err := tx.Exec(ctx, `INSERT INTO project_servers (project_id, server_id, role) VALUES ($1, $2, $3)
				ON CONFLICT (project_id, server_id) DO UPDATE SET role = excluded.role`,
				pid, l.ServerID, strings.TrimSpace(l.Role)); err != nil {
				return mapErr(err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.projectServers(ctx, pid)
}
