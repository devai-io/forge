-- Limited accounts: a "member" sees and works only in the projects listed in
-- project_members (tasks, agent prompts, runs). The owner (role 'owner')
-- sees everything, as before.
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'owner'
    CONSTRAINT users_role_check CHECK (role IN ('owner', 'member'));

CREATE TABLE project_members (
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (user_id, project_id)
);
CREATE INDEX project_members_project_idx ON project_members (project_id);
