-- A Claude Code chat may skip permission prompts (bypassPermissions, i.e.
-- --dangerously-skip-permissions) when its machine allows that mode.
ALTER TABLE chats ADD COLUMN skip_permissions INTEGER NOT NULL DEFAULT 0;
