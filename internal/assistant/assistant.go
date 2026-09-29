// Package assistant is Forge's main agent: a chat with an LLM (any
// OpenAI-compatible API) that reads and updates Forge through tools and
// delegates real work to Claude Code on the user's machines by queuing runs —
// the same runs the "New run" dialog creates, under the same machine rules.
//
// The loop runs on the server, one turn per user message: call the model,
// run the tools it asks for, append everything to the chat, repeat until it
// answers without tools. The web app only posts messages and polls.
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

var ErrOff = errors.New("the assistant is not set up: add an API key and turn it on in Settings → Assistant")

// ErrNoMachine: a Claude Code turn needs a machine that runs claude.
var ErrNoMachine = errors.New("no machine can run Claude Code for the assistant: pair one (the master) and install Claude Code there")

// Hooks are what the assistant needs from the API layer.
type Hooks struct {
	// Key returns the provider API key (vault, tag integration:assistant).
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
// the assistant is off or has no key.
func (a *Assistant) client(ctx context.Context) (*llm.Client, store.AssistantSettings, error) {
	set, err := a.store.AssistantSettings(ctx)
	if err != nil {
		return nil, set, err
	}
	if !set.Enabled {
		return nil, set, ErrOff
	}
	if k, err := a.hooks.Key(ctx); err != nil || k == "" {
		return nil, set, ErrOff
	}
	return llm.New(set.BaseURL, a.hooks.Key), set, nil
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
	ids, err := llm.New(set.BaseURL, a.hooks.Key).Models(ctx)
	if err != nil {
		return []string{}
	}
	return ids
}

// DefaultSettings are what a new chat starts with: the configured provider
// model on DeepSeek.
func (a *Assistant) DefaultSettings(ctx context.Context) store.ChatSettings {
	set, _ := a.store.AssistantSettings(ctx)
	return store.ChatSettings{Engine: "deepseek", Model: set.Model}
}

// Start runs a turn for the chat's newest user message. On DeepSeek the
// server runs it in the background; on Claude Code it is a run on the
// master machine and ends when that run does (FinishSession). It fails with
// ErrConflict when a turn is already running.
func (a *Assistant) Start(ctx context.Context, chatID int64, user *store.User) error {
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
	turn, err := a.store.CreateChatTurn(ctx, chatID, seq, chat.ChatSettings)
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
	model := turn.Model
	if model == "" {
		model = set.Model
	}
	meter := usage.NewMeter()
	ctx = usage.WithMeter(context.WithValue(ctx, turnKey{}, turn.ID), meter)
	defer func() { _ = a.store.SetChatTurnUsage(context.Background(), turn.ID, meter.Entries()) }()
	api := apiName(set.BaseURL)
	tools := toolDefs()
	for step := 0; step < maxSteps; step++ {
		history, err := a.store.ChatMessages(ctx, chat.ID, 0)
		if err != nil {
			return err
		}
		msg, u, err := client.Chat(ctx, model, turn.Effort, a.conversation(user, history), tools)
		if err != nil {
			return err
		}
		miss := int64(u.InputTokens - u.CachedTokens)
		meter.Add(usage.Entry{API: api, Model: model, Calls: 1, InputTokens: miss, CachedTokens: int64(u.CachedTokens),
			OutputTokens: int64(u.OutputTokens), CostUSD: usage.DeepSeekCost(model, miss, int64(u.CachedTokens), int64(u.OutputTokens), time.Now())})
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

// apiName names a provider for the bill: "deepseek" for DeepSeek, else its host.
func apiName(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return "llm"
	}
	if strings.HasSuffix(u.Hostname(), "deepseek.com") {
		return "deepseek"
	}
	return u.Hostname()
}

// conversation is what the model sees: the system prompt and the stored
// chat, with older tool results cut short (the recent ones matter; the old
// ones mostly cost tokens). DeepSeek's reasoning goes back with each of its
// messages, as its thinking mode requires.
func (a *Assistant) conversation(user *store.User, history []store.ChatMessage) []llm.Message {
	out := []llm.Message{{Role: "system", Content: systemPrompt(user, a.store.PublicURL)}}
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
- Engine: runs use Forge's default engine (normally DeepSeek, which costs a fraction of Claude). Only pass engine: "claude" when %s explicitly asks for Claude for that work; never pick it on your own because a task looks hard.
- Permission modes: "plan" (read-only) for questions, investigation and reviews; "acceptEdits" only when %s asked for changes. For changes, prefer worktree: true (an isolated branch) unless they want the edit in their checkout. Never try to bypass permissions.
- After delegating you may wait_for_runs (at most 10 minutes per call) and summarise the outcome with the key findings. For longer work, say what you queued and give the run link (%s/agents/runs/<id>); they can ask you to check later.
- Machines: omit machine to use the master. Commands marked confirm (store uploads, deploys) cannot be run from here — tell the user to run them from the Agents page.
- Keep the task board current when it helps: create or update tasks, link runs to tasks with task_ref, comment on what was done.
- Be concise and concrete. Answer in the user's language. Use Markdown.

Today is %s (%s).`, name, publicURL, name, name, publicURL, time.Now().In(loc).Format("Monday 2 January 2006"), user.Timezone)
}
