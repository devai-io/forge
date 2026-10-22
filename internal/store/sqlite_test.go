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
	must(st.UpdateServer(ctx, sv.ID, store.Patch{"critical": []byte(`true`), "tags": []byte(`["edge"]`),
		"ssh_target": []byte(`"root@203.0.113.9"`)}))
	got := must(st.ServerByID(ctx, sv.ID))
	if !got.Critical || len(got.Tags) != 1 || got.SSHTarget != "root@203.0.113.9" {
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
	for _, r := range []store.CheckResult{{OK: false, Code: &code, LatencyMS: &ms, Error: "HTTP 500"}, {OK: true, Code: &code, LatencyMS: &ms}} {
		_, _, err := st.RecordCheck(ctx, *e, r)
		ok(t, err)
	}
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
	must(st.WarningEndpoints(ctx))
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

	// Day two threads day one through: k1 is gone (fixed), k2 keeps its
	// first_seen and task, k3 is new; a snooze hides k2 from the counts.
	gameTask := must(st.CreateTask(ctx, store.TaskInput{ProjectKey: "GAME", Title: "fix k2"}))
	id1b := must(st.StartCheckup(ctx, today, "manual"))
	c1 := must(st.FinishCheckup(ctx, id1b, "warn", []store.CheckItem{
		{Key: "k1", Category: "tasks", Severity: "warn", Title: "t"},
		{Key: "k2", Category: "repos", Severity: "fail", Title: "k2 broken", TaskID: &gameTask.ID, TaskRef: &gameTask.Ref},
	}))
	if c1.Status != "fail" || c1.ActionsNew != 2 || c1.Items[1].TaskStatus == "" {
		t.Fatalf("day one = %+v", c1.CheckupSummary)
	}
	tomorrow := "2099-01-02"
	id2 := must(st.StartCheckup(ctx, tomorrow, "schedule"))
	c2 := must(st.FinishCheckup(ctx, id2, "warn", []store.CheckItem{
		{Key: "k2", Category: "repos", Severity: "fail", Title: "k2 broken"},
		{Key: "k3", Category: "tasks", Severity: "warn", Title: "new"},
		{Key: "ok:tasks", Category: "tasks", Severity: "ok", Title: "fine"},
	}))
	byKey := map[string]store.CheckItem{}
	for _, it := range c2.Items {
		byKey[it.Key] = it
	}
	if k2 := byKey["k2"]; k2.FirstSeen != today || k2.TaskID == nil || *k2.TaskID != gameTask.ID || k2.TaskStatus != gameTask.Status {
		t.Fatalf("k2 carry-over = %+v", k2)
	}
	if k3 := byKey["k3"]; k3.FirstSeen != tomorrow {
		t.Fatalf("k3 first_seen = %q", k3.FirstSeen)
	}
	if c2.ActionsNew != 1 || c2.FixedTotal != 1 || c2.Resolved[0].Key != "k1" || c2.Resolved[0].FirstSeen != today {
		t.Fatalf("day two = %+v resolved=%+v", c2.CheckupSummary, c2.Resolved)
	}
	c2 = must(st.SnoozeCheckItem(ctx, id2, "k2", nil))
	if c2.ActionsSnoozed != 1 || c2.ActionsTotal != 1 || c2.Status != "warn" || c2.Counts.Fail != 0 {
		t.Fatalf("snoozed = %+v", c2.CheckupSummary)
	}
	// A re-check that no longer reports k3 resolves it; one that reports k2
	// with a new title leaves it open (snoozed items are not re-checked).
	now := time.Now().UTC().Truncate(time.Second)
	c2 = must(st.ApplyRecheck(ctx, id2, []string{"k3"}, map[string]store.CheckItem{}, now))
	for _, it := range c2.Items {
		if it.Key == "k3" && (it.ResolvedAt == nil || it.CheckedAt == nil) {
			t.Fatalf("k3 not resolved: %+v", it)
		}
	}
	if c2.ActionsDone != 1 {
		t.Fatalf("resolved counts as done: %+v", c2.CheckupSummary)
	}
	// Day three: the snooze "until it changes" survives an unchanged title and
	// ends when the title changes.
	id3 := must(st.StartCheckup(ctx, "2099-01-03", "schedule"))
	c3 := must(st.FinishCheckup(ctx, id3, "ok", []store.CheckItem{{Key: "k2", Category: "repos", Severity: "fail", Title: "k2 broken"}}))
	if !c3.Items[0].Snoozed || c3.Status != "ok" {
		t.Fatalf("snooze should carry: %+v", c3.Items[0])
	}
	id4 := must(st.StartCheckup(ctx, "2099-01-04", "schedule"))
	c4 := must(st.FinishCheckup(ctx, id4, "ok", []store.CheckItem{{Key: "k2", Category: "repos", Severity: "fail", Title: "k2 broken differently"}}))
	if c4.Items[0].Snoozed || c4.Status != "fail" || c4.Items[0].FirstSeen != today {
		t.Fatalf("snooze should end on change: %+v", c4.Items[0])
	}
	must(st.UnsnoozeCheckItem(ctx, id4, "k2"))

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
	must(st.Heartbeat(ctx, rn.ID, store.HeartbeatInput{Hostname: "desk", Running: []int64{},
		Capabilities: store.RunnerCapabilities{Terminal: true, PermissionModes: []string{"plan", "bypassPermissions"}}}))
	ok(t, st.FailOrphanedRuns(ctx))
	if hosts := must(st.TerminalHosts(ctx)); len(hosts) != 1 || !hosts[0].Terminal || !hosts[0].SkipPermissions {
		t.Fatalf("terminal hosts = %+v", hosts)
	}
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

// A pull reaches the feed once, however many scans report the same sync.
func TestRepoSyncActivity(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	p := must(st.ProjectByKey(ctx, "2026-01-01", "SHOP"))
	repo := p.Repos[0]
	sync := &store.SyncStatus{At: "2026-09-28T10:00:00Z", Result: "pulled", Pulled: 3}
	for i := 0; i < 3; i++ {
		ok(t, st.SaveRepoScans(ctx, "desk", []store.RepoScan{{RepoID: repo.ID, GitStatus: store.GitStatus{Branch: "main", Sync: sync}}}))
	}
	sync2 := &store.SyncStatus{At: "2026-09-28T10:30:00Z", Result: "pulled", Pulled: 1}
	ok(t, st.SaveRepoScans(ctx, "desk", []store.RepoScan{{RepoID: repo.ID, GitStatus: store.GitStatus{Branch: "main", Sync: sync2}}}))
	var n int
	ok(t, st.DB.QueryRow(ctx, `SELECT count(*) FROM activity WHERE kind = 'repo.pulled'`).Scan(&n))
	if n != 2 {
		t.Fatalf("repo.pulled entries = %d, want 2", n)
	}
	got := must(st.RepoByID(ctx, repo.ID))
	if got.Git == nil || got.Git.Sync == nil || got.Git.Sync.Pulled != 1 {
		t.Fatalf("stored sync = %+v", got.Git)
	}
}

func TestProviderSettingsAndLegacyModels(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	p := must(st.ProviderSettings(ctx))
	if p.BaseURL != store.DefaultProviderURL || p.AnthropicURL() != "https://openrouter.ai/api" {
		t.Fatalf("provider = %+v (%s)", p, p.AnthropicURL())
	}
	// Defaults: Claude everywhere, DeepSeek models as OpenRouter ids.
	a := must(st.AssistantSettings(ctx))
	e := must(st.EngineSettings(ctx))
	if a.Engine != "claude" || a.Model != store.DefaultModel || a.BaseURL != store.DefaultProviderURL ||
		e.Default != "claude" || e.Model != store.DefaultModel || e.HeavyModel != store.DefaultHeavyModel {
		t.Fatalf("defaults: %+v %+v", a, e)
	}
	// Settings saved before OpenRouter (DeepSeek's own names and URL) read as OpenRouter ones.
	ok(t, st.SetAssistantSettings(ctx, store.AssistantSettings{Enabled: true, BaseURL: "https://api.deepseek.com", Model: "deepseek-flash"}))
	ok(t, st.SetEngineSettings(ctx, store.EngineSettings{Default: "deepseek", Model: "deepseek-flash", HeavyModel: "deepseek-v4-pro"}))
	ok(t, st.SetJevSettings(ctx, store.JevSettings{Enabled: true, Model: "jev-latest"}))
	a, e = must(st.AssistantSettings(ctx)), must(st.EngineSettings(ctx))
	j := must(st.JevSettings(ctx))
	if a.Engine != "claude" || a.Model != store.DefaultModel || a.BaseURL != store.DefaultProviderURL ||
		e.Model != store.DefaultModel || e.HeavyModel != store.DefaultHeavyModel || j.Model != store.DefaultJevModel {
		t.Fatalf("legacy: %+v %+v %+v", a, e, j)
	}
	ok(t, st.SetProviderSettings(ctx, store.ProviderSettings{BaseURL: "https://gateway.example.com/v1/"}))
	if p := must(st.ProviderSettings(ctx)); p.AnthropicURL() != "https://gateway.example.com" {
		t.Fatalf("anthropic url = %s", p.AnthropicURL())
	}
	if store.ModelID("vendor/other") != "vendor/other" {
		t.Fatal("ModelID changed an unknown model")
	}
}
