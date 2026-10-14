package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Bot settings and proactive work. A project's bot is on by default and
// waits to be spoken to; a proactive bot also gets turns Forge starts
// itself — the project's events (bot_events, queued by the store where they
// happen) and a daily review — up to BotDailyCap a day.

// BotDailyCap bounds the turns Forge starts for one bot in a day.
const BotDailyCap = 12

const maxBotInstructions = 4000

type BotSettings struct {
	Enabled   bool `json:"enabled"`
	Proactive bool `json:"proactive"`
	// ReviewAt is the daily review's local time ("HH:MM"; "" = none).
	ReviewAt string `json:"review_at"`
	// Instructions are the user's standing orders for this bot.
	Instructions   string  `json:"instructions"`
	LastReviewDate *string `json:"last_review_date"`
	// ProactiveToday counts the turns Forge started on ProactiveDate.
	ProactiveToday int     `json:"proactive_today"`
	ProactiveDate  *string `json:"proactive_date"`
	DailyCap       int     `json:"daily_cap"`
	// PendingEvents waiting to go to the bot.
	PendingEvents int `json:"pending_events"`
}

func DefaultBotSettings() BotSettings {
	return BotSettings{Enabled: true, ReviewAt: "08:30", DailyCap: BotDailyCap}
}

const botSettingsCols = `enabled, proactive, review_at, instructions, last_review_date, proactive_today, proactive_date,
	(SELECT count(*) FROM bot_events e WHERE e.project_id = project_bots.project_id AND e.delivered_at IS NULL)`

func scanBotSettings(row interface{ Scan(...any) error }) (BotSettings, error) {
	b := DefaultBotSettings()
	err := row.Scan(&b.Enabled, &b.Proactive, &b.ReviewAt, &b.Instructions, &b.LastReviewDate, &b.ProactiveToday,
		&b.ProactiveDate, &b.PendingEvents)
	return b, err
}

// BotSettingsFor is a project's bot settings; the defaults until changed.
func (s *Store) BotSettingsFor(ctx context.Context, projectID int64) (BotSettings, error) {
	b, err := scanBotSettings(s.DB.QueryRow(ctx, `SELECT `+botSettingsCols+` FROM project_bots WHERE project_id = $1`, projectID))
	if err != nil {
		if errors.Is(mapErr(err), ErrNotFound) {
			return DefaultBotSettings(), nil
		}
		return b, err
	}
	return b, nil
}

// allBotSettings maps project id to settings, for the projects that have a row.
func (s *Store) allBotSettings(ctx context.Context) (map[int64]BotSettings, error) {
	rows, err := s.DB.Query(ctx, `SELECT project_id, `+botSettingsCols+` FROM project_bots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]BotSettings{}
	for rows.Next() {
		var id int64
		b := DefaultBotSettings()
		if err := rows.Scan(&id, &b.Enabled, &b.Proactive, &b.ReviewAt, &b.Instructions, &b.LastReviewDate,
			&b.ProactiveToday, &b.ProactiveDate, &b.PendingEvents); err != nil {
			return nil, err
		}
		out[id] = b
	}
	return out, rows.Err()
}

type BotSettingsPatch struct {
	Enabled      *bool   `json:"enabled"`
	Proactive    *bool   `json:"proactive"`
	ReviewAt     *string `json:"review_at"`
	Instructions *string `json:"instructions"`
}

var reviewAtRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// UpdateBotSettings changes a bot's settings. Turning a bot off, or its
// proactive mode off, drops the events still waiting for it.
func (s *Store) UpdateBotSettings(ctx context.Context, projectID int64, p BotSettingsPatch) (BotSettings, error) {
	cur, err := s.BotSettingsFor(ctx, projectID)
	if err != nil {
		return cur, err
	}
	if p.Enabled != nil {
		cur.Enabled = *p.Enabled
	}
	if p.Proactive != nil {
		cur.Proactive = *p.Proactive
	}
	if p.ReviewAt != nil {
		v := strings.TrimSpace(*p.ReviewAt)
		if v != "" && !reviewAtRe.MatchString(v) {
			return cur, invalid("review_at", "must be a time like 08:30, or empty for no daily review")
		}
		cur.ReviewAt = v
	}
	if p.Instructions != nil {
		v := strings.TrimSpace(*p.Instructions)
		if len(v) > maxBotInstructions {
			return cur, invalid("instructions", "at most %d characters", maxBotInstructions)
		}
		cur.Instructions = v
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO project_bots (project_id, enabled, proactive, review_at, instructions)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (project_id) DO UPDATE SET enabled = excluded.enabled, proactive = excluded.proactive,
		review_at = excluded.review_at, instructions = excluded.instructions, updated_at = now()`,
		projectID, cur.Enabled, cur.Proactive, cur.ReviewAt, cur.Instructions); err != nil {
		return cur, mapErr(err)
	}
	if !cur.Enabled || !cur.Proactive {
		if _, err := s.DB.Exec(ctx, `UPDATE bot_events SET delivered_at = now()
			WHERE project_id = $1 AND delivered_at IS NULL`, projectID); err != nil {
			return cur, err
		}
	}
	return s.BotSettingsFor(ctx, projectID)
}

// ── Events ────────────────────────────────────────────────────────────────

type BotEvent struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// AddBotEvent queues something that happened for the project's bot — only
// when that bot is on and proactive, and not when the same thing already
// waits. Best effort: a bot that misses an event must never fail a write.
func (s *Store) AddBotEvent(ctx context.Context, projectID int64, kind, text string) {
	_, _ = s.DB.Exec(ctx, `INSERT INTO bot_events (project_id, kind, text)
		SELECT $1, $2, $3 WHERE EXISTS (SELECT 1 FROM project_bots b JOIN projects p ON p.id = b.project_id
		                               WHERE b.project_id = $1 AND b.enabled AND b.proactive AND p.status <> 'archived')
		  AND NOT EXISTS (SELECT 1 FROM bot_events WHERE project_id = $1 AND delivered_at IS NULL AND kind = $2 AND text = $3)`,
		projectID, kind, truncate(text, 2000))
}

func (s *Store) addBotEventKey(ctx context.Context, key, kind, text string) {
	if id, err := s.ProjectIDByKey(ctx, key); err == nil {
		s.AddBotEvent(ctx, id, kind, text)
	}
}

// DueBot is a proactive bot with events that have settled: the oldest is
// at least settle old.
type DueBot struct {
	ProjectID int64
	Key       string
	Events    []BotEvent
}

// DueBotEvents lists the bots whose waiting events are due, oldest first.
func (s *Store) DueBotEvents(ctx context.Context, settle time.Duration) ([]DueBot, error) {
	rows, err := s.DB.Query(ctx, `SELECT e.id, e.project_id, e.kind, e.text, e.created_at, p.key
		FROM bot_events e JOIN projects p ON p.id = e.project_id
		WHERE e.delivered_at IS NULL AND e.project_id IN (
			SELECT project_id FROM bot_events WHERE delivered_at IS NULL
			GROUP BY project_id HAVING min(created_at) <= ts_add(now(), $1))
		ORDER BY e.project_id, e.id`, -settle.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueBot
	for rows.Next() {
		var e BotEvent
		var key string
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Kind, &e.Text, &e.CreatedAt, &key); err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].ProjectID != e.ProjectID {
			out = append(out, DueBot{ProjectID: e.ProjectID, Key: key})
		}
		out[len(out)-1].Events = append(out[len(out)-1].Events, e)
	}
	return out, rows.Err()
}

// MarkBotEventsDelivered settles events: sent to the bot, or dropped.
func (s *Store) MarkBotEventsDelivered(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.DB.Exec(ctx, `UPDATE bot_events SET delivered_at = now()
		WHERE id IN (SELECT value FROM json_each($1)) AND delivered_at IS NULL`, ids)
	return err
}

// ── The daily cap and the review ──────────────────────────────────────────

// TakeProactiveTurn counts one turn Forge starts for the bot today (the
// user's date), or says false when the day's cap is used up.
func (s *Store) TakeProactiveTurn(ctx context.Context, projectID int64, today string, limit int) (bool, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE project_bots SET
		proactive_today = CASE WHEN proactive_date = $2 THEN proactive_today + 1 ELSE 1 END, proactive_date = $2
		WHERE project_id = $1 AND (proactive_date IS NULL OR proactive_date <> $2 OR proactive_today < $3)`,
		projectID, today, limit)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// NoteCapOnce says true the first time a day the cap stops the bot.
func (s *Store) NoteCapOnce(ctx context.Context, projectID int64, today string) bool {
	tag, err := s.DB.Exec(ctx, `UPDATE project_bots SET cap_noted_date = $2
		WHERE project_id = $1 AND (cap_noted_date IS NULL OR cap_noted_date <> $2)`, projectID, today)
	return err == nil && tag.RowsAffected() == 1
}

// ReviewBot is a proactive bot with a daily review.
type ReviewBot struct {
	ProjectID      int64
	Key            string
	ReviewAt       string
	LastReviewDate *string
}

// ReviewBots lists the proactive bots that have a daily review time.
func (s *Store) ReviewBots(ctx context.Context) ([]ReviewBot, error) {
	rows, err := s.DB.Query(ctx, `SELECT b.project_id, p.key, b.review_at, b.last_review_date
		FROM project_bots b JOIN projects p ON p.id = b.project_id
		WHERE b.enabled AND b.proactive AND b.review_at <> '' AND p.status <> 'archived' ORDER BY p.key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReviewBot
	for rows.Next() {
		var b ReviewBot
		if err := rows.Scan(&b.ProjectID, &b.Key, &b.ReviewAt, &b.LastReviewDate); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// MarkReviewed records the day's review (date is the user's local date).
func (s *Store) MarkReviewed(ctx context.Context, projectID int64, date string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO project_bots (project_id, last_review_date) VALUES ($1, $2)
		ON CONFLICT (project_id) DO UPDATE SET last_review_date = excluded.last_review_date`, projectID, date)
	return err
}

// ReviewDue says whether a review set for reviewAt ("HH:MM") is due at
// local time now, given the last review's date: once a local day, any time
// after reviewAt that day (a review missed in the morning still comes).
func ReviewDue(reviewAt string, last *string, now time.Time) bool {
	if !reviewAtRe.MatchString(reviewAt) {
		return false
	}
	today := now.Format("2006-01-02")
	if last != nil && *last == today {
		return false
	}
	return now.Format("15:04") >= reviewAt
}

// botEventLine is one line of a bot's event message.
func botEventLine(e BotEvent) string {
	return fmt.Sprintf("- [%s] %s", e.Kind, e.Text)
}

// BotEventsMessage is what a proactive bot is told about its events.
func BotEventsMessage(events []BotEvent) string {
	var b strings.Builder
	b.WriteString("Forge: something happened in this project since you last looked.\n\n")
	for _, e := range events {
		b.WriteString(botEventLine(e) + "\n")
	}
	b.WriteString("\nLook into what matters, keep the board and memory current, and tell me in a few lines what you found and what you propose. If none of it needs anything, answer \"Nothing to do.\" and stop.")
	return b.String()
}

// BotReviewMessage is the daily review's prompt.
const BotReviewMessage = "Forge: time for your daily review of this project. Go over its open tasks, recent runs, the check-up's findings, CI and endpoints. " +
	"Update what is stale on the board (status, comments) and in memory, then give me a short report: what changed since yesterday, what needs me today, and what you propose to do next. " +
	"If nothing needs anything, answer \"Nothing to do.\" and stop."
