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

// fakeJev answers like Jev through OpenRouter's chat completions: the JSON
// content has the schema's properties, a choice question (an object) the
// given choice, every other (noul) question the given probability, except
// names listed in low which get 0.05.
func fakeJev(t *testing.T, choice string, confidence, noul float64, low map[string]bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Model          string
			ResponseFormat struct {
				JSONSchema struct {
					Schema struct {
						Properties map[string]struct{ Type string }
					}
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		answers := map[string]any{}
		for name, q := range body.ResponseFormat.JSONSchema.Schema.Properties {
			if q.Type == "object" {
				answers[name] = map[string]any{"choice": choice, "confidence": confidence}
				continue
			}
			p := noul
			if low[name] {
				p = 0.05
			}
			answers[name] = p
		}
		content, _ := json.Marshal(answers)
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "routed/model",
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": string(content)}}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 3, "cost": 0.00001}})
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

// setProvider points the provider (Jev, the Assistant's DeepSeek engine,
// model lists) at a fake.
func (h *harness) setProvider(url string) {
	h.t.Helper()
	if err := h.store.SetProviderSettings(context.Background(), store.ProviderSettings{BaseURL: url}); err != nil {
		h.t.Fatal(err)
	}
}

// providerKey stores a (fake) provider key in the vault.
func (h *harness) providerKey() {
	h.t.Helper()
	if err := h.store.SaveIntegrationSecret(context.Background(), h.api.box, "OpenRouter API key", store.OpenRouterTag, "not-a-real-key-for-tests", "test"); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) enableJev(url string) {
	h.t.Helper()
	h.setProvider(url)
	h.providerKey()
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
			"kind": "agent", "prompt": "What does the README say about deploys?", "model": model, "engine": "claude"}, &run), 201)
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
	h.setProvider(srv2.URL)
	if run := queue(""); run.Model != "" {
		t.Fatalf("unsure: %+v", run)
	}
	// Routing switched off on its own.
	h.setProvider(srv.URL)
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
	h.setProvider("http://127.0.0.1:1")
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
	// Compaction (TypeSafe's plugin) cannot run through the provider: it is
	// off even with Jev on, and machines are told so.
	srv, _ := fakeJev(t, "", 0, 0.5, nil)
	h.enableJev(srv.URL)
	expect(t, "config", machine.do("GET", "/api/runner/jev", nil, &cfg, bearer...), 200)
	if cfg.Compaction || cfg.APIKey != "" || cfg.Rev == "" {
		t.Fatalf("config: %+v", cfg)
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
	if strings.Contains(string(raw), "not-a-real-key-for-tests") {
		t.Fatal("GET /api/jev leaks the key")
	}
}
