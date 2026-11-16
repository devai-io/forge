-- Call-level provider usage and idempotent agent usage snapshots. No prompts or secrets.
CREATE TABLE ai_usage (
 id INTEGER PRIMARY KEY,
 source_key TEXT UNIQUE,
 feature TEXT NOT NULL,
 project_key TEXT NOT NULL DEFAULT '',
 run_id INTEGER,
 chat_id INTEGER,
 api TEXT NOT NULL,
 model TEXT NOT NULL,
 calls INTEGER NOT NULL,
 failed INTEGER NOT NULL DEFAULT 0,
 input_tokens INTEGER NOT NULL,
 cached_tokens INTEGER NOT NULL,
 output_tokens INTEGER NOT NULL,
 cost_usd REAL,
 cost_kind TEXT NOT NULL,
 recorded_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX ai_usage_time ON ai_usage(recorded_at);
ALTER TABLE project_focus ADD COLUMN input_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE project_focus ADD COLUMN dirty_changed_at TEXT;
CREATE INDEX ai_usage_chat ON ai_usage(chat_id, feature, id);
