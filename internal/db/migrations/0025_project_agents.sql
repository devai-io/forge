-- Project agents: one Claude Code session per project (tmux F-<Project>),
-- opened when asked for. A session started by hand in tmux used to become a
-- run with its first hook, before anyone had asked it anything — every idle
-- session showed as an open "(empty prompt)" run waiting for you. A
-- conversation is a run from its first request now; the empty ones go.
CREATE TEMP TABLE empty_tmux_runs AS
    SELECT id FROM runs
    WHERE origin = 'tmux' AND kind = 'agent' AND trim(prompt) = '' AND coalesce(num_turns, 0) = 0 AND trim(result) = '';
DELETE FROM activity WHERE run_id IN (SELECT id FROM empty_tmux_runs);
DELETE FROM run_events WHERE run_id IN (SELECT id FROM empty_tmux_runs);
DELETE FROM run_inbox WHERE run_id IN (SELECT id FROM empty_tmux_runs);
DELETE FROM run_prompts WHERE run_id IN (SELECT id FROM empty_tmux_runs);
DELETE FROM telegram_links WHERE run_id IN (SELECT id FROM empty_tmux_runs);
UPDATE runs SET resume_run_id = NULL WHERE resume_run_id IN (SELECT id FROM empty_tmux_runs);
DELETE FROM runs WHERE id IN (SELECT id FROM empty_tmux_runs);
DROP TABLE empty_tmux_runs;
