-- Browsers that passed two-factor once: they sign in and confirm with the
-- password alone (cookie forge_device; only its hash is stored). A device
-- unused for 180 days, a password change or reset, turning two-factor off or
-- "sign out everywhere else" forgets it.
CREATE TABLE trusted_devices (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   BLOB NOT NULL UNIQUE,
    user_agent   TEXT NOT NULL DEFAULT '',
    ip           TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_used_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX trusted_devices_user_idx ON trusted_devices (user_id);
