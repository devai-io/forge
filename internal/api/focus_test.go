package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/devai-io/forge/internal/store"
)

func TestFocusRulesThenModel(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()

	// The first look computes rules on the spot and leaves it dirty, so the
	// model's version follows.
	var f store.ProjectFocus
	expect(t, "first look", h.do("GET", "/api/projects/shop/focus", nil, &f), 200)
	if f.Source != "rules" || f.Focus.Headline == "" || len(f.Focus.Now) == 0 || !f.Dirty || f.GeneratedAt == nil {
		t.Fatalf("first look = %+v", f)
	}
	expect(t, "unknown project", h.do("GET", "/api/projects/NOPE/focus", nil, nil), 404)

	// Without a key, Recalculate is rules again.
	expect(t, "refresh, no key", h.do("POST", "/api/projects/SHOP/focus/refresh", nil, &f), 200)
	if f.Source != "rules" || f.Dirty || f.Error != "" {
		t.Fatalf("refresh without key = %+v", f)
	}

	// With a key, the model writes it; refs it made up are dropped.
	fake := newFakeLLM(t, map[string]any{"role": "assistant", "content": "```json\n" + `{"headline": "Checkout first",
		"now": [{"title": "Finish the checkout redesign", "why": "In progress", "ref": "SHOP-1", "kind": "task"},
		        {"title": "Invented", "why": "x", "ref": "SHOP-999", "kind": "task"}],
		"next": [], "roadmap": [{"title": "Checkout v2", "status": "active", "refs": ["SHOP-1"]}], "blockers": []}` + "\n```"})
	h.setProvider(fake.srv.URL)
	h.providerKey()
	expect(t, "refresh with model", h.do("POST", "/api/projects/SHOP/focus/refresh", nil, &f), 200)
	if f.Source != "ai" || f.Focus.Headline != "Checkout first" || f.Model == "" || f.Trigger != "recalculated by hand" {
		t.Fatalf("model focus = %+v", f)
	}
	if f.Focus.Now[0].Ref == nil || *f.Focus.Now[0].Ref != "SHOP-1" || f.Focus.Now[1].Ref != nil || f.Tasks["SHOP-1"].ID == 0 {
		t.Fatalf("refs = %+v", f.Focus.Now)
	}
	if len(fake.seen) != 1 || !strings.Contains(fmt.Sprint(fake.seen[0]), "open_tasks") {
		t.Fatalf("the model saw the project state: %v", fake.seen)
	}
	if len(f.LastUsage) != 1 || f.LastUsage[0].Note != "focus" || f.LastUsage[0].Calls != 1 ||
		f.LastUsage[0].OutputTokens != 10 || f.LastUsage[0].CostUSD == nil || len(f.Usage) != 1 {
		t.Fatalf("model call not metered: last %+v, total %+v", f.LastUsage, f.Usage)
	}

	// A model that answers nonsense falls back to rules, saying so.
	fake.steps = []map[string]any{{"role": "assistant", "content": "I cannot help with that."}}
	expect(t, "refresh, bad answer", h.do("POST", "/api/projects/SHOP/focus/refresh", nil, &f), 200)
	if f.Source != "rules" || f.Error == "" {
		t.Fatalf("fallback = %+v", f)
	}
	// The unusable answer was still paid for.
	if len(f.Usage) != 1 || f.Usage[0].Calls != 2 || len(f.LastUsage) != 1 {
		t.Fatalf("usage after a bad answer: total %+v, last %+v", f.Usage, f.LastUsage)
	}
	var spend struct {
		Usage    []struct{ Calls int }
		TotalUSD float64 `json:"total_usd"`
	}
	expect(t, "focus usage", h.do("GET", "/api/focus/usage", nil, &spend), 200)
	if len(spend.Usage) != 1 || spend.Usage[0].Calls != 2 || spend.TotalUSD <= 0 {
		t.Fatalf("focus usage = %+v", spend)
	}

	// A task change marks it dirty; the loop picks it up once settled.
	var list struct{ Tasks []store.Task }
	expect(t, "tasks", h.do("GET", "/api/tasks?project=SHOP", nil, &list), 200)
	tasks := list.Tasks
	expect(t, "update task", h.do("PATCH", fmt.Sprintf("/api/tasks/%d", tasks[0].ID), map[string]any{"priority": "low"}, nil), 200)
	expect(t, "dirty", h.do("GET", "/api/projects/SHOP/focus", nil, &f), 200)
	if !f.Dirty || !strings.Contains(f.DirtyReason, tasks[0].Ref) {
		t.Fatalf("after a task update = %+v", f)
	}
	h.api.focusSettle = time.Hour
	h.api.focusPass(ctx)
	if expect(t, "still dirty", h.do("GET", "/api/projects/SHOP/focus", nil, &f), 200); !f.Dirty {
		t.Fatal("not settled yet: left alone")
	}
	h.api.focusSettle = 0
	fake.steps = []map[string]any{{"role": "assistant", "content": `{"headline": "Recomputed", "now": [{"title": "Go", "why": "now", "kind": "other"}]}`}}
	h.api.focusPass(ctx)
	if expect(t, "recomputed", h.do("GET", "/api/projects/SHOP/focus", nil, &f), 200); f.Dirty || f.Focus.Headline != "Recomputed" ||
		!strings.HasPrefix(f.Trigger, "after "+tasks[0].Ref) {
		t.Fatalf("after the loop = %+v", f)
	}
}

func TestFocusDirtyAfterRunFinishes(t *testing.T) {
	h := setup(t)
	h.login()
	var created struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "create runner", h.newRunner("desktop", &created.Runner, &created.Token), 201)
	runner := &harness{t: t, srv: h.srv, client: &http.Client{}}
	bearer := []string{"Authorization", "Bearer " + created.Token}
	var hb struct{ Repos []store.RunnerRepo }
	expect(t, "heartbeat", runner.do("POST", "/api/runner/heartbeat", map[string]any{
		"hostname": "desk", "os": "linux/amd64", "version": "test",
		"capabilities": map[string]any{"claude": true, "permission_modes": []string{"plan"}},
		"running":      []int64{},
	}, &hb, bearer...), 200)
	var run store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{
		"runner_id": created.Runner.ID, "repo_id": hb.Repos[0].ID, "kind": "agent", "prompt": "Summarise"}, &run), 201)
	var claimed struct{ Run store.Run }
	expect(t, "claim", runner.do("POST", "/api/runner/claim", nil, &claimed, bearer...), 200)

	ctx := context.Background()
	// Start clean: a computed focus, nothing pending.
	var f store.ProjectFocus
	expect(t, "focus", h.do("POST", "/api/projects/"+run.ProjectKey+"/focus/refresh", nil, &f), 200)
	if f.Dirty {
		t.Fatal("clean after refresh")
	}
	expect(t, "finish", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", run.ID),
		map[string]any{"status": "succeeded", "result": "done"}, nil, bearer...), 204)
	due, err := h.store.DirtyFocuses(ctx, 0, 10)
	if err != nil || len(due) != 1 || due[0].ProjectID != run.ProjectID || !strings.Contains(due[0].Reason, "succeeded") {
		t.Fatalf("after the run = %+v %v", due, err)
	}
}
