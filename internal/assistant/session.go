package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/usage"
)

// Claude Code turns. A chat on the "claude" engine is a Claude Code session
// on the master machine, in its home folder (the first allowed root): each
// turn is an agent run resuming the chat's previous session, so the
// conversation lives in Claude Code and costs the machine's Claude
// subscription, not an API key. The turn ends when its run does
// (FinishSession, called when the machine reports it, or Reconcile).

const missedChars = 20000 // transcript handed to Claude Code for turns it did not see

func (a *Assistant) startSession(ctx context.Context, chat *store.Chat, turn *store.ChatTurn, msgs []store.ChatMessage, user *store.User) error {
	rn, err := a.sessionMachine(ctx)
	if err != nil {
		return err
	}
	prompt := ""
	for _, m := range msgs {
		if m.Seq == turn.Seq {
			prompt = m.Content
		}
	}
	// What Claude Code has not seen: the whole chat before its first turn,
	// or what other engines answered since its last one (a Claude turn's
	// answer is the message right after its user message).
	seen := 0
	prev, err := a.store.LastSessionRun(ctx, chat.ID)
	if err == nil && prev.RunnerID != 0 {
		if pt, err := a.store.ChatTurnByID(ctx, *prev.ChatTurnID); err == nil {
			seen = pt.Seq + 1
		}
	} else {
		prev = nil
	}
	if missed := transcript(msgs, seen, turn.Seq); missed != "" {
		prompt = "Earlier in this chat (you did not see these turns; another engine answered them):\n\n" + missed +
			"\n\n---\n\n" + prompt
	}

	mode := "plan"
	if chat.Edits {
		mode = "acceptEdits"
	}
	effort := chat.Effort
	if !store.OneOf(effort, store.ClaudeEfforts) {
		effort = ""
	}
	in := store.RunInput{RunnerID: rn.ID, Kind: "agent", Prompt: prompt, PermissionMode: mode, Model: chat.Model,
		Effort: effort, Engine: "claude", ChatTurnID: &turn.ID, AppendSystem: sessionPrompt(user, rn.Name, mode, a.store.PublicURL)}
	if prev != nil {
		in.ResumeRunID = &prev.ID
	}
	run, err := a.store.CreateRun(ctx, in)
	if err != nil {
		return err
	}
	if err := a.store.SetChatTurnRun(ctx, turn.ID, run.ID); err != nil {
		return err
	}
	if a.hooks.Queued != nil {
		a.hooks.Queued(run.RunnerID)
	}
	return nil
}

// transcript renders user and assistant messages with from < seq < to.
func transcript(msgs []store.ChatMessage, from, to int) string {
	var parts []string
	for _, m := range msgs {
		if m.Seq <= from || m.Seq >= to || strings.TrimSpace(m.Content) == "" {
			continue
		}
		switch m.Role {
		case "user":
			parts = append(parts, "User: "+m.Content)
		case "assistant":
			parts = append(parts, "Assistant: "+m.Content)
		}
	}
	out := strings.Join(parts, "\n\n")
	if len(out) > missedChars {
		out = "…" + out[len(out)-missedChars:]
	}
	return out
}

// FinishSession ends the Claude Code turn a finished run belongs to: its
// answer becomes the assistant's message and its token counts the turn's
// usage. Harmless for any other run, or when the turn already ended.
func (a *Assistant) FinishSession(ctx context.Context, run *store.Run) {
	if run == nil || run.ChatTurnID == nil || run.RepoID != 0 {
		return
	}
	turn, err := a.store.ChatTurnByID(ctx, *run.ChatTurnID)
	if err != nil || turn.Status != "running" {
		return
	}
	status, msg := "done", ""
	switch run.Status {
	case "succeeded":
	case "cancelled":
		status, msg = "stopped", "stopped"
	case "failed":
		status, msg = "failed", run.Error
		if msg == "" {
			msg = "Claude Code failed"
		}
	default:
		return // still going
	}
	if ok, err := a.store.FinishChatTurn(ctx, turn.ID, status, msg); err != nil || !ok {
		return
	}
	entries := sessionUsage(run)
	_ = a.store.SetChatTurnUsage(ctx, turn.ID, entries)
	var total store.ChatUsage
	for _, e := range entries {
		total.InputTokens += int(e.InputTokens + e.CachedTokens)
		total.CachedTokens += int(e.CachedTokens)
		total.OutputTokens += int(e.OutputTokens)
	}
	_ = a.store.AddChatUsage(ctx, turn.ChatID, total)
	if strings.TrimSpace(run.Result) != "" {
		if _, err := a.store.AppendChatMessage(ctx, turn.ChatID, store.ChatMessage{Role: "assistant", Content: run.Result}); err != nil {
			slog.Error("assistant: session answer", "chat", turn.ChatID, "err", err)
		}
	}
	if err := a.store.ReleaseChat(ctx, turn.ChatID, msg); err != nil {
		slog.Error("assistant: release chat", "chat", turn.ChatID, "err", err)
	}
}

// Reconcile ends a Claude Code turn whose run finished without telling the
// assistant (a runner that went offline, a server restart mid-report).
func (a *Assistant) Reconcile(ctx context.Context, chatID int64) {
	turn, err := a.store.RunningChatTurn(ctx, chatID)
	if err != nil || turn.RunID == nil {
		return
	}
	run, err := a.store.RunByID(ctx, *turn.RunID)
	if errors.Is(err, store.ErrNotFound) {
		if ok, _ := a.store.FinishChatTurn(ctx, turn.ID, "failed", "its Claude Code run was deleted"); ok {
			_ = a.store.ReleaseChat(ctx, chatID, "its Claude Code run was deleted")
		}
		return
	}
	if err == nil && run.FinishedAt != nil {
		a.FinishSession(ctx, run)
	}
}

// sessionUsage turns Claude Code's per-model token counts into bill lines.
// Claude Code prices them at Anthropic's API rates; on a subscription that
// is what the work would have cost, not what was charged.
func sessionUsage(run *store.Run) []usage.Entry {
	var models map[string]struct {
		InputTokens              int64    `json:"inputTokens"`
		OutputTokens             int64    `json:"outputTokens"`
		CacheReadInputTokens     int64    `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int64    `json:"cacheCreationInputTokens"`
		CostUSD                  *float64 `json:"costUSD"`
	}
	const note = "Claude Code on the machine's login; cost at API prices"
	out := []usage.Entry{}
	if json.Unmarshal(run.Usage, &models) == nil {
		for model, u := range models {
			out = append(out, usage.Entry{API: "claude-code", Model: model, Calls: 1,
				InputTokens: u.InputTokens + u.CacheCreationInputTokens, CachedTokens: u.CacheReadInputTokens,
				OutputTokens: u.OutputTokens, CostUSD: u.CostUSD, Note: note})
		}
	}
	if len(out) == 0 && run.CostUSD != nil {
		out = append(out, usage.Entry{API: "claude-code", Model: run.Model, Calls: 1, CostUSD: run.CostUSD, Note: note})
	}
	return out
}

func sessionPrompt(user *store.User, machine, mode, publicURL string) string {
	name := user.DisplayName
	if name == "" {
		name = user.Username
	}
	loc, err := time.LoadLocation(user.Timezone)
	if err != nil {
		loc = time.UTC
	}
	perm := "This chat is read-only (plan mode): you can read files and Forge (forge_projects, forge_tasks, forge_task, " +
		"forge_context, forge_checkup) but change nothing — no files, no tasks. If a change is needed, say what you would do; " +
		name + " can allow changes for this chat."
	if mode == "acceptEdits" {
		perm = "You may edit files and update Forge tasks. Shell commands this machine has not pre-approved will be refused."
	}
	return fmt.Sprintf(`You are the assistant in Forge's chat (%s), talking with %s through its web app. This is Claude Code on their machine %q, started in its home folder (their projects live under it). Forge tracks their projects, repos, servers, tasks, machines and daily check-up: use the forge MCP tools for it. Messages come from the web app, not a terminal: nobody can approve a tool prompt, so a refused tool stays refused — say what you would need instead of retrying. %s Be concise and concrete, answer in the user's language, use Markdown. Today is %s.`,
		publicURL, name, machine, perm, time.Now().In(loc).Format("Monday 2 January 2006"))
}
