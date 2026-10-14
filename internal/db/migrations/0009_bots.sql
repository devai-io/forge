-- Bots: one long-lived Assistant chat per project, which commands everything
-- about it (its repos, tasks, servers, memory). A bot's chat is a chat with
-- a project_id; at most one per project. The Assistant's own chats keep
-- project_id NULL.
--
-- A conversation that outgrows the model's window is folded: the oldest
-- messages are summarised into `summary`, and the model sees that summary
-- plus the messages after summary_seq. The messages themselves stay.
ALTER TABLE chats ADD COLUMN project_id INTEGER REFERENCES projects (id) ON DELETE CASCADE;
ALTER TABLE chats ADD COLUMN summary TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN summary_seq INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX chats_project_idx ON chats (project_id) WHERE project_id IS NOT NULL;
