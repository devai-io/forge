-- forge:foreign-keys-off
-- Repo files: a vault item can be a git-ignored file of one of its project's
-- repos (.env, a keystore, a service-account JSON), placed at `path` inside
-- that repo when a machine is set up. One item per (repo, path). A new kind,
-- `file`, covers the files no other kind describes. The kind CHECK needs a
-- table rebuild (hence foreign keys off for this migration).

CREATE TABLE vault_items_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    aad              BLOB NOT NULL,
    name             TEXT NOT NULL CONSTRAINT vault_items_name_check CHECK (name <> ''),
    kind             TEXT NOT NULL CONSTRAINT vault_items_kind_check CHECK (kind IN ('password', 'api_key', 'private_key', 'keystore',
                         'certificate', 'provisioning_profile', 'service_account', 'token', 'env_file', 'file', 'note', 'reference')),
    project_id       INTEGER REFERENCES projects (id) ON DELETE SET NULL,
    repo_id          INTEGER REFERENCES repos (id) ON DELETE SET NULL,
    path             TEXT NOT NULL DEFAULT '',
    platform         TEXT NOT NULL DEFAULT '',
    host             TEXT NOT NULL DEFAULT '',
    identifier       TEXT NOT NULL DEFAULT '',
    fields           TEXT NOT NULL DEFAULT '{}',
    secret_keys      TEXT NOT NULL DEFAULT '[]',
    secret_sealed    BLOB,
    file_name        TEXT NOT NULL DEFAULT '',
    file_size        INTEGER NOT NULL DEFAULT 0,
    file_sealed      BLOB,
    location         TEXT NOT NULL DEFAULT '',
    expires_at       TEXT,
    notes            TEXT NOT NULL DEFAULT '',
    tags             TEXT NOT NULL DEFAULT '[]',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_revealed_at TEXT
);

INSERT INTO vault_items_new (id, aad, name, kind, project_id, platform, host, identifier, fields, secret_keys,
                             secret_sealed, file_name, file_size, file_sealed, location, expires_at, notes, tags,
                             created_at, updated_at, last_revealed_at)
SELECT id, aad, name, kind, project_id, platform, host, identifier, fields, secret_keys,
       secret_sealed, file_name, file_size, file_sealed, location, expires_at, notes, tags,
       created_at, updated_at, last_revealed_at
FROM vault_items;

DROP TABLE vault_items;
ALTER TABLE vault_items_new RENAME TO vault_items;

CREATE INDEX vault_items_project_idx ON vault_items (project_id);
CREATE UNIQUE INDEX vault_items_repo_path_idx ON vault_items (repo_id, path) WHERE repo_id IS NOT NULL AND path <> '';
