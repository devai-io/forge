-- The work driver (api/driver.go): what it last did for each task in
-- progress, so it keeps every one of them moving without nudges and without
-- running away — at most so many nudges a day, a gap between two, and a pause
-- after nudges that changed nothing on the task.
CREATE TABLE task_driver (
    task_id   INTEGER PRIMARY KEY REFERENCES tasks (id) ON DELETE CASCADE,
    day       TEXT NOT NULL DEFAULT '',     -- the UTC date nudges counts for
    nudges    INTEGER NOT NULL DEFAULT 0,   -- nudges that day
    last_at   TEXT,                         -- the last nudge (start, message, resume)
    idle      INTEGER NOT NULL DEFAULT 0,   -- nudges in a row after which the task had not changed
    snapshot  TEXT NOT NULL DEFAULT '',     -- the task as of the last nudge (status, updated_at, comments)
    state     TEXT NOT NULL DEFAULT '',     -- what the driver sees: working, starting, nudged, needs_you, paused, …
    note      TEXT NOT NULL DEFAULT '',     -- the same in a few words
    run_id    INTEGER,                      -- the run it drives
    checked_at TEXT
);
