package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type CheckItem struct {
	Key        string  `json:"key"`
	Category   string  `json:"category"`
	Severity   string  `json:"severity"`
	Title      string  `json:"title"`
	Detail     string  `json:"detail"`
	Action     string  `json:"action"`
	Link       *string `json:"link"`
	ProjectKey *string `json:"project_key"`
	Done       bool    `json:"done"`
	TaskID     *int64  `json:"task_id"`
	TaskRef    *string `json:"task_ref"`
}

type CheckCounts struct {
	OK   int `json:"ok"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

type CheckupSummary struct {
	ID           int64       `json:"id"`
	Date         string      `json:"date"`
	Trigger      string      `json:"trigger"`
	StartedAt    time.Time   `json:"started_at"`
	FinishedAt   *time.Time  `json:"finished_at"`
	Status       string      `json:"status"`
	Counts       CheckCounts `json:"counts"`
	ActionsTotal int         `json:"actions_total"`
	ActionsDone  int         `json:"actions_done"`
	Emailed      bool        `json:"emailed"`
}

type Checkup struct {
	CheckupSummary
	Items []CheckItem `json:"items"`
}

func summarize(c *Checkup) {
	c.Counts = CheckCounts{}
	c.ActionsTotal, c.ActionsDone = 0, 0
	for _, it := range c.Items {
		switch it.Severity {
		case "fail":
			c.Counts.Fail++
		case "warn":
			c.Counts.Warn++
		default:
			c.Counts.OK++
		}
		if it.Severity != "ok" {
			c.ActionsTotal++
			if it.Done {
				c.ActionsDone++
			}
		}
	}
}

const checkupCols = `id, date, trigger, started_at, finished_at, status, items, emailed`

func scanCheckup(row interface{ Scan(...any) error }) (*Checkup, error) {
	var c Checkup
	var items []byte
	if err := row.Scan(&c.ID, &c.Date, &c.Trigger, &c.StartedAt, &c.FinishedAt, &c.Status, &items, &c.Emailed); err != nil {
		return nil, mapErr(err)
	}
	c.Items = []CheckItem{}
	_ = json.Unmarshal(items, &c.Items)
	summarize(&c)
	return &c, nil
}

func (s *Store) CheckupExistsFor(ctx context.Context, date string) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(ctx, `SELECT exists(SELECT 1 FROM checkups WHERE date = $1 AND trigger = 'schedule')`, date).Scan(&ok)
	return ok, err
}

func (s *Store) StartCheckup(ctx context.Context, date, trigger string) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `INSERT INTO checkups (date, trigger) VALUES ($1, $2) RETURNING id`, date, trigger).Scan(&id)
	return id, err
}

func (s *Store) FinishCheckup(ctx context.Context, id int64, status string, items []CheckItem) (*Checkup, error) {
	if items == nil {
		items = []CheckItem{}
	}
	return scanCheckup(s.DB.QueryRow(ctx, `UPDATE checkups SET status = $2, items = $3, finished_at = now()
		WHERE id = $1 RETURNING `+checkupCols, id, status, items))
}

func (s *Store) MarkCheckupEmailed(ctx context.Context, id int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE checkups SET emailed = true WHERE id = $1`, id)
	return err
}

func (s *Store) CheckupByID(ctx context.Context, id int64) (*Checkup, error) {
	return scanCheckup(s.DB.QueryRow(ctx, `SELECT `+checkupCols+` FROM checkups WHERE id = $1`, id))
}

// LatestCheckup is the newest finished one (a run in flight is not "latest").
func (s *Store) LatestCheckup(ctx context.Context) (*Checkup, error) {
	return scanCheckup(s.DB.QueryRow(ctx, `SELECT `+checkupCols+` FROM checkups
		WHERE finished_at IS NOT NULL ORDER BY started_at DESC, id DESC LIMIT 1`))
}

func (s *Store) ListCheckups(ctx context.Context, limit int) ([]CheckupSummary, error) {
	if limit <= 0 || limit > 365 {
		limit = 30
	}
	rows, err := s.DB.Query(ctx, `SELECT `+checkupCols+` FROM checkups WHERE finished_at IS NOT NULL
		ORDER BY started_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CheckupSummary{}
	for rows.Next() {
		c, err := scanCheckup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c.CheckupSummary)
	}
	return out, rows.Err()
}

// UpdateCheckupItem edits one item in place under a row lock, so two quick
// ticks in a row cannot overwrite each other.
func (s *Store) UpdateCheckupItem(ctx context.Context, id int64, key string, fn func(*CheckItem) error) (*Checkup, error) {
	err := s.tx(ctx, func(tx pgxTx) error {
		c, err := scanCheckup(tx.QueryRow(ctx, `SELECT `+checkupCols+` FROM checkups WHERE id = $1`, id))
		if err != nil {
			return err
		}
		found := false
		for i := range c.Items {
			if c.Items[i].Key == key {
				if err := fn(&c.Items[i]); err != nil {
					return err
				}
				found = true
				break
			}
		}
		if !found {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `UPDATE checkups SET items = $2 WHERE id = $1`, id, c.Items)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.CheckupByID(ctx, id)
}

// ── Inputs the check-up reads ─────────────────────────────────────────────

// RepoWithProject is a repo plus the key of the project it belongs to.
type RepoWithProject struct {
	Repo
	ProjectKey    string
	ProjectStatus string
}

func (s *Store) AllRepos(ctx context.Context) ([]RepoWithProject, error) {
	rows, err := s.DB.Query(ctx, `SELECT r.id, r.project_id, r.name, r.path, r.remote_url, r.default_branch, r.kind,
		r.deploy, r.notes, r.sort_order, r.git, p.key, p.status
		FROM repos r JOIN projects p ON p.id = r.project_id ORDER BY p.priority, p.key, r.sort_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RepoWithProject{}
	for rows.Next() {
		var r RepoWithProject
		var git []byte
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Name, &r.Path, &r.RemoteURL, &r.DefaultBranch, &r.Kind,
			&r.Deploy, &r.Notes, &r.SortOrder, &git, &r.ProjectKey, &r.ProjectStatus); err != nil {
			return nil, err
		}
		if len(git) > 0 && string(git) != "null" {
			r.Git = &GitStatus{}
			if json.Unmarshal(git, r.Git) != nil {
				r.Git = nil
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StaleBlocked lists tasks blocked for more than `days` days.
func (s *Store) StaleBlocked(ctx context.Context, days int) ([]Task, error) {
	return s.queryTasks(ctx, taskSelect+fmt.Sprintf(` WHERE t.status = 'blocked' AND t.updated_at < ts_add(now(), -%d * 86400)
		AND p.status <> 'archived' ORDER BY t.updated_at`, days))
}

func (s *Store) DueOn(ctx context.Context, date string) ([]Task, error) {
	return s.queryTasks(ctx, taskSelect+` WHERE t.status <> 'done' AND t.due_date = $1 AND p.status <> 'archived'
		ORDER BY `+priorityOrder, date)
}

func (s *Store) OverdueTasks(ctx context.Context, today string) ([]Task, error) {
	return s.queryTasks(ctx, taskSelect+` WHERE t.status <> 'done' AND t.due_date < $1 AND p.status <> 'archived'
		ORDER BY t.due_date, `+priorityOrder, today)
}

// ServersForMonitoring returns name/ip/tags for the monitoring client.
func (s *Store) ServersForMonitoring(ctx context.Context) ([]Server, error) {
	return s.ListServers(ctx)
}

// FirstUser is the account (Forge has one) — for the scheduler's timezone.
func (s *Store) FirstUser(ctx context.Context) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users ORDER BY id LIMIT 1`))
}
