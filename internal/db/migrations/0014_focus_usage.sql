-- What computing a project's focus with the model has cost: `usage` adds up
-- every call (usage.Entry lines by API and model), `last_usage` is the latest
-- computation's.
ALTER TABLE project_focus ADD COLUMN usage TEXT NOT NULL DEFAULT '[]';
ALTER TABLE project_focus ADD COLUMN last_usage TEXT NOT NULL DEFAULT '[]';
