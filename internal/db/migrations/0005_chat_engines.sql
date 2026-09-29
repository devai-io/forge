-- forge:foreign-keys-off
-- The Assistant picks an engine per chat: the DeepSeek API loop on the server,
-- or a Claude Code session on a machine. Each turn is recorded with what it
-- spent, per API. A Claude Code turn is a run in the machine's home folder
-- (no repo), so runs.project_id / repo_id become optional: SQLite rebuilds
-- the table for that (hence foreign keys off for this migration).

CREATE TABLE chat_turns (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id     INTEGER NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,          -- the user message that started it
    engine      TEXT NOT NULL,
    model       TEXT NOT NULL DEFAULT '',
    effort      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'running'
                CONSTRAINT chat_turns_status_check CHECK (status IN ('running', 'done', 'failed', 'stopped')),
    error       TEXT NOT NULL DEFAULT '',
    usage       TEXT NOT NULL DEFAULT '[]', -- [{api, model, calls, input/cached/output tokens, cost_usd}]
    run_id      INTEGER REFERENCES runs (id) ON DELETE SET NULL, -- the Claude Code session run
    started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at TEXT
);
CREATE INDEX chat_turns_chat_idx ON chat_turns (chat_id, id);

ALTER TABLE chats ADD COLUMN engine TEXT NOT NULL DEFAULT 'deepseek';
ALTER TABLE chats ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN effort TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN edits INTEGER NOT NULL DEFAULT 0;   -- Claude Code may edit files (acceptEdits)

-- DeepSeek's thinking mode needs each assistant message's reasoning sent back.
ALTER TABLE chat_messages ADD COLUMN reasoning TEXT NOT NULL DEFAULT '';

CREATE TABLE runs_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    runner_id        INTEGER REFERENCES runners (id) ON DELETE SET NULL,
    runner_name      TEXT NOT NULL DEFAULT '',
    project_id       INTEGER REFERENCES projects (id) ON DELETE CASCADE,
    repo_id          INTEGER REFERENCES repos (id) ON DELETE CASCADE,
    task_id          INTEGER REFERENCES tasks (id) ON DELETE SET NULL,
    kind             TEXT NOT NULL CONSTRAINT runs_kind_check CHECK (kind IN ('agent', 'command')),
    prompt           TEXT NOT NULL DEFAULT '',
    command          TEXT NOT NULL DEFAULT '',
    permission_mode  TEXT NOT NULL DEFAULT '',
    model            TEXT NOT NULL DEFAULT '',
    worktree         INTEGER NOT NULL DEFAULT 0,
    resume_run_id    INTEGER REFERENCES runs (id) ON DELETE SET NULL,
    resume_session   TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'queued'
                     CONSTRAINT runs_status_check CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    cancel_requested INTEGER NOT NULL DEFAULT 0,
    session_id       TEXT NOT NULL DEFAULT '',
    result           TEXT NOT NULL DEFAULT '',
    error            TEXT NOT NULL DEFAULT '',
    exit_code        INTEGER,
    cost_usd         REAL,
    num_turns        INTEGER,
    duration_ms      INTEGER,
    event_seq        INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    started_at       TEXT,
    finished_at      TEXT,
    model_note       TEXT NOT NULL DEFAULT '',
    engine           TEXT NOT NULL DEFAULT '',
    effort           TEXT NOT NULL DEFAULT '',   -- claude --effort, "" = its default
    chat_turn_id     INTEGER REFERENCES chat_turns (id) ON DELETE SET NULL,
    usage            TEXT NOT NULL DEFAULT '{}', -- tokens per model, from Claude Code's result
    append_system    TEXT NOT NULL DEFAULT ''    -- Assistant sessions: added to Claude Code's system prompt
);
INSERT INTO runs_new (id, runner_id, runner_name, project_id, repo_id, task_id, kind, prompt, command, permission_mode,
                      model, worktree, resume_run_id, resume_session, status, cancel_requested, session_id, result,
                      error, exit_code, cost_usd, num_turns, duration_ms, event_seq, created_at, started_at,
                      finished_at, model_note, engine)
SELECT id, runner_id, runner_name, project_id, repo_id, task_id, kind, prompt, command, permission_mode,
       model, worktree, resume_run_id, resume_session, status, cancel_requested, session_id, result,
       error, exit_code, cost_usd, num_turns, duration_ms, event_seq, created_at, started_at,
       finished_at, model_note, engine
FROM runs;
DROP TABLE runs;
ALTER TABLE runs_new RENAME TO runs;
CREATE INDEX runs_runner_status_idx ON runs (runner_id, status);
CREATE INDEX runs_project_idx ON runs (project_id, created_at DESC);
CREATE INDEX runs_chat_turn_idx ON runs (chat_turn_id);
