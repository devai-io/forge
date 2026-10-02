-- Interactive agent runs: the Claude Code session stays open, its questions
-- and approval requests come to the web app, and the answers (and follow-up
-- messages) go back to the machine through the run's inbox.

ALTER TABLE runs ADD COLUMN interactive INTEGER NOT NULL DEFAULT 0;
-- '' or 'reply': the machine finished a turn and waits for a follow-up.
-- (A pending prompt is read from run_prompts, not stored here.)
ALTER TABLE runs ADD COLUMN awaiting TEXT NOT NULL DEFAULT '';

-- What the session asked: a question (AskUserQuestion), a tool permission,
-- or a plan to approve (ExitPlanMode).
CREATE TABLE run_prompts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      INTEGER NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    request_id  TEXT NOT NULL,
    kind        TEXT NOT NULL CONSTRAINT run_prompts_kind_check CHECK (kind IN ('question', 'permission', 'plan')),
    tool_name   TEXT NOT NULL DEFAULT '',
    input       TEXT NOT NULL DEFAULT '{}',
    suggestions TEXT NOT NULL DEFAULT '[]',
    description TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending'
                CONSTRAINT run_prompts_status_check CHECK (status IN ('pending', 'answered', 'expired')),
    answer      TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    answered_at TEXT,
    CONSTRAINT run_prompts_request_key UNIQUE (run_id, request_id)
);
CREATE INDEX run_prompts_pending ON run_prompts (run_id) WHERE status = 'pending';

-- What goes to the machine: answers, follow-up messages, "end the session".
CREATE TABLE run_inbox (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id     INTEGER NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CONSTRAINT run_inbox_kind_check CHECK (kind IN ('answer', 'message', 'end')),
    data       TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX run_inbox_run ON run_inbox (run_id, id);
