package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/devai-io/forge/internal/store"
)

func TestSavingsSettingsAndUsage(t *testing.T) {
	h := setup(t)
	expect(t, "private settings", h.do("GET", "/api/savings", nil, nil), 401)
	expect(t, "private usage", h.do("GET", "/api/usage", nil, nil), 401)
	h.login()
	var settings store.SavingsSettings
	expect(t, "defaults", h.do("GET", "/api/savings", nil, &settings), 200)
	if !settings.CompactContext || !settings.EarlySummaries || !settings.BackgroundFocusAI || !settings.ProactiveAI {
		t.Fatalf("defaults: %+v", settings)
	}
	expect(t, "partial settings", h.do("PATCH", "/api/savings", map[string]bool{"background_focus_ai": false}, &settings), 200)
	if settings.BackgroundFocusAI || !settings.CompactContext {
		t.Fatalf("patch: %+v", settings)
	}
	for _, days := range []string{"0", "366", "no"} {
		expect(t, "invalid days", h.do("GET", "/api/usage?days="+days, nil, nil), 422)
	}
	var report struct {
		Groups []store.UsageGroup
		Days   int
	}
	expect(t, "empty usage", h.do("GET", "/api/usage?days=7", nil, &report), 200)
	if report.Days != 7 || len(report.Groups) != 0 {
		t.Fatalf("report: %+v", report)
	}
}

func TestCompactContextAndFullRetrieval(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		if _, err := h.store.CreateTask(ctx, store.TaskInput{ProjectKey: "SHOP", Title: "Extra unrelated chore", Status: "backlog", Priority: "low"}); err != nil {
			t.Fatal(err)
		}
	}
	urgent, err := h.store.CreateTask(ctx, store.TaskInput{ProjectKey: "SHOP", Title: "Urgent operational fix", Status: "todo", Priority: "urgent"})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := h.store.ProjectByKey(ctx, "2026-01-01", "SHOP")
	if err := h.store.SaveFocus(ctx, p.ID, store.Focus{Headline: "Duplicate focus headline"}, "rules", "", "test", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	cwd := "/home/demo/dev/shop/shop_ui"
	compact, err := h.store.ContextForPath(ctx, cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	full, err := h.store.ContextForPathMode(ctx, cwd, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compact.Markdown, urgent.Ref) || strings.Contains(compact.Markdown, "Duplicate focus headline") || !strings.Contains(full.Markdown, "Duplicate focus headline") || len(compact.Markdown) >= len(full.Markdown) {
		t.Fatalf("compact/full:\n%s\n%s", compact.Markdown, full.Markdown)
	}
	if !strings.Contains(compact.Markdown, "Infra & change control") {
		t.Fatal("essential notes missing")
	}
}

func TestFocusReusesInputsAndRulesSwitch(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	fake := newFakeLLM(t, map[string]any{"role": "assistant", "content": `{"headline":"Next task","now":[],"next":[],"roadmap":[],"blockers":[]}`})
	h.setProvider(fake.srv.URL)
	h.providerKey()
	p, _ := h.store.ProjectByKey(ctx, "2026-01-01", "SHOP")
	compute := func(trigger string) *store.ProjectFocus {
		t.Helper()
		f, err := h.api.computeFocus(ctx, p.ID, "2026-01-01", trigger, true)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	if f := compute("after task change"); f.Source != "ai" {
		t.Fatal(f)
	}
	h.store.MarkFocusDirty(ctx, p.ID, "comment")
	f := compute("after comment")
	if f.Dirty || len(fake.seen) != 1 {
		t.Fatalf("unchanged input spent again: %d, %+v", len(fake.seen), f)
	}
	// An explicit refresh is allowed to make a new call even with identical inputs.
	compute("recalculated by hand")
	if len(fake.seen) != 2 {
		t.Fatal("manual refresh was cached")
	}
	settings, _ := h.store.SavingsSettings(ctx)
	settings.BackgroundFocusAI = false
	if err := h.store.SetSavingsSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if f := compute("after changes"); f.Source != "rules" || len(fake.seen) != 2 {
		t.Fatalf("rules switch: %+v", f)
	}
	groups, err := h.store.AIUsage(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Calls != 2 || groups[0].Feature != "focus" || groups[0].Project != "SHOP" || groups[0].InputTokens != 120 || groups[0].CachedTokens != 80 {
		t.Fatalf("meter: %+v", groups)
	}
}

func TestCodexRoutingPreservesExplicitChoices(t *testing.T) {
	h := setup(t)
	h.login()
	srv, calls := fakeJev(t, "light", 0.9, 0, nil)
	h.enableJev(srv.URL)
	ctx := context.Background()
	in := store.RunInput{Kind: "agent", Engine: "codex", Model: "chosen-model", Prompt: "Look up the README instructions"}
	h.api.prepareRun(ctx, &in)
	if in.Model != "chosen-model" || in.Effort != "low" {
		t.Fatalf("routed: %+v", in)
	}
	count := calls.Load()
	in.Effort = "high"
	h.api.prepareRun(ctx, &in)
	if in.Effort != "high" || calls.Load() != count {
		t.Fatalf("explicit effort routed: %+v", in)
	}
	prev := int64(1)
	in.ResumeRunID = &prev
	in.Effort = ""
	h.api.prepareRun(ctx, &in)
	if calls.Load() != count || in.Effort != "" {
		t.Fatal("resumed session routed")
	}
}
