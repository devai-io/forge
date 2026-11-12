-- Images a user attaches to an agent prompt or message (drag and drop,
-- paste, the paperclip). The prompt carries a marker
-- [image: NAME](forge-upload:TOKEN); the machine that runs the agent fetches
-- the image by its token and hands Claude a local file. Kept 30 days.
CREATE TABLE uploads (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    token        TEXT NOT NULL UNIQUE,
    user_id      INTEGER REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size         INTEGER NOT NULL,
    data         BLOB NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX uploads_created_idx ON uploads (created_at);
