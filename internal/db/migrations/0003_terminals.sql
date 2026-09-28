-- Phase 3: each runner's tmux sessions as of its last heartbeat.
ALTER TABLE runners
    ADD COLUMN tmux    jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN tmux_at timestamptz;
