package store

import (
	"context"
	"time"
)

// Dates on the dashboard are the viewer's calendar days, not UTC ones: a task
// finished at 00:30 local time counts for today, not yesterday.

// Today returns the date in loc and the Monday that starts its week.
func Today(now time.Time, loc *time.Location) (today, weekStart time.Time) {
	n := now.In(loc)
	today = time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	offset := (int(today.Weekday()) + 6) % 7 // Monday = 0
	return today, today.AddDate(0, 0, -offset)
}

func dateStr(t time.Time) string { return t.Format("2006-01-02") }

// Streaks computes the current streak (consecutive days with at least one
// completion, ending today — or yesterday, so the streak is not "lost" at
// breakfast before anything is done) and the best streak ever. days must be
// sorted ascending and distinct.
func Streaks(days []time.Time, today time.Time) (current, best int) {
	run := 0
	var prev time.Time
	for i, d := range days {
		if i > 0 && d.Sub(prev) == 24*time.Hour {
			run++
		} else {
			run = 1
		}
		if run > best {
			best = run
		}
		prev = d
	}
	if len(days) == 0 {
		return 0, 0
	}
	last := days[len(days)-1]
	if last.Equal(today) || last.Equal(today.AddDate(0, 0, -1)) {
		current = run
	}
	return current, best
}

func (s *Store) Dashboard(ctx context.Context, tz string) (*Dashboard, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
		tz = "UTC"
	}
	todayT, weekStart := Today(time.Now(), loc)
	today := dateStr(todayT)
	d := &Dashboard{Today: today}

	// Completion days, for streaks.
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT (completed_at AT TIME ZONE $1)::date AS d
		FROM tasks WHERE completed_at IS NOT NULL ORDER BY d`, tz)
	if err != nil {
		return nil, err
	}
	var days []time.Time
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return nil, err
		}
		days = append(days, time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	d.Stats.StreakDays, d.Stats.BestStreak = Streaks(days, todayT)

	st := &d.Stats
	err = s.DB.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE (t.completed_at AT TIME ZONE $1)::date = $2::date),
		  count(*) FILTER (WHERE (t.completed_at AT TIME ZONE $1)::date >= $3::date),
		  count(*) FILTER (WHERE (t.completed_at AT TIME ZONE $1)::date >= $3::date - 7
		                     AND (t.completed_at AT TIME ZONE $1)::date < $3::date),
		  count(*) FILTER (WHERE t.status <> 'done'),
		  count(*) FILTER (WHERE t.status = 'in_progress'),
		  count(*) FILTER (WHERE t.status = 'blocked'),
		  count(*) FILTER (WHERE t.status <> 'done' AND t.due_date < $2::date),
		  count(*) FILTER (WHERE t.status <> 'done' AND t.due_date >= $2::date AND t.due_date <= $2::date + 3)
		FROM tasks t JOIN projects p ON p.id = t.project_id
		WHERE p.status <> 'archived'`, tz, today, dateStr(weekStart)).Scan(
		&st.DoneToday, &st.DoneWeek, &st.DonePrevWeek, &st.OpenTasks, &st.InProgress, &st.Blocked,
		&st.Overdue, &st.DueSoon)
	if err != nil {
		return nil, err
	}
	if err := s.DB.QueryRow(ctx, `SELECT coalesce(max(weekly_goal), 10) FROM users`).Scan(&st.WeeklyGoal); err != nil {
		return nil, err
	}

	// 28 days, oldest first, today last.
	from := dateStr(todayT.AddDate(0, 0, -27))
	rows, err = s.DB.Query(ctx, `
		WITH days AS (SELECT generate_series($2::date, $3::date, interval '1 day')::date AS d),
		done AS (SELECT (completed_at AT TIME ZONE $1)::date AS d, count(*) AS n FROM tasks
		         WHERE completed_at >= $2::date - 1 GROUP BY 1),
		created AS (SELECT (created_at AT TIME ZONE $1)::date AS d, count(*) AS n FROM tasks
		            WHERE created_at >= $2::date - 1 GROUP BY 1)
		SELECT to_char(days.d, 'YYYY-MM-DD'), coalesce(done.n, 0), coalesce(created.n, 0)
		FROM days LEFT JOIN done ON done.d = days.d LEFT JOIN created ON created.d = days.d
		ORDER BY days.d`, tz, from, today)
	if err != nil {
		return nil, err
	}
	d.Daily = []DayCount{}
	for rows.Next() {
		var c DayCount
		if err := rows.Scan(&c.Date, &c.Done, &c.Created); err != nil {
			rows.Close()
			return nil, err
		}
		d.Daily = append(d.Daily, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	notArchived := ` AND p.status <> 'archived'`
	if d.Focus, err = s.queryTasks(ctx, taskSelect+` WHERE t.focus AND t.status <> 'done'`+notArchived+
		` ORDER BY `+priorityOrder+`, t.due_date NULLS LAST, p.priority, t.id LIMIT 50`); err != nil {
		return nil, err
	}
	if d.Overdue, err = s.queryTasks(ctx, taskSelect+` WHERE t.status <> 'done' AND t.due_date < $1::date`+notArchived+
		` ORDER BY t.due_date, `+priorityOrder+` LIMIT 20`, today); err != nil {
		return nil, err
	}
	if d.Projects, err = s.ListProjects(ctx, today, false); err != nil {
		return nil, err
	}
	if d.EndpointsDown, err = s.DownEndpoints(ctx); err != nil {
		return nil, err
	}
	if d.Runners, err = s.ListRunners(ctx); err != nil {
		return nil, err
	}
	if d.ActiveRuns, err = s.ListRuns(ctx, RunFilter{Statuses: []string{"queued", "running"}, Limit: 20}); err != nil {
		return nil, err
	}
	if d.RecentRuns, err = s.ListRuns(ctx, RunFilter{Finished: true, Limit: 6}); err != nil {
		return nil, err
	}
	if d.Activity, err = s.ListActivity(ctx, "", 30); err != nil {
		return nil, err
	}
	if c, err := s.LatestCheckup(ctx); err == nil {
		d.Checkup = &c.CheckupSummary
	}
	return d, nil
}

// TodayFor is the viewer's current date as YYYY-MM-DD.
func TodayFor(tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	t, _ := Today(time.Now(), loc)
	return dateStr(t)
}
