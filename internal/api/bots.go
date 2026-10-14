package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/store"
)

// Bots, continued: settings, and the proactive loop. A proactive bot gets
// turns Forge starts itself — the project's events, once a burst has
// settled (botSettle), and a daily review at its review time — at most
// store.BotDailyCap a day. Those turns are read-only for code (the
// assistant enforces it); a bot's answer to one goes to Telegram too,
// unless it is just "Nothing to do."

const (
	botTick   = 30 * time.Second
	botSettle = 2 * time.Minute
)

func errBotOff(key string) error {
	return &store.ValidationError{Field: "bot", Message: "the " + key + " bot is off: turn it on in its settings"}
}

func (s *Server) updateBot(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, err := s.store.ProjectIDByKey(r.Context(), strings.ToUpper(r.PathValue("key")))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in store.BotSettingsPatch
	if !decode(w, r, &in) {
		return
	}
	set, err := s.store.UpdateBotSettings(r.Context(), id, in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": set})
}

// reviewBot runs the daily review now, at the user's request: it works
// whether or not the bot is proactive, and does not count against the cap.
func (s *Server) reviewBot(w http.ResponseWriter, r *http.Request, u *store.User) {
	key := strings.ToUpper(r.PathValue("key"))
	chat, created, err := s.startBotTurn(r.Context(), u, key, "schedule", store.BotReviewMessage, false)
	if err != nil {
		s.assistantErr(w, r, err)
		return
	}
	msgs, err := s.store.ChatMessages(r.Context(), chat.ID, 0)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	turns, err := s.store.ChatTurns(r.Context(), chat.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	if len(msgs) > 0 {
		msgs = msgs[len(msgs)-1:]
	}
	writeJSON(w, status, map[string]any{"chat": chat, "messages": msgs, "turns": turns})
}

// startBotTurn sends content to the project's bot (creating its chat on the
// first message) and starts a turn with that origin: "telegram" for the
// user on Telegram, "event" or "schedule" for Forge. With toTelegram the
// answer goes to Telegram too. created says the chat is new.
func (s *Server) startBotTurn(ctx context.Context, u *store.User, key, origin, content string, toTelegram bool) (*store.Chat, bool, error) {
	p, err := s.store.ProjectByKey(ctx, store.TodayFor(u.Timezone), key)
	if err != nil {
		return nil, false, err
	}
	if p.Status == "archived" {
		return nil, false, &store.ValidationError{Field: "project", Message: "the project is archived"}
	}
	set, err := s.store.BotSettingsFor(ctx, p.ID)
	if err != nil {
		return nil, false, err
	}
	if !set.Enabled {
		return nil, false, errBotOff(p.Key)
	}
	unlock := s.botLocks.lock(p.ID)
	defer unlock()
	created := false
	chat, err := s.store.BotChat(ctx, p.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		cs := s.assistant.BotSettings(ctx)
		if err := s.assistant.Ready(ctx, cs.Engine); err != nil {
			return nil, false, err
		}
		if chat, err = s.store.EnsureBotChat(ctx, p.ID, p.Name+" bot", cs); err != nil {
			return nil, false, err
		}
		created = true
	case err != nil:
		return nil, false, err
	case chat.Busy:
		return nil, false, fmt.Errorf("%w: the %s bot is still working", store.ErrConflict, p.Key)
	default:
		if err := s.assistant.Ready(ctx, chat.Engine); err != nil {
			return nil, false, err
		}
	}
	m, err := s.store.AppendChatMessage(ctx, chat.ID, store.ChatMessage{Role: "user", Content: content, Origin: origin})
	if err != nil {
		return nil, false, err
	}
	if err := s.assistant.StartFrom(ctx, chat.ID, u, origin); err != nil {
		return nil, false, err
	}
	if toTelegram {
		s.tgFollowBot(chat.ID, m.Seq, p.Key, origin)
	}
	if chat, err = s.store.ChatByID(ctx, chat.ID); err != nil {
		return nil, false, err
	}
	return chat, created, nil
}

// RunBots gives proactive bots their events and daily reviews until ctx
// ends.
func (s *Server) RunBots(ctx context.Context) {
	t := time.NewTicker(s.botTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.botPass(ctx)
	}
}

func (s *Server) botPass(ctx context.Context) {
	user, err := s.store.FirstUser(ctx)
	if err != nil {
		return
	}
	loc, err := time.LoadLocation(user.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	today := now.Format("2006-01-02")

	due, err := s.store.DueBotEvents(ctx, s.botSettle)
	if err != nil {
		slog.Warn("bots: events", "err", err)
	}
	for _, d := range due {
		if ctx.Err() != nil {
			return
		}
		s.botEvents(ctx, user, today, d)
	}

	reviews, err := s.store.ReviewBots(ctx)
	if err != nil {
		slog.Warn("bots: reviews", "err", err)
		return
	}
	for _, b := range reviews {
		if ctx.Err() != nil {
			return
		}
		if !store.ReviewDue(b.ReviewAt, b.LastReviewDate, now) || !s.botCanStart(ctx, b.ProjectID) {
			continue
		}
		if err := s.store.MarkReviewed(ctx, b.ProjectID, today); err != nil {
			continue
		}
		s.proactiveTurn(ctx, user, today, b.ProjectID, b.Key, "schedule", store.BotReviewMessage, "the daily review")
	}
}

// botEvents sends a bot its settled events as one message — or keeps them
// while the bot is busy or cannot start, so they go together later.
func (s *Server) botEvents(ctx context.Context, user *store.User, today string, d store.DueBot) {
	ids := make([]int64, len(d.Events))
	for i, e := range d.Events {
		ids[i] = e.ID
	}
	set, err := s.store.BotSettingsFor(ctx, d.ProjectID)
	if err != nil {
		return
	}
	if !set.Enabled || !set.Proactive {
		_ = s.store.MarkBotEventsDelivered(ctx, ids) // turned off since
		return
	}
	if !s.botCanStart(ctx, d.ProjectID) {
		return // busy, or no machine right now: the events wait and coalesce
	}
	what := fmt.Sprintf("%d event(s)", len(d.Events))
	s.proactiveTurn(ctx, user, today, d.ProjectID, d.Key, "event", store.BotEventsMessage(d.Events), what)
	_ = s.store.MarkBotEventsDelivered(ctx, ids)
}

// botCanStart says whether the bot could take a turn now: not busy, and
// its engine ready.
func (s *Server) botCanStart(ctx context.Context, projectID int64) bool {
	engine := s.assistant.BotSettings(ctx).Engine
	if chat, err := s.store.BotChat(ctx, projectID); err == nil {
		if chat.Busy {
			s.assistant.Reconcile(ctx, chat.ID)
			if chat, err = s.store.ChatByID(ctx, chat.ID); err != nil || chat.Busy {
				return false
			}
		}
		engine = chat.Engine
	}
	return s.assistant.Ready(ctx, engine) == nil
}

// proactiveTurn starts a turn Forge asked for, within the day's cap. When
// the cap stops it, the bot's chat says so once that day.
func (s *Server) proactiveTurn(ctx context.Context, user *store.User, today string, projectID int64, key, origin, content, what string) {
	ok, err := s.store.TakeProactiveTurn(ctx, projectID, today, store.BotDailyCap)
	if err != nil {
		slog.Warn("bots: cap", "project", key, "err", err)
		return
	}
	if !ok {
		if s.store.NoteCapOnce(ctx, projectID, today) {
			if chat, err := s.store.BotChat(ctx, projectID); err == nil {
				_, _ = s.store.AppendChatMessage(ctx, chat.ID, store.ChatMessage{Role: "assistant",
					Content: fmt.Sprintf("Forge: this bot has had its %d proactive turns for today; %s and anything later today wait for you to ask.",
						store.BotDailyCap, what)})
			}
		}
		slog.Info("bots: daily cap reached", "project", key, "skipped", what)
		return
	}
	if _, _, err := s.startBotTurn(ctx, user, key, origin, content, true); err != nil {
		slog.Warn("bots: proactive turn", "project", key, "origin", origin, "err", err)
		return
	}
	slog.Info("bots: proactive turn", "project", key, "origin", origin, "what", what)
}
