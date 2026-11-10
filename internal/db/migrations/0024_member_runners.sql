-- A member's agent prompts run only on machines the owner opened to members
-- (`forge member-machine NAME on`). Off by default: a member's prompt runs an
-- agent with that machine's files and logins, so each machine is a choice.
ALTER TABLE runners ADD COLUMN members_allowed INTEGER NOT NULL DEFAULT 0;
