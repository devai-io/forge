package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

// An agent's CLI sessions arrive on the heartbeat, are tidied, survive a
// heartbeat that carries none, and show on the Agents page's runner list.
// The same heartbeat's engines decide what the OpenRouter engine is called
// on a claimed run.
func TestRunnerSessionsAndEngineName(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()
	var rn store.Runner
	var token string
	expect(t, "machine", h.newRunner("desk", &rn, &token), 201)
	agent := &harness{t: t, srv: h.srv, client: &http.Client{}}
	bearer := []string{"Authorization", "Bearer " + token}

	beat := func(body map[string]any) {
		t.Helper()
		body["hostname"], body["os"], body["version"] = "desk", "linux/amd64", "v0.1.30"
		if body["capabilities"] == nil {
			body["capabilities"] = map[string]any{"claude": true, "permission_modes": []string{"plan"}, "engines": []string{"claude", "openrouter"}}
		}
		expect(t, "heartbeat", agent.do("POST", "/api/runner/heartbeat", body, nil, bearer...), 200)
	}
	listed := func() store.Runner {
		t.Helper()
		var list struct{ Runners []store.Runner }
		expect(t, "runners", h.do("GET", "/api/runners", nil, &list), 200)
		for _, r := range list.Runners {
			if r.ID == rn.ID {
				return r
			}
		}
		t.Fatalf("runner %d not listed", rn.ID)
		return store.Runner{}
	}

	if got := listed(); got.Sessions == nil || len(got.Sessions) != 0 {
		t.Fatalf("before any report: %+v", got.Sessions)
	}
	beat(map[string]any{"sessions": []map[string]any{
		{"cli": "cursor", "state": "unknown", "version": "2026.01.28-abc1234", "detail": "status check timed out", "checked_at": "2026-10-14T21:00:00Z"},
		{"cli": "claude", "state": "logged_in", "version": "2.1.0", "detail": "claude.ai, pro", "checked_at": "2026-10-14T21:00:00Z"},
		{"cli": "codex", "state": "sideways", "checked_at": "2026-10-14T21:00:00Z"},
		{"cli": "emacs", "state": "logged_in"},
	}})
	got := listed().Sessions
	if len(got) != 3 || got[0].CLI != "claude" || got[0].State != "logged_in" || got[0].Detail != "claude.ai, pro" ||
		got[1].CLI != "codex" || got[1].State != "unknown" || got[2].CLI != "cursor" || got[2].Version != "2026.01.28-abc1234" ||
		got[0].CheckedAt == nil {
		t.Fatalf("sessions = %+v", got)
	}
	beat(map[string]any{}) // an older agent, or no check yet: keep the last report
	if again := listed().Sessions; len(again) != 3 {
		t.Fatalf("after a heartbeat without sessions: %+v", again)
	}

	// The old engine name is accepted and stored as "openrouter".
	h.providerKey()
	p, _ := h.store.ProjectByKey(ctx, "2026-01-01", "SHOP")
	var run store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": rn.ID, "repo_id": p.Repos[0].ID,
		"kind": "agent", "prompt": "Fix the flaky checkout test", "engine": "DeepSeek"}, &run), 201)
	if run.Engine != "openrouter" {
		t.Fatalf("queued engine = %q", run.Engine)
	}
	var claimed struct {
		Run    store.Run
		Engine map[string]string
	}
	expect(t, "claim", agent.do("POST", "/api/runner/claim", nil, &claimed, bearer...), 200)
	if claimed.Run.Engine != "openrouter" || claimed.Engine["name"] != "openrouter" {
		t.Fatalf("a current agent got %q / %+v", claimed.Run.Engine, claimed.Engine)
	}

	// An agent that lists no engines (before v0.1.30) gets "deepseek".
	beat(map[string]any{"capabilities": map[string]any{"claude": true, "permission_modes": []string{"plan"}}})
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": rn.ID, "repo_id": p.Repos[0].ID,
		"kind": "agent", "prompt": "And the other one", "engine": "openrouter"}, &run), 201)
	expect(t, "claim old", agent.do("POST", "/api/runner/claim", nil, &claimed, bearer...), 200)
	if claimed.Run.Engine != "deepseek" || claimed.Engine["name"] != "deepseek" {
		t.Fatalf("an older agent got %q / %+v", claimed.Run.Engine, claimed.Engine)
	}
	stored, err := h.store.RunByID(ctx, run.ID)
	if err != nil || stored.Engine != "openrouter" {
		t.Fatalf("stored run = %+v, %v", stored, err)
	}
}
