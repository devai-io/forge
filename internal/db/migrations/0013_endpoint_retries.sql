-- forge:foreign-keys-off
-- A failed check no longer marks an endpoint down straight away: it goes to
-- 'warning' and is rechecked a couple of times (fail_streak counts the
-- consecutive failures); only when the retries fail too is it 'down'.
-- The CHECK on last_status changes, so SQLite rebuilds the table (hence
-- foreign keys off: endpoint_checks references it).

CREATE TABLE endpoints_new (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id      INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name            TEXT NOT NULL CONSTRAINT endpoints_name_check CHECK (name <> ''),
    url             TEXT NOT NULL CONSTRAINT endpoints_url_check CHECK (url GLOB 'http://?*' OR url GLOB 'https://?*'),
    kind            TEXT NOT NULL DEFAULT 'web' CONSTRAINT endpoints_kind_check CHECK (kind IN ('web', 'api', 'health')),
    expect_status   INTEGER NOT NULL DEFAULT 200 CONSTRAINT endpoints_expect_status_check CHECK (expect_status BETWEEN 100 AND 599),
    enabled         INTEGER NOT NULL DEFAULT 1,
    last_status     TEXT NOT NULL DEFAULT 'unknown'
                    CONSTRAINT endpoints_last_status_check CHECK (last_status IN ('up', 'warning', 'down', 'unknown')),
    fail_streak     INTEGER NOT NULL DEFAULT 0,
    last_code       INTEGER,
    last_latency_ms INTEGER,
    last_error      TEXT NOT NULL DEFAULT '',
    last_checked_at TEXT,
    last_change_at  TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- An endpoint already down has, by definition, used up its retries.
INSERT INTO endpoints_new (id, project_id, name, url, kind, expect_status, enabled, last_status, fail_streak,
                           last_code, last_latency_ms, last_error, last_checked_at, last_change_at, created_at, updated_at)
SELECT id, project_id, name, url, kind, expect_status, enabled, last_status,
       CASE WHEN last_status = 'down' THEN 3 ELSE 0 END,
       last_code, last_latency_ms, last_error, last_checked_at, last_change_at, created_at, updated_at
FROM endpoints;

DROP TABLE endpoints;
ALTER TABLE endpoints_new RENAME TO endpoints;
