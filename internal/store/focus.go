package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/usage"
)

// Focus is a project's "what now": the immediate actions, what comes after,
// the roadmap and what blocks it. It is recomputed a little after the
// project changes (MarkFocusDirty) — by the model when there is a provider
// key, by FocusRules otherwise.
type Focus struct {
	Headline string        `json:"headline"`
	Now      []FocusItem   `json:"now"`
	Next     []FocusItem   `json:"next"`
	Roadmap  []RoadmapStep `json:"roadmap"`
	Blockers []FocusItem   `json:"blockers"`
}

// FocusItem is one action. Ref, when set, is a task of the project.
type FocusItem struct {
	Title string  `json:"title"`
	Why   string  `json:"why"`
	Ref   *string `json:"ref"`
	Kind  string  `json:"kind"`
}

type RoadmapStep struct {
	Title  string   `json:"title"`
	Status string   `json:"status"`
	Refs   []string `json:"refs"`
}

var (
	FocusKinds         = []string{"task", "checkup", "repo", "endpoint", "run", "other"}
	RoadmapStatuses    = []string{"done", "active", "planned"}
	focusNowMax        = 5
	focusNextMax       = 7
	focusRoadmapMax    = 8
	focusBlockersMax   = 5
	focusTitleMaxChars = 160
	focusWhyMaxChars   = 300
)

// ProjectFocus is the stored focus as the API answers it.
type ProjectFocus struct {
	Focus       Focus      `json:"focus"`
	Source      string     `json:"source"` // ai | rules
	Model       string     `json:"model"`
	Trigger     string     `json:"trigger"`
	GeneratedAt *time.Time `json:"generated_at"`
	Dirty       bool       `json:"dirty"`
	DirtyReason string     `json:"dirty_reason"`
	Error       string     `json:"error"`
	// Usage is what the model calls for this project's focus have cost in
	// all; LastUsage the latest computation's (empty when rules made it).
	Usage     []usage.Entry `json:"usage"`
	LastUsage []usage.Entry `json:"last_usage"`
	// Tasks are the tasks the focus names, by ref, as they are now (a step
	// done since the focus was computed shows as done).
	Tasks map[string]FocusTaskLink `json:"tasks"`
}

type FocusTaskLink struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
}

// MarkFocusDirty notes that the project changed. The first mark of a burst
// keeps its time (the debounce counts from it); the reason is the latest.
// Best effort: a focus that is late to update must never fail a write.
func (s *Store) MarkFocusDirty(ctx context.Context, projectID int64, reason string) {
	_, _ = s.DB.Exec(ctx, `INSERT INTO project_focus (project_id, dirty_since, dirty_reason) VALUES ($1, now(), $2)
		ON CONFLICT (project_id) DO UPDATE SET dirty_since = coalesce(project_focus.dirty_since, excluded.dirty_since),
		dirty_reason = excluded.dirty_reason`, projectID, truncate(reason, 200))
}

func (s *Store) markFocusDirtyKey(ctx context.Context, key, reason string) {
	if id, err := s.ProjectIDByKey(ctx, key); err == nil {
		s.MarkFocusDirty(ctx, id, reason)
	}
}

// FocusByProject is the stored focus; ErrNotFound until one was generated.
func (s *Store) FocusByProject(ctx context.Context, projectID int64) (*ProjectFocus, error) {
	var f ProjectFocus
	var raw []byte
	var dirty *time.Time
	var total, last string
	if err := s.DB.QueryRow(ctx, `SELECT focus, source, model, trigger, generated_at, dirty_since, dirty_reason, error,
		usage, last_usage
		FROM project_focus WHERE project_id = $1 AND generated_at IS NOT NULL`, projectID).
		Scan(&raw, &f.Source, &f.Model, &f.Trigger, &f.GeneratedAt, &dirty, &f.DirtyReason, &f.Error,
			&total, &last); err != nil {
		return nil, mapErr(err)
	}
	f.Usage, f.LastUsage = usageEntries(total), usageEntries(last)
	_ = json.Unmarshal(raw, &f.Focus)
	f.Focus = f.Focus.normalized()
	f.Dirty = dirty != nil
	if !f.Dirty {
		f.DirtyReason = ""
	}
	f.Tasks = map[string]FocusTaskLink{}
	named := map[string]bool{}
	for _, list := range [][]FocusItem{f.Focus.Now, f.Focus.Next, f.Focus.Blockers} {
		for _, it := range list {
			if it.Ref != nil {
				named[*it.Ref] = true
			}
		}
	}
	for _, st := range f.Focus.Roadmap {
		for _, r := range st.Refs {
			named[r] = true
		}
	}
	if len(named) > 0 {
		rows, err := s.DB.Query(ctx, `SELECT p.key || '-' || t.number, t.id, t.status, t.title
			FROM tasks t JOIN projects p ON p.id = t.project_id WHERE t.project_id = $1`, projectID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var ref string
			var l FocusTaskLink
			if err := rows.Scan(&ref, &l.ID, &l.Status, &l.Title); err != nil {
				return nil, err
			}
			if named[ref] {
				f.Tasks[ref] = l
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return &f, nil
}

// SaveFocus stores a generated focus. A change marked after `started` (while
// this one was being computed) keeps the project dirty.
func (s *Store) SaveFocus(ctx context.Context, projectID int64, f Focus, source, model, trigger, errText string, started time.Time) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO project_focus (project_id, focus, source, model, trigger, generated_at, error)
		VALUES ($1, $2, $3, $4, $5, now(), $6)
		ON CONFLICT (project_id) DO UPDATE SET focus = excluded.focus, source = excluded.source, model = excluded.model,
		trigger = excluded.trigger, generated_at = excluded.generated_at, error = excluded.error,
		dirty_since = CASE WHEN project_focus.dirty_since > $7 THEN project_focus.dirty_since END,
		dirty_reason = CASE WHEN project_focus.dirty_since > $7 THEN project_focus.dirty_reason ELSE '' END`,
		projectID, f.normalized(), source, model, truncate(trigger, 200), truncate(errText, 1000), started)
	return err
}

// DirtyFocus is a project that changed and is due for a new focus.
type DirtyFocus struct {
	ProjectID int64
	Reason    string
}

// DirtyFocuses lists projects whose first unprocessed change is at least
// `settle` old (so a burst of edits makes one recomputation), oldest first.
func (s *Store) DirtyFocuses(ctx context.Context, settle time.Duration, limit int) ([]DirtyFocus, error) {
	rows, err := s.DB.Query(ctx, `SELECT f.project_id, f.dirty_reason FROM project_focus f
		JOIN projects p ON p.id = f.project_id
		WHERE f.dirty_since IS NOT NULL AND f.dirty_since <= ts_add(now(), $1) AND p.status <> 'archived'
		ORDER BY f.dirty_since LIMIT $2`, -settle.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DirtyFocus{}
	for rows.Next() {
		var d DirtyFocus
		if err := rows.Scan(&d.ProjectID, &d.Reason); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ── What the focus is computed from ───────────────────────────────────────

type FocusTask struct {
	Ref      string   `json:"ref"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Priority string   `json:"priority"`
	Type     string   `json:"type"`
	Labels   []string `json:"labels,omitempty"`
	Due      *string  `json:"due,omitempty"`
	Focus    bool     `json:"focus,omitempty"`
	Repo     *string  `json:"repo,omitempty"`
	Updated  string   `json:"updated"`
}

type FocusRun struct {
	ID       int64   `json:"id"`
	Status   string  `json:"status"`
	Repo     string  `json:"repo"`
	TaskRef  *string `json:"task_ref,omitempty"`
	Prompt   string  `json:"prompt"`
	Result   string  `json:"result,omitempty"`
	Error    string  `json:"error,omitempty"`
	Finished string  `json:"finished,omitempty"`
}

type FocusRepo struct {
	Name   string `json:"name"`
	Branch string `json:"branch,omitempty"`
	Dirty  int    `json:"dirty,omitempty"`
	Ahead  int    `json:"ahead,omitempty"`
	Behind int    `json:"behind,omitempty"`
	CI     string `json:"ci,omitempty"` // failure, success, in_progress…
}

type FocusCheck struct {
	Severity string  `json:"severity"`
	Title    string  `json:"title"`
	Action   string  `json:"action"`
	TaskRef  *string `json:"task_ref,omitempty"`
}

// FocusInputs is everything the focus is computed from, in the shape the
// model reads (JSON).
type FocusInputs struct {
	Today       string       `json:"today"`
	Key         string       `json:"key"`
	Name        string       `json:"name"`
	Status      string       `json:"status"`
	Summary     string       `json:"summary,omitempty"`
	Description string       `json:"description,omitempty"`
	TargetDate  *string      `json:"target_date,omitempty"`
	Open        []FocusTask  `json:"open_tasks"`
	Done        []FocusTask  `json:"done_last_14_days"`
	Runs        []FocusRun   `json:"recent_runs"`
	Checkup     []FocusCheck `json:"checkup_actions"`
	Repos       []FocusRepo  `json:"repos"`
	DownURLs    []string     `json:"endpoints_down"`
	Memories    []string     `json:"memories"`
}

func focusTask(t Task) FocusTask {
	return FocusTask{Ref: t.Ref, Title: t.Title, Status: t.Status, Priority: t.Priority, Type: t.Type,
		Labels: t.Labels, Due: t.DueDate, Focus: t.Focus, Repo: t.RepoName, Updated: t.UpdatedAt.Format("2006-01-02")}
}

// FocusInputs gathers a project's state. today is the viewer's date.
func (s *Store) FocusInputs(ctx context.Context, projectID int64, today string) (*FocusInputs, error) {
	key, err := s.ProjectKeyByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	p, err := s.ProjectByKey(ctx, today, key)
	if err != nil {
		return nil, err
	}
	in := &FocusInputs{Today: today, Key: p.Key, Name: p.Name, Status: p.Status, Summary: p.Summary,
		Description: excerpt(p.Description, 1500), TargetDate: p.TargetDate,
		Open: []FocusTask{}, Done: []FocusTask{}, Runs: []FocusRun{}, Checkup: []FocusCheck{},
		Repos: []FocusRepo{}, DownURLs: []string{}, Memories: []string{}}

	open, err := s.ListTasks(ctx, TaskFilter{ProjectKey: p.Key, Open: true, Limit: 80})
	if err != nil {
		return nil, err
	}
	for _, t := range open {
		in.Open = append(in.Open, focusTask(t))
	}
	done, err := s.queryTasks(ctx, taskSelect+` WHERE t.project_id = $1 AND t.status = 'done'
		AND t.completed_at >= ts_add(now(), -14 * 86400) ORDER BY t.completed_at DESC LIMIT 20`, projectID)
	if err != nil {
		return nil, err
	}
	for _, t := range done {
		in.Done = append(in.Done, focusTask(t))
	}

	runs, err := s.ListRuns(ctx, RunFilter{ProjectKey: p.Key, Limit: 8})
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		fr := FocusRun{ID: r.ID, Status: r.Status, Repo: r.RepoName, TaskRef: r.TaskRef,
			Prompt: excerpt(r.Prompt, 200), Result: excerpt(r.Result, 400), Error: excerpt(r.Error, 200)}
		if r.FinishedAt != nil {
			fr.Finished = r.FinishedAt.Format("2006-01-02 15:04")
		}
		in.Runs = append(in.Runs, fr)
	}

	if c, err := s.LatestCheckup(ctx); err == nil {
		for _, it := range c.Items {
			if it.ProjectKey != nil && *it.ProjectKey == p.Key && it.Severity != "ok" && !it.Done {
				in.Checkup = append(in.Checkup, FocusCheck{Severity: it.Severity, Title: it.Title, Action: it.Action, TaskRef: it.TaskRef})
			}
		}
	}

	for _, r := range p.Repos {
		fr := FocusRepo{Name: r.Name}
		if r.Git != nil {
			fr.Branch, fr.Dirty, fr.Ahead, fr.Behind = r.Git.Branch, r.Git.Dirty, r.Git.Ahead, r.Git.Behind
			if r.Git.CI != nil {
				fr.CI = r.Git.CI.Conclusion
				if fr.CI == "" {
					fr.CI = r.Git.CI.Status
				}
			}
		}
		in.Repos = append(in.Repos, fr)
	}
	for _, e := range p.Endpoints {
		if e.Enabled && e.LastStatus == "down" {
			in.DownURLs = append(in.DownURLs, e.URL)
		}
	}

	mems, err := s.ListMemories(ctx, MemoryFilter{ProjectKey: p.Key, Limit: 8})
	if err == nil {
		for _, m := range mems {
			if m.Pinned || m.Kind == "decision" {
				in.Memories = append(in.Memories, m.Line(240))
			}
		}
	}
	return in, nil
}

// ── Rules: the focus without a model ─────────────────────────────────────

var priorityRank = map[string]int{"urgent": 0, "high": 1, "medium": 2, "low": 3}

// FocusRules computes a focus from the inputs alone: work in progress first,
// then what is urgent or due, the check-up's actions and repos that need a
// hand; the rest of the todo list is "next"; labels make the roadmap.
func FocusRules(in *FocusInputs) Focus {
	f := Focus{}
	ref := func(r string) *string { return &r }
	open := append([]FocusTask{}, in.Open...)
	sort.SliceStable(open, func(i, j int) bool {
		a, b := open[i], open[j]
		if a.Focus != b.Focus {
			return a.Focus
		}
		if priorityRank[a.Priority] != priorityRank[b.Priority] {
			return priorityRank[a.Priority] < priorityRank[b.Priority]
		}
		switch {
		case a.Due != nil && b.Due != nil:
			return *a.Due < *b.Due
		case a.Due != nil:
			return true
		case b.Due != nil:
			return false
		}
		return false
	})
	used := map[string]bool{}
	add := func(list *[]FocusItem, it FocusItem) {
		if it.Ref != nil {
			if used[*it.Ref] {
				return
			}
			used[*it.Ref] = true
		}
		*list = append(*list, it)
	}

	for _, t := range open {
		if t.Status == "blocked" {
			add(&f.Blockers, FocusItem{Title: t.Title, Why: "Blocked since " + t.Updated, Ref: ref(t.Ref), Kind: "task"})
		}
	}
	for _, u := range in.DownURLs {
		f.Blockers = append(f.Blockers, FocusItem{Title: "Endpoint down: " + u, Why: "The last check failed.", Kind: "endpoint"})
	}

	for _, t := range open {
		if t.Status == "in_progress" {
			add(&f.Now, FocusItem{Title: "Finish " + t.Title, Why: "In progress.", Ref: ref(t.Ref), Kind: "task"})
		}
	}
	for _, c := range in.Checkup {
		if c.Severity == "fail" {
			f.Now = append(f.Now, FocusItem{Title: c.Action, Why: c.Title, Ref: c.TaskRef, Kind: "checkup"})
		}
	}
	for _, t := range open {
		overdue := t.Due != nil && *t.Due < in.Today
		dueSoon := t.Due != nil && *t.Due <= addDays(in.Today, 3)
		if t.Status == "todo" && (t.Priority == "urgent" || overdue || dueSoon || t.Focus) {
			why := capitalize(t.Priority) + " priority."
			switch {
			case overdue:
				why = "Overdue since " + *t.Due + "."
			case dueSoon:
				why = "Due " + *t.Due + "."
			case t.Focus:
				why = "Marked as focus."
			}
			add(&f.Now, FocusItem{Title: t.Title, Why: why, Ref: ref(t.Ref), Kind: "task"})
		}
	}
	for _, r := range in.Repos {
		switch {
		case r.CI == "failure":
			f.Now = append(f.Now, FocusItem{Title: "Fix CI on " + r.Name, Why: "The latest run on the default branch failed.", Kind: "repo"})
		case r.Behind > 0:
			f.Now = append(f.Now, FocusItem{Title: "Pull " + r.Name, Why: fmt.Sprintf("%d commit(s) behind origin.", r.Behind), Kind: "repo"})
		case r.Dirty > 0:
			f.Now = append(f.Now, FocusItem{Title: "Commit or discard the changes in " + r.Name, Why: fmt.Sprintf("%d uncommitted change(s).", r.Dirty), Kind: "repo"})
		}
	}
	for _, c := range in.Checkup {
		if c.Severity == "warn" {
			f.Now = append(f.Now, FocusItem{Title: c.Action, Why: c.Title, Ref: c.TaskRef, Kind: "checkup"})
		}
	}
	if len(f.Now) == 0 {
		for _, t := range open {
			if t.Status == "todo" {
				add(&f.Now, FocusItem{Title: t.Title, Why: "Top of the todo list.", Ref: ref(t.Ref), Kind: "task"})
				break
			}
		}
	}
	if len(f.Now) > focusNowMax {
		for _, it := range f.Now[focusNowMax:] {
			if it.Ref != nil {
				delete(used, *it.Ref)
			}
		}
		f.Now = f.Now[:focusNowMax]
	}
	for _, st := range []string{"todo", "backlog"} {
		for _, t := range open {
			if t.Status == st {
				add(&f.Next, FocusItem{Title: t.Title, Why: capitalize(t.Priority) + " priority " + t.Type + ".", Ref: ref(t.Ref), Kind: "task"})
			}
		}
	}

	f.Roadmap = rulesRoadmap(in)
	switch {
	case len(f.Blockers) > 0 && len(f.Now) > 0:
		f.Headline = fmt.Sprintf("%d thing(s) to act on, %d blocked.", len(f.Now), len(f.Blockers))
	case len(f.Now) > 0:
		f.Headline = "Next up: " + f.Now[0].Title
	case len(in.Open) == 0:
		f.Headline = "No open tasks — plan the next step."
	default:
		f.Headline = "Nothing urgent."
	}
	return f.normalized()
}

// rulesRoadmap groups tasks by label (the first one) — a label is the
// closest thing to a milestone — or, with no labels, by status.
func rulesRoadmap(in *FocusInputs) []RoadmapStep {
	type group struct {
		title             string
		open, active, all int
		done              int
		refs              []string
		order             int
	}
	groups := map[string]*group{}
	var order int
	note := func(t FocusTask) {
		if len(t.Labels) == 0 {
			return
		}
		g := groups[t.Labels[0]]
		if g == nil {
			order++
			g = &group{title: t.Labels[0], order: order}
			groups[t.Labels[0]] = g
		}
		g.all++
		switch t.Status {
		case "done":
			g.done++
		case "in_progress", "blocked":
			g.active++
			g.open++
		default:
			g.open++
		}
		if len(g.refs) < 6 {
			g.refs = append(g.refs, t.Ref)
		}
	}
	for _, t := range in.Done {
		note(t)
	}
	for _, t := range in.Open {
		note(t)
	}
	out := []RoadmapStep{}
	if len(groups) > 0 {
		list := make([]*group, 0, len(groups))
		for _, g := range groups {
			list = append(list, g)
		}
		rank := func(g *group) int {
			switch {
			case g.open == 0:
				return 0
			case g.active > 0:
				return 1
			}
			return 2
		}
		sort.SliceStable(list, func(i, j int) bool {
			if rank(list[i]) != rank(list[j]) {
				return rank(list[i]) < rank(list[j])
			}
			return list[i].order < list[j].order
		})
		for _, g := range list {
			status := []string{"done", "active", "planned"}[rank(g)]
			title := g.title
			if g.all > 1 {
				title = fmt.Sprintf("%s (%d/%d done)", g.title, g.done, g.all)
			}
			out = append(out, RoadmapStep{Title: title, Status: status, Refs: g.refs})
		}
		return out
	}
	byStatus := func(status, title string, statuses ...string) {
		var refs []string
		n := 0
		for _, list := range [][]FocusTask{in.Done, in.Open} {
			for _, t := range list {
				if OneOf(t.Status, statuses) {
					n++
					if len(refs) < 6 {
						refs = append(refs, t.Ref)
					}
				}
			}
		}
		if n > 0 {
			out = append(out, RoadmapStep{Title: fmt.Sprintf("%s (%d)", title, n), Status: status, Refs: refs})
		}
	}
	byStatus("done", "Shipped in the last two weeks", "done")
	byStatus("active", "In progress", "in_progress", "blocked")
	byStatus("planned", "Up next", "todo")
	byStatus("planned", "Backlog", "backlog")
	return out
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func addDays(date string, n int) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

// ── Validation ────────────────────────────────────────────────────────────

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func (f Focus) normalized() Focus {
	f.Headline = clip(f.Headline, 200)
	f.Now = nonNil(f.Now)
	f.Next = nonNil(f.Next)
	f.Roadmap = nonNil(f.Roadmap)
	f.Blockers = nonNil(f.Blockers)
	for i := range f.Roadmap {
		f.Roadmap[i].Refs = nonNil(f.Roadmap[i].Refs)
	}
	return f
}

// CleanFocus makes a focus written by a model safe to store and show:
// lists clipped, text shortened, kinds and statuses known, and every ref a
// real task of the project (refs maps the project's task refs).
func CleanFocus(f Focus, refs map[string]bool) Focus {
	item := func(list []FocusItem, max int) []FocusItem {
		out := []FocusItem{}
		for _, it := range list {
			it.Title = clip(it.Title, focusTitleMaxChars)
			if it.Title == "" {
				continue
			}
			it.Why = clip(it.Why, focusWhyMaxChars)
			if it.Ref != nil {
				r := strings.ToUpper(strings.TrimSpace(*it.Ref))
				if refs[r] {
					it.Ref = &r
				} else {
					it.Ref = nil
				}
			}
			if !OneOf(it.Kind, FocusKinds) {
				it.Kind = "other"
				if it.Ref != nil {
					it.Kind = "task"
				}
			}
			out = append(out, it)
			if len(out) == max {
				break
			}
		}
		return out
	}
	f.Now = item(f.Now, focusNowMax)
	f.Next = item(f.Next, focusNextMax)
	f.Blockers = item(f.Blockers, focusBlockersMax)
	steps := []RoadmapStep{}
	for _, st := range f.Roadmap {
		st.Title = clip(st.Title, focusTitleMaxChars)
		if st.Title == "" {
			continue
		}
		if !OneOf(st.Status, RoadmapStatuses) {
			st.Status = "planned"
		}
		var ok []string
		for _, r := range st.Refs {
			r = strings.ToUpper(strings.TrimSpace(r))
			if refs[r] && len(ok) < 10 {
				ok = append(ok, r)
			}
		}
		st.Refs = ok
		steps = append(steps, st)
		if len(steps) == focusRoadmapMax {
			break
		}
	}
	f.Roadmap = steps
	return f.normalized()
}

// TaskRefs is every task ref of the project (open or not), for CleanFocus.
func (s *Store) TaskRefs(ctx context.Context, projectID int64) (map[string]bool, error) {
	rows, err := s.DB.Query(ctx, `SELECT p.key || '-' || t.number FROM tasks t JOIN projects p ON p.id = t.project_id
		WHERE t.project_id = $1`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out[r] = true
	}
	return out, rows.Err()
}

func usageEntries(raw string) []usage.Entry {
	var es []usage.Entry
	if json.Unmarshal([]byte(raw), &es) != nil || es == nil {
		return []usage.Entry{}
	}
	return es
}

// SetFocusUsage records what one focus computation cost: last_usage becomes
// es, and es is added to the project's running total. Best effort, like the
// dirty marks.
func (s *Store) SetFocusUsage(ctx context.Context, projectID int64, es []usage.Entry) {
	_ = s.tx(ctx, func(tx pgxTx) error {
		var raw string
		if err := tx.QueryRow(ctx, `SELECT usage FROM project_focus WHERE project_id = $1`, projectID).Scan(&raw); err != nil {
			return err
		}
		m := usage.NewMeter()
		for _, e := range usageEntries(raw) {
			m.Add(e)
		}
		for _, e := range es {
			m.Add(e)
		}
		if es == nil {
			es = []usage.Entry{}
		}
		all, _ := json.Marshal(m.Entries())
		last, _ := json.Marshal(es)
		_, err := tx.Exec(ctx, `UPDATE project_focus SET usage = $2, last_usage = $3 WHERE project_id = $1`,
			projectID, string(all), string(last))
		return err
	})
}

// FocusUsage adds up what focus computations have cost across projects.
func (s *Store) FocusUsage(ctx context.Context) ([]usage.Entry, error) {
	rows, err := s.DB.Query(ctx, `SELECT usage FROM project_focus WHERE usage <> '[]'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := usage.NewMeter()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		for _, e := range usageEntries(raw) {
			m.Add(e)
		}
	}
	return m.Entries(), rows.Err()
}
