-- Server-wide settings edited from the web app (config.json is for things
-- that must be known before the database opens). One JSON value per key.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Why a run uses the model it does, when Forge chose it (Jev routing).
ALTER TABLE runs ADD COLUMN model_note TEXT NOT NULL DEFAULT '';
