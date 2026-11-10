-- A member's agent: the one agent a limited account works with, on the
-- machine and with the model, effort and permission mode the owner chose
-- (`forge member-agent USER MACHINE [MODEL] [EFFORT] [MODE]`). That machine
-- is open to that member only — no need to open it to every member.
CREATE TABLE member_agents (
    user_id         INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    runner_id       INTEGER NOT NULL REFERENCES runners (id) ON DELETE CASCADE,
    model           TEXT NOT NULL DEFAULT '',
    effort          TEXT NOT NULL DEFAULT '',
    permission_mode TEXT NOT NULL DEFAULT 'acceptEdits',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Who started a run. A member steers (messages, answers, finishes) only the
-- runs it started itself: never a session the owner opened, which may run
-- with permissions a member cannot ask for.
ALTER TABLE runs ADD COLUMN user_id INTEGER REFERENCES users (id) ON DELETE SET NULL;
CREATE INDEX runs_user_idx ON runs (user_id, id DESC) WHERE user_id IS NOT NULL;
