-- Memory: what Claude sessions, the Assistant and the user learn about a
-- project (decisions, conventions, gotchas, how-tos) or about the user
-- (project_id NULL: preferences that apply everywhere). Sessions start with
-- the relevant ones and can search, add and correct them through MCP.
CREATE TABLE memories (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id       INTEGER REFERENCES projects (id) ON DELETE CASCADE,
    repo_id          INTEGER REFERENCES repos (id) ON DELETE SET NULL,
    kind             TEXT NOT NULL DEFAULT 'fact'
                     CONSTRAINT memories_kind_check CHECK (kind IN ('fact', 'decision', 'convention', 'gotcha', 'preference', 'howto')),
    content          TEXT NOT NULL,
    source           TEXT NOT NULL DEFAULT 'user'
                     CONSTRAINT memories_source_check CHECK (source IN ('user', 'claude', 'assistant', 'telegram')),
    origin           TEXT NOT NULL DEFAULT '',  -- machine, run or chat it came from
    pinned           INTEGER NOT NULL DEFAULT 0,
    recalls          INTEGER NOT NULL DEFAULT 0,
    last_recalled_at TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX memories_project_idx ON memories (project_id, pinned DESC, updated_at DESC);

-- Full-text index over memories.content, rowid = memories.id. The store keeps
-- it in step in the same transaction as each write (the migration runner
-- splits on semicolons, so no triggers).
CREATE VIRTUAL TABLE memories_fts USING fts5 (content, tokenize = 'porter unicode61');

-- Telegram: which bot message stands for what, so a button press or a reply
-- reaches the right prompt, run or Assistant chat.
CREATE TABLE telegram_links (
    tg_chat    INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    kind       TEXT NOT NULL CONSTRAINT telegram_links_kind_check CHECK (kind IN ('prompt', 'run', 'chat')),
    run_id     INTEGER REFERENCES runs (id) ON DELETE CASCADE,
    prompt_id  INTEGER REFERENCES run_prompts (id) ON DELETE CASCADE,
    chat_id    INTEGER REFERENCES chats (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (tg_chat, message_id)
);
CREATE INDEX telegram_links_prompt_idx ON telegram_links (prompt_id);
