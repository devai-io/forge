package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/assistant"
	"github.com/devai-io/forge/internal/llm"
	"github.com/devai-io/forge/internal/store"
)

// The Assistant (internal/assistant): its settings, and the chats it works in.

const assistantTag = "integration:assistant"

func (s *Server) assistantKey(ctx context.Context) (string, error) {
	if !s.box.Available() {
		return "", errors.New("vault unavailable")
	}
	k, err := s.store.IntegrationSecret(ctx, s.box, assistantTag)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return k, err
}

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
	k, _ := s.assistantKey(ctx)
	models := []string{}
	if k != "" {
		c := &s.models
		c.mu.Lock()
		cacheKey := set.BaseURL + "|" + k
		if c.key == cacheKey && time.Since(c.at) < 10*time.Minute {
			models = c.models
		}
		c.mu.Unlock()
		if len(models) == 0 {
			models = s.assistant.Models(ctx)
			c.mu.Lock()
			c.key, c.at, c.models = cacheKey, time.Now(), models
			c.mu.Unlock()
		}
	}
	claude := map[string]any{"available": false, "machine": "", "reason": ""}
	if rn, err := s.store.MasterRunner(ctx); err != nil {
		claude["reason"] = "no master machine"
	} else if rn.LastSeenAt != nil && !rn.Capabilities.Claude {
		claude["machine"], claude["reason"] = rn.Name, rn.Name+" has no Claude Code"
	} else {
		claude["available"], claude["machine"], claude["online"] = true, rn.Name, rn.Online
	}
	return map[string]any{"settings": set, "key_configured": k != "", "models": models,
		"engines": map[string]any{
			"deepseek": map[string]any{"available": k != "", "models": models, "efforts": llm.Efforts},
			"claude": map[string]any{"available": claude["available"], "machine": claude["machine"], "online": claude["online"],
				"reason": claude["reason"], "models": claudeModels, "efforts": store.ClaudeEfforts},
		}}, nil
}

// claudeModels are Claude Code's model aliases (the chat can also name a full id).
var claudeModels = []string{"opus", "sonnet", "haiku", "fable"}

var modelNameRe = regexp.MustCompile(`^[A-Za-z0-9._:/\[\]-]{1,100}$`)

// chatSettings validates what a request asks for, on top of cur.
func chatSettings(cur store.ChatSettings, engine, model, effort *string, edits *bool) (store.ChatSettings, error) {
	set := cur
	if engine != nil && *engine != set.Engine {
		if !store.OneOf(*engine, store.Engines) {
			return set, &store.ValidationError{Field: "engine", Message: "must be deepseek or claude"}
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
	if set.Model != "" && !modelNameRe.MatchString(set.Model) {
		return set, &store.ValidationError{Field: "model", Message: "not a model name"}
	}
	efforts := llm.Efforts
	if set.Engine == "claude" {
		efforts = store.ClaudeEfforts
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
		BaseURL *string `json:"base_url"`
		Model   *string `json:"model"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.BaseURL != nil {
		v := strings.TrimRight(strings.TrimSpace(*in.BaseURL), "/")
		if p, err := url.Parse(v); err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" {
			writeErr(w, r, &store.ValidationError{Field: "base_url", Message: "must be an http(s) URL"})
			return
		}
		set.BaseURL = v
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
	s.sec(r, "assistant_settings", set.BaseURL+" "+set.Model)
	s.getAssistant(w, r, u)
}

func (s *Server) setAssistantKey(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		APIKey string `json:"api_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.APIKey = strings.TrimSpace(in.APIKey)
	if len(in.APIKey) < 16 || strings.ContainsAny(in.APIKey, " \t\n") {
		writeErr(w, r, &store.ValidationError{Field: "api_key", Message: "that does not look like an API key"})
		return
	}
	if err := s.store.SaveIntegrationSecret(r.Context(), s.box, "LLM API key (Assistant)", assistantTag, in.APIKey, s.clientIP(r)); err != nil {
		writeErr(w, r, err)
		return
	}
	s.models.mu.Lock()
	s.models.key = ""
	s.models.mu.Unlock()
	s.sec(r, "assistant_key", "Assistant API key replaced")
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
	set, err := chatSettings(s.assistant.DefaultSettings(r.Context()), in.Engine, in.Model, in.Effort, in.Edits)
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
	set, err := chatSettings(chat.ChatSettings, in.Engine, in.Model, in.Effort, in.Edits)
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
	set, err := chatSettings(chat.ChatSettings, in.Engine, in.Model, in.Effort, in.Edits)
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
	set, err = chatSettings(s.assistant.DefaultSettings(ctx), opt(set.Engine), opt(set.Model), opt(set.Effort), &set.Edits)
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
