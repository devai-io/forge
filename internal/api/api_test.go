package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/devai-io/forge/internal/auth"
	"github.com/devai-io/forge/internal/checkup"
	"github.com/devai-io/forge/internal/config"
	"github.com/devai-io/forge/internal/db"
	"github.com/devai-io/forge/internal/mail"
	"github.com/devai-io/forge/internal/monitor"
	"github.com/devai-io/forge/internal/monitoring"
	"github.com/devai-io/forge/internal/seed"
	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/vault"
)

// These tests drive the real router against a real SQLite database (a fresh
// file per test), because the interesting failures here are SQL ones
// (ambiguous columns, CHECKs, the seed file).

const testPassword = "correct horse battery staple"

type harness struct {
	t      *testing.T
	srv    *httptest.Server
	store  *store.Store
	client *http.Client
	home   string
	api    *Server
}

func setup(t *testing.T) *harness { return setupWith(t, true) }

// setupWith starts a server on a fresh database with the demo data, and
// with the account "ada" unless withUser is false (a first run).
func setupWith(t *testing.T, withUser bool) *harness {
	t.Helper()
	ctx := context.Background()
	home := t.TempDir()
	pool, err := db.Open(ctx, filepath.Join(home, "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	if seeded, err := st.SeedIfEmpty(ctx, seed.Demo); err != nil || !seeded {
		t.Fatalf("seed: %v %v", seeded, err)
	}
	if withUser {
		hash, _ := auth.HashPassword(testPassword)
		if _, err := st.CreateUser(ctx, "ada", "ada@example.com", "Ada", hash, "Europe/Lisbon", true); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewUnstartedServer(nil)
	cfg := config.Config{Home: home, PublicURL: "http://" + srv.Listener.Addr().String(), Version: "test",
		MonitorInterval: time.Minute}
	box, _ := vault.New(bytes.Repeat([]byte{7}, 32))
	fleet := monitoring.New(monitoring.Config{}, MonitoringSources{Store: st, Box: box})
	mailer := mail.New(config.SMTP{})
	api := New(cfg, st, mailer, monitor.New(st, time.Minute, "test"),
		Deps{Box: box, Mon: fleet, Checkups: checkup.New(st, fleet, mailer, cfg.PublicURL)})
	api.claimWait = 500 * time.Millisecond
	srv.Config.Handler = api.Handler()
	srv.Start()
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &harness{t: t, srv: srv, store: st, client: &http.Client{Jar: jar}, home: home, api: api}
}

// do sends a browser-style request (CSRF header on writes) and decodes JSON.
func (h *harness) do(method, path string, body any, out any, headers ...string) int {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if method != http.MethodGet {
		req.Header.Set(csrfHeader, "web")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			req.Header.Del(headers[i])
		} else {
			req.Header.Set(headers[i], headers[i+1])
		}
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("%s %s: %v in %s", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

func (h *harness) login() {
	h.t.Helper()
	if code := h.do("POST", "/api/auth/login", map[string]string{"username": "ada", "password": testPassword}, nil); code != 200 {
		h.t.Fatalf("login: %d", code)
	}
}

// newRunner adds a machine the way the Agents page does and pairs it the way
// `forge agent pair` does, returning the HTTP status of the first step.
func (h *harness) newRunner(name string, rn *store.Runner, token *string) int {
	h.t.Helper()
	var created struct {
		Runner  store.Runner
		Pairing struct{ Code string }
	}
	if code := h.do("POST", "/api/runners", map[string]string{"name": name}, &created); code != 201 {
		return code
	}
	var paired struct{ Token string }
	machine := &harness{t: h.t, srv: h.srv, client: &http.Client{}}
	if code := machine.do("POST", "/api/runner/pair", map[string]string{"code": created.Pairing.Code}, &paired); code != 200 {
		h.t.Fatalf("pair %s: HTTP %d", name, code)
	}
	*rn, *token = created.Runner, paired.Token
	return 201
}

func expect(t *testing.T, what string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: HTTP %d, want %d", what, got, want)
	}
}

func TestAuthGuards(t *testing.T) {
	h := setup(t)
	expect(t, "anonymous dashboard", h.do("GET", "/api/dashboard", nil, nil), 401)
	expect(t, "login without CSRF header",
		h.do("POST", "/api/auth/login", map[string]string{"username": "ada", "password": testPassword}, nil, csrfHeader, ""), 403)
	expect(t, "login from another origin",
		h.do("POST", "/api/auth/login", map[string]string{"username": "ada", "password": testPassword}, nil, "Origin", "https://evil.example"), 403)
	expect(t, "wrong password", h.do("POST", "/api/auth/login", map[string]string{"username": "ada", "password": "nope-nope-nope"}, nil), 401)
	expect(t, "e-mail works as login", h.do("POST", "/api/auth/login", map[string]string{"username": "ADA@example.com", "password": testPassword}, nil), 200)

	var me struct{ User store.User }
	expect(t, "me", h.do("GET", "/api/auth/me", nil, &me), 200)
	if me.User.Username != "ada" {
		t.Fatalf("me = %+v", me.User)
	}
	expect(t, "write without CSRF header", h.do("POST", "/api/tasks", map[string]string{"project_key": "GAME", "title": "x"}, nil, csrfHeader, ""), 403)

	// A runner token is not a browser credential.
	var created struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "create runner", h.newRunner("box", &created.Runner, &created.Token), 201)
	anon := &harness{t: t, srv: h.srv, client: &http.Client{}}
	expect(t, "runner token on a browser route", anon.do("GET", "/api/dashboard", nil, nil, "Authorization", "Bearer "+created.Token), 401)
	expect(t, "session on a runner route", h.do("POST", "/api/runner/heartbeat", map[string]any{}, nil), 401)

	expect(t, "logout", h.do("POST", "/api/auth/logout", nil, nil), 204)
	expect(t, "after logout", h.do("GET", "/api/dashboard", nil, nil), 401)
}

func TestLoginRateLimit(t *testing.T) {
	h := setup(t)
	for i := 0; i < 8; i++ {
		expect(t, "wrong password", h.do("POST", "/api/auth/login", map[string]string{"username": "ada", "password": "wrong-wrong-wrong"}, nil), 401)
	}
	expect(t, "ninth attempt", h.do("POST", "/api/auth/login", map[string]string{"username": "ada", "password": testPassword}, nil), 429)
}

func TestPasswordChangeRevokesOtherSessions(t *testing.T) {
	h := setup(t)
	h.login()
	other := &harness{t: t, srv: h.srv, client: &http.Client{Jar: mustJar()}}
	other.login()

	expect(t, "weak new password", h.do("POST", "/api/auth/password",
		map[string]string{"current_password": testPassword, "new_password": "short"}, nil), 422)
	expect(t, "wrong current password", h.do("POST", "/api/auth/password",
		map[string]string{"current_password": "nope-nope-nope", "new_password": "a much better passphrase"}, nil), 422)
	expect(t, "change", h.do("POST", "/api/auth/password",
		map[string]string{"current_password": testPassword, "new_password": "a much better passphrase"}, nil), 204)
	expect(t, "this session survives", h.do("GET", "/api/auth/me", nil, nil), 200)
	expect(t, "the other one does not", other.do("GET", "/api/auth/me", nil, nil), 401)
}

func mustJar() http.CookieJar {
	j, _ := cookiejar.New(nil)
	return j
}

func TestProjectsTasksAndDashboard(t *testing.T) {
	h := setup(t)
	h.login()

	var ps struct{ Projects []store.Project }
	expect(t, "projects", h.do("GET", "/api/projects", nil, &ps), 200)
	if len(ps.Projects) != 3 || ps.Projects[0].Key != "SHOP" {
		t.Fatalf("seeded projects: %d, first %q", len(ps.Projects), ps.Projects[0].Key)
	}

	var task store.Task
	expect(t, "create task", h.do("POST", "/api/tasks", map[string]any{
		"project_key": "game", "title": "Tune the drift model", "labels": []string{"physics", "physics"},
		"due_date": "2026-01-01"}, &task), 201)
	if !strings.HasPrefix(task.Ref, "GAME-") || len(task.Labels) != 1 || task.Status != "todo" {
		t.Fatalf("task = %+v", task)
	}

	var bad map[string]map[string]string
	expect(t, "invalid status", h.do("PATCH", fmt.Sprintf("/api/tasks/%d", task.ID), map[string]string{"status": "later"}, &bad), 422)
	if bad["error"]["field"] != "status" {
		t.Errorf("error = %v", bad)
	}

	expect(t, "complete", h.do("PATCH", fmt.Sprintf("/api/tasks/%d", task.ID), map[string]string{"status": "done"}, &task), 200)
	if task.CompletedAt == nil {
		t.Fatal("completing must set completed_at")
	}
	expect(t, "reopen", h.do("PATCH", fmt.Sprintf("/api/tasks/%d", task.ID), map[string]any{"status": "in_progress", "sort_order": 1.5}, &task), 200)
	if task.CompletedAt != nil || task.SortOrder != 1.5 {
		t.Fatalf("reopen: %+v", task)
	}
	expect(t, "done again", h.do("PATCH", fmt.Sprintf("/api/tasks/%d", task.ID), map[string]string{"status": "done"}, &task), 200)

	var d store.Dashboard
	expect(t, "dashboard", h.do("GET", "/api/dashboard", nil, &d), 200)
	if d.Stats.DoneToday != 1 || d.Stats.StreakDays < 1 || len(d.Daily) != 28 || len(d.Projects) != 3 {
		t.Fatalf("dashboard stats = %+v, daily %d", d.Stats, len(d.Daily))
	}
	if d.Activity[0].Kind != "task.done" {
		t.Errorf("latest activity = %+v", d.Activity[0])
	}

	var moved store.Task
	expect(t, "move project", h.do("PATCH", fmt.Sprintf("/api/tasks/%d", task.ID), map[string]string{"project_key": "BLOG"}, &moved), 200)
	if moved.ProjectKey != "BLOG" || !strings.HasPrefix(moved.Ref, "BLOG-") {
		t.Fatalf("moved = %+v", moved)
	}

	var c store.Comment
	expect(t, "comment", h.do("POST", fmt.Sprintf("/api/tasks/%d/comments", task.ID), map[string]string{"body": "tuned"}, &c), 201)
	var detail store.TaskDetail
	expect(t, "detail", h.do("GET", fmt.Sprintf("/api/tasks/%d", task.ID), nil, &detail), 200)
	if len(detail.Comments) != 1 || detail.CommentCount != 1 {
		t.Fatalf("detail = %+v", detail)
	}

	var p store.ProjectDetail
	expect(t, "project detail", h.do("GET", "/api/projects/shop", nil, &p), 200)
	if len(p.Repos) == 0 || len(p.Servers) == 0 {
		t.Fatalf("SHOP should have seeded repos and servers: %+v", p)
	}
	expect(t, "duplicate key", h.do("POST", "/api/projects", map[string]string{"key": "SHOP", "name": "Again"}, nil), 409)
	expect(t, "bad key", h.do("POST", "/api/projects", map[string]string{"key": "a", "name": "x"}, nil), 422)
	expect(t, "unknown route", h.do("GET", "/api/nope", nil, nil), 404)
}

func TestRunnerProtocol(t *testing.T) {
	h := setup(t)
	h.login()

	var created struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "create runner", h.newRunner("desktop", &created.Runner, &created.Token), 201)
	runner := &harness{t: t, srv: h.srv, client: &http.Client{}}
	bearer := []string{"Authorization", "Bearer " + created.Token}

	var hb struct {
		RunnerID int64 `json:"runner_id"`
		Cancel   []int64
		Repos    []store.RunnerRepo
	}
	expect(t, "heartbeat", runner.do("POST", "/api/runner/heartbeat", map[string]any{
		"hostname": "desk", "os": "linux/amd64", "version": "test",
		"capabilities": map[string]any{"claude": true, "permission_modes": []string{"plan"}, "commands": []string{"test"}, "max_concurrent": 1},
		"running":      []int64{},
	}, &hb, bearer...), 200)
	if hb.RunnerID != created.Runner.ID || len(hb.Repos) == 0 {
		t.Fatalf("heartbeat = %+v", hb)
	}
	repoID := hb.Repos[0].ID

	expect(t, "mode the runner does not allow", h.do("POST", "/api/runs", map[string]any{
		"runner_id": created.Runner.ID, "repo_id": repoID, "kind": "agent", "prompt": "hi", "permission_mode": "bypassPermissions"}, nil), 422)
	expect(t, "command the runner does not have", h.do("POST", "/api/runs", map[string]any{
		"runner_id": created.Runner.ID, "repo_id": repoID, "kind": "command", "command": "rm -rf /"}, nil), 422)

	var run store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{
		"runner_id": created.Runner.ID, "repo_id": repoID, "kind": "agent", "prompt": "Summarise the repo"}, &run), 201)
	if run.Status != "queued" || run.PermissionMode != "plan" || run.RepoPath != "" {
		t.Fatalf("queued run = %+v (repo_path must not reach the browser)", run)
	}

	var claimed struct{ Run store.Run }
	expect(t, "claim", runner.do("POST", "/api/runner/claim", nil, &claimed, bearer...), 200)
	if claimed.Run.ID != run.ID || claimed.Run.Status != "running" || claimed.Run.RepoPath == "" {
		t.Fatalf("claimed = %+v", claimed.Run)
	}

	expect(t, "events", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/events", run.ID), map[string]any{"events": []map[string]any{
		{"kind": "system", "data": map[string]string{"text": "starting"}},
		{"kind": "claude", "data": map[string]any{"type": "system", "subtype": "init", "session_id": "s1"}},
		{"kind": "claude", "data": map[string]any{"type": "result", "result": "All good"}},
	}}, nil, bearer...), 204)

	var ev struct {
		Events []store.RunEvent
		Run    store.Run
	}
	expect(t, "list events", h.do("GET", fmt.Sprintf("/api/runs/%d/events?after=1", run.ID), nil, &ev), 200)
	if len(ev.Events) != 2 || ev.Events[0].Seq != 2 || ev.Run.Status != "running" {
		t.Fatalf("events = %+v", ev)
	}

	cost := 0.25
	expect(t, "finish", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", run.ID), map[string]any{
		"status": "succeeded", "exit_code": 0, "session_id": "s1", "result": "All good", "cost_usd": cost}, nil, bearer...), 204)
	expect(t, "run", h.do("GET", fmt.Sprintf("/api/runs/%d", run.ID), nil, &run), 200)
	if run.Status != "succeeded" || run.SessionID != "s1" || run.CostUSD == nil || *run.CostUSD != cost {
		t.Fatalf("finished run = %+v", run)
	}
	expect(t, "finish twice", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", run.ID), map[string]any{"status": "failed"}, nil, bearer...), 404)

	var cont store.Run
	expect(t, "continue", h.do("POST", "/api/runs", map[string]any{"resume_run_id": run.ID, "prompt": "and now?"}, &cont), 201)
	if cont.RunnerID != created.Runner.ID || cont.RepoID != repoID || cont.ResumeRunID == nil {
		t.Fatalf("continued = %+v", cont)
	}
	expect(t, "cancel queued", h.do("POST", fmt.Sprintf("/api/runs/%d/cancel", cont.ID), nil, &cont), 200)
	if cont.Status != "cancelled" {
		t.Fatalf("cancelled = %+v", cont)
	}
	expect(t, "nothing left to claim", runner.do("POST", "/api/runner/claim", nil, nil, bearer...), 204)

	expect(t, "repo scan", runner.do("POST", "/api/runner/repos", map[string]any{"repos": []map[string]any{
		{"repo_id": repoID, "branch": "main", "dirty": 2, "ahead": 1, "behind": 0, "commits_7d": 4, "last_commit_at": "2026-09-27T10:00:00Z", "error": ""}}}, nil, bearer...), 204)
	var projects struct{ Projects []store.Project }
	expect(t, "projects", h.do("GET", "/api/projects", nil, &projects), 200)
	var commits int
	for _, p := range projects.Projects {
		commits += p.Stats.Commits7d
	}
	if commits != 4 {
		t.Errorf("commits_7d across projects = %d, want 4", commits)
	}
}

func TestVaultNeedsElevationAndRoundTrips(t *testing.T) {
	h := setup(t)
	h.login()

	var item store.VaultItem
	expect(t, "create", h.do("POST", "/api/vault", map[string]any{
		"name": "Shop — Play upload keystore", "kind": "keystore", "project_key": "SHOP", "platform": "android",
		"identifier": "upload", "fields": map[string]string{"alias": "upload"},
		"secret":     map[string]string{"store_password": "s3cret-store", "key_password": "s3cret-key"},
		"file":       map[string]string{"name": "release.keystore", "content_base64": "AAECAwQ="},
		"expires_at": "2026-10-01", "tags": []string{"store"}}, &item), 201)
	if item.ProjectKey == nil || *item.ProjectKey != "SHOP" || !item.HasFile || item.FileSize != 5 ||
		len(item.SecretKeys) != 2 || item.SecretKeys[0] != "key_password" {
		t.Fatalf("item = %+v", item)
	}

	// Listing never carries values.
	var raw map[string]any
	expect(t, "list", h.do("GET", "/api/vault", nil, &raw), 200)
	if b, _ := json.Marshal(raw); strings.Contains(string(b), "s3cret") {
		t.Fatal("a secret value leaked into the list")
	}

	var e map[string]map[string]string
	expect(t, "reveal without elevation", h.do("POST", fmt.Sprintf("/api/vault/%d/reveal", item.ID), nil, &e), 403)
	if e["error"]["code"] != "elevation_required" {
		t.Fatalf("error = %v", e)
	}
	expect(t, "elevate with a wrong password", h.do("POST", "/api/auth/elevate", map[string]string{"password": "nope-nope-nope"}, nil), 422)
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)

	var rev struct{ Secret map[string]string }
	expect(t, "reveal", h.do("POST", fmt.Sprintf("/api/vault/%d/reveal", item.ID), nil, &rev), 200)
	if rev.Secret["store_password"] != "s3cret-store" {
		t.Fatalf("revealed = %v", rev.Secret)
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/vault/%d/file", h.srv.URL, item.ID), nil)
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(data, []byte{0, 1, 2, 3, 4}) ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "release.keystore") {
		t.Fatalf("download: %d %v %q", resp.StatusCode, data, resp.Header.Get("Content-Disposition"))
	}

	// Ciphertext at rest, bound to its row.
	var sealed []byte
	if err := h.store.DB.QueryRow(context.Background(), `SELECT secret_sealed FROM vault_items WHERE id = $1`, item.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("s3cret")) {
		t.Fatal("secret stored in plaintext")
	}

	expect(t, "patch secret", h.do("PATCH", fmt.Sprintf("/api/vault/%d", item.ID), map[string]any{
		"secret": map[string]string{"store_password": "rotated"}, "file": nil}, &item), 200)
	if item.HasFile || len(item.SecretKeys) != 1 {
		t.Fatalf("patched = %+v", item)
	}
	var audit struct{ Events []store.VaultAudit }
	expect(t, "audit", h.do("GET", "/api/vault/audit", nil, &audit), 200)
	if len(audit.Events) < 4 || audit.Events[0].Action != "update" {
		t.Fatalf("audit = %+v", audit.Events)
	}
	expect(t, "delete", h.do("DELETE", fmt.Sprintf("/api/vault/%d", item.ID), nil, nil), 204)
}

func TestTwoFactorLogin(t *testing.T) {
	h := setup(t)
	h.login()
	expect(t, "setup needs elevation", h.do("POST", "/api/auth/totp/setup", nil, nil), 403)
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)
	var setup struct {
		Secret     string
		OtpauthURL string `json:"otpauth_url"`
	}
	expect(t, "setup", h.do("POST", "/api/auth/totp/setup", nil, &setup), 200)
	if !strings.HasPrefix(setup.OtpauthURL, "otpauth://totp/Forge") {
		t.Fatalf("setup = %+v", setup)
	}
	expect(t, "enable with a wrong code", h.do("POST", "/api/auth/totp/enable", map[string]string{"code": "000000"}, nil), 422)
	code, _ := auth.TOTPCode(setup.Secret, time.Now())
	expect(t, "enable", h.do("POST", "/api/auth/totp/enable", map[string]string{"code": code}, nil), 204)

	fresh := &harness{t: t, srv: h.srv, client: &http.Client{Jar: mustJar()}}
	var e map[string]map[string]string
	expect(t, "password only", fresh.do("POST", "/api/auth/login", map[string]string{"username": "ada", "password": testPassword}, &e), 401)
	if e["error"]["code"] != "totp_required" {
		t.Fatalf("error = %v", e)
	}
	expect(t, "replayed enrolment code", fresh.do("POST", "/api/auth/login",
		map[string]string{"username": "ada", "password": testPassword, "code": code}, nil), 401)
	next, _ := auth.TOTPCode(setup.Secret, time.Now().Add(30*time.Second))
	expect(t, "next code", fresh.do("POST", "/api/auth/login",
		map[string]string{"username": "ada", "password": testPassword, "code": next}, nil), 200)
}

func TestCheckupAndItemTask(t *testing.T) {
	h := setup(t)
	h.login()
	// An overdue task guarantees at least one action.
	expect(t, "task", h.do("POST", "/api/tasks", map[string]any{"project_key": "GAME", "title": "Late thing", "due_date": "2020-01-01"}, nil), 201)

	var c store.Checkup
	expect(t, "run", h.do("POST", "/api/checkups", nil, &c), 201)
	if c.FinishedAt == nil || c.ActionsTotal == 0 || c.Status == "ok" {
		t.Fatalf("checkup = %+v", c.CheckupSummary)
	}
	var target *store.CheckItem
	for i := range c.Items {
		if strings.HasPrefix(c.Items[i].Key, "task:") {
			target = &c.Items[i]
		}
	}
	if target == nil {
		t.Fatalf("no overdue item in %+v", c.Items)
	}
	expect(t, "tick", h.do("PATCH", fmt.Sprintf("/api/checkups/%d/items/%s", c.ID, target.Key), map[string]bool{"done": true}, &c), 200)
	if c.ActionsDone != 1 {
		t.Fatalf("done = %d", c.ActionsDone)
	}
	var made struct {
		Task    store.Task
		Checkup store.Checkup
	}
	expect(t, "make task", h.do("POST", fmt.Sprintf("/api/checkups/%d/items/%s/task", c.ID, target.Key), map[string]string{}, &made), 201)
	if made.Task.ProjectKey != "GAME" || made.Task.Labels[0] != "checkup" {
		t.Fatalf("task = %+v", made.Task)
	}
	var d store.Dashboard
	expect(t, "dashboard", h.do("GET", "/api/dashboard", nil, &d), 200)
	if d.Checkup == nil || d.Checkup.ID != c.ID {
		t.Fatalf("dashboard checkup = %+v", d.Checkup)
	}
}

func TestScopedAndConfirmedCommands(t *testing.T) {
	h := setup(t)
	h.login()
	var created struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "runner", h.newRunner("mac", &created.Runner, &created.Token), 201)
	runner := &harness{t: t, srv: h.srv, client: &http.Client{}}
	var hb struct{ Repos []store.RunnerRepo }
	expect(t, "heartbeat", runner.do("POST", "/api/runner/heartbeat", map[string]any{
		"capabilities": map[string]any{"commands": []string{"release-ios", "git-status"},
			"command_details": []map[string]any{
				{"name": "release-ios", "description": "TestFlight", "repos": []string{"shop_mobile"}, "confirm": true},
				{"name": "git-status", "repos": []string{}}}},
	}, &hb, "Authorization", "Bearer "+created.Token), 200)
	var mobile, other int64
	for _, r := range hb.Repos {
		if r.Name == "shop_mobile" {
			mobile = r.ID
		} else if other == 0 {
			other = r.ID
		}
	}
	run := func(repo int64, confirmed bool) int {
		return h.do("POST", "/api/runs", map[string]any{"runner_id": created.Runner.ID, "repo_id": repo, "kind": "command",
			"command": "release-ios", "confirmed": confirmed}, nil)
	}
	expect(t, "wrong repo", run(other, true), 422)
	expect(t, "unconfirmed", run(mobile, false), 422)
	expect(t, "confirmed", run(mobile, true), 201)
}

// TestTerminalRelay plays a runner (control + tty sockets) and a browser, and
// checks bytes flow both ways through the API, plus a capture round-trip.
func TestTerminalRelay(t *testing.T) {
	h := setup(t)
	h.login()
	var created struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "runner", h.newRunner("desk", &created.Runner, &created.Token), 201)
	bearer := []string{"Authorization", "Bearer " + created.Token}
	runner := &harness{t: t, srv: h.srv, client: &http.Client{}}
	expect(t, "heartbeat", runner.do("POST", "/api/runner/heartbeat", map[string]any{
		"capabilities": map[string]any{"terminal": true},
		"tmux": []map[string]any{{"name": "Shop", "windows": 2, "attached": 0, "path": "/home/demo/dev/shop",
			"command": "claude", "claude": true, "window_list": []map[string]any{
				{"index": 0, "name": "api", "active": true, "command": "claude", "claude": true, "path": "/home/demo/dev/shop/shop_api/pkg"},
				{"index": 1, "name": "zsh", "command": "zsh", "path": "/tmp"}}}},
	}, nil, bearer...), 200)

	var hosts struct {
		Hosts           []store.TerminalHost
		DefaultRunnerID *int64 `json:"default_runner_id"`
	}
	expect(t, "hosts", h.do("GET", "/api/terminal/hosts", nil, &hosts), 200)
	s := hosts.Hosts[0].Sessions[0]
	if hosts.DefaultRunnerID == nil || *hosts.DefaultRunnerID != created.Runner.ID || s.ProjectKey == nil || *s.ProjectKey != "SHOP" ||
		s.WindowList[0].RepoName == nil || *s.WindowList[0].RepoName != "shop_api" || s.WindowList[1].ProjectKey != nil {
		t.Fatalf("hosts = %+v", hosts)
	}
	if hosts.Hosts[0].Online {
		t.Error("no control socket yet: must not count as online")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsBase := "ws" + strings.TrimPrefix(h.srv.URL, "http")
	hdr := http.Header{"Authorization": {"Bearer " + created.Token}}
	control, _, err := websocket.Dial(ctx, wsBase+"/api/runner/control", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer control.CloseNow()

	// The fake runner: answers capture, and for open dials the tty and echoes.
	go func() {
		for {
			_, data, err := control.Read(ctx)
			if err != nil {
				return
			}
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			switch m["type"] {
			case "capture":
				raw, _ := json.Marshal(map[string]any{"type": "result", "id": m["id"], "ok": true, "text": "claude> hello"})
				_ = control.Write(ctx, websocket.MessageText, raw)
			case "open":
				go func() {
					tty, _, err := websocket.Dial(ctx, wsBase+"/api/runner/tty/"+m["channel"].(string), &websocket.DialOptions{HTTPHeader: hdr})
					if err != nil {
						return
					}
					defer tty.CloseNow()
					for {
						typ, b, err := tty.Read(ctx)
						if err != nil {
							return
						}
						_ = tty.Write(ctx, typ, append([]byte("echo:"), b...))
					}
				}()
			}
		}
	}()
	time.Sleep(200 * time.Millisecond)

	expect(t, "capture needs elevation", h.do("GET", fmt.Sprintf("/api/terminal/%d/sessions/Shop/capture", created.Runner.ID), nil, nil), 403)
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)
	var cap struct{ Text string }
	expect(t, "capture", h.do("GET", fmt.Sprintf("/api/terminal/%d/sessions/Shop/capture", created.Runner.ID), nil, &cap), 200)
	if cap.Text != "claude> hello" {
		t.Fatalf("capture = %q", cap.Text)
	}

	u, _ := url.Parse(h.srv.URL)
	cookies := h.client.Jar.Cookies(u)
	bh := http.Header{"Origin": {h.srv.URL}}
	for _, c := range cookies {
		bh.Add("Cookie", c.Name+"="+c.Value)
	}
	attach := fmt.Sprintf("%s/api/terminal/%d/attach?session=Shop&cols=100&rows=30", wsBase, created.Runner.ID)
	if _, resp, err := websocket.Dial(ctx, attach, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://evil.example"}, "Cookie": bh["Cookie"]}}); err == nil || resp.StatusCode != 403 {
		t.Fatal("a foreign Origin must be refused")
	}
	browser, _, err := websocket.Dial(ctx, attach, &websocket.DialOptions{HTTPHeader: bh})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.CloseNow()
	if err := browser.Write(ctx, websocket.MessageBinary, []byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	typ, got, err := browser.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(got) != "echo:ls\r" {
		t.Fatalf("relay: %v %q %v", typ, got, err)
	}
}

func TestContextForPath(t *testing.T) {
	h := setup(t)
	c, err := h.store.ContextForPath(context.Background(), "/home/demo/dev/shop/shop_ui/src/pages", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.ProjectKey != "SHOP" || c.RepoName == nil || *c.RepoName != "shop_ui" ||
		!strings.Contains(c.Markdown, "## Open tasks") || !strings.Contains(c.Markdown, "forge_update_task") {
		t.Fatalf("context = %+v\n%s", c, c.Markdown)
	}
	if _, err := h.store.ContextForPath(context.Background(), "/tmp/elsewhere", nil); err != store.ErrNotFound {
		t.Fatalf("unknown path: %v", err)
	}
}

func TestMasterRunnerAndCodeProxy(t *testing.T) {
	h := setup(t)
	h.login()
	type created struct {
		Runner store.Runner
		Token  string
	}
	var master, laptop created
	expect(t, "master", h.newRunner("desk", &master.Runner, &master.Token), 201)
	expect(t, "laptop", h.newRunner("laptop", &laptop.Runner, &laptop.Token), 201)
	var rn store.Runner
	expect(t, "make master", h.do("PATCH", fmt.Sprintf("/api/runners/%d", master.Runner.ID), map[string]string{"role": "master"}, &rn), 200)
	if rn.Role != "master" {
		t.Fatalf("role = %q", rn.Role)
	}

	// A fake VS Code gateway: checks the secret, echoes what reached it.
	sum := sha256.Sum256([]byte(master.Token))
	secret := hex.EncodeToString(sum[:])
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forge-Gateway") != secret {
			http.Error(w, "forbidden", 403)
			return
		}
		fmt.Fprintf(w, "vscode path=%s host=%s cookie=%q", r.URL.RequestURI(), r.Host, r.Header.Get("Cookie"))
	}))
	defer gw.Close()

	runner := func(c created) *harness { return &harness{t: t, srv: h.srv, client: &http.Client{}} }
	var hb struct {
		Role  string
		Scan  bool
		Repos []store.RunnerRepo
	}
	expect(t, "master heartbeat", runner(master).do("POST", "/api/runner/heartbeat", map[string]any{
		"capabilities": map[string]any{"code": true, "code_gateway": gw.URL, "terminal": true}}, &hb, "Authorization", "Bearer "+master.Token), 200)
	if hb.Role != "master" || !hb.Scan {
		t.Fatalf("master heartbeat = %+v", hb)
	}
	expect(t, "laptop heartbeat", runner(laptop).do("POST", "/api/runner/heartbeat", map[string]any{}, &hb, "Authorization", "Bearer "+laptop.Token), 200)
	if hb.Scan {
		t.Fatal("a worker must be told not to scan")
	}
	repoID := hb.Repos[0].ID
	scan := func(c created, branch string) {
		expect(t, "scan", runner(c).do("POST", "/api/runner/repos", map[string]any{"repos": []map[string]any{
			{"repo_id": repoID, "branch": branch, "dirty": 0}}}, nil, "Authorization", "Bearer "+c.Token), 204)
	}
	scan(master, "feat/x")
	scan(laptop, "main")
	repo, err := h.store.RepoByID(context.Background(), repoID)
	if err != nil || repo.Git == nil || repo.Git.Branch != "feat/x" || repo.Git.RunnerName != "desk" {
		t.Fatalf("the worker's scan must not overwrite the master's: %+v", repo.Git)
	}

	// The fake master answers VS Code workspace requests over control.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	wsBase := "ws" + strings.TrimPrefix(h.srv.URL, "http")
	control, _, err := websocket.Dial(ctx, wsBase+"/api/runner/control", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + master.Token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer control.CloseNow()
	go func() {
		for {
			_, data, err := control.Read(ctx)
			if err != nil {
				return
			}
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			raw, _ := json.Marshal(map[string]any{"type": "result", "id": m["id"], "ok": true, "text": "/ws/" + fmt.Sprint(m["name"]) + ".code-workspace"})
			_ = control.Write(ctx, websocket.MessageText, raw)
		}
	}()
	time.Sleep(200 * time.Millisecond)

	var status struct {
		Available bool
		Reason    string
	}
	expect(t, "status", h.do("GET", "/api/code/status", nil, &status), 200)
	if !status.Available {
		t.Fatalf("status = %+v", status)
	}
	expect(t, "proxy without the code cookie", h.do("GET", "/code/", nil, nil), 403)
	expect(t, "open needs elevation", h.do("POST", "/api/code/open", map[string]string{"project_key": "SHOP"}, nil), 403)
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)
	var opened struct{ URL string }
	expect(t, "open", h.do("POST", "/api/code/open", map[string]string{"project_key": "SHOP"}, &opened), 200)
	if !strings.HasPrefix(opened.URL, "/code/?workspace=") || !strings.Contains(opened.URL, "SHOP.code-workspace") {
		t.Fatalf("url = %q", opened.URL)
	}
	resp, err := h.client.Get(h.srv.URL + "/code/stable-x/static/app.js?v=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "path=/code/stable-x/static/app.js?v=1") || !strings.Contains(string(body), "host="+strings.TrimPrefix(h.srv.URL, "http://")) ||
		strings.Contains(string(body), "forge_session") || strings.Contains(string(body), "forge_code") {
		t.Fatalf("proxied: %d %s", resp.StatusCode, body)
	}

	// Signing out ends the editor too.
	expect(t, "logout", h.do("POST", "/api/auth/logout", nil, nil), 204)
	h.login()
	expect(t, "after re-login the old code cookie is gone", h.do("GET", "/code/", nil, nil), 403)
}

func TestSecurityLogAndSystemFacts(t *testing.T) {
	h := setup(t)
	expect(t, "wrong password", h.do("POST", "/api/auth/login", map[string]string{"username": "ada", "password": "nope-nope-nope"}, nil), 401)
	h.login()
	other := &harness{t: t, srv: h.srv, client: &http.Client{Jar: mustJar()}}
	other.login()
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)

	var revoked struct{ Revoked int }
	expect(t, "revoke others", h.do("POST", "/api/auth/sessions/revoke-others", nil, &revoked), 200)
	if revoked.Revoked != 1 {
		t.Fatalf("revoked = %d", revoked.Revoked)
	}
	expect(t, "other session is gone", other.do("GET", "/api/auth/me", nil, nil), 401)

	var log struct{ Events []store.SecurityEvent }
	expect(t, "security log", h.do("GET", "/api/auth/security", nil, &log), 200)
	kinds := map[string]int{}
	for _, e := range log.Events {
		kinds[e.Kind]++
	}
	// The first sign-in from the test client is a new device; the second (same address+browser) is not.
	if kinds["login_failed"] != 1 || kinds["login_new_device"] != 1 || kinds["login"] != 1 || kinds["elevate"] != 1 || kinds["sessions_revoked_others"] != 1 {
		t.Fatalf("kinds = %v", kinds)
	}
	expect(t, "filter", h.do("GET", "/api/auth/security?kind=login_failed", nil, &log), 200)
	if len(log.Events) != 1 || log.Events[0].Detail == "" {
		t.Fatalf("filtered = %+v", log.Events)
	}

	var facts struct {
		Version   string
		Intervals map[string]string
		Features  map[string]any
		Counts    store.SystemCounts
		Checkup   map[string]any
	}
	expect(t, "system", h.do("GET", "/api/system", nil, &facts), 200)
	if facts.Version != "test" || facts.Intervals["endpoint_check"] != "1m0s" || facts.Counts.Projects != 3 ||
		facts.Features["vault"] != true || facts.Checkup["next_at"] == nil {
		t.Fatalf("facts = %+v", facts)
	}
}

func TestCheckupFlagsSecurityPosture(t *testing.T) {
	h := setup(t)
	h.login()
	var c store.Checkup
	expect(t, "run", h.do("POST", "/api/checkups", nil, &c), 201)
	seen := map[string]bool{}
	for _, it := range c.Items {
		if it.Category == "security" {
			seen[it.Key] = true
		}
	}
	if !seen["security:initial-password"] || !seen["security:totp"] {
		t.Fatalf("security items = %v", seen)
	}
}
