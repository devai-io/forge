-- Focus: per project, what to do now, what comes next, the roadmap and what
-- blocks it. Recomputed (by the model when there is a provider key, by rules
-- otherwise) a little after the project changes: a task, a run, a check-up
-- marks it dirty, and the server's focus loop picks it up once the burst of
-- changes has settled. `focus` is the whole Focus object (docs/API.md).
CREATE TABLE project_focus (
    project_id   INTEGER PRIMARY KEY REFERENCES projects (id) ON DELETE CASCADE,
    focus        TEXT NOT NULL DEFAULT '{}',
    source       TEXT NOT NULL DEFAULT 'rules' CONSTRAINT project_focus_source_check CHECK (source IN ('ai', 'rules')),
    model        TEXT NOT NULL DEFAULT '',
    trigger      TEXT NOT NULL DEFAULT '',
    generated_at TEXT,
    dirty_since  TEXT,
    dirty_reason TEXT NOT NULL DEFAULT '',
    error        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX project_focus_dirty_idx ON project_focus (dirty_since) WHERE dirty_since IS NOT NULL;
