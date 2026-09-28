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
	"time"

	"github.com/devai-io/forge/internal/store"
)

// fakeLLM plays an OpenAI-compatible provider from a script: each call
// returns the next step (tool calls or a final answer) and records what the
// assistant sent.
type fakeLLM struct {
	mu    sync.Mutex
	steps []map[string]any
	seen  [][]map[string]any
	srv   *httptest.Server
}

func newFakeLLM(t *testing.T, steps ...map[string]any) *fakeLLM {
	f := &fakeLLM{steps: steps}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer not-a-real-key-for-tests" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"fake-small"},{"id":"fake-large"}]}`))
			return
		}
		var body struct {
			Messages []map[string]any
			Tools    []any
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.seen = append(f.seen, body.Messages)
		step := map[string]any{"role": "assistant", "content": "done"}
		if len(f.steps) > 0 {
			step, f.steps = f.steps[0], f.steps[1:]
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": step}},
			"usage":   map[string]int{"prompt_tokens": 100, "completion_tokens": 10, "prompt_cache_hit_tokens": 40},
		})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func toolCall(id, name string, args any) map[string]any {
	raw, _ := json.Marshal(args)
	return map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{
		"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}}}
}

func (h *harness) enableAssistant(url string) {
	h.t.Helper()
	if err := h.store.SaveIntegrationSecret(context.Background(), h.api.box, "LLM", assistantTag, "not-a-real-key-for-tests", "test"); err != nil {
		h.t.Fatal(err)
	}
	expect(h.t, "enable", h.do("PATCH", "/api/assistant", map[string]any{"enabled": true, "base_url": url, "model": "fake-small"}, nil), 200)
}

func (h *harness) waitChat(id int64) (store.Chat, []store.ChatMessage) {
	h.t.Helper()
	var out struct {
		Chat     store.Chat
		Messages []store.ChatMessage
	}
	for i := 0; i < 100; i++ {
		expect(h.t, "get chat", h.do("GET", fmt.Sprintf("/api/chats/%d", id), nil, &out), 200)
		if !out.Chat.Busy {
			return out.Chat, out.Messages
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatal("the turn did not finish")
	return out.Chat, nil
}

func TestAssistantDelegatesToClaude(t *testing.T) {
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

	// Off: a friendly 503, nothing stored.
	expect(t, "off", h.do("POST", "/api/chats", map[string]string{"content": "hi"}, nil), 503)

	llm := newFakeLLM(t,
		toolCall("c1", "list_projects", map[string]any{}),
		toolCall("c2", "delegate_to_claude", map[string]any{"project": "shop", "repo": "shop_api",
			"prompt": "Review the error handling in the orders handler and report the riskiest spots.", "task_ref": "SHOP-1"}),
		toolCall("c3", "delegate_to_claude", map[string]any{"project": "SHOP", "repo": "shop_api", "prompt": "x",
			"permission_mode": "bypassPermissions"}),
		toolCall("c4", "wait_for_runs", map[string]any{"run_ids": []int{1}, "max_seconds": 600}),
		map[string]any{"role": "assistant", "content": "Queued a review on desk."},
	)
	h.enableAssistant(llm.srv.URL)

	var status struct {
		Settings      store.AssistantSettings
		KeyConfigured bool `json:"key_configured"`
		Models        []string
	}
	expect(t, "status", h.do("GET", "/api/assistant", nil, &status), 200)
	if !status.KeyConfigured || !status.Settings.Enabled || len(status.Models) != 2 {
		t.Fatalf("status = %+v", status)
	}

	var started struct {
		Chat     store.Chat
		Messages []store.ChatMessage
	}
	expect(t, "start", h.do("POST", "/api/chats", map[string]string{"content": "Have Claude review the SHOP API error handling"}, &started), 201)
	if !strings.HasPrefix(started.Chat.Title, "Have Claude review") || len(started.Messages) != 1 {
		t.Fatalf("started = %+v", started)
	}
	chat, msgs := h.waitChat(started.Chat.ID)
	if chat.LastError != "" {
		t.Fatalf("turn failed: %s", chat.LastError)
	}
	// user, (assistant+tool)×4, final assistant — and waiting on a machine
	// that is offline returns at once instead of after max_seconds.
	if len(msgs) != 10 || msgs[len(msgs)-1].Content != "Queued a review on desk." {
		t.Fatalf("messages: %d %+v", len(msgs), msgs[len(msgs)-1])
	}
	var delegated, refused store.ChatMessage
	for _, m := range msgs {
		switch m.ToolCallID {
		case "c2":
			delegated = m
		case "c3":
			refused = m
		}
	}
	var res struct {
		RunID          int64  `json:"run_id"`
		PermissionMode string `json:"permission_mode"`
		Machine        string
	}
	_ = json.Unmarshal(delegated.Result, &res)
	if delegated.IsError || res.RunID == 0 || res.PermissionMode != "plan" || res.Machine != "desk" {
		t.Fatalf("delegation: %+v %s", delegated, delegated.Result)
	}
	run, err := h.store.RunByID(context.Background(), res.RunID)
	if err != nil || run.Kind != "agent" || run.TaskRef == nil || *run.TaskRef != "SHOP-1" {
		t.Fatalf("run = %+v %v", run, err)
	}
	if !refused.IsError || !strings.Contains(refused.Content, "plan or acceptEdits") {
		t.Fatalf("bypassPermissions must be refused: %+v", refused)
	}
	for _, m := range msgs {
		if m.ToolCallID == "c4" && !strings.Contains(string(m.Result), "offline") {
			t.Fatalf("wait on an offline machine: %s", m.Result)
		}
	}
	if chat.Usage.InputTokens != 500 || chat.Usage.CachedTokens != 200 {
		t.Fatalf("usage = %+v", chat.Usage)
	}
	// The model saw the tool results and never the key.
	llm.mu.Lock()
	last := llm.seen[len(llm.seen)-1]
	llm.mu.Unlock()
	raw, _ := json.Marshal(last)
	if !strings.Contains(string(raw), `"role":"tool"`) || strings.Contains(string(raw), "not-a-real-key") {
		t.Fatalf("conversation sent: %s", raw)
	}

	// A follow-up in the same chat; a second message while busy is refused.
	llm2 := newFakeLLM(t, map[string]any{"role": "assistant", "content": "It is still running."})
	expect(t, "base url", h.do("PATCH", "/api/assistant", map[string]any{"base_url": llm2.srv.URL}, nil), 200)
	expect(t, "follow-up", h.do("POST", fmt.Sprintf("/api/chats/%d/messages", chat.ID), map[string]string{"content": "Status?"}, nil), 200)
	chat, msgs = h.waitChat(chat.ID)
	if msgs[len(msgs)-1].Content != "It is still running." {
		t.Fatalf("follow-up: %+v", msgs[len(msgs)-1])
	}

	var list struct{ Chats []store.Chat }
	expect(t, "list", h.do("GET", "/api/chats", nil, &list), 200)
	if len(list.Chats) != 1 {
		t.Fatalf("chats = %+v", list.Chats)
	}
	expect(t, "rename", h.do("PATCH", fmt.Sprintf("/api/chats/%d", chat.ID), map[string]string{"title": "SHOP review"}, nil), 200)
	expect(t, "delete", h.do("DELETE", fmt.Sprintf("/api/chats/%d", chat.ID), nil, nil), 204)
}

func TestAssistantProviderErrorEndsTheTurn(t *testing.T) {
	h := setup(t)
	h.login()
	h.enableAssistant("http://127.0.0.1:1")
	var started struct{ Chat store.Chat }
	expect(t, "start", h.do("POST", "/api/chats", map[string]string{"content": "hello"}, &started), 201)
	chat, _ := h.waitChat(started.Chat.ID)
	if chat.Busy || chat.LastError == "" {
		t.Fatalf("chat = %+v", chat)
	}
}
