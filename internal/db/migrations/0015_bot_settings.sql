-- Bots, continued: per-project settings and proactive work.
--
-- project_bots holds a bot's settings; a project without a row has the
-- defaults (enabled, not proactive, a daily review at 08:30 local time, no
-- standing orders). A proactive bot gets turns Forge starts itself: events
-- from the project (an endpoint down or back up, CI failing, the check-up's
-- findings, a run finishing) queue up in bot_events and go to the bot as
-- one message once a burst has settled, and the daily review comes at
-- review_at. proactive_today counts those turns on proactive_date, against
-- a daily cap.
CREATE TABLE project_bots (
    project_id       INTEGER PRIMARY KEY REFERENCES projects (id) ON DELETE CASCADE,
    enabled          INTEGER NOT NULL DEFAULT 1,
    proactive        INTEGER NOT NULL DEFAULT 0,
    review_at        TEXT NOT NULL DEFAULT '08:30',
    instructions     TEXT NOT NULL DEFAULT '',
    last_review_date TEXT,
    proactive_today  INTEGER NOT NULL DEFAULT 0,
    proactive_date   TEXT,
    cap_noted_date   TEXT,
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE bot_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id   INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    text         TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    delivered_at TEXT
);
CREATE INDEX bot_events_pending_idx ON bot_events (project_id, id) WHERE delivered_at IS NULL;

-- Who started a message or a turn: '' the user (web app), 'telegram',
-- 'event' or 'schedule' (Forge, for a proactive bot).
ALTER TABLE chat_messages ADD COLUMN origin TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_turns ADD COLUMN origin TEXT NOT NULL DEFAULT '';

-- Where an Assistant session starts on its machine ('' = the home folder):
-- a bot's sessions start in its project's folder.
ALTER TABLE runs ADD COLUMN cwd TEXT NOT NULL DEFAULT '';
