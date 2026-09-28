-- Security log: who signed in, from where, what sensitive thing they did.
-- Forge opens shells and hands out signing keys, so "what happened on my
-- account" has to be answerable from the app, not from container logs.
CREATE TABLE security_events (
    id         bigserial PRIMARY KEY,
    kind       text NOT NULL,        -- login | login_failed | login_new_device | logout | elevate |
                                     -- elevate_failed | password_changed | password_reset |
                                     -- totp_enabled | totp_disabled | session_revoked |
                                     -- vault_reveal | vault_download | terminal_attach |
                                     -- terminal_keys | terminal_create | code_open | runner_created |
                                     -- runner_rotated | run_confirmed
    detail     text NOT NULL DEFAULT '',
    ip         text NOT NULL DEFAULT '',
    user_agent text NOT NULL DEFAULT '',
    session_id bigint,
    at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX security_events_at_idx ON security_events (at DESC);
CREATE INDEX security_events_kind_at_idx ON security_events (kind, at DESC);

-- Login history survives the session row (which is deleted at logout), so a
-- "new device?" check has something to compare against.
CREATE TABLE known_devices (
    ip         text NOT NULL,
    user_agent text NOT NULL,
    first_seen timestamptz NOT NULL DEFAULT now(),
    last_seen  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (ip, user_agent)
);
