-- A one-turn run: its session answers the prompt (or the message that
-- resumed it) and the run finishes, instead of waiting for a reply and
-- parking. The dashboard's Refresh uses it, so a status check on a stale
-- session leaves no open run behind. Continue still works: the session stays.
ALTER TABLE runs ADD COLUMN one_turn INTEGER NOT NULL DEFAULT 0;
