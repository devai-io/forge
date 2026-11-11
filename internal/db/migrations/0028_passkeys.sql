-- Passkeys (WebAuthn): the main way to sign in. A password sign-in always
-- needs the authenticator code too.
--
-- webauthn_id is the account's WebAuthn user handle: random, made the first
-- time the account registers a passkey, never shown. A passkey's credential
-- is the go-webauthn record as JSON (public key, sign count, flags).
ALTER TABLE users ADD COLUMN webauthn_id BLOB;
CREATE UNIQUE INDEX users_webauthn_id_key ON users (webauthn_id) WHERE webauthn_id IS NOT NULL;

CREATE TABLE passkeys (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id BLOB NOT NULL UNIQUE,
    name          TEXT NOT NULL DEFAULT '',
    credential    TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_used_at  TEXT
);
CREATE INDEX passkeys_user_idx ON passkeys (user_id);
