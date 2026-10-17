-- Preserve existing transcripts while accepting Codex app-server events.
CREATE TABLE run_events_codex (
    run_id INTEGER NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    seq INTEGER NOT NULL,
    at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    kind TEXT NOT NULL CONSTRAINT run_events_kind_check CHECK (kind IN ('claude', 'codex', 'stdout', 'stderr', 'system')),
    data TEXT NOT NULL,
    PRIMARY KEY (run_id, seq)
);
INSERT INTO run_events_codex SELECT run_id, seq, at, kind, data FROM run_events;
DROP TABLE run_events;
ALTER TABLE run_events_codex RENAME TO run_events;
