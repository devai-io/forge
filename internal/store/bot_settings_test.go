package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/devai-io/forge/internal/store"
)

func TestBotSettings(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	shop := must(st.ProjectIDByKey(ctx, "SHOP"))

	// No row: the defaults.
	set := must(st.BotSettingsFor(ctx, shop))
	if !set.Enabled || set.Proactive || set.ReviewAt != "08:30" || set.Instructions != "" || set.DailyCap != store.BotDailyCap {
		t.Fatalf("defaults = %+v", set)
	}

	yes, no := true, false
	at, orders := "07:15", "  Keep releases on Mondays.  "
	set = must(st.UpdateBotSettings(ctx, shop, store.BotSettingsPatch{Proactive: &yes, ReviewAt: &at, Instructions: &orders}))
	if !set.Enabled || !set.Proactive || set.ReviewAt != "07:15" || set.Instructions != "Keep releases on Mondays." {
		t.Fatalf("after patch = %+v", set)
	}
	for _, bad := range []store.BotSettingsPatch{
		{ReviewAt: ptr("7:15")},
		{ReviewAt: ptr("24:00")},
		{Instructions: ptr(strings.Repeat("x", 4001))},
	} {
		if _, err := st.UpdateBotSettings(ctx, shop, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	none := ""
	set = must(st.UpdateBotSettings(ctx, shop, store.BotSettingsPatch{ReviewAt: &none}))
	if set.ReviewAt != "" || !set.Proactive {
		t.Fatalf("no review = %+v", set)
	}

	// The bot list carries the settings; other projects have the defaults.
	bots := must(st.ListBots(ctx, store.TodayFor("UTC")))
	for _, b := range bots {
		if b.Project.Key == "SHOP" && (!b.Settings.Proactive || b.Settings.Instructions == "") {
			t.Fatalf("SHOP settings = %+v", b.Settings)
		}
		if b.Project.Key != "SHOP" && (b.Settings.Proactive || !b.Settings.Enabled) {
			t.Fatalf("%s settings = %+v", b.Project.Key, b.Settings)
		}
	}

	// Events: only for a proactive bot, once while waiting.
	st.AddBotEvent(ctx, shop, "ci", "CI failed on shop_api")
	st.AddBotEvent(ctx, shop, "ci", "CI failed on shop_api")
	st.AddBotEvent(ctx, shop, "run", "Run #1 succeeded")
	if got := must(st.BotSettingsFor(ctx, shop)).PendingEvents; got != 2 {
		t.Fatalf("pending = %d, want 2", got)
	}
	other := int64(0)
	for _, b := range bots {
		if b.Project.Key != "SHOP" {
			other = b.Project.ID
		}
	}
	st.AddBotEvent(ctx, other, "ci", "not proactive: dropped")
	if got := must(st.BotSettingsFor(ctx, other)).PendingEvents; got != 0 {
		t.Fatalf("a bot that is not proactive got %d events", got)
	}

	// Due once the oldest has settled.
	if due := must(st.DueBotEvents(ctx, time.Hour)); len(due) != 0 {
		t.Fatalf("due before settling: %+v", due)
	}
	due := must(st.DueBotEvents(ctx, 0))
	if len(due) != 1 || due[0].Key != "SHOP" || len(due[0].Events) != 2 || due[0].Events[0].Kind != "ci" {
		t.Fatalf("due = %+v", due)
	}
	msg := store.BotEventsMessage(due[0].Events)
	if !strings.Contains(msg, "[ci] CI failed on shop_api") || !strings.Contains(msg, "Nothing to do.") {
		t.Fatalf("message = %q", msg)
	}
	if err := st.MarkBotEventsDelivered(ctx, []int64{due[0].Events[0].ID, due[0].Events[1].ID}); err != nil {
		t.Fatal(err)
	}
	if due := must(st.DueBotEvents(ctx, 0)); len(due) != 0 {
		t.Fatalf("due after delivery: %+v", due)
	}

	// Turning proactive off drops what waits.
	st.AddBotEvent(ctx, shop, "checkup", "Disk at 91%")
	set = must(st.UpdateBotSettings(ctx, shop, store.BotSettingsPatch{Proactive: &no}))
	if set.PendingEvents != 0 {
		t.Fatalf("events survived proactive off: %+v", set)
	}
	st.AddBotEvent(ctx, shop, "checkup", "Disk at 92%")
	if must(st.BotSettingsFor(ctx, shop)).PendingEvents != 0 {
		t.Fatal("an event for a bot that is not proactive")
	}
}

func TestBotDailyCapAndReview(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	shop := must(st.ProjectIDByKey(ctx, "SHOP"))
	yes := true
	must(st.UpdateBotSettings(ctx, shop, store.BotSettingsPatch{Proactive: &yes}))

	for i, want := range []bool{true, true, false, false} {
		if got := must(st.TakeProactiveTurn(ctx, shop, "2026-10-14", 2)); got != want {
			t.Fatalf("turn %d: %v, want %v", i, got, want)
		}
	}
	if !st.NoteCapOnce(ctx, shop, "2026-10-14") || st.NoteCapOnce(ctx, shop, "2026-10-14") {
		t.Fatal("the cap is noted once a day")
	}
	if !must(st.TakeProactiveTurn(ctx, shop, "2026-10-15", 2)) {
		t.Fatal("a new day, a new count")
	}
	if set := must(st.BotSettingsFor(ctx, shop)); set.ProactiveToday != 1 || set.ProactiveDate == nil || *set.ProactiveDate != "2026-10-15" {
		t.Fatalf("count = %+v", set)
	}

	// The review list and its once-a-day rule.
	rb := must(st.ReviewBots(ctx))
	if len(rb) != 1 || rb[0].Key != "SHOP" || rb[0].ReviewAt != "08:30" || rb[0].LastReviewDate != nil {
		t.Fatalf("review bots = %+v", rb)
	}
	at := func(s string) time.Time { return must(time.Parse("2006-01-02 15:04", s)) }
	if store.ReviewDue("08:30", nil, at("2026-10-14 08:29")) {
		t.Fatal("due before its time")
	}
	if !store.ReviewDue("08:30", nil, at("2026-10-14 08:30")) || !store.ReviewDue("08:30", nil, at("2026-10-14 19:00")) {
		t.Fatal("not due at or after its time (a missed review catches up the same day)")
	}
	day := "2026-10-14"
	if store.ReviewDue("08:30", &day, at("2026-10-14 19:00")) {
		t.Fatal("twice in a day")
	}
	if store.ReviewDue("", nil, at("2026-10-14 19:00")) {
		t.Fatal("no review time, no review")
	}
	if err := st.MarkReviewed(ctx, shop, day); err != nil {
		t.Fatal(err)
	}
	if rb := must(st.ReviewBots(ctx)); rb[0].LastReviewDate == nil || *rb[0].LastReviewDate != day {
		t.Fatalf("after review = %+v", rb)
	}
}

func TestChatOrigins(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	chat := must(st.CreateChat(ctx, "Origins", store.ChatSettings{Engine: "deepseek"}))
	must(st.AppendChatMessage(ctx, chat.ID, store.ChatMessage{Role: "user", Content: "Forge: CI failed", Origin: "event"}))
	must(st.AppendChatMessage(ctx, chat.ID, store.ChatMessage{Role: "user", Content: "hi"}))
	msgs := must(st.ChatMessages(ctx, chat.ID, 0))
	if msgs[0].Origin != "event" || msgs[1].Origin != "" {
		t.Fatalf("origins = %q %q", msgs[0].Origin, msgs[1].Origin)
	}
	turn := must(st.CreateChatTurnFrom(ctx, chat.ID, 1, chat.ChatSettings, "schedule"))
	if turn.Origin != "schedule" || !turn.Proactive() {
		t.Fatalf("turn = %+v", turn)
	}
	plain := must(st.CreateChatTurn(ctx, chat.ID, 2, chat.ChatSettings))
	if plain.Origin != "" || plain.Proactive() {
		t.Fatalf("plain turn = %+v", plain)
	}
}

func TestCIFailureReachesTheBot(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	shop := must(st.ProjectIDByKey(ctx, "SHOP"))
	yes := true
	must(st.UpdateBotSettings(ctx, shop, store.BotSettingsPatch{Proactive: &yes}))
	var repoID int64
	for _, r := range must(st.AllRepos(ctx)) {
		if r.ProjectKey == "SHOP" {
			repoID = r.ID
			break
		}
	}
	scan := func(conclusion string) {
		t.Helper()
		g := store.GitStatus{Branch: "main", CI: &store.CIStatus{Status: "completed", Conclusion: conclusion, Workflow: "ci", Title: "Fix login"}}
		if err := st.SaveRepoScans(ctx, "desk", []store.RepoScan{{RepoID: repoID, GitStatus: g}}); err != nil {
			t.Fatal(err)
		}
	}
	pending := func() int { return must(st.BotSettingsFor(ctx, shop)).PendingEvents }
	scan("success")
	if pending() != 0 {
		t.Fatal("an event for a green build")
	}
	scan("failure")
	if pending() != 1 {
		t.Fatalf("pending = %d after CI failed", pending())
	}
	due := must(st.DueBotEvents(ctx, 0))
	if !strings.Contains(due[0].Events[0].Text, "CI failed on") || !strings.Contains(due[0].Events[0].Text, "Fix login") {
		t.Fatalf("event = %+v", due[0].Events[0])
	}
	_ = st.MarkBotEventsDelivered(ctx, []int64{due[0].Events[0].ID})
	scan("failure") // still failing: not news
	if pending() != 0 {
		t.Fatal("a still-failing build is not a transition")
	}
}

func ptr[T any](v T) *T { return &v }

func TestRunFinishReachesTheBot(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	shop := must(st.ProjectIDByKey(ctx, "SHOP"))
	yes := true
	must(st.UpdateBotSettings(ctx, shop, store.BotSettingsPatch{Proactive: &yes}))
	rn := must(st.CreateRunner(ctx, "desk", []byte("tok")))
	var repoID int64
	for _, r := range must(st.AllRepos(ctx)) {
		if r.ProjectKey == "SHOP" {
			repoID = r.ID
			break
		}
	}
	finish := func(in store.RunInput) {
		t.Helper()
		run := must(st.CreateRun(ctx, in))
		must(st.ClaimRun(ctx, rn.ID))
		if err := st.FinishRun(ctx, run.ID, rn.ID, store.FinishInput{Status: "succeeded", Result: "All tests pass."}); err != nil {
			t.Fatal(err)
		}
	}
	finish(store.RunInput{RunnerID: rn.ID, RepoID: repoID, Kind: "agent", Prompt: "Run the test suite"})
	due := must(st.DueBotEvents(ctx, 0))
	if len(due) != 1 || due[0].Events[0].Kind != "run" || !strings.Contains(due[0].Events[0].Text, "Run the test suite") ||
		!strings.Contains(due[0].Events[0].Text, "All tests pass.") {
		t.Fatalf("due = %+v", due)
	}
	_ = st.MarkBotEventsDelivered(ctx, []int64{due[0].Events[0].ID})

	// A run the bot queued itself is not news to it.
	bot := must(st.EnsureBotChat(ctx, shop, "Shop bot", store.ChatSettings{Engine: "deepseek"}))
	turn := must(st.CreateChatTurn(ctx, bot.ID, 1, bot.ChatSettings))
	finish(store.RunInput{RunnerID: rn.ID, RepoID: repoID, Kind: "agent", Prompt: "Look at the logs", ChatTurnID: &turn.ID})
	if due := must(st.DueBotEvents(ctx, 0)); len(due) != 0 {
		t.Fatalf("the bot heard about its own run: %+v", due)
	}
}
