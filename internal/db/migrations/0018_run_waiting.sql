-- forge:foreign-keys-off
-- Agent runs stay open until the user says they are finished. A run whose
-- session answered and went quiet is 'waiting': no process on the machine
-- (the session is parked to free it), the conversation still open. A reply
-- queues the same run again to resume its session in place; Finish (or
-- Cancel) closes it. SQLite rebuilds the table to widen the status check,
-- hence foreign keys off for this migration.
--
--   inbox_after       the last inbox item the machine had read when it parked;
--                     a resumed session reads only what came after it
--   resumes           how many times the run was resumed in place (the
--                     machine must not send the original prompt again)
--   finish_requested  the user pressed Finish while the session was live: the
--                     run ends as succeeded instead of waiting
--   handoff           where the session went when it was opened in Claude
--                     Code outside Forge ("desk · tmux run-52", "desk · shell")

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
                     CONSTRAINT runs_status_check CHECK (status IN ('queued', 'running', 'waiting', 'succeeded', 'failed', 'cancelled')),
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
    effort           TEXT NOT NULL DEFAULT '',
    chat_turn_id     INTEGER REFERENCES chat_turns (id) ON DELETE SET NULL,
    usage            TEXT NOT NULL DEFAULT '{}',
    append_system    TEXT NOT NULL DEFAULT '',
    interactive      INTEGER NOT NULL DEFAULT 0,
    awaiting         TEXT NOT NULL DEFAULT '',
    cwd              TEXT NOT NULL DEFAULT '',
    inbox_after      INTEGER NOT NULL DEFAULT 0,
    resumes          INTEGER NOT NULL DEFAULT 0,
    finish_requested INTEGER NOT NULL DEFAULT 0,
    handoff          TEXT NOT NULL DEFAULT ''
);
INSERT INTO runs_new (id, runner_id, runner_name, project_id, repo_id, task_id, kind, prompt, command, permission_mode,
                      model, worktree, resume_run_id, resume_session, status, cancel_requested, session_id, result,
                      error, exit_code, cost_usd, num_turns, duration_ms, event_seq, created_at, started_at,
                      finished_at, model_note, engine, effort, chat_turn_id, usage, append_system, interactive,
                      awaiting, cwd)
SELECT id, runner_id, runner_name, project_id, repo_id, task_id, kind, prompt, command, permission_mode,
       model, worktree, resume_run_id, resume_session, status, cancel_requested, session_id, result,
       error, exit_code, cost_usd, num_turns, duration_ms, event_seq, created_at, started_at,
       finished_at, model_note, engine, effort, chat_turn_id, usage, append_system, interactive,
       awaiting, cwd
FROM runs;
DROP TABLE runs;
ALTER TABLE runs_new RENAME TO runs;
CREATE INDEX runs_runner_status_idx ON runs (runner_id, status);
CREATE INDEX runs_project_idx ON runs (project_id, created_at DESC);
CREATE INDEX runs_chat_turn_idx ON runs (chat_turn_id);

-- 'park': close the live session now and keep the run waiting (used before
-- opening the session elsewhere).
CREATE TABLE run_inbox_new (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id     INTEGER NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CONSTRAINT run_inbox_kind_check CHECK (kind IN ('answer', 'message', 'end', 'park')),
    data       TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO run_inbox_new (id, run_id, kind, data, created_at) SELECT id, run_id, kind, data, created_at FROM run_inbox;
DROP TABLE run_inbox;
ALTER TABLE run_inbox_new RENAME TO run_inbox;
CREATE INDEX run_inbox_run ON run_inbox (run_id, id);
