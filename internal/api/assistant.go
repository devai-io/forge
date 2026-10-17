package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/assistant"
	"github.com/devai-io/forge/internal/llm"
	"github.com/devai-io/forge/internal/store"
)

// The Assistant (internal/assistant): its settings, and the chats it works in.

// modelsCache keeps the provider's model list for a while: the Settings page
// asks on every load.
type modelsCache struct {
	mu     sync.Mutex
	at     time.Time
	key    string
	models []string
}

func (s *Server) assistantStatus(ctx context.Context) (map[string]any, error) {
	set, err := s.store.AssistantSettings(ctx)
	if err != nil {
		return nil, err
	}
	k, _ := s.providerKey(ctx)
	models := s.providerModels(ctx)
	claude := map[string]any{"available": false, "machine": "", "reason": ""}
	if rn, err := s.store.MasterRunner(ctx); err != nil {
		claude["reason"] = "no master machine"
	} else if rn.LastSeenAt != nil && !rn.Capabilities.Claude {
		claude["machine"], claude["reason"] = rn.Name, rn.Name+" has no Claude Code"
	} else {
		claude["available"], claude["machine"], claude["online"] = true, rn.Name, rn.Online
		claude["skip_permissions"] = store.OneOf("bypassPermissions", rn.Capabilities.PermissionModes)
	}
	return map[string]any{"settings": set, "key_configured": k != "", "models": models,
		"engines": map[string]any{
			"codex":    s.codexStatus(ctx),
			"deepseek": map[string]any{"available": k != "", "models": models, "efforts": llm.Efforts},
			"claude": map[string]any{"available": claude["available"], "machine": claude["machine"], "online": claude["online"],
				"reason": claude["reason"], "models": claudeModels, "efforts": store.ClaudeEfforts,
				"skip_permissions": claude["skip_permissions"] == true},
		}}, nil
}

// claudeModels are Claude Code's model aliases (the chat can also name a full id).
var claudeModels = []string{"opus", "sonnet", "haiku", "fable"}

var modelNameRe = regexp.MustCompile(`^[A-Za-z0-9._:/\[\]-]{1,100}$`)

// chatSettings validates what a request asks for, on top of cur.
func chatSettings(cur store.ChatSettings, engine, model, effort *string, edits, skip *bool) (store.ChatSettings, error) {
	set := cur
	if engine != nil && *engine != set.Engine {
		if !store.OneOf(*engine, store.Engines) {
			return set, &store.ValidationError{Field: "engine", Message: "must be claude, codex or deepseek"}
		}
		set.Engine, set.Model, set.Effort = *engine, "", "" // another engine's model and effort mean nothing here
	}
	if model != nil {
		set.Model = strings.TrimSpace(*model)
	}
	if effort != nil {
		set.Effort = *effort
	}
	if edits != nil {
		set.Edits = *edits
	}
	if skip != nil {
		set.SkipPermissions = *skip
	}
	if set.Model != "" && !modelNameRe.MatchString(set.Model) {
		return set, &store.ValidationError{Field: "model", Message: "not a model name"}
	}
	efforts := llm.Efforts
	if set.Engine == "claude" {
		efforts = store.ClaudeEfforts
	}
	if set.Engine == "codex" {
		efforts = store.CodexEfforts
	}
	if set.Effort != "" && !store.OneOf(set.Effort, efforts) {
		return set, &store.ValidationError{Field: "effort", Message: "must be one of " + strings.Join(efforts, ", ")}
	}
	return set, nil
}

type chatSettingsInput struct {
	Engine *string `json:"engine"`
	Model  *string `json:"model"`
	Effort *string `json:"effort"`
	Edits  *bool   `json:"edits"`
	// SkipPermissions: bypassPermissions (--dangerously-skip-permissions).
	SkipPermissions *bool `json:"skip_permissions"`
}

func (s *Server) getAssistant(w http.ResponseWriter, r *http.Request, u *store.User) {
	st, err := s.assistantStatus(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) updateAssistant(w http.ResponseWriter, r *http.Request, u *store.User) {
	set, err := s.store.AssistantSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in struct {
		Enabled *bool   `json:"enabled"`
		Engine  *string `json:"engine"`
		Model   *string `json:"model"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Engine != nil {
		if !store.OneOf(*in.Engine, store.Engines) {
			writeErr(w, r, &store.ValidationError{Field: "engine", Message: "must be claude, codex or deepseek"})
			return
		}
		set.Engine = *in.Engine
	}
	if in.Model != nil {
		v := strings.TrimSpace(*in.Model)
		if v == "" || len(v) > 100 {
			writeErr(w, r, &store.ValidationError{Field: "model", Message: "must be 1-100 characters"})
			return
		}
		set.Model = v
	}
	if in.Enabled != nil {
		set.Enabled = *in.Enabled
	}
	if err := s.store.SetAssistantSettings(r.Context(), set); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "assistant_settings", set.Engine+" "+set.Model)
	s.getAssistant(w, r, u)
}

// ── Chats ─────────────────────────────────────────────────────────────────

func (s *Server) assistantErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, assistant.ErrNoMachine) {
		writeError(w, http.StatusServiceUnavailable, "no_machine", err.Error())
		return
	}
	if errors.Is(err, assistant.ErrOff) {
		writeError(w, http.StatusServiceUnavailable, "assistant_off", err.Error())
		return
	}
	if errors.Is(err, assistant.ErrNoKey) {
		writeError(w, http.StatusServiceUnavailable, "no_provider_key", err.Error())
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "busy", "the assistant is still working on this chat")
		return
	}
	writeErr(w, r, err)
}

func (s *Server) listChats(w http.ResponseWriter, r *http.Request, u *store.User) {
	chats, err := s.store.ListChats(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": chats})
}

type messageInput struct {
	Content string `json:"content"`
	chatSettingsInput
}

func decodeContent(w http.ResponseWriter, r *http.Request) (messageInput, bool) {
	var in messageInput
	if !decode(w, r, &in) {
		return in, false
	}
	in.Content = strings.TrimSpace(in.Content)
	if in.Content == "" || len(in.Content) > 50000 {
		writeErr(w, r, &store.ValidationError{Field: "content", Message: "write a message (at most 50,000 characters)"})
		return in, false
	}
	return in, true
}

func (s *Server) createChat(w http.ResponseWriter, r *http.Request, u *store.User) {
	in, ok := decodeContent(w, r)
	if !ok {
		return
	}
	set, err := chatSettings(s.assistant.DefaultSettings(r.Context()), in.Engine, in.Model, in.Effort, in.Edits, in.SkipPermissions)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.assistant.Ready(r.Context(), set.Engine); err != nil {
		s.assistantErr(w, r, err)
		return
	}
	content := in.Content
	chat, err := s.store.CreateChat(r.Context(), content, set)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.startTurn(w, r, u, chat.ID, content, http.StatusCreated)
}

func (s *Server) sendChatMessage(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	in, ok := decodeContent(w, r)
	if !ok {
		return
	}
	chat, err := s.store.ChatByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if chat.Busy {
		s.assistantErr(w, r, store.ErrConflict)
		return
	}
	set, err := chatSettings(chat.ChatSettings, in.Engine, in.Model, in.Effort, in.Edits, in.SkipPermissions)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.assistant.Ready(r.Context(), set.Engine); err != nil {
		s.assistantErr(w, r, err)
		return
	}
	if set != chat.ChatSettings {
		if _, err := s.store.SetChatSettings(r.Context(), id, set); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	s.startTurn(w, r, u, id, in.Content, http.StatusOK)
}

// startTurn appends the user's message and sets the agent going.
func (s *Server) startTurn(w http.ResponseWriter, r *http.Request, u *store.User, chatID int64, content string, status int) {
	m, err := s.store.AppendChatMessage(r.Context(), chatID, store.ChatMessage{Role: "user", Content: content})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.assistant.Start(r.Context(), chatID, u); err != nil {
		s.assistantErr(w, r, err)
		return
	}
	chat, err := s.store.ChatByID(r.Context(), chatID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	turns, err := s.store.ChatTurns(r.Context(), chatID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, status, map[string]any{"chat": chat, "messages": []store.ChatMessage{*m}, "turns": turns})
}

func (s *Server) getChat(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	chat, err := s.store.ChatByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if chat.Busy {
		s.assistant.Reconcile(r.Context(), id)
		if chat, err = s.store.ChatByID(r.Context(), id); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	msgs, err := s.store.ChatMessages(r.Context(), id, queryInt(r, "after", 0))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	turns, err := s.store.ChatTurns(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat": chat, "messages": msgs, "turns": turns})
}

func (s *Server) stopChat(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.assistant.Stop(r.Context(), id)
	// The turn releases the chat as it unwinds; give it a moment so the
	// answer already says busy: false.
	for i := 0; i < 20; i++ {
		chat, err := s.store.ChatByID(r.Context(), id)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		if !chat.Busy || i == 19 {
			writeJSON(w, http.StatusOK, map[string]any{"chat": chat})
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Server) renameChat(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Title *string `json:"title"`
		chatSettingsInput
	}
	if !decode(w, r, &in) {
		return
	}
	chat, err := s.store.ChatByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if in.Title != nil {
		if chat, err = s.store.RenameChat(r.Context(), id, *in.Title); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	set, err := chatSettings(chat.ChatSettings, in.Engine, in.Model, in.Effort, in.Edits, in.SkipPermissions)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if set != chat.ChatSettings { // applies from the next turn
		if chat, err = s.store.SetChatSettings(r.Context(), id, set); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat": chat})
}

func (s *Server) deleteChat(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.assistant.Stop(r.Context(), id)
	if err := s.store.DeleteChat(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Ask runs one Assistant turn outside the web app (forge assistant ask): a
// new chat for the given account, waited on until it ends. A Claude Code
// turn is answered by the running server's machine, like any other.
func (s *Server) Ask(ctx context.Context, login, content string, set store.ChatSettings) (*store.Chat, error) {
	u, err := s.store.UserByLogin(ctx, login)
	if err != nil {
		return nil, fmt.Errorf("no account %q", login)
	}
	opt := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	set, err = chatSettings(s.assistant.DefaultSettings(ctx), opt(set.Engine), opt(set.Model), opt(set.Effort), &set.Edits, &set.SkipPermissions)
	if err != nil {
		return nil, err
	}
	if err := s.assistant.Ready(ctx, set.Engine); err != nil {
		return nil, err
	}
	chat, err := s.store.CreateChat(ctx, content, set)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.AppendChatMessage(ctx, chat.ID, store.ChatMessage{Role: "user", Content: content}); err != nil {
		return nil, err
	}
	if err := s.assistant.Start(ctx, chat.ID, u); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			s.assistant.Stop(context.Background(), chat.ID)
			return chat, ctx.Err()
		case <-time.After(time.Second):
		}
		s.assistant.Reconcile(ctx, chat.ID)
		if chat, err = s.store.ChatByID(ctx, chat.ID); err != nil || !chat.Busy {
			return chat, err
		}
	}
}

// ── Bots ──────────────────────────────────────────────────────────────────
//
// A bot is a project's own long-lived chat. The conversation itself goes
// through the chat endpoints; these list the bots and start (or continue)
// one by project key.

func (s *Server) listBots(w http.ResponseWriter, r *http.Request, u *store.User) {
	bots, err := s.store.ListBots(r.Context(), store.TodayFor(u.Timezone))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bots": bots})
}

func (s *Server) sendBotMessage(w http.ResponseWriter, r *http.Request, u *store.User) {
	p, err := s.store.ProjectByKey(r.Context(), store.TodayFor(u.Timezone), r.PathValue("key"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if p.Status == "archived" {
		writeErr(w, r, &store.ValidationError{Field: "project", Message: "the project is archived"})
		return
	}
	in, ok := decodeContent(w, r)
	if !ok {
		return
	}
	if bs, err := s.store.BotSettingsFor(r.Context(), p.ID); err != nil {
		writeErr(w, r, err)
		return
	} else if !bs.Enabled {
		writeErr(w, r, errBotOff(p.Key))
		return
	}
	status := http.StatusOK
	chat, err := s.store.BotChat(r.Context(), p.ID)
	cur := s.assistant.BotSettings(r.Context())
	switch {
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusCreated
	case err != nil:
		writeErr(w, r, err)
		return
	case chat.Busy:
		s.assistantErr(w, r, store.ErrConflict)
		return
	default:
		cur = chat.ChatSettings
	}
	set, err := chatSettings(cur, in.Engine, in.Model, in.Effort, in.Edits, in.SkipPermissions)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.assistant.Ready(r.Context(), set.Engine); err != nil {
		s.assistantErr(w, r, err)
		return
	}
	if chat == nil {
		if chat, err = s.store.EnsureBotChat(r.Context(), p.ID, p.Name+" bot", set); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	if set != chat.ChatSettings {
		if _, err := s.store.SetChatSettings(r.Context(), chat.ID, set); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	s.startTurn(w, r, u, chat.ID, in.Content, status)
}

func (s *Server) codexStatus(ctx context.Context) map[string]any {
	st := map[string]any{"available": false, "machine": "", "online": nil, "reason": "no master machine", "models": []string{}, "efforts": store.CodexEfforts, "skip_permissions": false}
	if rn, err := s.store.MasterRunner(ctx); err == nil {
		st["machine"], st["online"] = rn.Name, rn.Online
		st["reason"] = rn.Name + " has no Codex; install Codex and run forge agent setup-codex"
		if rn.Capabilities.Codex {
			st["available"], st["reason"] = true, ""
		}
		st["skip_permissions"] = store.OneOf("bypassPermissions", rn.Capabilities.PermissionModes)
	}
	return st
}
