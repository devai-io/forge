package store

import "context"

type ActivityInput struct {
	ProjectID *int64
	TaskID    *int64
	TaskRef   *string
	RunID     *int64
	Kind      string
	Summary   string
}

func logActivity(ctx context.Context, q querier, a ActivityInput) error {
	_, err := q.Exec(ctx, `INSERT INTO activity (project_id, task_id, task_ref, run_id, kind, summary)
		VALUES ($1, $2, $3, $4, $5, $6)`, a.ProjectID, a.TaskID, a.TaskRef, a.RunID, a.Kind, truncate(a.Summary, 500))
	return err
}

func (s *Store) LogActivity(ctx context.Context, a ActivityInput) error {
	return logActivity(ctx, s.DB, a)
}

// ListActivity returns the newest entries, for one project or (key "") all.
func (s *Store) ListActivity(ctx context.Context, projectKey string, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT a.id, a.project_id, p.key, p.color, a.task_id, a.task_ref, a.run_id, a.kind, a.summary, a.created_at
		FROM activity a LEFT JOIN projects p ON p.id = a.project_id`
	args := []any{limit}
	if projectKey != "" {
		q += ` WHERE p.key = $2`
		args = append(args, projectKey)
	}
	q += ` ORDER BY a.created_at DESC, a.id DESC LIMIT $1`
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.ProjectKey, &a.ProjectColor, &a.TaskID, &a.TaskRef, &a.RunID,
			&a.Kind, &a.Summary, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
