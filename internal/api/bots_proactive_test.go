package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/telegram"
)

func TestBotSettingsAPI(t *testing.T) {
	h := setup(t)
	h.login()

	var got struct{ Settings store.BotSettings }
	expect(t, "patch", h.do("PATCH", "/api/bots/shop", map[string]any{"proactive": true, "review_at": "07:00",
		"instructions": "Be terse. Releases go out on Mondays."}, &got), 200)
	if !got.Settings.Proactive || got.Settings.ReviewAt != "07:00" || !got.Settings.Enabled || got.Settings.DailyCap != store.BotDailyCap {
		t.Fatalf("settings = %+v", got.Settings)
	}
	expect(t, "bad time", h.do("PATCH", "/api/bots/SHOP", map[string]any{"review_at": "7am"}, nil), 422)
	expect(t, "unknown", h.do("PATCH", "/api/bots/NOPE", map[string]any{"proactive": true}, nil), 404)

	var list struct {
		Bots []struct {
			Project  store.Project
			Settings store.BotSettings
		}
	}
	expect(t, "bots", h.do("GET", "/api/bots", nil, &list), 200)
	for _, b := range list.Bots {
		if b.Project.Key == "SHOP" && !b.Settings.Proactive {
			t.Fatalf("SHOP is proactive: %+v", b.Settings)
		}
	}

	// The standing orders reach the model.
	llm := newFakeLLM(t, map[string]any{"role": "assistant", "content": "Noted."})
	h.enableAssistant(llm.srv.URL)
	var started struct{ Chat store.Chat }
	expect(t, "message", h.do("POST", "/api/bots/SHOP/messages", map[string]string{"content": "Hi", "engine": "deepseek"}, &started), 201)
	h.waitChat(started.Chat.ID)
	llm.mu.Lock()
	system, _ := llm.seen[0][0]["content"].(string)
	llm.mu.Unlock()
	if !strings.Contains(system, "standing orders") || !strings.Contains(system, "Releases go out on Mondays.") {
		t.Fatalf("system prompt: %.600s", system)
	}

	// A bot that is off answers nothing.
	expect(t, "off", h.do("PATCH", "/api/bots/SHOP", map[string]any{"enabled": false}, nil), 200)
	expect(t, "message to an off bot", h.do("POST", "/api/bots/SHOP/messages", map[string]string{"content": "Hi"}, nil), 422)
	expect(t, "review of an off bot", h.do("POST", "/api/bots/SHOP/review", nil, nil), 422)
}

func TestBotRunsClaudeInTheProjectFolder(t *testing.T) {
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
		"capabilities": map[string]any{"claude": true, "permission_modes": []string{"plan", "acceptEdits", "bypassPermissions"}, "max_concurrent": 1},
		"running":      []int64{},
	}, nil, bearer...), 200)
	h.enableAssistant("http://127.0.0.1:1") // the Assistant's own chats are on DeepSeek; bots still use Claude

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
	finish := func(id int64, result string) {
		t.Helper()
		expect(t, "finish", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", id),
			map[string]any{"status": "succeeded", "result": result, "session_id": "sess-1"}, nil, bearer...), 204)
	}

	var started struct{ Chat store.Chat }
	expect(t, "first message", h.do("POST", "/api/bots/SHOP/messages", map[string]string{"content": "What is open?"}, &started), 201)
	if started.Chat.Engine != "claude" || !started.Chat.SkipPermissions {
		t.Fatalf("a bot starts on Claude Code: %+v", started.Chat.ChatSettings)
	}
	c := claimOne()
	want := []string{"/home/demo/dev/shop", "/home/demo/dev/shop/shop_api"}
	if strings.Join(c.Assistant.Folders, "|") != strings.Join(want, "|") || c.Run.PermissionMode != "bypassPermissions" ||
		!strings.Contains(c.Assistant.AppendSystem, "project's folder /home/demo/dev/shop") {
		t.Fatalf("claim = %+v folders %q", c.Run, c.Assistant.Folders)
	}
	finish(c.Run.ID, "Three tasks are open.")
	h.waitChat(started.Chat.ID)

	// A review the user asks for: read-only, resuming the same session in
	// the same folder, and marked as Forge's.
	var review struct {
		Chat     store.Chat
		Messages []store.ChatMessage
	}
	expect(t, "review", h.do("POST", "/api/bots/SHOP/review", nil, &review), 200)
	if len(review.Messages) != 1 || review.Messages[0].Origin != "schedule" || !strings.Contains(review.Messages[0].Content, "daily review") {
		t.Fatalf("review message = %+v", review.Messages)
	}
	c = claimOne()
	if c.Run.PermissionMode != "plan" || c.Run.ResumeRunID == nil || strings.Join(c.Assistant.Folders, "|") != strings.Join(want, "|") ||
		!strings.Contains(c.Assistant.AppendSystem, "Forge started this turn") {
		t.Fatalf("review claim = %+v folders %q", c.Run, c.Assistant.Folders)
	}
	finish(c.Run.ID, "Nothing to do.")
	h.waitChat(started.Chat.ID)
}

func TestProactiveBot(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()
	shop, err := h.store.ProjectIDByKey(ctx, "SHOP")
	if err != nil {
		t.Fatal(err)
	}
	bot, set := setupTelegram(t, h, 4242)
	_ = set
	h.api.botSettle = 0

	llm := newFakeLLM(t,
		toolCall("c1", "delegate_to_claude", map[string]any{"repo": "shop_api", "prompt": "Fix the build", "permission_mode": "acceptEdits"}),
		toolCall("c2", "run_command", map[string]any{"repo": "shop_api", "command": "deploy"}),
		map[string]any{"role": "assistant", "content": "Nothing to do."},
		map[string]any{"role": "assistant", "content": "**Review**: SHOP-3 is overdue; I moved it to today."},
	)
	h.enableAssistant(llm.srv.URL)
	chat, err := h.store.EnsureBotChat(ctx, shop, "Shop bot", store.ChatSettings{Engine: "deepseek", Edits: true})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "proactive", h.do("PATCH", "/api/bots/SHOP", map[string]any{"proactive": true, "review_at": ""}, nil), 200)

	pending := func() int {
		t.Helper()
		s, err := h.store.BotSettingsFor(ctx, shop)
		if err != nil {
			t.Fatal(err)
		}
		return s.PendingEvents
	}
	turns := func() []store.ChatTurn {
		t.Helper()
		ts, err := h.store.ChatTurns(ctx, chat.ID)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}

	// Busy: the events wait, and go together once the bot is free.
	h.store.AddBotEvent(ctx, shop, "ci", "CI failed on shop_api")
	h.store.AddBotEvent(ctx, shop, "endpoint", "Shop API is down")
	if ok, err := h.store.ClaimChat(ctx, chat.ID); err != nil || !ok {
		t.Fatal("claim", err)
	}
	h.api.botPass(ctx)
	if pending() != 2 || len(turns()) != 0 {
		t.Fatalf("a busy bot got a turn: pending %d, turns %d", pending(), len(turns()))
	}
	if err := h.store.ReleaseChat(ctx, chat.ID, ""); err != nil {
		t.Fatal(err)
	}
	h.api.botPass(ctx)
	_, msgs := h.waitChat(chat.ID)
	if pending() != 0 {
		t.Fatalf("pending after the turn = %d", pending())
	}
	var event *store.ChatMessage
	errs := map[string]store.ChatMessage{}
	for i, m := range msgs {
		if m.Role == "user" && m.Origin == "event" {
			event = &msgs[i]
		}
		if m.Role == "tool" {
			errs[m.ToolCallID] = m
		}
	}
	if event == nil || !strings.Contains(event.Content, "CI failed on shop_api") || !strings.Contains(event.Content, "Shop API is down") {
		t.Fatalf("event message = %+v", event)
	}
	// Read-only: no edits through Claude Code, no commands.
	if !errs["c1"].IsError || !strings.Contains(errs["c1"].Content, "read-only") || !errs["c2"].IsError {
		t.Fatalf("tool results = %+v", errs)
	}
	if ts := turns(); len(ts) != 1 || ts[0].Origin != "event" {
		t.Fatalf("turns = %+v", ts)
	}

	// The daily review comes once a day; its answer goes to Telegram.
	expect(t, "review time", h.do("PATCH", "/api/bots/SHOP", map[string]any{"review_at": "00:00"}, nil), 200)
	h.api.botPass(ctx)
	h.waitChat(chat.ID)
	sent := bot.waitSent(t, "SHOP-3 is overdue")
	if !strings.Contains(fmt.Sprint(sent["text"]), "SHOP bot") {
		t.Fatalf("telegram = %v", sent["text"])
	}
	h.api.botPass(ctx)
	if ts := turns(); len(ts) != 2 || ts[1].Origin != "schedule" {
		t.Fatalf("turns after two passes = %+v", ts)
	}
	// "Nothing to do." never reached Telegram.
	bot.mu.Lock()
	for _, m := range bot.sent {
		if strings.Contains(fmt.Sprint(m["text"]), "Nothing to do") {
			t.Errorf("a quiet answer was sent: %v", m["text"])
		}
	}
	bot.mu.Unlock()

	// The daily cap: further events are dropped, and the chat says so once.
	today := store.TodayFor(func() string {
		u, _ := h.store.FirstUser(ctx)
		return u.Timezone
	}())
	for i := 0; i < store.BotDailyCap; i++ {
		_, _ = h.store.TakeProactiveTurn(ctx, shop, today, store.BotDailyCap)
	}
	h.store.AddBotEvent(ctx, shop, "run", "Run #9 failed")
	h.api.botPass(ctx)
	h.store.AddBotEvent(ctx, shop, "run", "Run #10 failed")
	h.api.botPass(ctx)
	if pending() != 0 || len(turns()) != 2 {
		t.Fatalf("past the cap: pending %d, turns %d", pending(), len(turns()))
	}
	_, msgs = h.waitChat(chat.ID)
	notes := 0
	for _, m := range msgs {
		if m.Role == "assistant" && strings.Contains(m.Content, "proactive turns for today") {
			notes++
		}
	}
	if notes != 1 {
		t.Fatalf("cap notes = %d", notes)
	}
}

func TestTelegramTalksToBots(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()
	bot, set := setupTelegram(t, h, 4242)
	llm := newFakeLLM(t,
		map[string]any{"role": "assistant", "content": "Two releases are queued."},
		map[string]any{"role": "assistant", "content": "Monday it is."},
	)
	h.enableAssistant(llm.srv.URL)
	shop, _ := h.store.ProjectIDByKey(ctx, "SHOP")
	if _, err := h.store.EnsureBotChat(ctx, shop, "Shop bot", store.ChatSettings{Engine: "deepseek"}); err != nil {
		t.Fatal(err)
	}
	send := func(id int64, text string, reply *telegram.Message) {
		h.api.handleTelegram(ctx, set, telegram.Update{Message: &telegram.Message{MessageID: id, From: &telegram.User{ID: 4242},
			Chat: telegram.Chat{ID: 4242}, Text: text, ReplyTo: reply}})
	}

	send(1, "/bots", nil)
	list := bot.waitSent(t, "SHOP")
	if !strings.Contains(fmt.Sprint(list["text"]), "on demand") {
		t.Fatalf("/bots = %v", list["text"])
	}
	send(2, "/bot shop what is queued?", nil)
	answer := bot.waitSent(t, "Two releases are queued.")
	if !strings.Contains(fmt.Sprint(answer["text"]), "SHOP bot") || !strings.Contains(fmt.Sprint(answer["text"]), "/assistant/bot/SHOP") {
		t.Fatalf("answer = %v", answer["text"])
	}
	// A reply goes on with the bot, not the Assistant.
	send(3, "ship it monday", &telegram.Message{MessageID: bot.sentID(answer)})
	bot.waitSent(t, "Monday it is.")
	chat, err := h.store.BotChat(ctx, shop)
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := h.store.ChatMessages(ctx, chat.ID, 0)
	origins := 0
	for _, m := range msgs {
		if m.Role == "user" && m.Origin == "telegram" {
			origins++
		}
	}
	if origins != 2 || h.store.TelegramChat(ctx) != 0 {
		t.Fatalf("telegram messages to the bot = %d, assistant chat %d", origins, h.store.TelegramChat(ctx))
	}
	send(4, "/bot NOPE hi", nil)
	bot.waitSent(t, "No project NOPE")
}
