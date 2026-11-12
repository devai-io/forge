-- Archiving a run takes it off the Agents page (Open and History) without
-- stopping or finishing anything. It comes back by itself when it needs the
-- user again: queued anew (a reply resumes it) or asking a question.
ALTER TABLE runs ADD COLUMN archived_at TEXT;

CREATE TRIGGER runs_unarchive_on_queue AFTER UPDATE OF status ON runs
WHEN NEW.archived_at IS NOT NULL AND NEW.status = 'queued' AND OLD.status <> 'queued'
BEGIN
    UPDATE runs SET archived_at = NULL WHERE id = NEW.id;
END;

CREATE TRIGGER runs_unarchive_on_prompt AFTER INSERT ON run_prompts
BEGIN
    UPDATE runs SET archived_at = NULL WHERE id = NEW.run_id AND archived_at IS NOT NULL;
END;
