package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

func TestBotChats(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	today := store.TodayFor("UTC")
	shop := must(st.ProjectIDByKey(ctx, "SHOP"))
	set := store.ChatSettings{Engine: "deepseek"}

	if _, err := st.BotChat(ctx, shop); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("no bot chat before the first message: %v", err)
	}

	// Many first messages at once still make one chat.
	var wg sync.WaitGroup
	ids := make(chan int64, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := st.EnsureBotChat(ctx, shop, "Shop bot", set)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- c.ID
		}()
	}
	wg.Wait()
	close(ids)
	var botID int64
	for id := range ids {
		if botID != 0 && id != botID {
			t.Fatalf("two bot chats for one project: %d and %d", botID, id)
		}
		botID = id
	}
	bot := must(st.BotChat(ctx, shop))
	if bot.ID != botID || bot.ProjectID == nil || *bot.ProjectID != shop || bot.ProjectKey != "SHOP" || bot.Title != "Shop bot" {
		t.Fatalf("bot chat = %+v", bot)
	}

	// The Assistant's own chats do not list the bots.
	plain := must(st.CreateChat(ctx, "Plain chat", set))
	chats := must(st.ListChats(ctx))
	if len(chats) != 1 || chats[0].ID != plain.ID || chats[0].ProjectKey != "" {
		t.Fatalf("ListChats = %+v", chats)
	}

	// The bot's latest line; tool chatter and empty assistant steps do not count.
	must(st.AppendChatMessage(ctx, botID, store.ChatMessage{Role: "user", Content: "What is open?"}))
	must(st.AppendChatMessage(ctx, botID, store.ChatMessage{Role: "assistant", Content: "Two tasks are open."}))
	must(st.AppendChatMessage(ctx, botID, store.ChatMessage{Role: "assistant", Content: ""}))

	bots := must(st.ListBots(ctx, today))
	if len(bots) < 2 || bots[0].Chat == nil || bots[0].Chat.ID != botID {
		t.Fatalf("the active bot comes first: %+v", bots)
	}
	if bots[0].Last == nil || bots[0].Last.Role != "assistant" || bots[0].Last.Content != "Two tasks are open." {
		t.Fatalf("last line = %+v", bots[0].Last)
	}
	for _, b := range bots[1:] {
		if b.Chat != nil || b.Last != nil {
			t.Fatalf("a project without a bot chat: %+v", b)
		}
		if b.Project.Status == "archived" {
			t.Fatalf("archived projects have no bot: %s", b.Project.Key)
		}
	}

	// Folding: the summary stands for the messages up to seq.
	ok(t, st.SetChatSummary(ctx, botID, "- the user asked about open tasks", 1))
	bot = must(st.BotChat(ctx, shop))
	if bot.Summary != "- the user asked about open tasks" || bot.SummarySeq != 1 {
		t.Fatalf("summary = %q @ %d", bot.Summary, bot.SummarySeq)
	}
	if rest := must(st.ChatMessages(ctx, botID, bot.SummarySeq)); len(rest) != 2 {
		t.Fatalf("messages after the summary: %d", len(rest))
	}

	// Deleting a bot's chat resets the bot: the next message starts a new one.
	ok(t, st.DeleteChat(ctx, botID))
	if _, err := st.BotChat(ctx, shop); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bot chat after delete: %v", err)
	}
	fresh := must(st.EnsureBotChat(ctx, shop, "Shop bot", set))
	if fresh.ID == botID || fresh.Summary != "" {
		t.Fatalf("fresh bot chat = %+v", fresh)
	}
}
