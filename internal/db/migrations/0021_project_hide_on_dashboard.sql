-- A project can stay out of the dashboard (its card, and its tasks in the
-- focus, needs-attention and working-on lists and the open-task counts)
-- while staying everywhere else: Projects, Tasks, the check-up, bots.
ALTER TABLE projects ADD COLUMN hide_on_dashboard INTEGER NOT NULL DEFAULT 0;
