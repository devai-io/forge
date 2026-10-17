package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

type chatView struct {
	Chat     store.Chat
	Messages []store.ChatMessage
	Turns    []store.ChatTurn
}

func (h *harness) chat(id int64) chatView {
	h.t.Helper()
	var v chatView
	expect(h.t, "get chat", h.do("GET", fmt.Sprintf("/api/chats/%d", id), nil, &v), 200)
	return v
}

func TestAssistantDeepSeekTurnIsMetered(t *testing.T) {
	h := setup(t)
	h.login()
	var created struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "machine", h.newRunner("desk", &created.Runner, &created.Token), 201)
	if _, err := h.store.SetRunnerRole(context.Background(), created.Runner.ID, "master"); err != nil {
		t.Fatal(err)
	}

	// A provider that thinks: each answer carries reasoning (OpenRouter's
	// field), which must come back on the next request. The same provider
	// answers Jev's questions.
	var mu sync.Mutex
	var bodies []map[string]any
	steps := []map[string]any{
		{"role": "assistant", "content": "", "reasoning": "think-1", "tool_calls": []any{map[string]any{
			"id": "c1", "type": "function", "function": map[string]any{"name": "delegate_to_claude",
				"arguments": `{"project":"SHOP","repo":"shop_api","prompt":"Summarise the README"}`}}}},
		{"role": "assistant", "content": "Queued.", "reasoning": "think-2"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"deepseek/deepseek-v4-flash"}]}`))
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] == store.DefaultJevModel {
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "routed/model", "choices": []any{map[string]any{"message": map[string]any{
				"content": `{"effort":{"choice":"light","confidence":0.9}}`}}}, "usage": map[string]any{"prompt_tokens": 30, "cost": 0.00002}})
			return
		}
		mu.Lock()
		bodies = append(bodies, body)
		step := steps[0]
		steps = steps[1:]
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": step}},
			"usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 10, "prompt_cache_hit_tokens": 40}})
	}))
	t.Cleanup(srv.Close)
	h.enableAssistant(srv.URL)
	expect(t, "enable jev", h.do("PATCH", "/api/jev", map[string]bool{"enabled": true}, nil), 200)

	expect(t, "bad effort", h.do("POST", "/api/chats", map[string]any{"content": "hi", "effort": "xhigh"}, nil), 422)
	var started chatView
	expect(t, "start", h.do("POST", "/api/chats", map[string]any{"content": "Summarise the shop API README",
		"model": "deepseek-flash", "effort": "high"}, &started), 201)
	if started.Chat.Engine != "deepseek" || started.Chat.Effort != "high" {
		t.Fatalf("settings = %+v", started.Chat.ChatSettings)
	}
	chat, _ := h.waitChat(started.Chat.ID)
	if chat.LastError != "" {
		t.Fatalf("turn failed: %s", chat.LastError)
	}

	mu.Lock()
	first, second := bodies[0], bodies[1]
	mu.Unlock()
	if fmt.Sprint(first["reasoning"]) != "map[effort:high]" || first["model"] != "deepseek/deepseek-v4-flash" {
		t.Fatalf("request = %v", first)
	}
	raw, _ := json.Marshal(second["messages"])
	if !strings.Contains(string(raw), `"reasoning_content":"think-1"`) {
		t.Fatalf("reasoning not sent back: %s", raw)
	}

	v := h.chat(chat.ID)
	if len(v.Turns) != 1 {
		t.Fatalf("turns = %+v", v.Turns)
	}
	turn := v.Turns[0]
	if turn.Status != "done" || turn.Engine != "deepseek" || len(turn.Runs) != 1 || turn.Runs[0].RepoName != "shop_api" {
		t.Fatalf("turn = %+v", turn)
	}
	var llmLine, jevLine bool
	for _, e := range turn.Usage {
		switch e.API {
		case "127.0.0.1":
			llmLine = e.Model == "deepseek/deepseek-v4-flash" && e.Calls == 2 && e.InputTokens == 120 && e.CachedTokens == 80 &&
				e.OutputTokens == 20 && e.CostUSD != nil && *e.CostUSD > 0
		case "openrouter":
			jevLine = e.Model == store.DefaultJevModel && e.Calls == 1 && e.CostUSD != nil && *e.CostUSD == 0.00002
		}
	}
	if !llmLine || !jevLine {
		t.Fatalf("usage = %+v", turn.Usage)
	}

	// Thinking off is its own setting.
	expect(t, "effort off", h.do("PATCH", fmt.Sprintf("/api/chats/%d", chat.ID), map[string]any{"effort": "off"}, nil), 200)
}

func TestAssistantClaudeCodeTurns(t *testing.T) {
	h := setup(t)
	h.login()
	var created struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "machine", h.newRunner("desk", &created.Runner, &created.Token), 201)
	if _, err := h.store.SetRunnerRole(context.Background(), created.Runner.ID, "master"); err != nil {
		t.Fatal(err)
	}
	runner := &harness{t: t, srv: h.srv, client: &http.Client{}}
	bearer := []string{"Authorization", "Bearer " + created.Token}
	expect(t, "heartbeat", runner.do("POST", "/api/runner/heartbeat", map[string]any{
		"hostname": "desk", "os": "linux/amd64", "version": "test",
		"capabilities": map[string]any{"claude": true, "permission_modes": []string{"plan", "acceptEdits"}, "max_concurrent": 1},
		"running":      []int64{},
	}, nil, bearer...), 200)
	h.enableAssistant("http://127.0.0.1:1") // the DeepSeek side is unused here

	var status struct {
		Engines map[string]struct {
			Available bool
			Machine   string
			Efforts   []string
		}
	}
	expect(t, "status", h.do("GET", "/api/assistant", nil, &status), 200)
	if c := status.Engines["claude"]; !c.Available || c.Machine != "desk" || len(c.Efforts) != 5 {
		t.Fatalf("engines = %+v", status.Engines)
	}

	type claim struct {
		Run       store.Run
		Assistant struct {
			AppendSystem string   `json:"append_system"`
			Folders      []string `json:"folders"`
		}
	}
	claimOne := func() claim {
		t.Helper()
		var c claim
		expect(t, "claim", runner.do("POST", "/api/runner/claim", nil, &c, bearer...), 200)
		return c
	}
	finish := func(id int64, body map[string]any) {
		t.Helper()
		expect(t, "finish", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", id), body, nil, bearer...), 204)
	}

	expect(t, "bad effort", h.do("POST", "/api/chats", map[string]any{"content": "hi", "engine": "claude", "effort": "off"}, nil), 422)
	var started chatView
	expect(t, "start", h.do("POST", "/api/chats", map[string]any{"content": "What is open on SHOP?",
		"engine": "claude", "model": "sonnet", "effort": "high"}, &started), 201)
	if !started.Chat.Busy || len(started.Turns) != 1 || started.Turns[0].RunID == nil {
		t.Fatalf("started = %+v", started)
	}
	c := claimOne()
	if c.Run.RepoID != 0 || c.Run.ChatTurnID == nil || c.Run.Model != "sonnet" || c.Run.Effort != "high" ||
		c.Run.PermissionMode != "plan" || c.Run.Prompt != "What is open on SHOP?" || !strings.Contains(c.Assistant.AppendSystem, "forge MCP") {
		t.Fatalf("claimed = %+v %v", c.Run, c.Assistant)
	}
	// The session is not a board run.
	var runs struct{ Runs []store.Run }
	expect(t, "runs", h.do("GET", "/api/runs", nil, &runs), 200)
	if len(runs.Runs) != 0 {
		t.Fatalf("session listed as a run: %+v", runs.Runs)
	}
	finish(c.Run.ID, map[string]any{"status": "succeeded", "session_id": "s1", "result": "Two tasks are open.", "cost_usd": 0.02,
		"usage": map[string]any{"claude-sonnet-5-5": map[string]any{"inputTokens": 10, "outputTokens": 5, "cacheReadInputTokens": 100, "costUSD": 0.02}}})

	v := h.chat(started.Chat.ID)
	last := v.Messages[len(v.Messages)-1]
	if v.Chat.Busy || last.Role != "assistant" || last.Content != "Two tasks are open." {
		t.Fatalf("after the session: %+v %+v", v.Chat, last)
	}
	u := v.Turns[0].Usage
	if v.Turns[0].Status != "done" || len(u) != 1 || u[0].API != "claude-code" || u[0].CachedTokens != 100 ||
		u[0].CostUSD == nil || *u[0].CostUSD != 0.02 {
		t.Fatalf("turn = %+v", v.Turns[0])
	}

	// Stopping a turn whose run has not started ends it at once.
	expect(t, "send", h.do("POST", fmt.Sprintf("/api/chats/%d/messages", started.Chat.ID), map[string]any{"content": "and on ALPHA?"}, nil), 200)
	expect(t, "stop", h.do("POST", fmt.Sprintf("/api/chats/%d/stop", started.Chat.ID), nil, nil), 200)
	if v := h.chat(started.Chat.ID); v.Chat.Busy || v.Turns[1].Status != "stopped" {
		t.Fatalf("stop: %+v %+v", v.Chat, v.Turns[1])
	}

	// The next turn resumes the session, here allowed to edit.
	expect(t, "send", h.do("POST", fmt.Sprintf("/api/chats/%d/messages", started.Chat.ID),
		map[string]any{"content": "Close the first one", "edits": true}, nil), 200)
	c = claimOne()
	if c.Run.ResumeSession != "s1" || c.Run.PermissionMode != "acceptEdits" {
		t.Fatalf("resume = %+v", c.Run)
	}
	finish(c.Run.ID, map[string]any{"status": "failed", "error": "claude exited with 1"})
	if v := h.chat(started.Chat.ID); v.Chat.Busy || v.Turns[2].Status != "failed" || v.Chat.LastError != "claude exited with 1" {
		t.Fatalf("failed turn: %+v %+v", v.Chat, v.Turns[2])
	}

	// Skipping permissions needs the machine to allow bypassPermissions.
	expect(t, "skip refused", h.do("POST", fmt.Sprintf("/api/chats/%d/messages", started.Chat.ID),
		map[string]any{"content": "Just do it", "skip_permissions": true}, nil), 422)
	expect(t, "heartbeat", runner.do("POST", "/api/runner/heartbeat", map[string]any{
		"hostname": "desk", "os": "linux/amd64", "version": "test",
		"capabilities": map[string]any{"claude": true, "permission_modes": []string{"plan", "acceptEdits", "bypassPermissions"}, "max_concurrent": 1},
		"running":      []int64{},
	}, nil, bearer...), 200)
	var skipStatus struct {
		Engines map[string]struct {
			SkipPermissions bool `json:"skip_permissions"`
		}
	}
	expect(t, "status", h.do("GET", "/api/assistant", nil, &skipStatus), 200)
	if !skipStatus.Engines["claude"].SkipPermissions {
		t.Fatalf("skip_permissions not advertised: %+v", skipStatus.Engines)
	}
	expect(t, "skip", h.do("POST", fmt.Sprintf("/api/chats/%d/messages", started.Chat.ID),
		map[string]any{"content": "Just do it", "skip_permissions": true}, nil), 200)
	c = claimOne()
	if c.Run.PermissionMode != "bypassPermissions" || !strings.Contains(c.Assistant.AppendSystem, "Permission prompts are off") {
		t.Fatalf("skip = %+v %v", c.Run, c.Assistant)
	}
	finish(c.Run.ID, map[string]any{"status": "succeeded", "session_id": "s1", "result": "Done."})

	// Where the machine allows it, a new chat skips permissions by default.
	var fresh chatView
	expect(t, "fresh", h.do("POST", "/api/chats", map[string]any{"content": "Anything new?", "engine": "claude"}, &fresh), 201)
	if c := claimOne(); !fresh.Chat.SkipPermissions || c.Run.PermissionMode != "bypassPermissions" {
		t.Fatalf("default = %+v, run mode %q", fresh.Chat.ChatSettings, c.Run.PermissionMode)
	} else {
		finish(c.Run.ID, map[string]any{"status": "succeeded", "result": "No."})
	}

	// Switching engine drops the other engine's model and effort.
	var patched struct{ Chat store.Chat }
	expect(t, "switch", h.do("PATCH", fmt.Sprintf("/api/chats/%d", started.Chat.ID), map[string]any{"engine": "deepseek"}, &patched), 200)
	if patched.Chat.Engine != "deepseek" || patched.Chat.Model != "" || patched.Chat.Effort != "" || !patched.Chat.Edits {
		t.Fatalf("switched = %+v", patched.Chat.ChatSettings)
	}
}

func TestCodexChatAndEngineIsolation(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()
	var machine struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "machine", h.newRunner("desk", &machine.Runner, &machine.Token), 201)
	if _, err := h.store.SetRunnerRole(ctx, machine.Runner.ID, "master"); err != nil {
		t.Fatal(err)
	}
	rn := &harness{t: t, srv: h.srv, client: &http.Client{}}
	bearer := []string{"Authorization", "Bearer " + machine.Token}
	expect(t, "heartbeat", rn.do("POST", "/api/runner/heartbeat", map[string]any{"capabilities": map[string]any{"codex": true, "claude": true, "permission_modes": []string{"plan", "acceptEdits"}}}, nil, bearer...), 200)
	var saved store.MemorySaved
	expect(t, "Codex memory", rn.do("POST", "/api/runner/memories", map[string]any{"agent": "codex", "global": true, "content": "Prefer concise build reports."}, &saved, bearer...), 201)
	if saved.Memory.Source != "assistant" || saved.Memory.Origin != "Codex on desk" {
		t.Fatalf("Codex memory = %+v", saved)
	}
	// Subscription-backed Codex needs no provider key.
	expect(t, "enable", h.do("PATCH", "/api/assistant", map[string]any{"enabled": true, "engine": "codex"}, nil), 200)
	var started chatView
	expect(t, "bad effort", h.do("POST", "/api/chats", map[string]any{"content": "Hello", "engine": "codex", "effort": "max"}, nil), 422)
	expect(t, "start", h.do("POST", "/api/chats", map[string]any{"content": "Read SHOP status", "engine": "codex", "effort": "high"}, &started), 201)
	var claim struct {
		Run    store.Run
		Engine map[string]json.RawMessage
	}
	expect(t, "claim", rn.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	if claim.Run.Engine != "codex" || claim.Run.PermissionMode != "plan" || claim.Run.ResumeSession != "" {
		t.Fatalf("claim = %+v", claim)
	}
	events := map[string]any{"events": []any{map[string]any{"kind": "codex", "data": map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]any{"type": "agentMessage", "text": "Ready."}}}}}}
	expect(t, "events", rn.do("POST", fmt.Sprintf("/api/runner/runs/%d/events", claim.Run.ID), events, nil, bearer...), 204)
	if got := h.store.LastRunText(ctx, claim.Run.ID); got != "Ready." {
		t.Fatalf("last Codex text = %q", got)
	}
	expect(t, "finish", rn.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", claim.Run.ID), map[string]any{"status": "succeeded", "session_id": "codex-thread", "result": "Ready.", "usage": map[string]any{"model": "test-model", "inputTokens": 100, "cachedInputTokens": 20, "outputTokens": 30, "threadTotal": map[string]int{"inputTokens": 100, "cachedInputTokens": 20, "outputTokens": 30}}}, nil, bearer...), 204)
	v := h.chat(started.Chat.ID)
	if v.Chat.Busy || v.Turns[0].Status != "done" || len(v.Turns[0].Usage) != 1 || v.Turns[0].Usage[0].InputTokens != 80 || v.Turns[0].Usage[0].CostUSD != nil {
		t.Fatalf("chat = %+v", v)
	}
	expect(t, "resume", h.do("POST", fmt.Sprintf("/api/chats/%d/messages", started.Chat.ID), map[string]any{"content": "More detail"}, nil), 200)
	claim.Run = store.Run{}
	expect(t, "claim", rn.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	if claim.Run.ResumeSession != "codex-thread" || len(claim.Engine["usage_baseline"]) == 0 {
		t.Fatalf("resume = %+v", claim)
	}
	expect(t, "finish", rn.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", claim.Run.ID), map[string]any{"status": "succeeded", "session_id": "codex-thread", "result": "More detail."}, nil, bearer...), 204)
	// Switching engines must start a new session and hand over the transcript.
	expect(t, "switch", h.do("POST", fmt.Sprintf("/api/chats/%d/messages", started.Chat.ID), map[string]any{"content": "Now use Claude", "engine": "claude"}, nil), 200)
	claim.Run = store.Run{}
	expect(t, "claim", rn.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	if claim.Run.Engine != "claude" || claim.Run.ResumeSession != "" || !strings.Contains(claim.Run.Prompt, "Ready.") {
		t.Fatalf("cross-engine claim = %+v", claim.Run)
	}
}
