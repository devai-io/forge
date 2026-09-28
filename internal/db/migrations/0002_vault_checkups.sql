-- Phase 2: two-factor + step-up auth, the encrypted vault, daily check-ups.

ALTER TABLE users
    ADD COLUMN totp_enabled      boolean NOT NULL DEFAULT false,
    ADD COLUMN totp_secret       bytea,           -- sealed with the vault key
    ADD COLUMN totp_pending      bytea,           -- set up but not yet confirmed
    ADD COLUMN totp_last_counter bigint NOT NULL DEFAULT 0,  -- a code works once
    ADD COLUMN checkup_time      text NOT NULL DEFAULT '07:30' CHECK (checkup_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
    ADD COLUMN checkup_email     boolean NOT NULL DEFAULT true;

-- Step-up: reveal a secret, download a keystore, turn 2FA on — each needs the
-- password re-entered within the last few minutes, on this session only.
ALTER TABLE sessions ADD COLUMN elevated_until timestamptz;

-- Secrets and files, sealed with AES-256-GCM under a key that is NOT in this
-- database (sops on the host). Everything outside secret_sealed/file_sealed is
-- metadata and is returned by list endpoints; `aad` binds each ciphertext to
-- its row so sealed blobs cannot be swapped between items.
CREATE TABLE vault_items (
    id               bigserial PRIMARY KEY,
    aad              bytea NOT NULL,
    name             text NOT NULL CHECK (name <> ''),
    kind             text NOT NULL CHECK (kind IN ('password', 'api_key', 'private_key', 'keystore', 'certificate',
                         'provisioning_profile', 'service_account', 'token', 'env_file', 'note', 'reference')),
    project_id       bigint REFERENCES projects (id) ON DELETE SET NULL,
    platform         text NOT NULL DEFAULT '',
    host             text NOT NULL DEFAULT '',
    identifier       text NOT NULL DEFAULT '',
    fields           jsonb NOT NULL DEFAULT '{}',
    secret_keys      text[] NOT NULL DEFAULT '{}',
    secret_sealed    bytea,
    file_name        text NOT NULL DEFAULT '',
    file_size        integer NOT NULL DEFAULT 0,
    file_sealed      bytea,
    location         text NOT NULL DEFAULT '',
    expires_at       date,
    notes            text NOT NULL DEFAULT '',
    tags             text[] NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    last_revealed_at timestamptz
);
CREATE INDEX vault_items_project_idx ON vault_items (project_id);
CREATE INDEX vault_items_tags_idx ON vault_items USING gin (tags);

CREATE TABLE vault_audit (
    id        bigserial PRIMARY KEY,
    item_id   bigint REFERENCES vault_items (id) ON DELETE SET NULL,
    item_name text NOT NULL,
    action    text NOT NULL CHECK (action IN ('create', 'update', 'reveal', 'download', 'delete')),
    ip        text NOT NULL DEFAULT '',
    at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX vault_audit_at_idx ON vault_audit (at DESC);

-- One row per check-up run. `items` is the whole result (CheckItem[] in
-- docs/API.md); done/task_id are edited in place as the day's actions get
-- ticked off, so the row is the action list.
CREATE TABLE checkups (
    id          bigserial PRIMARY KEY,
    date        date NOT NULL,
    trigger     text NOT NULL CHECK (trigger IN ('schedule', 'manual')),
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    status      text NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'warn', 'fail')),
    items       jsonb NOT NULL DEFAULT '[]',
    emailed     boolean NOT NULL DEFAULT false
);
CREATE INDEX checkups_date_idx ON checkups (date DESC, id DESC);
