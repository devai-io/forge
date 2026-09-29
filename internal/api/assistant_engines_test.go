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

	// A provider that thinks: each answer carries reasoning_content, which
	// must come back on the next request.
	var mu sync.Mutex
	var bodies []map[string]any
	steps := []map[string]any{
		{"role": "assistant", "content": "", "reasoning_content": "think-1", "tool_calls": []any{map[string]any{
			"id": "c1", "type": "function", "function": map[string]any{"name": "delegate_to_claude",
				"arguments": `{"project":"SHOP","repo":"shop_api","prompt":"Summarise the README"}`}}}},
		{"role": "assistant", "content": "Queued.", "reasoning_content": "think-2"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-flash"}]}`))
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
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
	jevSrv, _ := fakeJev(t, "light", 0.9, 0.5, nil)
	h.enableJev(jevSrv.URL)

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
	if first["reasoning_effort"] != "high" || fmt.Sprint(first["thinking"]) != "map[type:enabled]" || first["model"] != "deepseek-flash" {
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
			llmLine = e.Model == "deepseek-flash" && e.Calls == 2 && e.InputTokens == 120 && e.CachedTokens == 80 &&
				e.OutputTokens == 20 && e.CostUSD != nil && *e.CostUSD > 0
		case "jev":
			jevLine = e.Calls == 1 && e.CostUSD != nil
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
		Assistant map[string]string
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
		c.Run.PermissionMode != "plan" || c.Run.Prompt != "What is open on SHOP?" || !strings.Contains(c.Assistant["append_system"], "forge MCP") {
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

	// Switching engine drops the other engine's model and effort.
	var patched struct{ Chat store.Chat }
	expect(t, "switch", h.do("PATCH", fmt.Sprintf("/api/chats/%d", started.Chat.ID), map[string]any{"engine": "deepseek"}, &patched), 200)
	if patched.Chat.Engine != "deepseek" || patched.Chat.Model != "" || patched.Chat.Effort != "" || !patched.Chat.Edits {
		t.Fatalf("switched = %+v", patched.Chat.ChatSettings)
	}
}
