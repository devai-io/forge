-- Which backend an agent run's Claude Code talks to: 'claude' (Anthropic) or
-- 'deepseek' (DeepSeek's Anthropic-compatible API). Empty for command runs.
ALTER TABLE runs ADD COLUMN engine TEXT NOT NULL DEFAULT '';
UPDATE runs SET engine = 'claude' WHERE kind = 'agent';
