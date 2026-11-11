-- Impersonation: an owner's session may act as another account ("view Forge
-- as …"), to see what that account sees and try what it can do. The session
-- stays the owner's; this column says whom it acts as until it is stopped.
ALTER TABLE sessions ADD COLUMN impersonate_user_id INTEGER REFERENCES users (id) ON DELETE SET NULL;
