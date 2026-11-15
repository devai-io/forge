-- A member's agent can be Codex as well as Claude Code, and can be set to
-- work freely inside its project (`forge member-agent USER MACHINE ... project`):
-- no permission prompts for what stays in the project's folder, the
-- machine's sandbox (Codex's, or Claude Code's) keeping it there.
ALTER TABLE member_agents ADD COLUMN engine TEXT NOT NULL DEFAULT 'claude';

-- Sandbox: the run's agent works in its folder without asking, inside the
-- agent's OS sandbox (writes only there, no unapproved network).
ALTER TABLE runs ADD COLUMN sandbox INTEGER NOT NULL DEFAULT 0;
