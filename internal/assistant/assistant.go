// Package assistant is Forge's main agent: a chat that reads and updates
// Forge through tools and
// delegates real work to Claude Code on the user's machines by queuing runs —
// the same runs the "New run" dialog creates, under the same machine rules.
//
// A chat runs on one of two engines. "claude" (the default) is a Claude Code
// session on the master machine, on the subscription logged in there
// (session.go). "deepseek" is a loop on the server through the provider's
// OpenAI-compatible API (OpenRouter): call the model, run the tools it asks
// for, append everything to the chat, repeat until it answers without tools.
// The web app only posts messages and polls.
package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/llm"
	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/usage"
)

const (
	maxSteps       = 30               // model calls per turn
	turnTimeout    = 45 * time.Minute // a turn that waits on runs can be long
	oldToolResult  = 1500             // chars kept of tool results older than the last few messages
	freshToolLimit = 16000            // chars kept of a recent tool result
)

var ErrOff = errors.New("the assistant is off: turn it on in Settings → Assistant")

// ErrNoKey: a DeepSeek turn needs the provider key.
var ErrNoKey = errors.New("the DeepSeek engine needs an OpenRouter key: add one in Settings → AI provider, or use Claude")

// ErrNoMachine: a Claude Code turn needs a machine that runs claude.
var ErrNoMachine = errors.New("no machine can run Claude Code for the assistant: pair one (the master) and install Claude Code there")

// Hooks are what the assistant needs from the API layer.
type Hooks struct {
	// Key returns the provider API key (vault, tag integration:openrouter).
	Key func(context.Context) (string, error)
	// Route may pick a model for a new agent run (Jev); may be nil.
	Route func(context.Context, *store.RunInput)
	// Queued wakes the machine's long-poll after a run is created.
	Queued func(runnerID int64)
}

type Assistant struct {
	store *store.Store
	hooks Hooks

	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
}

func New(st *store.Store, h Hooks) *Assistant {
	return &Assistant{store: st, hooks: h, cancels: map[int64]context.CancelFunc{}}
}

// client builds the provider client from the current settings; ErrOff when
// the assistant is off, ErrNoKey without a provider key.
func (a *Assistant) client(ctx context.Context) (*llm.Client, store.AssistantSettings, error) {
	set, err := a.store.AssistantSettings(ctx)
	if err != nil {
		return nil, set, err
	}
	if !set.Enabled {
		return nil, set, ErrOff
	}
	if k, err := a.hooks.Key(ctx); err != nil || k == "" {
		return nil, set, ErrNoKey
	}
	return llm.New(set.BaseURL, a.hooks.Key).App("Forge", a.store.PublicURL), set, nil
}

// Ready says whether a turn on this engine could start now.
func (a *Assistant) Ready(ctx context.Context, engine string) error {
	if engine == "claude" {
		set, err := a.store.AssistantSettings(ctx)
		if err != nil {
			return err
		}
		if !set.Enabled {
			return ErrOff
		}
		_, err = a.sessionMachine(ctx)
		return err
	}
	_, _, err := a.client(ctx)
	return err
}

// sessionMachine is where Claude Code turns run: the master machine.
func (a *Assistant) sessionMachine(ctx context.Context) (*store.Runner, error) {
	rn, err := a.store.MasterRunner(ctx)
	if err != nil {
		return nil, ErrNoMachine
	}
	if rn.LastSeenAt != nil && !rn.Capabilities.Claude {
		return nil, ErrNoMachine
	}
	return rn, nil
}

// Models lists the provider's models for the configured key.
func (a *Assistant) Models(ctx context.Context) []string {
	set, err := a.store.AssistantSettings(ctx)
	if err != nil {
		return []string{}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ids, err := llm.New(set.BaseURL, a.hooks.Key).App("Forge", a.store.PublicURL).Models(ctx)
	if err != nil {
		return []string{}
	}
	return ids
}

// DefaultSettings are what a new chat starts with: the configured engine
// (Claude unless Settings say DeepSeek; DeepSeek with its model), and — on
// Claude Code — no permission prompts when the master machine allows
// bypassPermissions.
func (a *Assistant) DefaultSettings(ctx context.Context) store.ChatSettings {
	set, _ := a.store.AssistantSettings(ctx)
	cs := store.ChatSettings{Engine: set.Engine}
	if cs.Engine == "deepseek" {
		cs.Model = set.Model
	}
	if rn, err := a.store.MasterRunner(ctx); err == nil {
		cs.SkipPermissions = store.OneOf("bypassPermissions", rn.Capabilities.PermissionModes)
	}
	return cs
}

// BotSettings are what a new bot chat starts with: Claude Code on the
// master machine — the subscription logged in there — whatever engine the
// Assistant's own chats use; no permission prompts where the master allows
// bypassPermissions.
func (a *Assistant) BotSettings(ctx context.Context) store.ChatSettings {
	cs := store.ChatSettings{Engine: "claude"}
	if rn, err := a.store.MasterRunner(ctx); err == nil {
		cs.SkipPermissions = store.OneOf("bypassPermissions", rn.Capabilities.PermissionModes)
	}
	return cs
}

// Start runs a turn for the chat's newest user message. On DeepSeek the
// server runs it in the background; on Claude Code it is a run on the
// master machine and ends when that run does (FinishSession). It fails with
// ErrConflict when a turn is already running.
func (a *Assistant) Start(ctx context.Context, chatID int64, user *store.User) error {
	return a.StartFrom(ctx, chatID, user, "")
}

// StartFrom is Start with the turn's origin (store.ChatTurn.Origin). A
// proactive turn ("event", "schedule") is read-only whatever the chat
// allows: it may update Forge (tasks, comments, memory) but not code.
func (a *Assistant) StartFrom(ctx context.Context, chatID int64, user *store.User, origin string) error {
	chat, err := a.store.ChatByID(ctx, chatID)
	if err != nil {
		return err
	}
	if err := a.Ready(ctx, chat.Engine); err != nil {
		return err
	}
	msgs, err := a.store.ChatMessages(ctx, chatID, 0)
	if err != nil {
		return err
	}
	seq := 0
	for _, m := range msgs {
		if m.Role == "user" {
			seq = m.Seq
		}
	}
	ok, err := a.store.ClaimChat(ctx, chatID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: the assistant is still working on this chat", store.ErrConflict)
	}
	turn, err := a.store.CreateChatTurnFrom(ctx, chatID, seq, chat.ChatSettings, origin)
	if err != nil {
		_ = a.store.ReleaseChat(ctx, chatID, err.Error())
		return err
	}
	if chat.Engine == "claude" {
		if err := a.startSession(ctx, chat, turn, msgs, user); err != nil {
			a.end(chatID, turn.ID, err)
			return err
		}
		return nil
	}

	tctx, cancel := context.WithTimeout(context.Background(), turnTimeout)
	a.mu.Lock()
	a.cancels[chatID] = cancel
	a.mu.Unlock()
	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.cancels, chatID)
			a.mu.Unlock()
			cancel()
		}()
		a.end(chatID, turn.ID, a.turn(tctx, chat, turn, user))
	}()
	return nil
}

// end records how a turn ended and frees the chat.
func (a *Assistant) end(chatID, turnID int64, err error) {
	ctx := context.Background()
	status, msg := "done", ""
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		status, msg = "stopped", "stopped"
	case errors.Is(err, context.DeadlineExceeded):
		status, msg = "failed", "the turn took too long and was stopped"
	default:
		status, msg = "failed", err.Error()
		slog.Warn("assistant turn failed", "chat", chatID, "err", err)
	}
	if _, err := a.store.FinishChatTurn(ctx, turnID, status, msg); err != nil {
		slog.Error("assistant: finish turn", "chat", chatID, "err", err)
	}
	if err := a.store.ReleaseChat(ctx, chatID, msg); err != nil {
		slog.Error("assistant: release chat", "chat", chatID, "err", err)
	}
}

// Stop cancels a running turn (a no-op when none runs). A Claude Code
// turn's run is cancelled; the turn ends when the machine says it stopped.
func (a *Assistant) Stop(ctx context.Context, chatID int64) {
	a.mu.Lock()
	cancel := a.cancels[chatID]
	a.mu.Unlock()
	if cancel != nil {
		cancel()
		return
	}
	turn, err := a.store.RunningChatTurn(ctx, chatID)
	if err != nil || turn.RunID == nil {
		return
	}
	if run, err := a.store.CancelRun(ctx, *turn.RunID); err == nil && run.Status == "cancelled" {
		a.FinishSession(ctx, run) // it never started: nothing will report back
	}
}

type turnKey struct{}

// turn is one DeepSeek-engine turn: call the model, run the tools it asks
// for, repeat until it answers. Every API call it makes (the model's, and
// Jev's when a delegated run is routed) is metered into the turn's usage.
func (a *Assistant) turn(ctx context.Context, chat *store.Chat, turn *store.ChatTurn, user *store.User) error {
	client, set, err := a.client(ctx)
	if err != nil {
		return err
	}
	model := store.ModelID(turn.Model)
	if model == "" {
		model = set.Model
	}
	meter := usage.NewMeter()
	ctx = usage.WithMeter(context.WithValue(ctx, turnKey{}, turn.ID), meter)
	defer func() { _ = a.store.SetChatTurnUsage(context.Background(), turn.ID, meter.Entries()) }()
	api := apiName(set.BaseURL)
	tools := toolDefs()
	system := systemPrompt(user, a.store.PublicURL)
	if turn.Proactive() {
		ctx = withProactive(ctx)
	}
	if chat.ProjectKey != "" {
		ctx = withBot(ctx, chat.ProjectKey)
		brief, err := a.botBrief(ctx, user, chat.ProjectKey)
		if err != nil {
			return err
		}
		system += botRules(chat.ProjectKey) + a.botOrders(ctx, chat) + brief
		if turn.Proactive() {
			system += proactiveNote
		}
	}
	system += a.userMemories(ctx)
	a.fold(ctx, client, model, api, chat, meter)
	system += summaryNote(chat)
	for step := 0; step < maxSteps; step++ {
		history, err := a.store.ChatMessages(ctx, chat.ID, chat.SummarySeq)
		if err != nil {
			return err
		}
		msg, u, err := client.Chat(ctx, model, turn.Effort, conversation(system, history), tools)
		if err != nil {
			return err
		}
		miss := int64(u.InputTokens - u.CachedTokens)
		cost := u.CostUSD // as the provider reports it, else estimated
		if cost == nil {
			cost = usage.DeepSeekCost(model, miss, int64(u.CachedTokens), int64(u.OutputTokens), time.Now())
		}
		meter.Add(usage.Entry{API: api, Model: model, Calls: 1, InputTokens: miss, CachedTokens: int64(u.CachedTokens),
			OutputTokens: int64(u.OutputTokens), CostUSD: cost})
		_ = a.store.SetChatTurnUsage(ctx, turn.ID, meter.Entries())
		_ = a.store.AddChatUsage(ctx, chat.ID, store.ChatUsage{InputTokens: u.InputTokens,
			OutputTokens: u.OutputTokens, CachedTokens: u.CachedTokens})

		stored := store.ChatMessage{Role: "assistant", Content: msg.Content, Reasoning: msg.ReasoningContent}
		for _, tc := range msg.ToolCalls {
			args := json.RawMessage(tc.Function.Arguments)
			if !json.Valid(args) {
				args = json.RawMessage("{}")
			}
			stored.ToolCalls = append(stored.ToolCalls, store.ChatToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: args})
		}
		if _, err := a.store.AppendChatMessage(ctx, chat.ID, stored); err != nil {
			return err
		}
		if len(msg.ToolCalls) == 0 {
			return nil
		}
		for _, tc := range stored.ToolCalls {
			result, summary, terr := a.call(ctx, user, tc.Name, tc.Arguments)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			out := store.ChatMessage{Role: "tool", ToolCallID: tc.ID, ToolName: tc.Name, Content: summary}
			if terr != nil {
				out.IsError = true
				out.Content = terr.Error()
				result = map[string]string{"error": terr.Error()}
			}
			raw, _ := json.Marshal(result)
			out.Result = raw
			if _, err := a.store.AppendChatMessage(ctx, chat.ID, out); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("stopped after %d steps without a final answer", maxSteps)
}

// apiName names a provider for the bill: "openrouter", "deepseek", else its host.
func apiName(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return "llm"
	}
	switch h := u.Hostname(); {
	case h == "openrouter.ai" || strings.HasSuffix(h, ".openrouter.ai"):
		return "openrouter"
	case strings.HasSuffix(h, "deepseek.com"):
		return "deepseek"
	default:
		return h
	}
}

// conversation is what the model sees: the system prompt and the stored
// chat (after its folded part), with older tool results cut short (the recent ones matter; the old
// ones mostly cost tokens). DeepSeek's reasoning goes back with each of its
// messages, as its thinking mode requires.
func conversation(system string, history []store.ChatMessage) []llm.Message {
	out := []llm.Message{{Role: "system", Content: system}}
	recentFrom := len(history) - 8
	for i, m := range history {
		switch m.Role {
		case "user":
			out = append(out, llm.Message{Role: "user", Content: m.Content})
		case "assistant":
			msg := llm.Message{Role: "assistant", Content: m.Content, ReasoningContent: m.Reasoning}
			for _, tc := range m.ToolCalls {
				var c llm.ToolCall
				c.ID, c.Type = tc.ID, "function"
				c.Function.Name, c.Function.Arguments = tc.Name, string(tc.Arguments)
				msg.ToolCalls = append(msg.ToolCalls, c)
			}
			out = append(out, msg)
		case "tool":
			content := string(m.Result)
			limit := freshToolLimit
			if i < recentFrom {
				limit = oldToolResult
			}
			if len(content) > limit {
				content = content[:limit] + "…(truncated)"
			}
			out = append(out, llm.Message{Role: "tool", ToolCallID: m.ToolCallID, Content: content})
		}
	}
	return out
}

func systemPrompt(user *store.User, publicURL string) string {
	name := user.DisplayName
	if name == "" {
		name = user.Username
	}
	loc, err := time.LoadLocation(user.Timezone)
	if err != nil {
		loc = time.UTC
	}
	return fmt.Sprintf(`You are the assistant inside Forge, %s's command center (%s). Forge tracks their projects (with repos, servers and monitored endpoints), a task board, a daily check-up, and the machines that run the Forge agent. Those machines execute Claude Code sessions and named shell commands that Forge queues as "runs".

How to work:
- Look things up with the tools instead of guessing. Project keys, repo names and machine names come from list_projects / list_machines.
- Real work on code — reading it, answering questions about it, reviewing, fixing bugs, writing tests or features — is delegated to Claude Code with delegate_to_claude. Claude starts in the repo with the project's Forge context but NOT this conversation, so write a complete, self-contained prompt: the goal, relevant details from the chat, constraints, and what to report back.
- Engine: runs use Forge's default engine (Settings → Agent engine). Only pass engine ("claude" or "deepseek") when %s explicitly asks for one for that work.
- Permission modes: "plan" (read-only) for questions, investigation and reviews; "acceptEdits" only when %s asked for changes. For changes, prefer worktree: true (an isolated branch) unless they want the edit in their checkout. Never try to bypass permissions.
- After delegating you may wait_for_runs (at most 10 minutes per call) and summarise the outcome with the key findings. For longer work, say what you queued and give the run link (%s/agents/runs/<id>); they can ask you to check later.
- Machines: omit machine to use the master. Commands marked confirm (store uploads, deploys) cannot be run from here — tell the user to run them from the Agents page.
- Memory: Forge keeps durable notes per project (decisions, conventions, gotchas, how-tos) and about the user. recall before answering questions about how a project works or what was decided; remember what the user tells you that should outlast this chat (a preference, a decision and its reason) — one fact per memory, no secrets; forget a memory that turned out wrong. Claude Code sessions start with these memories, so a good memory saves every later session from rediscovering it.
- Keep the task board current when it helps: create or update tasks, link runs to tasks with task_ref, comment on what was done.
- Be concise and concrete. Answer in the user's language. Use Markdown.

Today is %s (%s).`, name, publicURL, name, name, publicURL, time.Now().In(loc).Format("Monday 2 January 2006"), user.Timezone)
}

// userMemories is what Forge remembers about the user (memories with no
// project), appended to the system prompt of each turn.
func (a *Assistant) userMemories(ctx context.Context) string {
	ms, err := a.store.ListMemories(ctx, store.MemoryFilter{Global: true, Limit: 15})
	if err != nil || len(ms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nWhat you remember about the user (memory; recall searches the projects' memories):\n")
	for _, m := range ms {
		b.WriteString("- " + m.Line(300) + "\n")
	}
	return b.String()
}
