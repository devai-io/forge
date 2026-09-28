package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const taskSelect = `
SELECT t.id, t.project_id, p.key, p.name, p.color, t.number, t.title, t.description, t.status, t.priority,
       t.type, t.labels, to_char(t.due_date, 'YYYY-MM-DD'), t.focus, t.repo_id, r.name, t.estimate,
       t.sort_order, t.completed_at, t.created_at, t.updated_at,
       (SELECT count(*) FROM comments c WHERE c.task_id = t.id)
FROM tasks t
JOIN projects p ON p.id = t.project_id
LEFT JOIN repos r ON r.id = t.repo_id`

// statusOrder is the kanban column order, used to sort flat lists too.
const statusOrder = `array_position(ARRAY['backlog','todo','in_progress','blocked','done'], t.status)`

const priorityOrder = `array_position(ARRAY['urgent','high','medium','low'], t.priority)`

func scanTask(row interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	if err := row.Scan(&t.ID, &t.ProjectID, &t.ProjectKey, &t.ProjectName, &t.ProjectColor, &t.Number, &t.Title,
		&t.Description, &t.Status, &t.Priority, &t.Type, &t.Labels, &t.DueDate, &t.Focus, &t.RepoID, &t.RepoName,
		&t.Estimate, &t.SortOrder, &t.CompletedAt, &t.CreatedAt, &t.UpdatedAt, &t.CommentCount); err != nil {
		return nil, mapErr(err)
	}
	t.Ref = fmt.Sprintf("%s-%d", t.ProjectKey, t.Number)
	t.Labels = nonNil(t.Labels)
	return &t, nil
}

func (s *Store) queryTasks(ctx context.Context, q string, args ...any) ([]Task, error) {
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

type TaskFilter struct {
	ProjectKey string
	Statuses   []string
	Focus      bool
	Query      string
	Priority   string
	Type       string
	Label      string
	Overdue    bool
	Open       bool
	Today      string // viewer's date, for Overdue
	Limit      int
}

func (s *Store) ListTasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	var where []string
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }

	if f.ProjectKey != "" {
		where = append(where, "p.key = "+arg(strings.ToUpper(f.ProjectKey)))
	}
	if len(f.Statuses) > 0 {
		where = append(where, "t.status = ANY("+arg(f.Statuses)+")")
	}
	if f.Focus {
		where = append(where, "t.focus")
	}
	if f.Open {
		where = append(where, "t.status <> 'done'")
	}
	if f.Priority != "" {
		where = append(where, "t.priority = "+arg(f.Priority))
	}
	if f.Type != "" {
		where = append(where, "t.type = "+arg(f.Type))
	}
	if f.Label != "" {
		where = append(where, arg(f.Label)+" = ANY(t.labels)")
	}
	if f.Overdue {
		where = append(where, "t.status <> 'done' AND t.due_date < "+arg(f.Today)+"::date")
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
		n := arg(like)
		where = append(where, fmt.Sprintf("(t.title ILIKE %s OR p.key || '-' || t.number ILIKE %s OR t.description ILIKE %s)", n, n, n))
	}
	q := taskSelect
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	if f.Limit <= 0 || f.Limit > 2000 {
		f.Limit = 500
	}
	q += fmt.Sprintf(" ORDER BY %s, t.sort_order, t.id LIMIT %d", statusOrder, f.Limit)
	return s.queryTasks(ctx, q, args...)
}

func (s *Store) TaskByID(ctx context.Context, id int64) (*Task, error) {
	return scanTask(s.DB.QueryRow(ctx, taskSelect+` WHERE t.id = $1`, id))
}

func (s *Store) TaskDetail(ctx context.Context, id int64) (*TaskDetail, error) {
	t, err := s.TaskByID(ctx, id)
	if err != nil {
		return nil, err
	}
	d := &TaskDetail{Task: *t}
	if d.Comments, err = s.listComments(ctx, id); err != nil {
		return nil, err
	}
	if d.Runs, err = s.ListRuns(ctx, RunFilter{TaskID: id, Limit: 50}); err != nil {
		return nil, err
	}
	return d, nil
}

type TaskInput struct {
	ProjectKey  string   `json:"project_key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Priority    string   `json:"priority"`
	Type        string   `json:"type"`
	Labels      []string `json:"labels"`
	DueDate     *string  `json:"due_date"`
	Focus       bool     `json:"focus"`
	RepoID      *int64   `json:"repo_id"`
	Estimate    *int     `json:"estimate"`

	// Seed-only: backdates completion so imported history does not count as
	// work done today.
	CompletedAt *string `json:"-"`
}

func (in *TaskInput) normalize() error {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len(in.Title) > 300 {
		return invalid("title", "must be 1-300 characters")
	}
	if in.Status == "" {
		in.Status = "todo"
	}
	if !OneOf(in.Status, TaskStatuses) {
		return invalid("status", "must be one of %s", strings.Join(TaskStatuses, ", "))
	}
	if in.Priority == "" {
		in.Priority = "medium"
	}
	if !OneOf(in.Priority, Priorities) {
		return invalid("priority", "must be one of %s", strings.Join(Priorities, ", "))
	}
	if in.Type == "" {
		in.Type = "feature"
	}
	if !OneOf(in.Type, TaskTypes) {
		return invalid("type", "must be one of %s", strings.Join(TaskTypes, ", "))
	}
	if in.DueDate != nil && !IsDate(*in.DueDate) {
		return invalid("due_date", "must be a YYYY-MM-DD date")
	}
	if in.Estimate != nil && (*in.Estimate < 0 || *in.Estimate > 1000) {
		return invalid("estimate", "must be between 0 and 1000")
	}
	labels, err := CleanStrings("labels", in.Labels, 20, 40)
	if err != nil {
		return err
	}
	in.Labels = labels
	return nil
}

// CreateTask takes the next number from the project row (locking it, so two
// creates cannot both get ALPHA-7) and appends the task to the bottom of its
// column.
func (s *Store) CreateTask(ctx context.Context, in TaskInput) (*Task, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	var id int64
	err := s.tx(ctx, func(tx pgxTx) error {
		var pid int64
		var number int
		var key string
		err := tx.QueryRow(ctx, `UPDATE projects SET next_task_number = next_task_number + 1
			WHERE key = $1 RETURNING id, next_task_number - 1, key`,
			strings.ToUpper(strings.TrimSpace(in.ProjectKey))).Scan(&pid, &number, &key)
		if err != nil {
			if mapErr(err) == ErrNotFound {
				return invalid("project_key", "no such project")
			}
			return err
		}
		if err := checkRepo(ctx, tx, in.RepoID, pid); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO tasks (project_id, number, title, description, status, priority, type, labels, due_date,
			                   focus, repo_id, estimate, sort_order, completed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			        (SELECT coalesce(max(sort_order), 0) + 1000 FROM tasks WHERE project_id = $1 AND status = $5),
			        CASE WHEN $5 = 'done' THEN coalesce($13::timestamptz, now()) END)
			RETURNING id`,
			pid, number, in.Title, in.Description, in.Status, in.Priority, in.Type, in.Labels, in.DueDate,
			in.Focus, in.RepoID, in.Estimate, in.CompletedAt).Scan(&id)
		if err != nil {
			return mapErr(err)
		}
		ref := fmt.Sprintf("%s-%d", key, number)
		return logActivity(ctx, tx, ActivityInput{ProjectID: &pid, TaskID: &id, TaskRef: &ref,
			Kind: "task.created", Summary: fmt.Sprintf("Created %s %s", ref, in.Title)})
	})
	if err != nil {
		return nil, err
	}
	return s.TaskByID(ctx, id)
}

// checkRepo refuses a repo that belongs to another project.
func checkRepo(ctx context.Context, q querier, repoID *int64, projectID int64) error {
	if repoID == nil {
		return nil
	}
	var ok bool
	if err := q.QueryRow(ctx, `SELECT exists(SELECT 1 FROM repos WHERE id = $1 AND project_id = $2)`,
		*repoID, projectID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return invalid("repo_id", "not a repo of this project")
	}
	return nil
}

var taskFields = map[string]field{
	"title":       {"title", pString(300, true)},
	"description": {"description", pString(100000, false)},
	"status":      {"status", pEnum(TaskStatuses)},
	"priority":    {"priority", pEnum(Priorities)},
	"type":        {"type", pEnum(TaskTypes)},
	"labels":      {"labels", pStrings(20, 40)},
	"due_date":    {"due_date", pNullableDate},
	"focus":       {"focus", pBool},
	"repo_id":     {"repo_id", pNullableID},
	"estimate":    {"estimate", pNullableInt(0, 1000)},
	"sort_order":  {"sort_order", pFloat},
}

var taskReadOnly = []string{"id", "project_id", "project_name", "project_color", "number", "ref",
	"repo_name", "completed_at", "created_at", "updated_at", "comment_count", "comments", "runs"}

func pNullableID(key string, raw json.RawMessage) (any, error) {
	if isNull(raw) {
		return nil, nil
	}
	var v int64
	if err := json.Unmarshal(raw, &v); err != nil || v <= 0 {
		return nil, invalid(key, "must be an id or null")
	}
	return v, nil
}

// UpdateTask applies a partial update. Status changes keep completed_at
// honest (set on entering done, cleared on leaving it) and land in the feed;
// project_key moves the task, which renumbers it in the new project.
func (s *Store) UpdateTask(ctx context.Context, id int64, p Patch) (*Task, error) {
	var moveTo string
	if raw, ok := p["project_key"]; ok {
		if err := json.Unmarshal(raw, &moveTo); err != nil {
			return nil, invalid("project_key", "must be a project key")
		}
		moveTo = strings.ToUpper(strings.TrimSpace(moveTo))
	}
	rest := Patch{}
	for k, v := range p {
		if k != "project_key" {
			rest[k] = v
		}
	}
	set, args, err := updateSet(rest, taskFields, taskReadOnly, 2)
	if err != nil {
		return nil, err
	}

	err = s.tx(ctx, func(tx pgxTx) error {
		cur, err := scanTask(tx.QueryRow(ctx, taskSelect+` WHERE t.id = $1 FOR UPDATE OF t`, id))
		if err != nil {
			return err
		}
		projectID, ref := cur.ProjectID, cur.Ref

		if moveTo != "" && moveTo != cur.ProjectKey {
			var number int
			var key string
			err := tx.QueryRow(ctx, `UPDATE projects SET next_task_number = next_task_number + 1
				WHERE key = $1 RETURNING id, next_task_number - 1, key`, moveTo).Scan(&projectID, &number, &key)
			if err != nil {
				if mapErr(err) == ErrNotFound {
					return invalid("project_key", "no such project")
				}
				return err
			}
			// A repo belongs to its project; it cannot follow the task.
			if _, err := tx.Exec(ctx, `UPDATE tasks SET project_id = $2, number = $3, repo_id = NULL,
				updated_at = now() WHERE id = $1`, id, projectID, number); err != nil {
				return mapErr(err)
			}
			newRef := fmt.Sprintf("%s-%d", key, number)
			if err := logActivity(ctx, tx, ActivityInput{ProjectID: &projectID, TaskID: &id, TaskRef: &newRef,
				Kind: "task.moved", Summary: fmt.Sprintf("Moved %s to %s", ref, newRef)}); err != nil {
				return err
			}
			ref = newRef
		}

		if rest.Has("repo_id") {
			var repoID *int64
			_ = json.Unmarshal(rest["repo_id"], &repoID)
			if err := checkRepo(ctx, tx, repoID, projectID); err != nil {
				return err
			}
		}
		if set == "" {
			return nil
		}

		var status, title string
		if err := tx.QueryRow(ctx, `UPDATE tasks SET `+set+`, updated_at = now() WHERE id = $1
			RETURNING status, title`, append([]any{id}, args...)...).Scan(&status, &title); err != nil {
			return mapErr(err)
		}
		if !rest.Has("status") || status == cur.Status {
			return nil
		}

		// SET expressions see the old row, so completed_at is fixed up against
		// the new status here. A status change without an explicit position
		// lands at the bottom of its new column — or the top of done, where the
		// most recent win belongs.
		pos := `(SELECT coalesce(max(sort_order), 0) + 1000 FROM tasks WHERE project_id = $2 AND status = $3 AND id <> $1)`
		if status == "done" {
			pos = `(SELECT coalesce(min(sort_order), 0) - 1000 FROM tasks WHERE project_id = $2 AND status = $3 AND id <> $1)`
		}
		if rest.Has("sort_order") {
			pos = "sort_order"
		}
		if _, err := tx.Exec(ctx, `UPDATE tasks SET sort_order = `+pos+`,
			completed_at = CASE WHEN status = 'done' THEN coalesce(completed_at, now()) ELSE NULL END
			WHERE id = $1 AND project_id = $2 AND status = $3`, id, projectID, status); err != nil {
			return err
		}
		kind, summary := "task.status", fmt.Sprintf("%s → %s: %s", ref, strings.ReplaceAll(status, "_", " "), title)
		switch {
		case status == "done":
			kind, summary = "task.done", fmt.Sprintf("Completed %s %s", ref, title)
		case cur.Status == "done":
			kind, summary = "task.reopened", fmt.Sprintf("Reopened %s %s", ref, title)
		}
		return logActivity(ctx, tx, ActivityInput{ProjectID: &projectID, TaskID: &id, TaskRef: &ref,
			Kind: kind, Summary: summary})
	})
	if err != nil {
		return nil, err
	}
	return s.TaskByID(ctx, id)
}

func (s *Store) DeleteTask(ctx context.Context, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Comments ──────────────────────────────────────────────────────────────

func (s *Store) listComments(ctx context.Context, taskID int64) ([]Comment, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, task_id, body, created_at FROM comments
		WHERE task_id = $1 ORDER BY created_at, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateComment(ctx context.Context, taskID int64, body string) (*Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" || len(body) > 50000 {
		return nil, invalid("body", "must be 1-50000 characters")
	}
	t, err := s.TaskByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var c Comment
	err = s.tx(ctx, func(tx pgxTx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO comments (task_id, body) VALUES ($1, $2)
			RETURNING id, task_id, body, created_at`, taskID, body).Scan(&c.ID, &c.TaskID, &c.Body, &c.CreatedAt); err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE tasks SET updated_at = now() WHERE id = $1`, taskID); err != nil {
			return err
		}
		return logActivity(ctx, tx, ActivityInput{ProjectID: &t.ProjectID, TaskID: &t.ID, TaskRef: &t.Ref,
			Kind: "task.comment", Summary: fmt.Sprintf("Commented on %s: %s", t.Ref, excerpt(body, 100))})
	})
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) DeleteComment(ctx context.Context, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM comments WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}
