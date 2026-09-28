package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

// fakeJev answers like TypeSafe's System One: "effort" gets the given
// choice, every other (noul) question the given probability, except names
// listed in low which get 0.05.
func fakeJev(t *testing.T, choice string, confidence, noul float64, low map[string]bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			State     json.RawMessage
			Questions map[string]struct{ Type string }
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		answers := map[string]any{}
		for name, q := range body.Questions {
			if q.Type == "choice" {
				answers[name] = map[string]any{"type": "choice", "choice": choice, "confidence": confidence}
				continue
			}
			p := noul
			if low[name] {
				p = 0.05
			}
			answers[name] = map[string]any{"type": "noul", "noul": p}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "jev-test", "answers": answers, "usage": map[string]int{"input_tokens": 10}})
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func (h *harness) enableJev(url string) {
	h.t.Helper()
	h.api.jev.URL = url
	if err := h.store.SaveIntegrationSecret(context.Background(), h.api.box, "TypeSafe Jev API key", jevTag, "not-a-real-key-for-tests", "test"); err != nil {
		h.t.Fatal(err)
	}
	expect(h.t, "enable jev", h.do("PATCH", "/api/jev", map[string]bool{"enabled": true}, nil), 200)
}

func TestJevRoutesRunsToCheaperModels(t *testing.T) {
	h := setup(t)
	h.login()
	var created struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "machine", h.newRunner("desk", &created.Runner, &created.Token), 201)
	p, _ := h.store.ProjectByKey(context.Background(), "2026-01-01", "SHOP")
	repoID := p.Repos[0].ID
	queue := func(model string) store.Run {
		var run store.Run
		expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": created.Runner.ID, "repo_id": repoID,
			"kind": "agent", "prompt": "What does the README say about deploys?", "model": model}, &run), 201)
		return run
	}

	// Off: nothing changes.
	if run := queue(""); run.Model != "" || run.ModelNote != "" {
		t.Fatalf("jev off: %+v", run)
	}
	srv, calls := fakeJev(t, "light", 0.9, 0.5, nil)
	h.enableJev(srv.URL)
	if run := queue(""); run.Model != "haiku" || !strings.Contains(run.ModelNote, "Jev") {
		t.Fatalf("light task: model %q note %q", run.Model, run.ModelNote)
	}
	// A model picked by hand is never overridden, and costs no call.
	before := calls.Load()
	if run := queue("opus"); run.Model != "opus" || calls.Load() != before {
		t.Fatalf("explicit model: %+v", run)
	}

	// Unsure or "heavy" keeps the machine's default.
	srv2, _ := fakeJev(t, "light", 0.5, 0.5, nil)
	h.api.jev.URL = srv2.URL
	if run := queue(""); run.Model != "" {
		t.Fatalf("unsure: %+v", run)
	}
	// Routing switched off on its own.
	h.api.jev.URL = srv.URL
	expect(t, "routing off", h.do("PATCH", "/api/jev", map[string]bool{"routing": false}, nil), 200)
	if run := queue(""); run.Model != "" {
		t.Fatalf("routing off: %+v", run)
	}

	var status struct {
		KeyConfigured bool `json:"key_configured"`
		Settings      store.JevSettings
		Stats         struct{ Calls int }
	}
	expect(t, "status", h.do("GET", "/api/jev", nil, &status), 200)
	if !status.KeyConfigured || !status.Settings.Enabled || status.Settings.Routing || status.Stats.Calls < 2 {
		t.Fatalf("status = %+v", status)
	}
}

func TestJevTrimsSessionContext(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		if _, err := h.store.CreateTask(ctx, store.TaskInput{ProjectKey: "SHOP", Title: "Extra chore " + itoa(int64(i)), Status: "backlog", Priority: "low"}); err != nil {
			t.Fatal(err)
		}
	}
	cwd := "/home/demo/dev/shop/shop_ui"
	full, err := h.store.ContextForPath(ctx, cwd, h.api.jevTaskTrimmer(ctx))
	if err != nil || strings.Contains(full.Markdown, "picked by Jev") {
		t.Fatalf("jev off must not trim: %v", err)
	}
	// Every question says "not relevant": only the must-keep tasks remain.
	low := map[string]bool{}
	for i := 0; i < 80; i++ {
		low["t"+itoa(int64(i))] = true
	}
	srv, calls := fakeJev(t, "", 0, 0.9, low)
	h.enableJev(srv.URL)
	trimmed, err := h.store.ContextForPath(ctx, cwd, h.api.jevTaskTrimmer(ctx))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(trimmed.Markdown, "picked by Jev as relevant to shop_ui") || strings.Contains(trimmed.Markdown, "Extra chore") {
		t.Fatalf("not trimmed:\n%s", trimmed.Markdown)
	}
	if len(trimmed.Markdown) >= len(full.Markdown) {
		t.Fatal("trimming did not shrink the context")
	}
	// The same task set is answered from the cache.
	n := calls.Load()
	if _, err := h.store.ContextForPath(ctx, cwd, h.api.jevTaskTrimmer(ctx)); err != nil || calls.Load() != n {
		t.Fatalf("cache: %v calls %d → %d", err, n, calls.Load())
	}
	// Jev down: the full list, never an error.
	h.api.jev.URL = "http://127.0.0.1:1"
	h.api.jevCache = map[string]relevanceEntry{}
	again, err := h.store.ContextForPath(ctx, cwd, h.api.jevTaskTrimmer(ctx))
	if err != nil || !strings.Contains(again.Markdown, "Extra chore") {
		t.Fatalf("fail-open: %v", err)
	}
}

func TestRunnerJevConfig(t *testing.T) {
	h := setup(t)
	h.login()
	var created struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "machine", h.newRunner("desk", &created.Runner, &created.Token), 201)
	machine := &harness{t: t, srv: h.srv, client: &http.Client{}}
	bearer := []string{"Authorization", "Bearer " + created.Token}
	var cfg struct {
		Compaction bool   `json:"compaction"`
		APIKey     string `json:"api_key"`
		Rev        string `json:"rev"`
	}
	expect(t, "off", machine.do("GET", "/api/runner/jev", nil, &cfg, bearer...), 200)
	offRev := cfg.Rev
	if cfg.Compaction || cfg.APIKey != "" {
		t.Fatalf("off: %+v", cfg)
	}
	srv, _ := fakeJev(t, "", 0, 0.5, nil)
	h.enableJev(srv.URL)
	expect(t, "on", machine.do("GET", "/api/runner/jev", nil, &cfg, bearer...), 200)
	if !cfg.Compaction || cfg.APIKey != "not-a-real-key-for-tests" || cfg.Rev == offRev {
		t.Fatalf("on: %+v", cfg)
	}
	var hb struct {
		JevRev string `json:"jev_rev"`
	}
	expect(t, "heartbeat", machine.do("POST", "/api/runner/heartbeat", map[string]any{"hostname": "desk"}, &hb, bearer...), 200)
	if hb.JevRev != cfg.Rev {
		t.Fatalf("heartbeat rev %q, config rev %q", hb.JevRev, cfg.Rev)
	}
	// The key is never shown to the browser.
	var status map[string]any
	expect(t, "status", h.do("GET", "/api/jev", nil, &status), 200)
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "apikey_test") {
		t.Fatal("GET /api/jev leaks the key")
	}
}
