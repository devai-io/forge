-- Agent machines report the coding-agent CLIs they have and whether each is
-- logged in (Claude Code, OpenAI Codex CLI, Cursor's cursor-agent), as a JSON
-- list carried on the heartbeat: [{"cli", "state", "version", "detail",
-- "checked_at"}]. No credentials, only the state.
ALTER TABLE runners ADD COLUMN sessions TEXT NOT NULL DEFAULT '[]';

-- The engine that runs Claude Code against OpenRouter's Anthropic-compatible
-- endpoint was called "deepseek"; it is "openrouter" now ("deepseek" is still
-- accepted as input).
UPDATE runs SET engine = 'openrouter' WHERE engine = 'deepseek';
UPDATE chats SET engine = 'openrouter' WHERE engine = 'deepseek';
UPDATE chat_turns SET engine = 'openrouter' WHERE engine = 'deepseek';
UPDATE settings SET value = json_set(value, '$.default', 'openrouter')
    WHERE key = 'engine' AND json_valid(value) AND json_extract(value, '$.default') = 'deepseek';
UPDATE settings SET value = json_set(value, '$.engine', 'openrouter')
    WHERE key = 'assistant' AND json_valid(value) AND json_extract(value, '$.engine') = 'deepseek';
