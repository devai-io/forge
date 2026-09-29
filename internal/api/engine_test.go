package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

func TestAgentRunsDefaultToDeepSeek(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()
	var created struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "machine", h.newRunner("desk", &created.Runner, &created.Token), 201)
	p, _ := h.store.ProjectByKey(ctx, "2026-01-01", "SHOP")
	repoID := p.Repos[0].ID
	queue := func(body map[string]any) store.Run {
		t.Helper()
		body["runner_id"], body["repo_id"] = created.Runner.ID, repoID
		if body["kind"] == nil {
			body["kind"], body["prompt"] = "agent", "Fix the flaky checkout test"
		}
		var run store.Run
		expect(t, "queue", h.do("POST", "/api/runs", body, &run), 201)
		return run
	}

	// No DeepSeek key yet: the run falls back to Claude and says why.
	if run := queue(map[string]any{}); run.Engine != "claude" || !strings.Contains(run.ModelNote, "DeepSeek") {
		t.Fatalf("no key: %+v", run)
	}

	// With the key (here: the Assistant's, pointed at DeepSeek) runs default
	// to DeepSeek's fast model.
	if err := h.store.SaveIntegrationSecret(ctx, h.api.box, "LLM API key (Assistant)", assistantTag, "not-a-real-key-for-tests", "test"); err != nil {
		t.Fatal(err)
	}
	ds := queue(map[string]any{})
	if ds.Engine != "deepseek" || ds.Model != "deepseek-flash" || ds.ModelNote != "" {
		t.Fatalf("default: %+v", ds)
	}
	// Claude only when asked for.
	if run := queue(map[string]any{"engine": "claude"}); run.Engine != "claude" || run.Model != "" {
		t.Fatalf("explicit claude: %+v", run)
	}
	expect(t, "unknown engine", h.do("POST", "/api/runs", map[string]any{"runner_id": created.Runner.ID, "repo_id": repoID,
		"kind": "agent", "prompt": "hi", "engine": "gpt"}, nil), 422)
	// A picked model and effort are kept, on either engine.
	if run := queue(map[string]any{"engine": "claude", "model": "claude-opus-5-5", "effort": "xhigh"}); run.Model != "claude-opus-5-5" || run.Effort != "xhigh" {
		t.Fatalf("claude model+effort: %+v", run)
	}
	if run := queue(map[string]any{"engine": "deepseek", "model": "deepseek-v4-pro", "effort": "high"}); run.Model != "deepseek-v4-pro" || run.Effort != "high" {
		t.Fatalf("deepseek model+effort: %+v", run)
	}
	expect(t, "bad effort", h.do("POST", "/api/runs", map[string]any{"runner_id": created.Runner.ID, "repo_id": repoID,
		"kind": "agent", "prompt": "hi", "effort": "extreme"}, nil), 422)

	// The run form's choices: each engine's models, and the efforts.
	var offered struct {
		Models  map[string][]string
		Efforts []string
	}
	expect(t, "engine status", h.do("GET", "/api/engine", nil, &offered), 200)
	if !store.OneOf("claude-opus-5-5", offered.Models["claude"]) || strings.Join(offered.Models["deepseek"], ",") != "deepseek-flash,deepseek-v4-pro" ||
		!store.OneOf("max", offered.Efforts) {
		t.Fatalf("offered = %+v", offered)
	}

	// A heavy task (Jev, confident) moves up to the heavy DeepSeek model.
	srv, _ := fakeJev(t, "heavy", 0.9, 0.5, nil)
	h.enableJev(srv.URL)
	if run := queue(map[string]any{}); run.Engine != "deepseek" || run.Model != "deepseek-v4-pro" || !strings.Contains(run.ModelNote, "Jev") {
		t.Fatalf("heavy: %+v", run)
	}
	light, _ := fakeJev(t, "light", 0.95, 0.5, nil)
	h.api.jev.URL = light.URL
	if run := queue(map[string]any{}); run.Model != "deepseek-flash" {
		t.Fatalf("light on deepseek: %+v", run)
	}

	// The default can be switched to Claude; a command run has no engine.
	var st struct{ Settings store.EngineSettings }
	expect(t, "bad default", h.do("PATCH", "/api/engine", map[string]any{"default": "gpt"}, nil), 422)
	expect(t, "default claude", h.do("PATCH", "/api/engine", map[string]any{"default": "claude"}, &st), 200)
	if st.Settings.Default != "claude" {
		t.Fatalf("settings = %+v", st.Settings)
	}
	if run := queue(map[string]any{"prompt": "x", "kind": "agent"}); run.Engine != "claude" {
		t.Fatalf("default claude: %+v", run)
	}
	expect(t, "default deepseek", h.do("PATCH", "/api/engine", map[string]any{"default": "deepseek"}, nil), 200)

	// The machine gets DeepSeek's endpoint and key with the run — and only
	// for DeepSeek runs. Drain the queue in order.
	runner := &harness{t: t, srv: h.srv, client: &http.Client{}}
	bearer := []string{"Authorization", "Bearer " + created.Token}
	for {
		var claimed struct {
			Run    store.Run
			Engine map[string]string
		}
		code := runner.do("POST", "/api/runner/claim", nil, &claimed, bearer...)
		if code == 204 {
			break
		}
		expect(t, "claim", code, 200)
		switch claimed.Run.Engine {
		case "deepseek":
			if claimed.Engine["api_key"] != "not-a-real-key-for-tests" || !strings.HasPrefix(claimed.Engine["base_url"], "https://api.deepseek.com") {
				t.Fatalf("deepseek setup = %+v", claimed.Engine)
			}
		case "claude":
			if claimed.Engine != nil {
				t.Fatalf("claude run got an engine setup: %+v", claimed.Engine)
			}
		}
		if claimed.Run.ID == ds.ID {
			// Continuing a DeepSeek run stays on DeepSeek.
			if err := h.store.FinishRun(ctx, ds.ID, created.Runner.ID, store.FinishInput{Status: "succeeded", SessionID: "s1"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if run := queue(map[string]any{"prompt": "and the other one", "kind": "agent", "resume_run_id": ds.ID}); run.Engine != "deepseek" {
		t.Fatalf("resume: %+v", run)
	}
}
