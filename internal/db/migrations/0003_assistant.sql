-- The Assistant: chats with an LLM agent that reads and updates Forge and
-- delegates work to Claude Code by queuing runs. The server runs the agent
-- loop; `busy` says a turn is in progress (reset at boot: a restart ends it).
CREATE TABLE chats (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    title         TEXT NOT NULL DEFAULT '',
    busy          INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT NOT NULL DEFAULT '',
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cached_tokens INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- The conversation as the model sees it: user and assistant turns, the
-- assistant's tool calls, and one `tool` message per call with its result.
CREATE TABLE chat_messages (
    chat_id      INTEGER NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    seq          INTEGER NOT NULL,
    role         TEXT NOT NULL CONSTRAINT chat_messages_role_check CHECK (role IN ('user', 'assistant', 'tool')),
    content      TEXT NOT NULL DEFAULT '',
    tool_calls   TEXT NOT NULL DEFAULT '[]',
    tool_call_id TEXT NOT NULL DEFAULT '',
    tool_name    TEXT NOT NULL DEFAULT '',
    result       TEXT,
    is_error     INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (chat_id, seq)
);
