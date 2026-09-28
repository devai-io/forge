-- Forge schema v1: one person's projects, their repos/servers/endpoints, the
-- tasks on them, and the agent runs dispatched to runners.

CREATE TABLE users (
    id                  bigserial PRIMARY KEY,
    username            text NOT NULL,
    email               text NOT NULL,
    display_name        text NOT NULL DEFAULT '',
    password_hash       text NOT NULL,
    timezone            text NOT NULL DEFAULT 'UTC',
    weekly_goal         integer NOT NULL DEFAULT 10 CHECK (weekly_goal BETWEEN 1 AND 500),
    password_changed_at timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_username_key ON users (lower(username));
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

-- Only a SHA-256 of the cookie value is stored: a leaked table is not a set
-- of live sessions.
CREATE TABLE sessions (
    id           bigserial PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL UNIQUE,
    user_agent   text NOT NULL DEFAULT '',
    ip           text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE password_resets (
    id         bigserial PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE projects (
    id               bigserial PRIMARY KEY,
    key              text NOT NULL UNIQUE CHECK (key ~ '^[A-Z][A-Z0-9]{1,9}$'),
    name             text NOT NULL CHECK (name <> ''),
    category         text NOT NULL DEFAULT 'personal' CHECK (category IN ('work', 'personal')),
    status           text NOT NULL DEFAULT 'building'
                     CHECK (status IN ('live', 'building', 'radar', 'paused', 'archived')),
    priority         integer NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 5),
    color            text NOT NULL DEFAULT '#6366f1' CHECK (color ~ '^#[0-9a-fA-F]{6}$'),
    summary          text NOT NULL DEFAULT '',
    description      text NOT NULL DEFAULT '',
    infra_notes      text NOT NULL DEFAULT '',
    target_date      date,
    links            jsonb NOT NULL DEFAULT '[]',
    next_task_number integer NOT NULL DEFAULT 1,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- `git` is the latest scan a runner reported (GitStatus in docs/API.md).
-- A snapshot, not history: the dashboard wants "now", and commits_7d is
-- computed by the runner from git itself.
CREATE TABLE repos (
    id             bigserial PRIMARY KEY,
    project_id     bigint NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name           text NOT NULL CHECK (name <> ''),
    path           text NOT NULL DEFAULT '',
    remote_url     text NOT NULL DEFAULT '',
    default_branch text NOT NULL DEFAULT 'main',
    kind           text NOT NULL DEFAULT 'other'
                   CHECK (kind IN ('api', 'ui', 'mobile', 'infra', 'lib', 'site', 'other')),
    deploy         text NOT NULL DEFAULT '',
    notes          text NOT NULL DEFAULT '',
    sort_order     integer NOT NULL DEFAULT 0,
    git            jsonb,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);

CREATE TABLE servers (
    id             bigserial PRIMARY KEY,
    name           text NOT NULL UNIQUE CHECK (name <> ''),
    role           text NOT NULL DEFAULT '',
    provider       text NOT NULL DEFAULT '',
    arch           text NOT NULL DEFAULT '',
    public_address text NOT NULL DEFAULT '',
    tailscale_ip   text NOT NULL DEFAULT '',
    environment    text NOT NULL DEFAULT 'production'
                   CHECK (environment IN ('production', 'staging', 'dev', 'infra')),
    critical       boolean NOT NULL DEFAULT false,
    tags           text[] NOT NULL DEFAULT '{}',
    notes          text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE project_servers (
    project_id bigint NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    server_id  bigint NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    role       text NOT NULL DEFAULT '',
    PRIMARY KEY (project_id, server_id)
);

CREATE TABLE endpoints (
    id              bigserial PRIMARY KEY,
    project_id      bigint NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name            text NOT NULL CHECK (name <> ''),
    url             text NOT NULL CHECK (url ~ '^https?://'),
    kind            text NOT NULL DEFAULT 'web' CHECK (kind IN ('web', 'api', 'health')),
    expect_status   integer NOT NULL DEFAULT 200 CHECK (expect_status BETWEEN 100 AND 599),
    enabled         boolean NOT NULL DEFAULT true,
    last_status     text NOT NULL DEFAULT 'unknown' CHECK (last_status IN ('up', 'down', 'unknown')),
    last_code       integer,
    last_latency_ms integer,
    last_error      text NOT NULL DEFAULT '',
    last_checked_at timestamptz,
    last_change_at  timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE endpoint_checks (
    id          bigserial PRIMARY KEY,
    endpoint_id bigint NOT NULL REFERENCES endpoints (id) ON DELETE CASCADE,
    at          timestamptz NOT NULL DEFAULT now(),
    ok          boolean NOT NULL,
    code        integer,
    latency_ms  integer,
    error       text NOT NULL DEFAULT ''
);
CREATE INDEX endpoint_checks_endpoint_at_idx ON endpoint_checks (endpoint_id, at DESC);

CREATE TABLE tasks (
    id           bigserial PRIMARY KEY,
    project_id   bigint NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    number       integer NOT NULL,
    title        text NOT NULL CHECK (title <> ''),
    description  text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'todo'
                 CHECK (status IN ('backlog', 'todo', 'in_progress', 'blocked', 'done')),
    priority     text NOT NULL DEFAULT 'medium' CHECK (priority IN ('urgent', 'high', 'medium', 'low')),
    type         text NOT NULL DEFAULT 'feature'
                 CHECK (type IN ('feature', 'bug', 'chore', 'research', 'ops')),
    labels       text[] NOT NULL DEFAULT '{}',
    due_date     date,
    focus        boolean NOT NULL DEFAULT false,
    repo_id      bigint REFERENCES repos (id) ON DELETE SET NULL,
    estimate     integer CHECK (estimate IS NULL OR estimate BETWEEN 0 AND 1000),
    sort_order   double precision NOT NULL DEFAULT 0,
    completed_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, number)
);
CREATE INDEX tasks_project_status_idx ON tasks (project_id, status, sort_order);
CREATE INDEX tasks_completed_idx ON tasks (completed_at) WHERE completed_at IS NOT NULL;

CREATE TABLE comments (
    id         bigserial PRIMARY KEY,
    task_id    bigint NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    body       text NOT NULL CHECK (body <> ''),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX comments_task_idx ON comments (task_id);

CREATE TABLE runners (
    id           bigserial PRIMARY KEY,
    name         text NOT NULL UNIQUE CHECK (name <> ''),
    token_hash   bytea NOT NULL UNIQUE,
    hostname     text NOT NULL DEFAULT '',
    os           text NOT NULL DEFAULT '',
    version      text NOT NULL DEFAULT '',
    capabilities jsonb NOT NULL DEFAULT '{}',
    last_seen_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE runs (
    id               bigserial PRIMARY KEY,
    runner_id        bigint REFERENCES runners (id) ON DELETE SET NULL,
    runner_name      text NOT NULL DEFAULT '',
    project_id       bigint NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    repo_id          bigint NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    task_id          bigint REFERENCES tasks (id) ON DELETE SET NULL,
    kind             text NOT NULL CHECK (kind IN ('agent', 'command')),
    prompt           text NOT NULL DEFAULT '',
    command          text NOT NULL DEFAULT '',
    permission_mode  text NOT NULL DEFAULT '',
    model            text NOT NULL DEFAULT '',
    worktree         boolean NOT NULL DEFAULT false,
    resume_run_id    bigint REFERENCES runs (id) ON DELETE SET NULL,
    resume_session   text NOT NULL DEFAULT '',
    status           text NOT NULL DEFAULT 'queued'
                     CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    cancel_requested boolean NOT NULL DEFAULT false,
    session_id       text NOT NULL DEFAULT '',
    result           text NOT NULL DEFAULT '',
    error            text NOT NULL DEFAULT '',
    exit_code        integer,
    cost_usd         double precision,
    num_turns        integer,
    duration_ms      bigint,
    event_seq        integer NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    finished_at      timestamptz
);
CREATE INDEX runs_runner_status_idx ON runs (runner_id, status);
CREATE INDEX runs_project_idx ON runs (project_id, created_at DESC);

CREATE TABLE run_events (
    run_id bigint NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    seq    integer NOT NULL,
    at     timestamptz NOT NULL DEFAULT now(),
    kind   text NOT NULL CHECK (kind IN ('claude', 'stdout', 'stderr', 'system')),
    data   jsonb NOT NULL,
    PRIMARY KEY (run_id, seq)
);

-- The feed. task_ref is a snapshot so history still reads after a task is
-- deleted or moved to another project.
CREATE TABLE activity (
    id         bigserial PRIMARY KEY,
    project_id bigint REFERENCES projects (id) ON DELETE CASCADE,
    task_id    bigint REFERENCES tasks (id) ON DELETE SET NULL,
    task_ref   text,
    run_id     bigint REFERENCES runs (id) ON DELETE SET NULL,
    kind       text NOT NULL,
    summary    text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX activity_created_idx ON activity (created_at DESC);
CREATE INDEX activity_project_idx ON activity (project_id, created_at DESC);
