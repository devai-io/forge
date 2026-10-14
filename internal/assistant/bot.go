package assistant

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/llm"
	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/usage"
)

// Bots. A bot is a project's own Assistant chat: one conversation that goes
// on for as long as the project does, with the project's snapshot and
// memory in its system prompt and its tools pointed at the project. The
// turn loop is the Assistant's; this file adds what makes a chat a bot, and
// the folding that keeps a conversation that never ends inside the model's
// window.

type botKey struct{}

// withBot marks a turn's context as the bot of project key.
func withBot(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, botKey{}, key)
}

// botOf is the project whose bot runs this turn ("" for the Assistant).
func botOf(ctx context.Context) string {
	k, _ := ctx.Value(botKey{}).(string)
	return k
}

// botBrief is what a bot knows of its project at the start of each turn:
// the project, its repos, servers, endpoints that are down, the open tasks
// that matter most, and its memories. Kept short: it is sent on every step.
func (a *Assistant) botBrief(ctx context.Context, user *store.User, key string) (string, error) {
	today := store.TodayFor(user.Timezone)
	p, err := a.store.ProjectByKey(ctx, today, key)
	if err != nil {
		return "", fmt.Errorf("the bot's project %s: %w", key, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nYour project: %s (%s) — status %s, priority %d.", p.Name, p.Key, p.Status, p.Priority)
	if p.Summary != "" {
		b.WriteString(" " + p.Summary)
	}
	if d := strings.TrimSpace(p.Description); d != "" {
		b.WriteString("\n\nAbout it:\n" + clip(d, 1500))
	}
	if n := strings.TrimSpace(p.InfraNotes); n != "" {
		b.WriteString("\n\nInfra and change-control notes (respect them):\n" + clip(n, 1500))
	}
	if len(p.Repos) > 0 {
		b.WriteString("\n\nRepos:")
		for _, r := range p.Repos {
			fmt.Fprintf(&b, "\n- %s (%s)", r.Name, r.Kind)
			if r.Path != "" {
				b.WriteString(" at " + r.Path)
			}
			if r.Deploy != "" {
				b.WriteString("; deploy: " + clip(r.Deploy, 200))
			}
		}
	}
	if len(p.Servers) > 0 {
		b.WriteString("\n\nServers:")
		for _, s := range p.Servers {
			fmt.Fprintf(&b, "\n- %s (%s, %s", s.Name, s.Role, s.Environment)
			if s.Critical {
				b.WriteString(", critical")
			}
			b.WriteString(")")
		}
	}
	for _, e := range p.Endpoints {
		if e.LastStatus == "down" {
			fmt.Fprintf(&b, "\n\nEndpoint DOWN: %s %s", e.Name, e.URL)
		}
	}
	tasks, err := a.store.ListTasks(ctx, store.TaskFilter{ProjectKey: p.Key, Open: true, Limit: 12, Today: today})
	if err == nil && len(tasks) > 0 {
		fmt.Fprintf(&b, "\n\nOpen tasks (%d open in all; list_tasks for more):", p.Stats.Total-p.Stats.Done)
		for _, t := range tasks {
			fmt.Fprintf(&b, "\n- %s [%s, %s] %s", t.Ref, t.Status, t.Priority, clip(t.Title, 120))
		}
	}
	ms, err := a.store.ListMemories(ctx, store.MemoryFilter{ProjectKey: p.Key, Limit: 30})
	if err == nil && len(ms) > 0 {
		b.WriteString("\n\nThe project's memory (pinned and newest first; recall searches all of it):")
		for _, m := range ms {
			b.WriteString("\n- " + m.Line(300))
		}
	}
	return b.String(), nil
}

// botRules is how a bot works, on either engine.
func botRules(key string) string {
	return fmt.Sprintf(`

You are the %[1]s bot: %[1]s's own agent, in charge of everything about this one project — its code (through Claude Code runs in its repos), its task board, its servers, endpoints and deploys, and its memory. This is one conversation that goes on for weeks: older parts are folded into a summary, and Forge memory keeps what must outlast even that.

How a bot works:
- Tools default to %[1]s: omit project. Touch other projects only when asked.
- Machines: work runs on the master machine (omit machine). Anything that needs macOS or Xcode — iOS builds, signing, simulators, TestFlight, Swift or Xcode projects — goes to machine "ios" (the machine with the ios role).
- Spend tokens like they are yours. Answer from the brief, memory and Forge tools before delegating; check list_runs / get_run before redoing work a recent run already did. Give Claude Code narrow, self-contained prompts: the files or area, the goal, and a short report back. Claude Code sessions on the machines have a code-graph index (tokensave) and compact shell output (rtk): tell them to explore with the index rather than reading whole files.
- You keep %[1]s's memory: when something durable is settled — a decision and why, a convention, a gotcha, how to build, test or deploy, where a long effort stands — remember it (project defaults to %[1]s; project "global" for something about the user everywhere). Correct or forget memories that turned out wrong.
- Changes to production follow the change-control notes; when they ask for approval, ask and wait.`, key)
}

// ── Folding ───────────────────────────────────────────────────────────────

const (
	foldAt      = 240_000 // chars of conversation that set off a fold (~60k tokens)
	foldKeep    = 60_000  // chars of the newest turns that stay word for word
	summaryMax  = 12_000  // chars of summary kept
	foldExtract = 200_000 // chars of the folded part shown to the summariser
)

// msgChars is about what a message costs to send again.
func msgChars(m store.ChatMessage) int {
	n := len(m.Content) + len(m.Reasoning)
	for _, tc := range m.ToolCalls {
		n += len(tc.Arguments) + len(tc.Name)
	}
	if m.Role == "tool" {
		n = min(len(m.Result), freshToolLimit)
	}
	return n
}

// foldPoint is the index of the user message from which the conversation
// stays word for word (the newest turns, keep chars or a little more), or 0
// when nothing should fold. It never cuts inside a turn: a tool result
// always follows its call.
func foldPoint(ms []store.ChatMessage, keep int) int {
	total := 0
	for i := len(ms) - 1; i > 0; i-- {
		total += msgChars(ms[i])
		if total < keep {
			continue
		}
		for j := i; j < len(ms); j++ {
			if ms[j].Role == "user" {
				return j
			}
		}
		for j := len(ms) - 1; j > 0; j-- { // the newest turn alone is bigger than keep
			if ms[j].Role == "user" {
				return j
			}
		}
		return 0
	}
	return 0
}

// fold summarises the chat's oldest messages when the conversation the
// model would see has grown past foldAt. Best effort: if the summariser
// fails, the turn goes on with the whole conversation.
func (a *Assistant) fold(ctx context.Context, client *llm.Client, model, api string, chat *store.Chat, meter *usage.Meter) {
	ms, err := a.store.ChatMessages(ctx, chat.ID, chat.SummarySeq)
	if err != nil {
		return
	}
	total := 0
	for _, m := range ms {
		total += msgChars(m)
	}
	if total < foldAt {
		return
	}
	cut := foldPoint(ms, foldKeep)
	if cut <= 0 {
		return
	}
	old := foldTranscript(ms[:cut])
	prev := chat.Summary
	if prev == "" {
		prev = "(none yet)"
	}
	msgs := []llm.Message{
		{Role: "system", Content: `You keep the running summary of a long conversation between a user and their AI agent in Forge (a project dashboard that also runs Claude Code on the user's machines). The summary replaces the older messages: the agent will see only it and the newest messages. Merge the summary so far with the newer part below into one updated summary.

Keep: what the user wants and decided, and why; open threads and promises; what was done (task refs, run ids, commits, files, outcomes); facts about the project learned along the way; the user's preferences. Drop small talk, retries and tool chatter. Newer facts win over older ones. Plain Markdown bullets under a few short headings, at most about 1,500 words. Write it in the language the user writes in.`},
		{Role: "user", Content: "Summary so far:\n" + prev + "\n\n---\n\nThe newer part of the conversation, oldest first:\n\n" + old},
	}
	msg, u, err := client.Chat(ctx, model, "off", msgs, nil)
	if err != nil {
		slog.Warn("assistant: fold", "chat", chat.ID, "err", err)
		return
	}
	miss := int64(u.InputTokens - u.CachedTokens)
	meter.Add(usage.Entry{API: api, Model: model, Calls: 1, InputTokens: miss, CachedTokens: int64(u.CachedTokens),
		OutputTokens: int64(u.OutputTokens), CostUSD: usage.DeepSeekCost(model, miss, int64(u.CachedTokens), int64(u.OutputTokens), time.Now()),
		Note: "summarising older messages"})
	_ = a.store.AddChatUsage(ctx, chat.ID, store.ChatUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokens: u.CachedTokens})
	summary := strings.TrimSpace(msg.Content)
	if summary == "" {
		return
	}
	if len(summary) > summaryMax {
		summary = summary[:summaryMax] + "…"
	}
	seq := ms[cut-1].Seq
	if err := a.store.SetChatSummary(ctx, chat.ID, summary, seq); err != nil {
		slog.Error("assistant: save fold", "chat", chat.ID, "err", err)
		return
	}
	chat.Summary, chat.SummarySeq = summary, seq
}

// foldTranscript renders messages for the summariser: what was said, and a
// line per tool call with the start of its result.
func foldTranscript(ms []store.ChatMessage) string {
	var parts []string
	for _, m := range ms {
		switch m.Role {
		case "user":
			parts = append(parts, "User: "+clip(m.Content, 8000))
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				parts = append(parts, "Agent: "+clip(m.Content, 6000))
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, "Agent calls "+tc.Name+" "+clip(string(tc.Arguments), 400))
			}
		case "tool":
			line := "→ " + m.ToolName + ": " + m.Content
			if m.IsError {
				line += " (failed)"
			} else if r := string(m.Result); r != "" && r != "null" {
				line += " " + clip(r, 400)
			}
			parts = append(parts, line)
		}
	}
	out := strings.Join(parts, "\n\n")
	if len(out) > foldExtract {
		out = "…" + out[len(out)-foldExtract:]
	}
	return out
}

// summaryNote is the folded part of a chat, for the system prompt.
func summaryNote(chat *store.Chat) string {
	if chat.Summary == "" {
		return ""
	}
	return "\n\nThe conversation so far, summarised (the older messages are folded into this; the newer ones follow as messages):\n" + chat.Summary
}
