package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/telegram"
)

const testBotToken = "123456789:AAtest-token-not-real-0123456789abcdef"

// fakeBot is the Bot API: it records what Forge sends and hands out updates.
type fakeBot struct {
	mu      sync.Mutex
	sent    []map[string]any // sendMessage / editMessageText params, with "method"
	answers []string         // answerCallbackQuery texts
	nextID  int64
	updates chan telegram.Update
}

func newFakeBot(t *testing.T) (*fakeBot, *httptest.Server) {
	b := &fakeBot{nextID: 100, updates: make(chan telegram.Update, 10)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/bot" + testBotToken + "/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 401, "description": "Unauthorized"})
			return
		}
		method := strings.TrimPrefix(r.URL.Path, prefix)
		raw, _ := io.ReadAll(r.Body)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		var result any = true
		b.mu.Lock()
		switch method {
		case "getMe":
			result = map[string]any{"id": 1, "is_bot": true, "username": "forge_test_bot"}
		case "getUpdates":
			b.mu.Unlock()
			var ups []telegram.Update
			select {
			case u := <-b.updates:
				ups = append(ups, u)
			case <-time.After(200 * time.Millisecond):
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": ups})
			return
		case "sendMessage", "editMessageText":
			p["method"] = method
			b.sent = append(b.sent, p)
			if method == "sendMessage" {
				b.nextID++
				result = map[string]any{"message_id": b.nextID, "chat": map[string]any{"id": p["chat_id"]}, "text": p["text"]}
			}
		case "answerCallbackQuery":
			b.answers = append(b.answers, fmt.Sprint(p["text"]))
		}
		b.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(srv.Close)
	return b, srv
}

// waitSent waits for a sent message whose text contains want.
func (b *fakeBot) waitSent(t *testing.T, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		b.mu.Lock()
		for _, m := range b.sent {
			if m["method"] == "sendMessage" && strings.Contains(fmt.Sprint(m["text"]), want) {
				b.mu.Unlock()
				return m
			}
		}
		b.mu.Unlock()
		if time.Now().After(deadline) {
			b.mu.Lock()
			defer b.mu.Unlock()
			t.Fatalf("no message with %q; sent: %v", want, b.sent)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (b *fakeBot) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.sent)
}

// buttons lists a sent message's inline buttons as text → callback data.
func buttons(m map[string]any) map[string]string {
	out := map[string]string{}
	kb, _ := m["reply_markup"].(map[string]any)
	rows, _ := kb["inline_keyboard"].([]any)
	for _, row := range rows {
		for _, b := range row.([]any) {
			btn := b.(map[string]any)
			if d, ok := btn["callback_data"].(string); ok {
				out[btn["text"].(string)] = d
			}
		}
	}
	return out
}

func setupTelegram(t *testing.T, h *harness, chatID int64) (*fakeBot, store.TelegramSettings) {
	t.Helper()
	bot, srv := newFakeBot(t)
	h.api.tg.BaseURL = srv.URL
	ctx := context.Background()
	if err := h.store.SaveIntegrationSecret(ctx, h.api.box, "Telegram bot token", telegramTag, testBotToken, "test"); err != nil {
		t.Fatal(err)
	}
	set := store.TelegramSettings{Enabled: true, ChatID: chatID, Prompts: true, Runs: true, Checkup: true, Assistant: true}
	if err := h.store.SetTelegramSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	return bot, set
}

// sentID is the message id the fake gave the n-th sendMessage (from 101).
func (b *fakeBot) sentID(m map[string]any) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := int64(100)
	for _, x := range b.sent {
		if x["method"] == "sendMessage" {
			n++
		}
		if fmt.Sprint(x) == fmt.Sprint(m) {
			return n
		}
	}
	return 0
}

func TestTelegramDrivesAnInteractiveRun(t *testing.T) {
	h := setup(t)
	h.login()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const chat = 4242
	bot, set := setupTelegram(t, h, chat)
	rn, repoID := startFakeMachine(ctx, t, h)

	var run store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": rn.ID, "repo_id": repoID, "kind": "agent",
		"prompt": "Pick a color", "engine": "claude", "interactive": true}, &run), 201)

	press := func(m map[string]any, label string) {
		t.Helper()
		data, ok := buttons(m)[label]
		if !ok {
			t.Fatalf("no %q button in %v", label, buttons(m))
		}
		h.api.handleTelegram(ctx, set, telegram.Update{CallbackQuery: &telegram.CallbackQuery{ID: "cb", Data: data,
			Message: &telegram.Message{MessageID: bot.sentID(m), Chat: telegram.Chat{ID: chat}, Text: "x"}}})
	}

	// The question comes with one button per option; Blue answers it.
	q := bot.waitSent(t, "Color?")
	press(q, "Blue")
	// Then the Bash permission, with the command shown.
	p := bot.waitSent(t, "make clean")
	press(p, "Allow for session")
	// The turn ends: the run waits, and says what Claude said.
	wait := bot.waitSent(t, "waits for you")
	if !strings.Contains(fmt.Sprint(wait["text"]), "turn one") {
		t.Fatalf("awaiting message = %v", wait["text"])
	}
	// A reply to that message is the follow-up.
	h.api.handleTelegram(ctx, set, telegram.Update{Message: &telegram.Message{MessageID: 9001, From: &telegram.User{ID: chat},
		Chat: telegram.Chat{ID: chat}, Text: "and now?", ReplyTo: &telegram.Message{MessageID: bot.sentID(wait)}}})
	bot.waitSent(t, "Sent to run #")
	second := bot.waitSent(t, "turn two")
	press(second, "End session")
	done := bot.waitSent(t, "succeeded")
	if !strings.Contains(fmt.Sprint(done["text"]), "Pick a color") {
		t.Fatalf("finished message = %v", done["text"])
	}

	final, err := h.store.RunByID(ctx, run.ID)
	if err != nil || final.Status != "succeeded" || final.Result != "turn two" {
		t.Fatalf("run = %+v %v", final, err)
	}
	var events struct{ Events []store.RunEvent }
	expect(t, "events", h.do("GET", fmt.Sprintf("/api/runs/%d/events", run.ID), nil, &events), 200)
	var transcript strings.Builder
	for _, e := range events.Events {
		transcript.Write(e.Data)
	}
	for _, want := range []string{"color=Blue", "bash=allow", "got and now?"} {
		if !strings.Contains(transcript.String(), want) {
			t.Fatalf("transcript lacks %s", want)
		}
	}
	// Each answer is in the security log.
	var log struct{ Events []store.SecurityEvent }
	expect(t, "security log", h.do("GET", "/api/auth/security", nil, &log), 200)
	kinds := map[string]int{}
	for _, e := range log.Events {
		kinds[e.Kind]++
	}
	if kinds["telegram_answer"] != 2 || kinds["telegram_run_message"] != 1 || kinds["telegram_end_run"] != 1 {
		t.Fatalf("security kinds = %v", kinds)
	}
}

func TestTelegramOnlyTalksToItsChat(t *testing.T) {
	h := setup(t)
	h.login()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bot, set := setupTelegram(t, h, 4242)
	h.api.tgPoll = 100 * time.Millisecond
	go h.api.RunTelegram(ctx)

	// A stranger is ignored, button presses included.
	bot.updates <- telegram.Update{UpdateID: 1, Message: &telegram.Message{MessageID: 1, From: &telegram.User{ID: 7},
		Chat: telegram.Chat{ID: 7}, Text: "/tasks"}}
	h.api.handleTelegram(ctx, set, telegram.Update{CallbackQuery: &telegram.CallbackQuery{ID: "x", Data: "e:1",
		Message: &telegram.Message{MessageID: 5, Chat: telegram.Chat{ID: 7}}}})
	// The owner's commands answer.
	bot.updates <- telegram.Update{UpdateID: 2, Message: &telegram.Message{MessageID: 2, From: &telegram.User{ID: 4242},
		Chat: telegram.Chat{ID: 4242}, Text: "/tasks SHOP"}}
	tasks := bot.waitSent(t, "SHOP-")
	if tasks["chat_id"].(float64) != 4242 {
		t.Fatalf("answered in chat %v", tasks["chat_id"])
	}
	bot.mu.Lock()
	for _, m := range bot.sent {
		if m["chat_id"].(float64) != 4242 {
			t.Fatalf("wrote to another chat: %v", m)
		}
	}
	if len(bot.answers) != 1 || !strings.Contains(bot.answers[0], "not linked") {
		t.Fatalf("callback answers = %v", bot.answers)
	}
	bot.mu.Unlock()

	// /remember and /recall work on memory, per project.
	say := func(text string) {
		h.api.handleTelegram(ctx, set, telegram.Update{Message: &telegram.Message{MessageID: 3, From: &telegram.User{ID: 4242},
			Chat: telegram.Chat{ID: 4242}, Text: text}})
	}
	say("/remember SHOP Checkout deploys need the payments flag flipped first")
	bot.waitSent(t, "Remembered in SHOP")
	say("/recall SHOP payments flag")
	bot.waitSent(t, "payments flag flipped")

	// Without a linked chat the bot only says the chat's id.
	set.ChatID = 0
	n := bot.count()
	h.api.handleTelegram(ctx, set, telegram.Update{Message: &telegram.Message{MessageID: 4, From: &telegram.User{ID: 99},
		Chat: telegram.Chat{ID: 99}, Text: "hi"}})
	if m := bot.waitSent(t, "Its id is <code>99</code>"); m == nil || bot.count() != n+1 {
		t.Fatal("unlinked chat")
	}
}

func TestTelegramSettings(t *testing.T) {
	h := setup(t)
	h.login()
	var st struct {
		Settings        store.TelegramSettings
		TokenConfigured bool `json:"token_configured"`
	}
	expect(t, "get", h.do("GET", "/api/telegram", nil, &st), 200)
	if st.TokenConfigured || st.Settings.Enabled || !st.Settings.Prompts {
		t.Fatalf("defaults = %+v", st)
	}
	expect(t, "token needs elevation", h.do("PUT", "/api/telegram/token", map[string]string{"token": testBotToken}, nil), 403)
	expect(t, "chat needs elevation", h.do("PATCH", "/api/telegram", map[string]any{"chat_id": 5}, nil), 403)
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)
	expect(t, "bad token", h.do("PUT", "/api/telegram/token", map[string]string{"token": "nope"}, nil), 422)
	expect(t, "token", h.do("PUT", "/api/telegram/token", map[string]string{"token": testBotToken}, &st), 200)
	expect(t, "settings", h.do("PATCH", "/api/telegram", map[string]any{"enabled": true, "chat_id": 5, "runs": false}, &st), 200)
	if !st.TokenConfigured || !st.Settings.Enabled || st.Settings.ChatID != 5 || st.Settings.Runs || !st.Settings.Prompts {
		t.Fatalf("after = %+v", st)
	}
	bot, srv := newFakeBot(t)
	h.api.tg.BaseURL = srv.URL
	var test struct {
		OK   bool
		Sent bool
		Bot  string
	}
	expect(t, "test", h.do("POST", "/api/telegram/test", nil, &test), 200)
	if !test.OK || !test.Sent || test.Bot != "forge_test_bot" {
		t.Fatalf("test = %+v", test)
	}
	bot.waitSent(t, "Forge is connected")
}

func TestTelegramTalksToTheAssistantWithMemory(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()
	bot, set := setupTelegram(t, h, 4242)
	h.api.tgFollowEvery = 50 * time.Millisecond
	if _, err := h.store.CreateMemory(ctx, store.MemoryInput{Kind: "preference", Content: "Likes answers short and in bullets"}); err != nil {
		t.Fatal(err)
	}
	llm := newFakeLLM(t,
		toolCall("c1", "remember", map[string]any{"project": "shop", "kind": "decision",
			"content": "Orders API stays on REST; GraphQL was rejected for caching reasons"}),
		toolCall("c2", "recall", map[string]any{"project": "SHOP", "query": "graphql"}),
		map[string]any{"role": "assistant", "content": "Noted: **REST** stays."},
	)
	h.enableAssistant(llm.srv.URL)

	h.api.handleTelegram(ctx, set, telegram.Update{Message: &telegram.Message{MessageID: 1, From: &telegram.User{ID: 4242},
		Chat: telegram.Chat{ID: 4242}, Text: "Remember we keep the orders API on REST, GraphQL was rejected for caching"}})
	answer := bot.waitSent(t, "Noted")
	if !strings.Contains(fmt.Sprint(answer["text"]), "<b>REST</b>") {
		t.Fatalf("answer = %v", answer["text"])
	}
	ms, _ := h.store.ListMemories(ctx, store.MemoryFilter{ProjectKey: "SHOP", Query: "graphql"})
	if len(ms) != 1 || ms[0].Source != "assistant" || ms[0].Kind != "decision" {
		t.Fatalf("memories = %+v", ms)
	}
	llm.mu.Lock()
	system := fmt.Sprint(llm.seen[0][0]["content"])
	recalled := fmt.Sprint(llm.seen[2][len(llm.seen[2])-1]["content"])
	llm.mu.Unlock()
	if !strings.Contains(system, "Likes answers short") {
		t.Fatalf("system prompt lacks the user's memory:\n%s", system)
	}
	if !strings.Contains(recalled, "GraphQL was rejected") {
		t.Fatalf("recall result = %s", recalled)
	}

	// A reply to the answer continues the same chat; /new starts another.
	chatID := h.store.TelegramChat(ctx)
	if chatID == 0 {
		t.Fatal("no current chat")
	}
	h.api.handleTelegram(ctx, set, telegram.Update{Message: &telegram.Message{MessageID: 2, From: &telegram.User{ID: 4242},
		Chat: telegram.Chat{ID: 4242}, Text: "thanks", ReplyTo: &telegram.Message{MessageID: bot.sentID(answer)}}})
	bot.waitSent(t, "done")
	var chats struct{ Chats []store.Chat }
	expect(t, "chats", h.do("GET", "/api/chats", nil, &chats), 200)
	if len(chats.Chats) != 1 {
		t.Fatalf("chats = %d", len(chats.Chats))
	}
	h.api.handleTelegram(ctx, set, telegram.Update{Message: &telegram.Message{MessageID: 3, From: &telegram.User{ID: 4242},
		Chat: telegram.Chat{ID: 4242}, Text: "/new"}})
	if h.store.TelegramChat(ctx) != 0 {
		t.Fatal("/new kept the chat")
	}
}
