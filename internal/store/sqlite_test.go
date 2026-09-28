package store_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/devai-io/forge/internal/db"
	"github.com/devai-io/forge/internal/seed"
	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/vault"
)

// open is a migrated database with the demo data, in a temp dir.
func open(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	st := store.New(d)
	if ok, err := st.SeedIfEmpty(ctx, seed.Demo); err != nil || !ok {
		t.Fatalf("seed: %v %v", ok, err)
	}
	return st
}

// cur is the running test, so must can wrap a two-value call directly.
var cur *testing.T

func must[T any](v T, err error) T {
	cur.Helper()
	if err != nil {
		cur.Fatal(err)
	}
	return v
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Every query the API tests do not reach, run once against SQLite: the port
// from Postgres is only as good as the statements actually executed.
func TestQueriesRunOnSQLite(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	today := store.TodayFor("Europe/Lisbon")

	// Projects, repos, servers and their links.
	ok(t, st.UpdateProject(ctx, "SHOP", store.Patch{"summary": []byte(`"the shop"`), "target_date": []byte(`"2031-01-02"`),
		"links": []byte(`[{"label":"site","url":"https://example.com"}]`), "color": []byte(`"#AABBCC"`)}))
	p := must(st.ProjectByKey(ctx, today, "shop"))
	if p.TargetDate == nil || *p.TargetDate != "2031-01-02" || p.Color != "#aabbcc" || len(p.Links) != 1 {
		t.Fatalf("project = %+v", p.Project)
	}
	err := st.UpdateProject(ctx, "SHOP", store.Patch{"priority": []byte(`9`)})
	var verr *store.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("priority 9: %v", err)
	}
	repo := must(st.CreateRepo(ctx, "SHOP", store.RepoInput{Name: "shop_docs", Path: "/home/demo/dev/shop/shop_docs"}))
	if _, err := st.CreateRepo(ctx, "SHOP", store.RepoInput{Name: "shop_docs"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate repo: %v", err)
	}
	must(st.UpdateRepo(ctx, repo.ID, store.Patch{"kind": []byte(`"site"`)}))
	last := "2026-09-01T10:00:00+02:00"
	ok(t, st.SaveRepoScans(ctx, "desk", []store.RepoScan{{RepoID: repo.ID, GitStatus: store.GitStatus{Branch: "main", Dirty: 2,
		Commits7d: 5, LastCommitAt: &last}}}))
	p = must(st.ProjectByKey(ctx, today, "SHOP"))
	if p.Stats.Commits7d < 5 || p.Stats.DirtyRepos < 1 || p.Stats.LastActivityAt == nil {
		t.Fatalf("stats = %+v", p.Stats)
	}
	ok(t, st.DeleteRepo(ctx, repo.ID))

	sv := must(st.CreateServer(ctx, store.ServerInput{Name: "edge-1", PublicAddress: "203.0.113.9", Tags: []string{"edge", "eu"}}))
	must(st.UpdateServer(ctx, sv.ID, store.Patch{"critical": []byte(`true`), "tags": []byte(`["edge"]`)}))
	got := must(st.ServerByID(ctx, sv.ID))
	if !got.Critical || len(got.Tags) != 1 {
		t.Fatalf("server = %+v", got)
	}
	links := must(st.SetProjectServers(ctx, "SHOP", []store.ProjectServerInput{{ServerID: sv.ID, Role: "edge"}}))
	if len(links) != 1 {
		t.Fatalf("links = %+v", links)
	}
	must(st.ListServers(ctx))
	must(st.ServersForMonitoring(ctx))
	ok(t, st.DeleteServer(ctx, sv.ID))

	// Endpoints and their probe history.
	e := must(st.CreateEndpoint(ctx, "SHOP", store.EndpointInput{Name: "home", URL: "https://example.com/"}))
	code, ms := 500, 120
	ok(t, st.RecordCheck(ctx, *e, store.CheckResult{OK: false, Code: &code, LatencyMS: &ms, Error: "HTTP 500"}))
	ok(t, st.RecordCheck(ctx, *e, store.CheckResult{OK: true, Code: &code, LatencyMS: &ms}))
	e = must(st.EndpointByID(ctx, e.ID))
	if e.LastStatus != "up" || e.LastChangeAt == nil {
		t.Fatalf("endpoint = %+v", e)
	}
	if checks := must(st.EndpointChecks(ctx, e.ID, 24)); len(checks) != 2 {
		t.Fatalf("checks = %d", len(checks))
	}
	must(st.UpdateEndpoint(ctx, e.ID, store.Patch{"enabled": []byte(`false`)}))
	ok(t, st.PruneChecks(ctx, time.Hour))
	must(st.DownEndpoints(ctx))
	ok(t, st.DeleteEndpoint(ctx, e.ID))

	// Tasks: search, labels, overdue, done, comments, refs.
	due := "2020-01-01"
	task := must(st.CreateTask(ctx, store.TaskInput{ProjectKey: "SHOP", Title: "Fix 100% of the_bugs",
		Labels: []string{"api", "urgent"}, DueDate: &due, Focus: true}))
	for name, f := range map[string]store.TaskFilter{
		"label":   {Label: "urgent"},
		"search":  {Query: "100%"},
		"escape":  {Query: "the_bugs"},
		"overdue": {Overdue: true, Today: today},
		"status":  {Statuses: []string{"todo", "backlog"}, ProjectKey: "shop"},
	} {
		list := must(st.ListTasks(ctx, f))
		found := false
		for _, x := range list {
			found = found || x.ID == task.ID
		}
		if !found {
			t.Errorf("%s filter missed the task", name)
		}
	}
	if list := must(st.ListTasks(ctx, store.TaskFilter{Query: "1000%"})); len(list) != 0 {
		t.Errorf("LIKE escaping: %d matches", len(list))
	}
	must(st.UpdateTask(ctx, task.ID, store.Patch{"status": []byte(`"done"`)}))
	if x := must(st.TaskByID(ctx, task.ID)); x.CompletedAt == nil {
		t.Fatal("done without completed_at")
	}
	c := must(st.CreateComment(ctx, task.ID, "looks good"))
	ok(t, st.DeleteComment(ctx, c.ID))
	if x := must(st.TaskByRef(ctx, task.Ref)); x.ID != task.ID {
		t.Fatal("TaskByRef")
	}
	must(st.StaleBlocked(ctx, 7))

	// The dashboard counts the completion in the viewer's calendar.
	d := must(st.Dashboard(ctx, "Europe/Lisbon"))
	if d.Stats.DoneToday < 1 || d.Stats.StreakDays < 1 || len(d.Daily) != 28 || d.Daily[27].Date != d.Today {
		t.Fatalf("dashboard = %+v daily=%d", d.Stats, len(d.Daily))
	}
	ok(t, st.DeleteTask(ctx, task.ID))

	// Check-ups.
	id := must(st.StartCheckup(ctx, today, "schedule"))
	link := "/tasks"
	must(st.FinishCheckup(ctx, id, "warn", []store.CheckItem{{Key: "k1", Category: "tasks", Severity: "warn", Title: "t", Link: &link}}))
	if exists := must(st.CheckupExistsFor(ctx, today)); !exists {
		t.Fatal("CheckupExistsFor")
	}
	must(st.UpdateCheckupItem(ctx, id, "k1", func(it *store.CheckItem) error { it.Done = true; return nil }))
	ok(t, st.MarkCheckupEmailed(ctx, id))
	if list := must(st.ListCheckups(ctx, 10)); len(list) != 1 || list[0].Date != today {
		t.Fatalf("checkups = %+v", list)
	}

	// Users, sessions, resets.
	u := must(st.CreateUser(ctx, "ada", "ada@example.com", "Ada", "x", "UTC", false))
	if _, err := st.CreateUser(ctx, "ADA", "other@example.com", "", "x", "UTC", false); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("case-insensitive username: %v", err)
	}
	sid := must(st.CreateSession(ctx, u.ID, []byte("hash"), "ua", "203.0.113.1", time.Hour))
	if _, info, err := st.SessionUser(ctx, []byte("hash"), time.Hour); err != nil || info.ID != sid {
		t.Fatalf("session: %v", err)
	}
	must(st.ElevateSession(ctx, sid, time.Minute))
	if list := must(st.ListSessions(ctx, u.ID, sid)); len(list) != 1 || !list[0].Current {
		t.Fatalf("sessions = %+v", list)
	}
	ok(t, st.CreatePasswordReset(ctx, u.ID, []byte("reset"), time.Hour))
	if uid := must(st.ConsumePasswordReset(ctx, []byte("reset"))); uid != u.ID {
		t.Fatal("reset")
	}
	if _, err := st.ConsumePasswordReset(ctx, []byte("reset")); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a reset link worked twice")
	}
	ok(t, st.DisableTOTP(ctx, u.ID))
	ok(t, st.PruneSessions(ctx))
	ok(t, st.DeleteSession(ctx, u.ID, sid))
	known := must(st.SeenDevice(ctx, "203.0.113.1", "Firefox Linux"))
	again := must(st.SeenDevice(ctx, "203.0.113.1", "Firefox Linux"))
	if known || !again {
		t.Fatalf("known devices: %v %v", known, again)
	}
	ok(t, st.LogSecurity(ctx, "login_new_device", "", "203.0.113.1", "ua", 0))
	if n := must(st.NewDeviceLogins(ctx, time.Hour)); n != 1 {
		t.Fatalf("new device logins = %d", n)
	}
	must(st.ListSecurity(ctx, "", 10))
	must(st.SystemCounts(ctx))

	// Runners and runs.
	rn := must(st.CreateRunner(ctx, "desk", []byte("tok")))
	ok(t, st.RotateRunnerToken(ctx, rn.ID, []byte("tok2")))
	must(st.SetRunnerRole(ctx, rn.ID, "master"))
	must(st.Heartbeat(ctx, rn.ID, store.HeartbeatInput{Hostname: "desk", Running: []int64{}}))
	ok(t, st.FailOrphanedRuns(ctx))
	must(st.TerminalHosts(ctx))
	ok(t, st.DeleteRunner(ctx, rn.ID))

	// Vault search and tag lookup.
	box := must(vault.New(bytes.Repeat([]byte{1}, 32)))
	must(st.CreateVault(ctx, box, store.VaultInput{Name: "grafana token", Kind: "token", Tags: []string{"integration:grafana"},
		Secret: map[string]string{"token": "s3cret"}}, "203.0.113.1"))
	if list := must(st.ListVault(ctx, store.VaultFilter{Query: "integration"})); len(list) != 1 {
		t.Fatalf("vault search = %d", len(list))
	}
	must(st.ListVaultAudit(ctx, 10))

	// Import skips what exists.
	rep := must(st.Import(ctx, box, []byte(`{"servers":[{"name":"web-1"},{"name":"new-1"}]}`), "cli"))
	if len(rep.ServersAdded) != 1 || len(rep.Skipped) != 1 {
		t.Fatalf("import = %+v", rep)
	}
	ok(t, st.LogActivity(ctx, store.ActivityInput{Kind: "note", Summary: "hello"}))
	ok(t, st.DeleteProject(ctx, "BLOG"))
}
