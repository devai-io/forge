package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

// sampleTurn is one user turn: the question, a tool call and its result, the answer.
func sampleTurn(seq int, size int) []store.ChatMessage {
	body := strings.Repeat("x", size)
	return []store.ChatMessage{
		{Seq: seq, Role: "user", Content: body},
		{Seq: seq + 1, Role: "assistant", ToolCalls: []store.ChatToolCall{{ID: "c", Name: "list_tasks", Arguments: json.RawMessage(`{}`)}}},
		{Seq: seq + 2, Role: "tool", ToolCallID: "c", ToolName: "list_tasks", Result: json.RawMessage(`"` + body + `"`)},
		{Seq: seq + 3, Role: "assistant", Content: body},
	}
}

func TestFoldPointKeepsWholeTurns(t *testing.T) {
	var ms []store.ChatMessage
	for i := 0; i < 6; i++ {
		ms = append(ms, sampleTurn(len(ms)+1, 10_000)...)
	}
	if got := foldPoint(ms[:4], 1_000_000); got != 0 {
		t.Fatalf("below keep nothing folds, got %d", got)
	}
	cut := foldPoint(ms, 25_000)
	if cut <= 0 || ms[cut].Role != "user" {
		t.Fatalf("the cut must land on a user message: %d", cut)
	}
	kept := 0
	for _, m := range ms[cut:] {
		kept += msgChars(m)
	}
	if kept < 25_000 {
		t.Fatalf("kept %d chars, want at least keep", kept)
	}
	// A newest turn bigger than keep alone: it stays, the rest folds.
	big := append(append([]store.ChatMessage{}, ms[:8]...), sampleTurn(9, 100_000)...)
	if cut := foldPoint(big, 25_000); cut != 8 {
		t.Fatalf("an oversized newest turn: cut %d, want 8", cut)
	}
	// One turn only: nothing to fold.
	if cut := foldPoint(sampleTurn(1, 100_000), 25_000); cut != 0 {
		t.Fatalf("a single turn folds nothing, got %d", cut)
	}
}

func TestFoldTranscriptAndSummaryNote(t *testing.T) {
	out := foldTranscript(sampleTurn(1, 20))
	for _, want := range []string{"User: ", "Agent calls list_tasks", "→ list_tasks", "Agent: "} {
		if !strings.Contains(out, want) {
			t.Fatalf("transcript lacks %q:\n%s", want, out)
		}
	}
	if summaryNote(&store.Chat{}) != "" {
		t.Fatal("no summary, no note")
	}
	if n := summaryNote(&store.Chat{Summary: "- shipped v1"}); !strings.Contains(n, "- shipped v1") {
		t.Fatalf("note = %q", n)
	}
}

func TestBotContext(t *testing.T) {
	ctx := context.Background()
	if botOf(ctx) != "" || origin(ctx) != "Assistant" {
		t.Fatal("outside a bot")
	}
	ctx = withBot(ctx, "SHOP")
	if botOf(ctx) != "SHOP" || origin(ctx) != "SHOP bot" {
		t.Fatalf("inside the SHOP bot: %q %q", botOf(ctx), origin(ctx))
	}
	rules := botRules("SHOP")
	for _, want := range []string{"SHOP bot", `machine "ios"`, "omit project"} {
		if !strings.Contains(rules, want) {
			t.Fatalf("bot rules lack %q", want)
		}
	}
}

func TestBotFolders(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, nil},
		{[]string{"", "relative/path"}, nil},
		{[]string{"/home/ada/dev/shop/shop_api"}, []string{"/home/ada/dev/shop/shop_api"}},
		{[]string{"/home/ada/dev/shop/shop_api", "/home/ada/dev/shop/shop_ui/"},
			[]string{"/home/ada/dev/shop", "/home/ada/dev/shop/shop_api"}},
		// Only /home/ada in common: too broad, the first repo instead.
		{[]string{"/home/ada/shop_api", "/home/ada/work/shop_ui"}, []string{"/home/ada/shop_api"}},
		// A repo inside another: the outer one is the folder.
		{[]string{"/home/ada/dev/shop", "/home/ada/dev/shop/mobile"}, []string{"/home/ada/dev/shop"}},
	}
	for _, c := range cases {
		got := botFolders(c.in)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("botFolders(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSessionPromptForABot(t *testing.T) {
	u := &store.User{Username: "ada", Timezone: "UTC"}
	p := sessionPrompt(u, "desk", "plan", "https://forge.example.com", []string{"/home/ada/dev/shop", "/home/ada/dev/shop/shop_api"}, false)
	if !strings.Contains(p, "started in the project's folder /home/ada/dev/shop") || !strings.Contains(p, "then its home folder") {
		t.Fatalf("bot prompt: %s", p)
	}
	p = sessionPrompt(u, "desk", "plan", "https://forge.example.com", nil, true)
	if !strings.Contains(p, "started in its home folder") || !strings.Contains(p, "Forge started this turn") {
		t.Fatalf("proactive prompt: %s", p)
	}
}

func TestNothingToDoIsQuiet(t *testing.T) {
	if !strings.Contains(proactiveNote, `"Nothing to do."`) {
		t.Fatal("the proactive note must name the quiet answer")
	}
	if !strings.Contains(botRules("SHOP"), "When proactive") {
		t.Fatal("the bot rules explain proactive turns")
	}
}
