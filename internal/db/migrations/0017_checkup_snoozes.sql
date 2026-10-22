-- Check-up items that the user put aside. Keyed by the item key, which is
-- stable across days ("vault:52", "task:ALPHA-3"). `until` is a date the
-- snooze lasts to; NULL means "until the item changes", compared on `title`.
CREATE TABLE checkup_snoozes (
    key        TEXT PRIMARY KEY,
    until      TEXT,
    title      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Items that were open in the previous check-up and gone in this one
-- (ResolvedItem[] in docs/API.md): the "fixed since last time" list.
ALTER TABLE checkups ADD COLUMN resolved TEXT NOT NULL DEFAULT '[]';
