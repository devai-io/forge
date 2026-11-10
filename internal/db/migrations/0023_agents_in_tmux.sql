-- Agents in tmux: an agent run's Claude Code session lives in a tmux session
-- on its machine (named after the task, e.g. SHOP-12), so the same session is
-- one `tmux attach` away and Forge drives it by typing into it. Claude Code
-- sessions started by hand in tmux register themselves (origin 'tmux').
ALTER TABLE runs ADD COLUMN tmux_session TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN origin TEXT NOT NULL DEFAULT 'forge';
CREATE INDEX runs_session_idx ON runs (session_id) WHERE session_id <> '';
CREATE INDEX runs_task_idx ON runs (task_id, id DESC) WHERE task_id IS NOT NULL;
