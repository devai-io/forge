package store

import (
	"context"
	"fmt"
	"time"
)

// DriverState is what the work driver (api/driver.go) knows about one task in
// progress: its nudges today, the last one, how many nudges in a row changed
// nothing, and what it sees now.
type DriverState struct {
	TaskID    int64      `json:"task_id"`
	Day       string     `json:"-"`
	Nudges    int        `json:"nudges_today"`
	LastAt    *time.Time `json:"last_nudge_at"`
	Idle      int        `json:"idle"`
	Snapshot  string     `json:"-"`
	State     string     `json:"state"`
	Note      string     `json:"note"`
	RunID     *int64     `json:"run_id"`
	CheckedAt *time.Time `json:"checked_at"`
}

// DriverStates is every task's driver state, by task id.
func (s *Store) DriverStates(ctx context.Context) (map[int64]DriverState, error) {
	rows, err := s.DB.Query(ctx, `SELECT task_id, day, nudges, last_at, idle, snapshot, state, note, run_id, checked_at FROM task_driver`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]DriverState{}
	for rows.Next() {
		var d DriverState
		if err := rows.Scan(&d.TaskID, &d.Day, &d.Nudges, &d.LastAt, &d.Idle, &d.Snapshot, &d.State, &d.Note, &d.RunID, &d.CheckedAt); err != nil {
			return nil, err
		}
		out[d.TaskID] = d
	}
	return out, rows.Err()
}

// PutDriverState records a task's driver state.
func (s *Store) PutDriverState(ctx context.Context, d DriverState) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO task_driver (task_id, day, nudges, last_at, idle, snapshot, state, note, run_id, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		ON CONFLICT (task_id) DO UPDATE SET day = excluded.day, nudges = excluded.nudges, last_at = excluded.last_at,
			idle = excluded.idle, snapshot = excluded.snapshot, state = excluded.state, note = excluded.note,
			run_id = excluded.run_id, checked_at = excluded.checked_at`,
		d.TaskID, d.Day, d.Nudges, d.LastAt, d.Idle, d.Snapshot, d.State, d.Note, d.RunID)
	return mapErr(err)
}

// DriverTasks are the tasks the driver keeps moving: in progress, in a
// project that is neither paused nor archived.
func (s *Store) DriverTasks(ctx context.Context) ([]Task, error) {
	tasks, err := s.ListTasks(ctx, TaskFilter{Statuses: []string{"in_progress"}, Limit: 200})
	if err != nil {
		return nil, err
	}
	out := tasks[:0]
	for _, t := range tasks {
		var status string
		if err := s.DB.QueryRow(ctx, `SELECT status FROM projects WHERE id = $1`, t.ProjectID).Scan(&status); err != nil {
			continue
		}
		if status != "paused" && status != "archived" {
			out = append(out, t)
		}
	}
	return out, nil
}

// TaskSnapshot is what changes when work happens on a task: its status, its
// last update and its comments.
func (s *Store) TaskSnapshot(ctx context.Context, taskID int64) string {
	var status, updated string
	var comments int
	if err := s.DB.QueryRow(ctx, `SELECT t.status, t.updated_at, (SELECT count(*) FROM comments c WHERE c.task_id = t.id)
		FROM tasks t WHERE t.id = $1`, taskID).Scan(&status, &updated, &comments); err != nil {
		return ""
	}
	return fmt.Sprintf("%s|%s|%d", status, updated, comments)
}

// RunLastActivity is when the run last did anything: its newest event, else
// when it started or was created.
func (s *Store) RunLastActivity(ctx context.Context, run *Run) time.Time {
	var at *time.Time
	_ = s.DB.QueryRow(ctx, `SELECT max(at) FROM run_events WHERE run_id = $1`, run.ID).Scan(&at)
	switch {
	case at != nil:
		return *at
	case run.StartedAt != nil:
		return *run.StartedAt
	}
	return run.CreatedAt
}

// FirstOwner is the account the driver acts as.
func (s *Store) FirstOwner(ctx context.Context) (*User, error) {
	var id int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM users WHERE role = 'owner' ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		return nil, mapErr(err)
	}
	return s.UserByID(ctx, id)
}
