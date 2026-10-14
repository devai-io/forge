package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

type botsList struct {
	Bots []struct {
		Project store.Project
		Chat    *store.Chat
		Last    *store.BotLine
	}
}

func TestProjectBots(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()
	var desk, mini store.Runner
	var tok string
	expect(t, "master", h.newRunner("desk", &desk, &tok), 201)
	expect(t, "ios machine", h.newRunner("mini", &mini, &tok), 201)
	if _, err := h.store.SetRunnerRole(ctx, desk.ID, "master"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.SetRunnerRole(ctx, mini.ID, "ios"); err != nil {
		t.Fatal(err)
	}

	var list botsList
	expect(t, "bots", h.do("GET", "/api/bots", nil, &list), 200)
	if len(list.Bots) == 0 || list.Bots[0].Chat != nil {
		t.Fatalf("no bot has talked yet: %+v", list.Bots)
	}
	var other string
	for _, b := range list.Bots {
		if b.Project.Key != "SHOP" {
			other = b.Project.Key
		}
	}

	llm := newFakeLLM(t,
		toolCall("c1", "list_tasks", map[string]any{}),
		toolCall("c2", "delegate_to_claude", map[string]any{"repo": "shop_api", "prompt": "Archive the iOS build.", "machine": "ios"}),
		toolCall("c3", "remember", map[string]any{"content": "The user wants answers in short bullets.", "project": "global", "kind": "preference"}),
		toolCall("c4", "remember", map[string]any{"content": "Releases of the shop are cut on Mondays.", "kind": "convention"}),
		map[string]any{"role": "assistant", "content": "On it."},
	)
	h.enableAssistant(llm.srv.URL)

	var started struct{ Chat store.Chat }
	expect(t, "first message", h.do("POST", "/api/bots/shop/messages", map[string]string{"content": "Ship the iOS build"}, &started), 201)
	chat, msgs := h.waitChat(started.Chat.ID)
	if chat.LastError != "" || chat.ProjectKey != "SHOP" || chat.ProjectID == nil {
		t.Fatalf("bot chat = %+v", chat)
	}
	results := map[string]store.ChatMessage{}
	for _, m := range msgs {
		if m.Role == "tool" {
			results[m.ToolCallID] = m
		}
	}
	// list_tasks without a project: the bot's project.
	if r := string(results["c1"].Result); results["c1"].IsError || !strings.Contains(r, "SHOP-") {
		t.Fatalf("list_tasks: %s", r)
	}
	for _, ref := range []string{"\"" + other + "-"} {
		if strings.Contains(string(results["c1"].Result), ref) {
			t.Fatalf("list_tasks left the project: %s", results["c1"].Result)
		}
	}
	// machine "ios": the machine with that role.
	var run struct{ Machine string }
	_ = json.Unmarshal(results["c2"].Result, &run)
	if results["c2"].IsError || run.Machine != "mini" {
		t.Fatalf("delegate to ios: %+v %s", results["c2"], results["c2"].Result)
	}
	// Memory: "global" is about the user; no project is the bot's project.
	global, err := h.store.ListMemories(ctx, store.MemoryFilter{Global: true})
	if err != nil || len(global) == 0 || global[0].Origin != "SHOP bot" || global[0].ProjectKey != nil {
		t.Fatalf("global memory = %+v %v", global, err)
	}
	shop, err := h.store.ListMemories(ctx, store.MemoryFilter{ProjectKey: "SHOP", Query: "Mondays"})
	if err != nil || len(shop) != 1 || shop[0].Origin != "SHOP bot" {
		t.Fatalf("project memory = %+v %v", shop, err)
	}
	// The model was told it is the SHOP bot, with the project's brief.
	llm.mu.Lock()
	system, _ := llm.seen[0][0]["content"].(string)
	llm.mu.Unlock()
	if !strings.Contains(system, "You are the SHOP bot") || !strings.Contains(system, "Your project:") {
		t.Fatalf("system prompt: %.400s", system)
	}

	// The bot's chat is not one of the Assistant's chats; it leads the bots.
	var chats struct{ Chats []store.Chat }
	expect(t, "chats", h.do("GET", "/api/chats", nil, &chats), 200)
	if len(chats.Chats) != 0 {
		t.Fatalf("the Assistant lists the bot: %+v", chats.Chats)
	}
	expect(t, "bots again", h.do("GET", "/api/bots", nil, &list), 200)
	if list.Bots[0].Project.Key != "SHOP" || list.Bots[0].Chat == nil || list.Bots[0].Last == nil || list.Bots[0].Last.Content != "On it." {
		t.Fatalf("bots = %+v", list.Bots[0])
	}

	// The next message continues the same chat.
	llm2 := newFakeLLM(t, map[string]any{"role": "assistant", "content": "Still building."})
	h.setProvider(llm2.srv.URL)
	var next struct{ Chat store.Chat }
	expect(t, "next message", h.do("POST", "/api/bots/SHOP/messages", map[string]string{"content": "Status?"}, &next), 200)
	if next.Chat.ID != chat.ID {
		t.Fatalf("a second bot chat: %d vs %d", next.Chat.ID, chat.ID)
	}
	h.waitChat(chat.ID)

	// Busy: refused.
	if claimed, err := h.store.ClaimChat(ctx, chat.ID); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	expect(t, "busy", h.do("POST", "/api/bots/SHOP/messages", map[string]string{"content": "And now?"}, nil), 409)
	if err := h.store.ReleaseChat(ctx, chat.ID, ""); err != nil {
		t.Fatal(err)
	}

	// Archived projects have no bot; unknown ones 404.
	expect(t, "archive", h.do("PATCH", "/api/projects/"+other, map[string]string{"status": "archived"}, nil), 200)
	expect(t, "archived", h.do("POST", "/api/bots/"+other+"/messages", map[string]string{"content": "hi"}, nil), 422)
	expect(t, "unknown", h.do("POST", "/api/bots/NOPE/messages", map[string]string{"content": "hi"}, nil), 404)

	// Deleting the chat resets the bot.
	expect(t, "reset", h.do("DELETE", fmt.Sprintf("/api/chats/%d", chat.ID), nil, nil), 204)
	expect(t, "bots after reset", h.do("GET", "/api/bots", nil, &list), 200)
	for _, b := range list.Bots {
		if b.Chat != nil {
			t.Fatalf("a bot chat survived the reset: %+v", b.Chat)
		}
	}
}

func TestBotFoldsALongConversation(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()
	pid, err := h.store.ProjectIDByKey(ctx, "SHOP")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := h.store.EnsureBotChat(ctx, pid, "Shop bot", store.ChatSettings{Engine: "deepseek"})
	if err != nil {
		t.Fatal(err)
	}
	// About 300k chars of earlier conversation: past the fold threshold.
	long := strings.Repeat("Earlier detail about the checkout flow. ", 750) // 30k chars
	for i := 0; i < 5; i++ {
		for _, m := range []store.ChatMessage{
			{Role: "user", Content: fmt.Sprintf("Question %d: %s", i, long)},
			{Role: "assistant", Content: fmt.Sprintf("Answer %d: %s", i, long)},
		} {
			if _, err := h.store.AppendChatMessage(ctx, chat.ID, m); err != nil {
				t.Fatal(err)
			}
		}
	}

	llm := newFakeLLM(t,
		map[string]any{"role": "assistant", "content": "## Summary\n- The user is reworking the checkout flow."},
		map[string]any{"role": "assistant", "content": "Noted."},
	)
	h.enableAssistant(llm.srv.URL)
	expect(t, "message", h.do("POST", "/api/bots/SHOP/messages", map[string]string{"content": "Where were we?"}, nil), 200)
	got, msgs := h.waitChat(chat.ID)
	if got.LastError != "" || !strings.Contains(got.Summary, "checkout flow") || got.SummarySeq == 0 {
		t.Fatalf("after the fold: summary %q @ %d, error %q", got.Summary, got.SummarySeq, got.LastError)
	}
	if msgs[len(msgs)-1].Content != "Noted." {
		t.Fatalf("the turn's answer: %+v", msgs[len(msgs)-1])
	}
	llm.mu.Lock()
	defer llm.mu.Unlock()
	if len(llm.seen) != 2 {
		t.Fatalf("calls = %d, want the summariser then the turn", len(llm.seen))
	}
	turn := llm.seen[1]
	system, _ := turn[0]["content"].(string)
	if !strings.Contains(system, "reworking the checkout flow") {
		t.Fatalf("the turn did not get the summary: %.300s", system)
	}
	// The folded messages are not sent one by one any more.
	if len(turn) >= 12 {
		t.Fatalf("the turn still sent %d messages", len(turn))
	}
	if first, _ := turn[1]["content"].(string); strings.HasPrefix(first, "Question 0:") {
		t.Fatal("the oldest question was sent again")
	}
}
