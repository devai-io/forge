package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	// TaskStatus is the linked task's current status ("" when there is none),
	// so a done task under a still-failing item is visible as such.
	TaskStatus string `json:"task_status"`
	// FirstSeen is the date of the first consecutive check-up that had this
	// key (carried over from the previous check-up): "new today" vs chronic.
	FirstSeen string `json:"first_seen"`
	// CheckedAt is the last re-check of this item after the run; ResolvedAt is
	// set when a re-check found it gone. A resolved item counts as done.
	CheckedAt  *time.Time `json:"checked_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	// Snoozed items are out of the action list, the counts, the e-mail and
	// the bots. SnoozedUntil is a date, or nil for "until the item changes".
	Snoozed      bool    `json:"snoozed"`
	SnoozedUntil *string `json:"snoozed_until"`
	// Fixes are what Forge can do about the item in one click (then it
	// re-checks it): "done", "week", "clear_due", "unblock", "backlog" on
	// tasks, "probe" on endpoints, a machine command ("git-pull") on repos.
	Fixes []CheckFix `json:"fixes"`
}

type CheckFix struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Open: still something to act on — not ok, not ticked, not verified gone,
// not put aside. Every consumer of the action list (focus, bots, e-mail,
// session context) goes through this.
func (it CheckItem) Open() bool {
	return it.Severity != "ok" && !it.Done && it.ResolvedAt == nil && !it.Snoozed
}

// ResolvedItem was open in the previous check-up and is gone in this one.
type ResolvedItem struct {
	Key        string    `json:"key"`
	Category   string    `json:"category"`
	Severity   string    `json:"severity"`
	Title      string    `json:"title"`
	ProjectKey *string   `json:"project_key"`
	TaskRef    *string   `json:"task_ref"`
	FirstSeen  string    `json:"first_seen"`
	SinceAt    time.Time `json:"since_at"` // start of the check-up that last had it
}

type CheckCounts struct {
	OK   int `json:"ok"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

type CheckupSummary struct {
	ID             int64       `json:"id"`
	Date           string      `json:"date"`
	Trigger        string      `json:"trigger"`
	StartedAt      time.Time   `json:"started_at"`
	FinishedAt     *time.Time  `json:"finished_at"`
	Status         string      `json:"status"`
	Counts         CheckCounts `json:"counts"`
	ActionsTotal   int         `json:"actions_total"`
	ActionsDone    int         `json:"actions_done"`
	ActionsNew     int         `json:"actions_new"`
	ActionsSnoozed int         `json:"actions_snoozed"`
	FixedTotal     int         `json:"fixed_total"`
	Emailed        bool        `json:"emailed"`
}

type Checkup struct {
	CheckupSummary
	Items    []CheckItem    `json:"items"`
	Resolved []ResolvedItem `json:"resolved"`
}

// summarize derives the counts; snoozed items are kept out of all of them
// except their own.
func summarize(c *Checkup) {
	c.Counts = CheckCounts{}
	c.ActionsTotal, c.ActionsDone, c.ActionsNew, c.ActionsSnoozed = 0, 0, 0, 0
	for _, it := range c.Items {
		if it.Severity == "ok" {
			c.Counts.OK++
			continue
		}
		if it.Snoozed {
			c.ActionsSnoozed++
			continue
		}
		if it.Severity == "fail" {
			c.Counts.Fail++
		} else {
			c.Counts.Warn++
		}
		c.ActionsTotal++
		if it.Done || it.ResolvedAt != nil {
			c.ActionsDone++
		}
		if it.FirstSeen == "" || it.FirstSeen == c.Date {
			c.ActionsNew++
		}
	}
	c.FixedTotal = len(c.Resolved)
}

// statusOf is the check-up's verdict: the worst severity still in the list.
func statusOf(items []CheckItem) string {
	status := "ok"
	for _, it := range items {
		if it.Snoozed {
			continue
		}
		if it.Severity == "fail" {
			return "fail"
		}
		if it.Severity == "warn" {
			status = "warn"
		}
	}
	return status
}

const checkupCols = `id, date, trigger, started_at, finished_at, status, items, emailed, resolved`

func scanCheckup(row interface{ Scan(...any) error }) (*Checkup, error) {
	var c Checkup
	var items, resolved []byte
	if err := row.Scan(&c.ID, &c.Date, &c.Trigger, &c.StartedAt, &c.FinishedAt, &c.Status, &items, &c.Emailed, &resolved); err != nil {
		return nil, mapErr(err)
	}
	c.Items, c.Resolved = []CheckItem{}, []ResolvedItem{}
	_ = json.Unmarshal(items, &c.Items)
	_ = json.Unmarshal(resolved, &c.Resolved)
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

// previousCheckup is the newest finished check-up other than `id`.
func (s *Store) previousCheckup(ctx context.Context, id int64) (*Checkup, error) {
	return scanCheckup(s.DB.QueryRow(ctx, `SELECT `+checkupCols+` FROM checkups
		WHERE finished_at IS NOT NULL AND id <> $1 ORDER BY started_at DESC, id DESC LIMIT 1`, id))
}

type snooze struct {
	Until *string
	Title string
}

func (s *Store) snoozes(ctx context.Context) (map[string]snooze, error) {
	rows, err := s.DB.Query(ctx, `SELECT key, until, title FROM checkup_snoozes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]snooze{}
	for rows.Next() {
		var k string
		var sn snooze
		if err := rows.Scan(&k, &sn.Until, &sn.Title); err != nil {
			return nil, err
		}
		out[k] = sn
	}
	return out, rows.Err()
}

// FinishCheckup stores the run's items, after threading yesterday through
// them: how long each key has been showing up, the task it was already
// turned into, which ones are snoozed — and which of yesterday's open items
// are gone today (the fixed list). The status passed in is recomputed over
// the items that are not snoozed.
func (s *Store) FinishCheckup(ctx context.Context, id int64, status string, items []CheckItem) (*Checkup, error) {
	if items == nil {
		items = []CheckItem{}
	}
	var date string
	if err := s.DB.QueryRow(ctx, `SELECT date FROM checkups WHERE id = $1`, id).Scan(&date); err != nil {
		return nil, mapErr(err)
	}
	prev, err := s.previousCheckup(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	prevItems := map[string]CheckItem{}
	if prev != nil {
		for _, it := range prev.Items {
			if it.Severity != "ok" {
				prevItems[it.Key] = it
			}
		}
	}
	snoozed, err := s.snoozes(ctx)
	if err != nil {
		return nil, err
	}

	present := map[string]bool{}
	for i := range items {
		it := &items[i]
		if it.Severity == "ok" {
			continue
		}
		present[it.Key] = true
		it.FirstSeen = date
		if p, ok := prevItems[it.Key]; ok {
			if p.FirstSeen != "" {
				it.FirstSeen = p.FirstSeen
			} else {
				it.FirstSeen = prev.Date
			}
			if it.TaskID == nil && p.TaskID != nil {
				it.TaskID, it.TaskRef = p.TaskID, p.TaskRef
			}
		}
		if it.TaskID != nil {
			switch t, err := s.TaskByID(ctx, *it.TaskID); {
			case err == nil:
				it.TaskStatus = t.Status
			case errors.Is(err, ErrNotFound):
				it.TaskID, it.TaskRef = nil, nil // the task was deleted
			}
		}
		if sn, ok := snoozed[it.Key]; ok {
			switch {
			case sn.Until != nil && date < *sn.Until:
				it.Snoozed, it.SnoozedUntil = true, sn.Until
			case sn.Until == nil && sn.Title == it.Title:
				it.Snoozed = true
			default: // ran out, or the item changed
				_, _ = s.DB.Exec(ctx, `DELETE FROM checkup_snoozes WHERE key = $1`, it.Key)
			}
		}
	}

	resolved := []ResolvedItem{}
	if prev != nil {
		for _, p := range prev.Items {
			if p.Severity == "ok" || p.Snoozed || present[p.Key] {
				continue
			}
			first := p.FirstSeen
			if first == "" {
				first = prev.Date
			}
			resolved = append(resolved, ResolvedItem{Key: p.Key, Category: p.Category, Severity: p.Severity, Title: p.Title,
				ProjectKey: p.ProjectKey, TaskRef: p.TaskRef, FirstSeen: first, SinceAt: prev.StartedAt})
		}
	}
	status = statusOf(items)

	c, err := scanCheckup(s.DB.QueryRow(ctx, `UPDATE checkups SET status = $2, items = $3, resolved = $4, finished_at = now()
		WHERE id = $1 RETURNING `+checkupCols, id, status, items, resolved))
	if err == nil {
		seen := map[string]bool{}
		findings := map[string][]string{}
		var keys []string
		for _, it := range c.Items {
			if it.ProjectKey == nil {
				continue
			}
			if !seen[*it.ProjectKey] {
				seen[*it.ProjectKey] = true
				keys = append(keys, *it.ProjectKey)
				s.markFocusDirtyKey(ctx, *it.ProjectKey, "check-up")
			}
			if it.Open() {
				line := it.Severity + ": " + it.Title
				if it.Action != "" {
					line += " → " + it.Action
				}
				findings[*it.ProjectKey] = append(findings[*it.ProjectKey], line)
			}
		}
		for _, k := range keys {
			if f := findings[k]; len(f) > 0 {
				s.addBotEventKey(ctx, k, "checkup", fmt.Sprintf("The %s check-up found %d thing(s): %s", c.Date, len(f), strings.Join(f, "; ")))
			}
		}
	}
	return c, err
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
	return s.updateCheckupItems(ctx, id, func(items []CheckItem) error {
		for i := range items {
			if items[i].Key == key {
				return fn(&items[i])
			}
		}
		return ErrNotFound
	})
}

func (s *Store) updateCheckupItems(ctx context.Context, id int64, fn func([]CheckItem) error) (*Checkup, error) {
	touched := map[string]bool{}
	err := s.tx(ctx, func(tx pgxTx) error {
		c, err := scanCheckup(tx.QueryRow(ctx, `SELECT `+checkupCols+` FROM checkups WHERE id = $1`, id))
		if err != nil {
			return err
		}
		before, _ := json.Marshal(c.Items)
		if err := fn(c.Items); err != nil {
			return err
		}
		var old []CheckItem
		_ = json.Unmarshal(before, &old)
		for i := range c.Items {
			if i < len(old) && c.Items[i].ProjectKey != nil {
				a, _ := json.Marshal(old[i])
				b, _ := json.Marshal(c.Items[i])
				if string(a) != string(b) {
					touched[*c.Items[i].ProjectKey] = true
				}
			}
		}
		_, err = tx.Exec(ctx, `UPDATE checkups SET items = $2, status = $3 WHERE id = $1`, id, c.Items, statusOf(c.Items))
		return err
	})
	if err != nil {
		return nil, err
	}
	for k := range touched {
		s.markFocusDirtyKey(ctx, k, "check-up action updated")
	}
	return s.CheckupByID(ctx, id)
}

// SnoozeCheckItem puts an item aside until a date, or (until == nil) until
// its title changes. The snooze is remembered by key, so tomorrow's check-up
// applies it too.
func (s *Store) SnoozeCheckItem(ctx context.Context, id int64, key string, until *string) (*Checkup, error) {
	var title string
	c, err := s.UpdateCheckupItem(ctx, id, key, func(it *CheckItem) error {
		if it.Severity == "ok" {
			return invalid("key", "an ok item cannot be snoozed")
		}
		it.Snoozed, it.SnoozedUntil, title = true, until, it.Title
		return nil
	})
	if err != nil {
		return nil, err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO checkup_snoozes (key, until, title) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET until = excluded.until, title = excluded.title, created_at = now()`, key, until, title)
	return c, err
}

func (s *Store) UnsnoozeCheckItem(ctx context.Context, id int64, key string) (*Checkup, error) {
	if _, err := s.DB.Exec(ctx, `DELETE FROM checkup_snoozes WHERE key = $1`, key); err != nil {
		return nil, err
	}
	return s.UpdateCheckupItem(ctx, id, key, func(it *CheckItem) error {
		it.Snoozed, it.SnoozedUntil = false, nil
		return nil
	})
}

// ApplyRecheck writes a fresh evaluation into a check-up: every item in
// `keys` gets CheckedAt = now; one that `fresh` still reports takes the new
// severity/title/detail (and is open again if it had been resolved); one it
// no longer reports is resolved.
func (s *Store) ApplyRecheck(ctx context.Context, id int64, keys []string, fresh map[string]CheckItem, now time.Time) (*Checkup, error) {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	return s.updateCheckupItems(ctx, id, func(items []CheckItem) error {
		found := false
		for i := range items {
			it := &items[i]
			if !want[it.Key] || it.Severity == "ok" {
				continue
			}
			found = true
			it.CheckedAt = &now
			if f, ok := fresh[it.Key]; ok && f.Severity != "ok" {
				it.Severity, it.Title, it.Detail, it.Action, it.Link, it.Fixes = f.Severity, f.Title, f.Detail, f.Action, f.Link, f.Fixes
				it.ResolvedAt = nil
			} else if it.ResolvedAt == nil {
				it.ResolvedAt = &now
			}
		}
		if !found {
			return ErrNotFound
		}
		return nil
	})
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
