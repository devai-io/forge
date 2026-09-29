# Forge API contract

The single source of truth between the server (Go, `internal/`) and the web app
(React, `web/`). Change
both halves in the same session when this file changes.

- Base path: everything is under `/api` (a reverse proxy may route `/api/*` separately
  with the prefix kept; the SPA calls same-origin). `GET /health` exists at the
  root for health checks.
- JSON in, JSON out. Field names are `snake_case`. IDs are integers. Timestamps
  are RFC 3339 strings (UTC). Calendar dates are `"YYYY-MM-DD"`.
- Nullable fields are always present and `null` when empty (never omitted).
  Arrays are never `null` — empty is `[]`.
- Errors: non-2xx with `{"error": {"code": "not_found", "message": "…"}}`.
  Codes: `bad_request`, `validation` (+ `"field"` key), `unauthorized`,
  `forbidden`, `csrf`, `not_found`, `conflict`, `rate_limited`, `internal`.
- `PATCH` bodies are partial: only keys present are changed; `null` clears a
  nullable field.

## Auth model

Browser: session cookie `forge_session` (HttpOnly, Secure, SameSite=Lax, 30 days,
sliding). Every non-GET request made with the cookie must carry the header
`X-Forge-Client: web` (CSRF guard — a custom header forces a CORS preflight, so
no other origin can send it). Missing header → `403 csrf`.

Runner: `Authorization: Bearer frg_…` (a runner token). Runner routes live
under `/api/runner/*` and accept nothing else; browser routes never accept a
runner token.

Unauthenticated routes: `POST /api/auth/login`, `POST /api/auth/forgot`,
`POST /api/auth/reset`, `GET /api/status`, `GET /api/setup`, `POST /api/setup`.
Everything else → `401 unauthorized` without a valid session.

## Types

```ts
type User = {
  id: number; username: string; email: string; display_name: string;
  timezone: string;          // IANA, e.g. "Europe/Lisbon"; drives "today"
  weekly_goal: number;       // tasks to finish per week (dashboard ring)
  created_at: string;
};

type Session = { id: number; current: boolean; user_agent: string; ip: string;
                 created_at: string; last_seen_at: string; expires_at: string };

type ProjectStatus = "live" | "building" | "radar" | "paused" | "archived";
type Category = "work" | "personal";

type ProjectStats = {
  total: number; backlog: number; todo: number; in_progress: number;
  blocked: number; done: number;
  overdue: number;           // not done, due_date < today
  done_7d: number;           // completed in the last 7 days
  created_7d: number;
  commits_7d: number;        // sum over repos from the latest runner scan
  dirty_repos: number;       // repos with uncommitted changes
  endpoints_total: number; endpoints_up: number; endpoints_down: number;
  active_runs: number;       // queued + running agent runs
  last_activity_at: string | null;
  progress: number;          // done / total, 0..1 (0 when total = 0)
};

type Link = { label: string; url: string };

type Project = {
  id: number; key: string;   // key: 2-10 uppercase letters/digits, starts with a letter
  name: string; category: Category; status: ProjectStatus;
  priority: number;          // 1 (top focus) .. 5
  color: string;             // "#rrggbb"
  summary: string; description: string /* markdown */; infra_notes: string /* markdown */;
  target_date: string | null; links: Link[];
  created_at: string; updated_at: string;
  stats: ProjectStats;
};

type GitHead = { hash: string; subject: string; author: string; at: string };
type GitStatus = {
  branch: string; dirty: number; ahead: number; behind: number;
  head: GitHead | null; commits_7d: number; last_commit_at: string | null;
  scanned_at: string; runner_name: string; error: string;
  untracked?: number; ci: CIStatus | null;
  sync?: SyncStatus | null;  // the master's latest automatic fetch + fast-forward
};

// Every pull_interval (agent.json, default 30m) the master fetches each repo
// and fast-forwards it only with no tracked changes, no unpushed commits, no
// merge/rebase in progress and no run working there. A sync with pulled > 0
// is logged once as activity kind "repo.pulled".
type SyncStatus = {
  at: string;                                          // RFC 3339
  result: "up_to_date" | "pulled" | "skipped" | "error";
  detail: string;                                      // why skipped / the error
  pulled: number;                                      // commits fast-forwarded
};

type RepoKind = "api" | "ui" | "mobile" | "infra" | "lib" | "site" | "other";
type Repo = {
  id: number; project_id: number; name: string; path: string /* absolute, on the runner host */;
  remote_url: string; default_branch: string; kind: RepoKind;
  deploy: string; notes: string; sort_order: number;
  git: GitStatus | null;     // null until a runner has scanned it
};

type Environment = "production" | "staging" | "dev" | "infra";
type Server = {
  id: number; name: string; role: string; provider: string; arch: string;
  public_address: string; tailscale_ip: string; environment: Environment;
  critical: boolean; tags: string[]; notes: string /* markdown */;
  projects: { key: string; name: string; color: string; role: string }[];
  created_at: string; updated_at: string;
};
type ProjectServer = { server_id: number; name: string; role: string;
                       environment: Environment; critical: boolean };

type EndpointKind = "web" | "api" | "health";
type Endpoint = {
  id: number; project_id: number; project_key: string; project_name: string; project_color: string;
  name: string; url: string; kind: EndpointKind; expect_status: number; enabled: boolean;
  last_status: "up" | "down" | "unknown";
  last_code: number | null; last_latency_ms: number | null; last_error: string;
  last_checked_at: string | null; last_change_at: string | null;
  uptime_24h: number | null; // 0..1, null when no checks in the window
};
type EndpointCheck = { at: string; ok: boolean; code: number | null; latency_ms: number | null; error: string };

type ProjectDetail = Project & { repos: Repo[]; servers: ProjectServer[]; endpoints: Endpoint[] };

type TaskStatus = "backlog" | "todo" | "in_progress" | "blocked" | "done";
type Priority = "urgent" | "high" | "medium" | "low";
type TaskType = "feature" | "bug" | "chore" | "research" | "ops";
type Task = {
  id: number; project_id: number; project_key: string; project_name: string; project_color: string;
  number: number; ref: string;             // "ALPHA-12"
  title: string; description: string /* markdown */;
  status: TaskStatus; priority: Priority; type: TaskType; labels: string[];
  due_date: string | null; focus: boolean; // focus = on today's list
  repo_id: number | null; repo_name: string | null;
  estimate: number | null;                 // points, optional
  sort_order: number;                      // ascending within a status column
  completed_at: string | null; created_at: string; updated_at: string;
  comment_count: number;
};
type Comment = { id: number; task_id: number; body: string /* markdown */; created_at: string };
type TaskDetail = Task & { comments: Comment[]; runs: Run[] };

type Activity = {
  id: number; project_id: number | null; project_key: string | null; project_color: string | null;
  task_id: number | null; task_ref: string | null; run_id: number | null;
  kind: string;   // task.created | task.status | task.done | task.reopened | task.comment |
                  // project.created | project.updated | run.queued | run.finished |
                  // endpoint.down | endpoint.up
  summary: string; created_at: string;
};

type RunnerCapabilities = {
  claude: boolean;              // `claude` binary found on the runner
  permission_modes: string[];   // modes this runner will accept, e.g. ["plan","acceptEdits"]
  commands: string[];           // named shell commands defined in the runner's local config
  max_concurrent: number;
};
type Runner = {
  id: number; name: string; hostname: string; os: string; version: string;
  online: boolean;              // heartbeat within the last 90 s
  last_seen_at: string | null; capabilities: RunnerCapabilities;
  running: number; created_at: string;
};

type RunStatus = "queued" | "running" | "succeeded" | "failed" | "cancelled";
type Run = {
  id: number; runner_id: number; runner_name: string;
  project_id: number; project_key: string; project_color: string;
  repo_id: number; repo_name: string;
  task_id: number | null; task_ref: string | null;
  kind: "agent" | "command";
  prompt: string;               // agent runs
  command: string;              // command runs: the name from the runner's config
  permission_mode: string;      // agent runs: plan | acceptEdits | auto | dontAsk | bypassPermissions
  model: string;                // "" = runner default
  worktree: boolean;            // agent runs in a fresh git worktree (claude --worktree)
  resume_run_id: number | null; // continues that run's Claude session
  status: RunStatus; cancel_requested: boolean;
  session_id: string; result: string /* final assistant text, markdown */; error: string;
  exit_code: number | null; cost_usd: number | null; num_turns: number | null; duration_ms: number | null;
  created_at: string; started_at: string | null; finished_at: string | null;
};
type RunEvent = {
  seq: number; at: string;
  kind: "claude" | "stdout" | "stderr" | "system";
  data: any;  // claude: one parsed `claude -p --output-format stream-json` line (object with "type")
              // stdout/stderr/system: { text: string }
};

type DayCount = { date: string; done: number; created: number };
type Dashboard = {
  today: string;               // in the user's timezone
  stats: {
    streak_days: number;       // consecutive days ending today (or yesterday) with >= 1 task done
    best_streak: number;
    done_today: number; done_week: number; done_prev_week: number; // week = Monday-start, user tz
    weekly_goal: number;
    open_tasks: number; in_progress: number; blocked: number; overdue: number;
    due_soon: number;          // not done, due within 3 days
  };
  daily: DayCount[];           // last 28 days, oldest first, includes today
  focus: Task[];               // focus = true and not done, ordered by priority then due date
  overdue: Task[];             // not done, due before today (max 20)
  projects: Project[];         // status != archived, ordered by priority, then name
  endpoints_down: Endpoint[];
  runners: Runner[];
  active_runs: Run[];          // queued + running
  recent_runs: Run[];          // latest 6 finished
  activity: Activity[];        // latest 30
};
```

## Routes

### Auth & account
| Method | Path | Body | Response |
|---|---|---|---|
| POST | /api/auth/login | `{username, password}` (username may be the e-mail) | `200 {user: User}` + cookie; `401 unauthorized`; `429 rate_limited` |
| POST | /api/auth/logout | – | `204` (clears cookie) |
| GET | /api/auth/me | – | `{user: User}` |
| PATCH | /api/auth/me | `{email?, display_name?, timezone?, weekly_goal?}` | `{user: User}` |
| POST | /api/auth/password | `{current_password, new_password}` (min 12 chars) | `204`; other sessions are revoked |
| POST | /api/auth/forgot | `{login}` (username or e-mail) | always `204` — e-mails a reset link if SMTP is configured, otherwise logs that it could not |
| POST | /api/auth/reset | `{token, new_password}` | `204`; all sessions revoked; `400 bad_request` on an invalid/expired token |
| GET | /api/auth/sessions | – | `{sessions: Session[]}` |
| DELETE | /api/auth/sessions/{id} | – | `204` |

### Dashboard
| GET | /api/dashboard | – | `Dashboard` |

### Projects
| Method | Path | Body | Response |
|---|---|---|---|
| GET | /api/projects | query `include_archived=1` | `{projects: Project[]}` |
| POST | /api/projects | `{key, name, category?, status?, priority?, color?, summary?, description?, infra_notes?, target_date?, links?}` | `201 ProjectDetail` |
| GET | /api/projects/{key} | – | `ProjectDetail` |
| PATCH | /api/projects/{key} | any Project editable field (not `key`) | `ProjectDetail` |
| DELETE | /api/projects/{key} | – | `204` (cascades tasks, repos, endpoints, runs) |
| GET | /api/projects/{key}/activity | query `limit` (default 50) | `{activity: Activity[]}` |
| PUT | /api/projects/{key}/servers | `{servers: [{server_id, role}]}` | `{servers: ProjectServer[]}` (replaces the set) |
| POST | /api/projects/{key}/repos | `{name, path?, remote_url?, default_branch?, kind?, deploy?, notes?}` | `201 Repo` |
| PATCH | /api/repos/{id} | Repo editable fields | `Repo` |
| DELETE | /api/repos/{id} | – | `204` |
| POST | /api/projects/{key}/endpoints | `{name, url, kind?, expect_status?, enabled?}` | `201 Endpoint` |

### Endpoints (monitoring)
| GET | /api/endpoints | – | `{endpoints: Endpoint[]}` |
| PATCH | /api/endpoints/{id} | `{name?, url?, kind?, expect_status?, enabled?}` | `Endpoint` |
| DELETE | /api/endpoints/{id} | – | `204` |
| POST | /api/endpoints/{id}/check | – | `Endpoint` (checks now) |
| GET | /api/endpoints/{id}/checks | query `hours` (default 24, max 168) | `{checks: EndpointCheck[]}` newest first |

The API checks every enabled endpoint every 2 minutes (GET, 10 s timeout, no
redirect following; up = status == expect_status). Checks older than 7 days are
pruned.

### Servers (shared infrastructure)
| GET | /api/servers | – | `{servers: Server[]}` |
| POST | /api/servers | `{name, role?, provider?, arch?, public_address?, tailscale_ip?, environment?, critical?, tags?, notes?}` | `201 Server` |
| GET | /api/servers/{id} | – | `Server` |
| PATCH | /api/servers/{id} | Server editable fields | `Server` |
| DELETE | /api/servers/{id} | – | `204` |

### Tasks
| Method | Path | Body | Response |
|---|---|---|---|
| GET | /api/tasks | query: `project` (key), `status` (comma list), `focus=1`, `q` (title/ref search), `priority`, `type`, `label`, `overdue=1`, `open=1` (not done), `limit` (default 500) | `{tasks: Task[]}` ordered by status column order, then `sort_order` |
| POST | /api/tasks | `{project_key, title, description?, status?, priority?, type?, labels?, due_date?, focus?, repo_id?, estimate?}` | `201 Task` (appended to the bottom of its column) |
| GET | /api/tasks/{id} | – | `TaskDetail` |
| PATCH | /api/tasks/{id} | any editable field incl. `status`, `sort_order`, `project_key` (move) | `Task`. Moving to `done` sets `completed_at`; moving out clears it |
| DELETE | /api/tasks/{id} | – | `204` |
| POST | /api/tasks/{id}/comments | `{body}` | `201 Comment` |
| DELETE | /api/comments/{id} | – | `204` |

Kanban reordering: the UI sends `PATCH {status, sort_order}` with a
`sort_order` between its new neighbours (midpoint; bottom = last + 1000,
top = first − 1000).

### Agents (runners + runs)
| Method | Path | Body | Response |
|---|---|---|---|
| GET | /api/runners | – | `{runners: Runner[]}` |
| POST | /api/runners | `{name, role?}` | `201 {runner: Runner, pairing: Pairing}` — see *Machine pairing* below |
| POST | /api/runners/{id}/pair | – | `{runner: Runner, pairing: Pairing}` — a new code for an existing machine |
| POST | /api/runners/{id}/rotate | – | `{runner: Runner, token: string}` — the token is shown once (manual setup) |
| DELETE | /api/runners/{id} | – | `204` |
| GET | /api/runs | query `project` (key), `task` (id), `status`, `limit` (default 50) | `{runs: Run[]}` newest first |
| POST | /api/runs | `{runner_id, repo_id, kind, prompt?, command?, permission_mode?, model?, engine?, worktree?, task_id?, resume_run_id?}` | `201 Run` (queued). `resume_run_id` copies runner/repo from that run and requires it to have a `session_id` |
| GET | /api/runs/{id} | – | `Run` |
| GET | /api/runs/{id}/events | query `after` (seq, default 0), `limit` (default 500) | `{events: RunEvent[], run: Run}` — poll every ~1.5 s while the run is active |
| POST | /api/runs/{id}/cancel | – | `Run` (queued → cancelled immediately; running → `cancel_requested`, the runner kills it) |

Validation for `POST /api/runs`: the runner must exist; `kind = agent` needs a
non-empty `prompt` and a `permission_mode` listed in the runner's capabilities
(default `plan`); `kind = command` needs a `command` listed in the runner's
capabilities. The repo must have a `path`.

### Runner protocol (`Authorization: Bearer frg_…`)
| Method | Path | Body | Response |
|---|---|---|---|
| POST | /api/runner/heartbeat | `{hostname, os, version, capabilities: RunnerCapabilities, running: number[]}` | `{runner_id, cancel: number[] /* run ids to kill */, repos: [{id, project_key, name, path, default_branch}]}` |
| POST | /api/runner/claim | – | long-poll up to 25 s: `200 {run: Run, engine}` (now `running`; see *Agent engine*) or `204` |
| POST | /api/runner/runs/{id}/events | `{events: [{kind, data}]}` | `204` (seq assigned server-side, in order) |
| POST | /api/runner/runs/{id}/finish | `{status: "succeeded"|"failed"|"cancelled", exit_code?, session_id?, result?, error?, cost_usd?, num_turns?, duration_ms?, usage?}` | `204` — `usage`: Claude Code's `modelUsage`, stored on the run |
| POST | /api/runner/repos | `{repos: [{repo_id, branch, dirty, ahead, behind, head, commits_7d, last_commit_at, error}]}` | `204` |

A runner only ever sees runs addressed to it. Runs stuck `running` on a runner
that has been offline for 10 minutes are marked `failed` with
`error = "runner went offline"`.

### Misc
| GET | /health | – | `{"status":"ok"}` (health check, no DB) |
| GET | /api/status | – | `{"status":"ok","db":"ok","version":"…"}` |

---

# vault, two-factor, monitoring, daily check-ups, scoped commands

## Changed types

```ts
type User = {
  // … as above, plus:
  totp_enabled: boolean;
  checkup_time: string;    // "HH:MM" in the user's timezone; the daily check-up runs at/after it
  checkup_email: boolean;  // e-mail the daily check-up result
};

// GET /api/auth/me and POST /api/auth/login now answer
type MeResponse = { user: User; elevated_until: string | null };

type CommandDetail = {
  name: string; description: string;
  repos: string[];     // repo names it applies to; [] = any repo
  confirm: boolean;    // destructive/outward-facing (store upload, deploy): UI must ask, API needs `confirmed: true`
};
type RunnerCapabilities = {
  // … as above, plus:
  command_details: CommandDetail[];  // same names as `commands`, with metadata
  ci: boolean;                       // runner reports GitHub Actions status for repos (gh CLI)
  terminal: boolean;                 // reserved for the tmux terminal 
};

type CIStatus = {
  status: string;      // queued | in_progress | completed
  conclusion: string;  // success | failure | cancelled | … ("" while running)
  workflow: string; title: string; url: string; sha: string; at: string;
};
type GitStatus = { /* … as above, plus */ ci: CIStatus | null;
  untracked: number };   // "??" files; `dirty` counts tracked changes only

type Dashboard = { /* … as above, plus */ checkup: CheckupSummary | null };
```

## Auth additions

- `POST /api/auth/login` body gains optional `code` (6-digit TOTP). With 2FA on and
  no code → `401 {"code":"totp_required"}`; wrong code → `401 unauthorized`.
- **Elevation** (step-up): some actions need the password (and TOTP code when on)
  re-entered within the last 10 minutes. Without it they answer
  `403 {"code":"elevation_required"}`; the UI then asks and retries.

| Method | Path | Body | Response |
|---|---|---|---|
| POST | /api/auth/elevate | `{password, code?}` | `{elevated_until}` |
| POST | /api/auth/totp/setup | – (elevation required) | `{secret, otpauth_url}` — pending until enabled |
| POST | /api/auth/totp/enable | `{code}` | `204` |
| POST | /api/auth/totp/disable | `{password, code}` | `204` |
| PATCH | /api/auth/me | also `checkup_time`, `checkup_email` | `{user}` |

## Vault — encrypted secrets and files

Encrypted at rest with AES-256-GCM under a key that lives outside the database
(a key file outside the database). List/get never return secret values. `available: false`
(and `503 vault_unavailable` on writes) when the server has no vault key.

```ts
type VaultKind = "password" | "api_key" | "private_key" | "keystore" | "certificate"
  | "provisioning_profile" | "service_account" | "token" | "env_file" | "note" | "reference";
type VaultItem = {
  id: number; name: string; kind: VaultKind;
  project_key: string | null; project_color: string | null;
  platform: string;            // "ios" | "android" | "web" | "ci" | "infra" | ""
  host: string;                // where the original lives: "desktop" | "mac" | "1password" | "github" | …
  identifier: string;          // non-secret handle: username, e-mail, key id, alias
  fields: Record<string, string>;   // non-secret metadata (issuer_id, team_id, bundle_id, fingerprint…)
  secret_keys: string[];       // names of stored secret fields, e.g. ["value", "store_password"]
  has_file: boolean; file_name: string; file_size: number;
  location: string;            // where it lives outside Forge (path, sops key, GitHub secret name)
  expires_at: string | null;   // YYYY-MM-DD; the check-up warns 30 days ahead
  notes: string; tags: string[];
  created_at: string; updated_at: string; last_revealed_at: string | null;
};
type VaultAudit = { id: number; item_id: number | null; item_name: string;
  action: "create" | "update" | "reveal" | "download" | "delete"; ip: string; at: string };
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | /api/vault | query `project`, `q`, `kind` | `{items: VaultItem[], available: boolean}` |
| POST | /api/vault | `{name, kind, project_key?, platform?, host?, identifier?, fields?, secret?: Record<string,string>, file?: {name, content_base64}, location?, expires_at?, notes?, tags?}` | `201 VaultItem` |
| GET | /api/vault/{id} | – | `VaultItem` |
| PATCH | /api/vault/{id} | same keys; `secret` / `file` replace wholesale, `null` removes | `VaultItem` |
| DELETE | /api/vault/{id} | – (elevation) | `204` |
| POST | /api/vault/{id}/reveal | – (elevation) | `{secret: Record<string,string>}` |
| GET | /api/vault/{id}/file | – (elevation) | the file bytes (`Content-Disposition: attachment`) |
| GET | /api/vault/audit | query `limit` (50) | `{events: VaultAudit[]}` |

Integrations read their credentials from the vault by tag: an item tagged
`integration:grafana` (secret field `value` = a Grafana service-account token)
turns on Grafana alerts and dashboards in monitoring.

## Monitoring (VictoriaMetrics, Grafana, Nomad — over a private network)

```ts
type HostMetrics = {
  name: string; server_id: number | null;
  up: boolean;                 // reported metrics in the last 3 minutes
  last_seen: string | null;
  uptime_seconds: number | null;
  cpu_pct: number | null; mem_pct: number | null; disk_pct: number | null; // root filesystem
  load1: number | null;
};
type Probe = { url: string; up: boolean; ssl_days_left: number | null };  // Grafana blackbox probes
type NomadJob = { id: string; type: string; status: string;             // running | pending | dead
  running: number; failed: number; queued: number };
type NomadHost = { host: string; reachable: boolean; error: string; jobs: NomadJob[] };
type Alert = { name: string; state: "firing" | "pending" | "normal"; severity: string;
  summary: string; labels: Record<string,string>; since: string | null };
type Dashboard_ = { uid: string; title: string; folder: string; url: string };
type Monitoring = {
  fetched_at: string;
  hosts: HostMetrics[];
  probes: Probe[];
  nomad: NomadHost[];
  grafana: {
    url: string;                // browser URL, e.g. http://grafana.internal:3000
    connected: boolean;         // a token is in the vault and works
    error: string;
    alerts: Alert[];            // firing + pending only
    dashboards: Dashboard_[];   // from the Grafana API when connected, else from the repo's list
  };
};
```

| GET | /api/monitoring | query `fresh=1` bypasses the 30 s cache | `Monitoring` |

## Daily check-up

Runs automatically once per day at `user.checkup_time` (user timezone), and on
demand. Every check yields items; the non-ok ones are the day's action list.

```ts
type CheckSeverity = "ok" | "warn" | "fail";
type CheckItem = {
  key: string;               // stable within a day, e.g. "endpoint:12", "disk:web-1"
  category: "endpoints" | "tls" | "hosts" | "nomad" | "alerts" | "runners" | "repos" | "ci" | "tasks" | "vault" | "backups";
  severity: CheckSeverity;
  title: string; detail: string;
  action: string;            // what to do about it ("" for ok items)
  link: string | null;       // in-app route ("/p/ALPHA", "/infra") or external URL
  project_key: string | null;
  done: boolean;             // ticked off in the action list
  task_id: number | null; task_ref: string | null;  // when turned into a task
};
type CheckupSummary = {
  id: number; date: string; trigger: "schedule" | "manual";
  started_at: string; finished_at: string | null;
  status: CheckSeverity; counts: { ok: number; warn: number; fail: number };
  actions_total: number; actions_done: number; emailed: boolean;
};
type Checkup = CheckupSummary & { items: CheckItem[] };  // non-ok first (fail, warn), then ok
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | /api/checkups | query `limit` (30) | `{checkups: CheckupSummary[]}` newest first |
| GET | /api/checkups/latest | – | `Checkup` or `404` |
| GET | /api/checkups/{id} | – | `Checkup` |
| POST | /api/checkups | – | `201 Checkup` (runs now, ~10–30 s) |
| PATCH | /api/checkups/{id}/items/{key} | `{done}` | `Checkup` |
| POST | /api/checkups/{id}/items/{key}/task | `{project_key?}` | `{task: Task, checkup: Checkup}` (label `checkup`) |

## Runs: confirmed commands

`POST /api/runs` with `kind = command` gains `confirmed?: boolean`. A command
whose detail says `confirm: true` is refused (`422`, field `confirmed`) unless it
is `true`; a command scoped to repos is refused for any other repo.

---

# remote tmux terminals and Claude context

Runners that opt in (`"terminal": true` in their `~/.config/forge/agent.json`) report their tmux
sessions on every heartbeat and hold a control WebSocket open to the API. The
browser attaches to a session through the API, which relays bytes between the
browser's WebSocket and a per-terminal WebSocket the runner dials back — so no
machine ever accepts an inbound connection. Everything here needs **elevation**
(a terminal is a shell).

```ts
type TmuxSession = {
  name: string; windows: number;
  attached: number;           // clients currently attached (any machine)
  created: string; activity: string;   // RFC 3339
  path: string;               // active pane's working directory
  command: string;            // active pane's foreground command ("claude", "zsh", …)
  claude: boolean;            // some pane in the session runs claude
  project_key: string | null; project_color: string | null;   // matched from path (or session name)
  repo_id: number | null; repo_name: string | null;
};
type TerminalHost = {
  runner_id: number; runner_name: string; hostname: string;
  online: boolean;
  terminal: boolean;          // the runner allows terminals
  sessions: TmuxSession[];
  updated_at: string | null;  // when the session list was last reported
};
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | /api/terminal/hosts | – | `{hosts: TerminalHost[], default_runner_id: number \| null}` (the master runner) |
| POST | /api/terminal/{runner_id}/sessions | `{name?, repo_id?, project_key?, start: "claude" \| "shell", prompt?}` | `201 {session: string}` — detached session in the repo's directory; `start: claude` launches Claude Code with the Forge project context; `prompt` is typed into it once it starts |
| DELETE | /api/terminal/{runner_id}/sessions/{name} | – | `204` (kills the session) |
| GET | /api/terminal/{runner_id}/sessions/{name}/capture | query `lines` (default 60, max 400) | `{text: string}` — the visible screen + scrollback, no escape codes |
| POST | /api/terminal/{runner_id}/sessions/{name}/keys | `{text, enter: boolean}` | `204` — types `text` literally (then Enter) — answer Claude from a phone without a terminal |
| GET (WebSocket) | /api/terminal/{runner_id}/attach | query `session`, `cols`, `rows` | upgrade; see framing below |

WebSocket framing (browser side): **binary** frames carry terminal bytes in both
directions; **text** frames are JSON control: browser → `{"type":"resize","cols":N,"rows":N}`,
server → `{"type":"exit","reason":"…"}` before closing. The `Origin` header must
equal the app's own origin. Errors before the upgrade are ordinary JSON errors
(`elevation_required`, `not_found`, `runner_offline`, `terminal_disabled`).

Session names: `[A-Za-z0-9_.-]{1,40}`; a name given for a session that exists
just returns it.

## Runner side (Bearer runner token)

| Method | Path | Notes |
|---|---|---|
| POST | /api/runner/heartbeat | body gains `tmux: TmuxSession[]` (without the project fields); capabilities `terminal: true` |
| GET (WebSocket) | /api/runner/control | server → runner JSON: `{"type":"open","channel","session","cols","rows"}`, `{"type":"create","id","session","cwd","start","context","prompt"}`, `{"type":"capture","id","session","lines"}`, `{"type":"keys","id","session","text","enter"}`, `{"type":"kill","id","session"}`; runner → server `{"type":"result","id","ok","error","text"}` |
| GET (WebSocket) | /api/runner/tty/{channel} | the runner's end of one terminal: binary = bytes, text = the same JSON control frames |
| GET | /api/runner/context | query `cwd`; `{project_key, repo_name, markdown}` — compact Forge context for a Claude session started in that directory (404 when the path belongs to no project) |
| GET | /api/runner/projects | `{projects: [{key, name, status, summary}]}` |
| GET | /api/runner/tasks | query `project`, `open=1`, `q` → `{tasks: Task[]}` |
| POST | /api/runner/tasks | `{project_key, title, description?, priority?, type?, labels?, due_date?, status?}` → `201 Task` |
| PATCH | /api/runner/tasks/{id} | `{status?, title?, description?, priority?, focus?, labels?, due_date?}` → `Task` |
| POST | /api/runner/tasks/{id}/comments | `{body}` → `201 Comment` |

The last five back `forge agent mcp` (an MCP server Claude Code launches on
each machine) and `forge agent context` (a SessionStart hook that prints the
context for the session's directory), so every Claude session on a registered
repo starts knowing its Forge project, open tasks and today's check-up actions.

---

# the master machine, and VS Code in the browser

## Runner roles

Every runner has a `role`: **`master`** (exactly one — your always-on machine: the
primary place anything runs; the only source of repo git/CI state and the
default runner for runs and terminals), **`ios`** (a Mac: iOS builds only)
or **`worker`** (anything else, e.g. a laptop). Only the master's repo
scans are stored; the heartbeat tells other runners not to scan. The daily
check-up fails when the master has been offline for more than 10 minutes and
ignores sleeping non-master machines.

```ts
type RunnerRole = "master" | "ios" | "worker";
type Runner = { /* … as above, plus */ role: RunnerRole };
type RunnerCapabilities = { /* … as above, plus */ code: boolean };  // runner can serve VS Code
type TerminalHost = { /* … as above, plus */ role: RunnerRole };
// GET /api/terminal/hosts: default_runner_id is the master.
```

| Method | Path | Body | Response |
|---|---|---|---|
| PATCH | /api/runners/{id} | `{role}` | `Runner` — making one runner master demotes the previous master to `worker` |
| POST | /api/runner/heartbeat | – | response gains `role` and `scan: boolean` (false for non-masters) |

## VS Code (web) on the master

The master serves VS Code's own web build (`code serve-web`) on localhost; its
runner exposes it to the server over a private network through an authenticated
gateway, and the server proxies it to the browser at **`/code/`** (same origin,
so it embeds in Forge). Opening needs elevation; after that a `forge_code`
cookie (HttpOnly, Path=/code, 12 h, bound to the session) lets the editor's
own requests and WebSockets through. Signing out ends it.

| Method | Path | Body | Response |
|---|---|---|---|
| GET | /api/code/status | – | `{available: boolean, runner_name: string \| null, reason: string}` |
| POST | /api/code/open | `{project_key?, repo_ids?: number[], folder?: string}` (elevation) | `{url: string, workspace: string, expires_at: string}` — `url` is `/code/?workspace=…` (or `?folder=…` for one repo/folder); the runner writes a multi-root `.code-workspace` for a project (all its repos, or the listed ones) |
| * | /code/… | – | VS Code itself (HTML, assets, WebSockets) — needs the `forge_code` cookie; `401`/`403` JSON otherwise |

---

# security log, system facts, VS Code theme

## Security log

Every security-relevant action is recorded (`security_events`) and readable
from Settings → Security. Sign-ins from a device Forge has not seen before
(new IP + browser) also e-mail the account when SMTP is on.

```ts
type SecurityEvent = {
  id: number;
  kind: "login" | "login_failed" | "login_new_device" | "logout" | "elevate" | "elevate_failed"
    | "password_changed" | "password_reset" | "totp_enabled" | "totp_disabled" | "session_revoked"
    | "sessions_revoked_others" | "vault_reveal" | "vault_download" | "terminal_attach"
    | "terminal_keys" | "terminal_create" | "code_open" | "runner_created" | "runner_rotated";
  detail: string; ip: string; user_agent: string; session_id: number | null; at: string;
};
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | /api/auth/security | query `limit` (default 100, max 500), `kind` | `{events: SecurityEvent[]}` newest first |
| POST | /api/auth/sessions/revoke-others | – | `{revoked: number}` — signs out every other session |

Sessions now also expire 90 days after they were created, however active.

## System facts (for the Documentation page)

| GET | /api/system | – | `SystemFacts` — live, nothing cached |

```ts
type SystemFacts = {
  version: string; started_at: string; public_url: string;
  intervals: { endpoint_check: string; repo_scan: string; repo_sync: string; ci_status: string; runner_heartbeat: string;
               monitoring_cache: string; checkup_time: string; timezone: string; session_ttl: string;
               session_max: string; elevation: string; code_session: string };
  features: { smtp: boolean; vault: boolean; totp_enabled: boolean; grafana_connected: boolean;
              victoriametrics: boolean; monitoring_sources: string[] };
  master: { name: string; online: boolean; code: boolean; terminal: boolean } | null;
  runners: { name: string; role: string; online: boolean; commands: number; terminal: boolean }[];
  counts: { projects: number; repos: number; repos_scanned: number; servers: number; endpoints: number;
            tasks_open: number; tasks_done: number; vault_items: number; vault_with_values: number;
            checkups: number; runs: number; security_events: number };
  checkup: { last_at: string | null; last_trigger: string | null; next_at: string; emailed_last: boolean };
  seeded_at: string | null;   // when the initial data was loaded
};
```

## VS Code theme

`GET /code/?…&forge_theme=dark|light` — the master's gateway rewrites the
editor's boot configuration so VS Code's *default* theme is "Dark 2026" or
"Light 2026" and the first paint is already that colour. A theme chosen inside
VS Code still wins (it is a user setting; this only sets the default). The UI
appends the resolved Forge theme to the editor URL and reloads the frame when
it changes.

---

# first-run setup, machine pairing, accent themes, project files

## First-run setup

A server with no account prints a setup link, `<PUBLIC_URL>/setup?token=…`, in
its log on first start; `forge setup-token` on the server prints it again. The
token is only good until the account exists.

```ts
type SetupStatus = { needed: boolean };  // true while the server has no account
type SetupInput = {
  token: string; username: string; email: string;
  password: string;          // 12–72 characters (bcrypt's 72-byte limit)
  display_name?: string;
  timezone: string;          // IANA
  demo_data: boolean;        // load the demo seed (projects, servers, tasks)
};
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | /api/setup | – (no auth) | `{needed: boolean}` |
| POST | /api/setup | `SetupInput` (no auth) | `201 MeResponse` + session cookie, like login. `409 setup_done` (an account exists — sign in), `403 bad_setup_token`, `422 validation` with `field` (one of the `SetupInput` keys), `429 rate_limited` |

The web app's `/login` redirects to `/setup` (keeping `?token=`) while `needed`
is true; `/setup` redirects to `/login` once it is false. Usernames are 2–32
letters, digits, dots, dashes or underscores; `display_name` (at most 100
characters) defaults to the username and `timezone` to the server's.

The security log gains the kinds `setup` (the account was created here),
`runner_pair_code` (a code was issued) and `runner_paired` (a machine used one).

## Machine pairing

Adding a machine no longer shows its token. The server hands out a one-time
pairing code instead; on the machine, `forge agent pair <origin> <code>` (or
the install script's `--pair <origin> <code>`) swaps it for the token. Only the
code's hash is stored.

```ts
type Pairing = { code: string;         // e.g. "K7QD-M3XP"
                 expires_at: string };  // 15 minutes after it was issued; single use
type Runner = { /* … as above, plus */
  pair_expires_at: string | null;  // non-null = a code is outstanding and unused
};
```

- `POST /api/runners` `{name, role?: "master" | "ios" | "worker"}` →
  `201 {runner, pairing}`.
- `POST /api/runners/{id}/pair` → `{runner, pairing}`: a new code for an
  existing machine (reinstalled or moved). Any earlier unused code stops
  working. The machine's current token keeps working until the new code is used.
- `pair_expires_at` is set while a code is out and cleared (`null`) when it is
  used. The web app treats "cleared" as "used", and "used, and `last_seen_at`
  at or after `expires_at − 15 min`" as connected — so keep `pair_expires_at`
  set on an expired, unused code (don't clear it in a cleanup job), and keep
  the 15-minute lifetime in step with the UI.
- `POST /api/runners/{id}/rotate` is unchanged (manual setup: the token goes
  into `~/.config/forge/agent.json` as `"token"`).

The machine's side — no session, no CSRF header (it is not a browser); the
code is the credential, so failures are rate-limited (10 per address, 60 in
all, per 15 minutes):

- `POST /api/runner/pair` `{code, hostname?, os?, version?}` → `200
  {runner_id, name, role, token, api_url}`. The code is matched
  case-insensitively with separators ignored. `404 bad_code` for an unknown,
  used or expired code; `429 rate_limited`. The returned `token` replaces the
  machine's previous one.

## Task matching (Claude Code prompts)

`forge agent setup-claude` installs a `UserPromptSubmit` hook,
`forge agent match --hook`, next to the SessionStart context hook. For each
request typed into Claude Code it posts:

- `POST /api/runner/match` `{cwd, prompt, known?: string[]}` →
  `{project_key, matches: [{ref, title, status, priority, score, why}], markdown}`.
  `why`: `named` (a ref such as `SHOP-3` in the prompt, score 1), `jev` (Jev
  probability ≥ 0.6 that the request is work on that task; Jev on with `match`),
  or `keywords` (≥ 2 significant title words shared, covering ≥ ⅓ of the title).
  Candidates: the open tasks of the project `cwd` resolves to (as the session
  context does; all projects when none), up to 60. Prompts under 12 characters or
  starting with `/` only match named refs. At most 3 matches; refs in `known` are
  left out. `markdown` is empty when nothing matched.

The hook adds `markdown` as context and remembers the refs per session (user
cache dir, `forge/prompt-matches/<session>.json`, pruned after 7 days), so a
session hears about each task once. Errors print nothing.

## Agent engine

An agent run's Claude Code talks to Anthropic (`claude`) or to DeepSeek's
Anthropic-compatible API (`deepseek`). The DeepSeek key is the vault item tagged
`integration:deepseek`, else the Assistant's key when its `base_url` is on
`deepseek.com`.

```ts
type Engine = "deepseek" | "claude";
type EngineSettings = { default: Engine; model: string; heavy_model: string };
// defaults: "deepseek", "deepseek-flash", "deepseek-v4-pro"
type EngineStatus = { settings: EngineSettings; deepseek_key: boolean };
type Run = { /* … */ engine: Engine | "";     // "" for command runs
              effort: string;                // claude --effort, "" = default
              chat_turn_id: number | null;   // the Assistant turn that queued it
              usage: Record<string, unknown> }; // Claude Code's modelUsage
type RunInput = { /* … */ engine?: Engine };  // omitted = settings.default
```

- `GET /api/engine` → `EngineStatus`. `PATCH /api/engine` `Partial<EngineSettings>` → `EngineStatus`.
- `PUT /api/engine/key` `{api_key}` → `EngineStatus` (elevation; creates/updates the vault item).
- `POST /api/runs` (agent): no `engine` → `settings.default`; `deepseek` without a
  key → `claude` with `model_note` saying so. Then Jev routing (below), then a
  DeepSeek run without a model gets `settings.model`. `resume_run_id` keeps the
  original run's engine. Unknown engines → 422.
- `POST /api/runner/claim` → `{run, engine}`: `engine` is `null` for Claude runs,
  `{name: "deepseek", base_url, api_key, model, heavy_model}` for DeepSeek runs. The
  agent drops `ANTHROPIC_*`/`CLAUDE_CODE_OAUTH_TOKEN` from that process's environment,
  sets `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_MODEL` (+ `[1m]`) and
  the default/sub-agent model variables, and reports `cost_usd` estimated from the
  result's `modelUsage` at DeepSeek's list prices (doubled in its peak hours).

## Jev (token saving)

TypeSafe's Jev answers typed questions (yes/no probabilities, pick-one) for a
fraction of Claude's tokens. Off until enabled; the key is the `value` secret of
the newest vault item tagged `integration:jev`. Every use fails open.

```ts
type JevSettings = { enabled: boolean; routing: boolean; context: boolean; compaction: boolean; match: boolean };
type JevStatus = {
  settings: JevSettings;
  key_configured: boolean;                   // the key itself is never returned
  stats: { calls: number; errors: number; input_tokens: number; last_at: string | null;
           last_error: string; last_model: string };  // since the server started
};
type Run = { /* … */ model_note: string };   // "Jev: light task (91% sure) → haiku", else ""
```

- `GET /api/jev` → `JevStatus`. `PATCH /api/jev` `Partial<JevSettings>` → `JevStatus`.
- `PUT /api/jev/key` `{api_key}` → `JevStatus` (elevation; creates/updates the vault item).
- `POST /api/jev/test` → `{ok, ms?, error?}` — one tiny call.
- **Routing**: `POST /api/runs` for `kind: "agent"` with no `model` (and not a resume)
  asks Jev light / medium / heavy. On `claude`: ≥ 70 % confident light → `haiku`,
  medium → `sonnet`; otherwise the model stays `""` (the machine's default). On
  `deepseek`: ≥ 70 % confident heavy → `heavy_model`; otherwise `model`.
- **Context**: `GET /api/runner/context` lists, when a project has more than 8 open
  tasks, the always-kept ones (focus, in progress, blocked, urgent, the repo's own)
  plus up to 12 in total that Jev scores ≥ 0.4 relevant to the repo; the heading
  says so. Scores are cached 30 minutes per task set.
- **Match**: see *Task matching* below — Jev scores the candidates when on.
- **Machines**: the heartbeat answer carries `jev_rev`; when it changes the agent
  reads `GET /api/runner/jev` → `{compaction, api_key?, plugin_repo, plugin_commit,
  rev}` and, with compaction on, installs the plugin at exactly `plugin_commit` and
  sets `TYPESAFE_API_KEY` + `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1` in
  `~/.claude/settings.json` (marked `FORGE_MANAGES_JEV`); off undoes only that.
  `"claude_jev": false` in `agent.json` opts a machine out.

## Accent

```ts
type User = { /* … as above, plus */
  accent: string;  // "" = default | preset id | custom "#rrggbb" (lowercase)
};
```

Preset ids: `indigo` (the default look), `blue`, `sky`, `teal`, `green`,
`lime`, `amber`, `orange`, `red`, `rose`, `pink`, `violet`. `PATCH
/api/auth/me` accepts `{accent}`; anything other than `""`, a preset id or a
`#rrggbb` colour → `422 validation` (`field: "accent"`). The web app sends
`""` for the default rather than `"indigo"`, and treats an unknown value as
the default.

`GET /code/?…&forge_accent=rrggbb` (six hex digits, no `#`) — the resolved
accent for the current theme, next to `forge_theme`, so the gateway can match
VS Code's accent. It is omitted when not a valid colour.

## Project files

Each project has a folder on the server (`projects/<KEY>/` in the workspace).

```ts
type ProjectFile = { name: string; size: number /* bytes */; modified_at: string };
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | /api/projects/{key}/files | – | `{files: ProjectFile[]}` |
| POST | /api/projects/{key}/files | `multipart/form-data`, one field `file` (max 100 MB); the file's name is kept, the same name overwrites | `201 {file: ProjectFile}` |
| GET | /api/projects/{key}/files/{name} | – | the file, `Content-Disposition: attachment` |
| DELETE | /api/projects/{key}/files/{name} | – | `204` |

Names: no slashes, no leading dot, no control characters, at most 200 bytes
(`422 validation` otherwise); over 100 MB → `413 too_large`. `{name}` is URL-encoded in the path. Uploads
and deletes need `X-Forge-Client: web` like every other write.

## Assistant

A chat with an LLM "main agent" (any OpenAI-compatible API; DeepSeek by
default) that can read and update Forge and delegate work to Claude Code by
queuing runs. The agent loop runs on the server; the web app posts messages
and polls. Off until enabled; the key is the `value` secret of the newest vault
item tagged `integration:assistant`.

```ts
type AssistantSettings = {
  enabled: boolean;
  base_url: string;   // OpenAI-compatible API base, default "https://api.deepseek.com"
  model: string;      // default "deepseek-flash"
};
type AssistantStatus = {
  settings: AssistantSettings;
  key_configured: boolean;       // the key itself is never returned
  models: string[];              // models the provider lists for this key ([] if unknown/unreachable)
  engines: {
    deepseek: { available: boolean; models: string[]; efforts: string[] };  // efforts: off|low|high|max
    claude: { available: boolean; machine: string; online: boolean | null; reason: string;
              models: string[]; efforts: string[] };                        // efforts: low|medium|high|xhigh|max
  };
};
// What answers a chat, per chat (changeable any time; applies from the next turn).
type ChatSettings = {
  engine: "deepseek" | "claude"; // the DeepSeek API loop on the server | a Claude Code session on the master
  model: string;                 // "" = default (AssistantSettings.model | Claude Code's own)
  effort: string;                // "" = default; deepseek: off|low|high|max (thinking off / reasoning_effort)
  edits: boolean;                // claude: acceptEdits instead of plan
};
type UsageEntry = {                // one line of a turn's bill
  api: string;                     // "deepseek" | "jev" | "claude-code" | the provider's host
  model: string; calls: number;
  input_tokens: number;            // not from cache
  cached_tokens: number; output_tokens: number;
  cost_usd: number | null;         // null: no public price. DeepSeek: list price (peak hours ×2);
                                   // Jev: $0.42/M input (smallest pack); claude-code: Claude Code's figure at API
                                   // prices (the session runs on the machine's Claude login)
  note?: string;
};
type ChatTurn = {                  // one user message's processing
  id: number; chat_id: number;
  seq: number;                     // the user message that started it
  engine: string; model: string; effort: string;
  status: "running" | "done" | "failed" | "stopped";
  error: string;
  usage: UsageEntry[];             // its own API calls (the chat model's, Jev's), live while running
  run_id: number | null;           // claude: the session run
  started_at: string; finished_at: string | null;
  runs: Run[];                     // runs it queued (their cost_usd arrives as they finish) + its session run
};
type ChatUsage = { input_tokens: number; output_tokens: number; cached_tokens: number };
type Chat = {
  id: number;
  title: string;
  busy: boolean;          // the agent is working on this chat right now
  last_error: string;     // "" or why the last turn failed
  usage: ChatUsage;       // totals for the chat
  created_at: string;
  updated_at: string;
} & ChatSettings;
type ChatToolCall = { id: string; name: string; arguments: Record<string, unknown> };
type ChatMessage = {
  seq: number;                              // increasing per chat, starts at 1
  role: "user" | "assistant" | "tool";
  content: string;                          // markdown for user/assistant; for tool: short human summary line
  tool_calls: ChatToolCall[];               // assistant messages that call tools ([] otherwise)
  tool_call_id: string;                     // tool messages: which call this answers ("" otherwise)
  tool_name: string;                        // tool messages: the tool's name ("" otherwise)
  result: unknown;                          // tool messages: the structured result (object), else null
  is_error: boolean;                        // tool messages: the tool failed
  created_at: string;
};
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | /api/assistant | – | `AssistantStatus` |
| PATCH | /api/assistant | `Partial<AssistantSettings>` | `AssistantStatus` |
| PUT | /api/assistant/key | `{api_key}` | `AssistantStatus` (elevation; creates/updates the vault item) |
| GET | /api/chats | – | `{chats: Chat[]}`, newest first |
| POST | /api/chats | `{content, ...Partial<ChatSettings>}` | `201 {chat, messages, turns}` — the chat with its first user message; the agent starts (`busy: true`) |
| GET | /api/chats/{id}?after=N | – | `{chat, messages, turns}` — only messages with `seq > N` (no `after` → all); `turns` always whole |
| POST | /api/chats/{id}/messages | `{content, ...Partial<ChatSettings>}` | `{chat, messages, turns}` (the new user message); `409 busy` while the agent is still on this chat |
| POST | /api/chats/{id}/stop | – | `{chat}` — cancels the running turn (claude: cancels its run) |
| PATCH | /api/chats/{id} | `{title?, ...Partial<ChatSettings>}` | `{chat}` — changing `engine` resets `model` and `effort` |
| DELETE | /api/chats/{id} | – | `204` |

- `503 assistant_off` when the assistant is not enabled, or (deepseek) has no key;
  `503 no_machine` (claude) without a master machine that has Claude Code. A
  bad engine/model/effort → `422`.
- **Claude Code turns** (`engine: "claude"`): each turn is an agent run on the
  master machine, with no repo (`repo_id` 0, not listed by `GET /api/runs`), in
  the machine's first allowed root, `chat_turn_id` set. The first turn starts a
  session; later ones resume the chat's latest session run (`resume_run_id`),
  and turns another engine answered in between are handed over as a transcript.
  The claim answer carries `assistant: {append_system}` (the chat's
  instructions); the agent adds `--append-system-prompt` and
  `--allowedTools mcp__forge` (Forge's MCP tools; nothing else is widened). When
  the run finishes (or is found finished on a poll), its `result` becomes the
  assistant message and its `usage` (Claude Code's `modelUsage`) the turn's.
  A server restart does not end a Claude Code turn; it does end a DeepSeek one.
- DeepSeek turns send `reasoning_content` back on every earlier assistant
  message (required by its thinking mode with tools).
- The web app polls `GET /api/chats/{id}?after=<last seq it holds>` every 1.5 s
  while `chat.busy` and stops when it is false — so every message of a turn
  must be stored before `busy` flips to false. Messages are not expected to
  change once written; if the same `seq` arrives twice, the later copy wins.
- An assistant message's `tool_calls` are answered by `tool` messages whose
  `tool_call_id` matches a call's `id`; the app shows a call as running until
  its answer arrives.
- Tools (`tool_name`), with the arguments and result fields the web app reads:
  - `delegate_to_claude` `{project, repo, prompt, machine?, permission_mode?,
    engine?, model?, worktree?, task_ref?}` → `{run_id, machine, project, repo,
    permission_mode, engine, model, model_note}`. Queued like `POST /api/runs` with the
    same machine rules; `bypassPermissions` is never used. The model is told to
    set `engine: "claude"` only when the user explicitly asks for Claude.
  - `run_command` `{project, repo, command, machine?}` → `{run_id, machine,
    command, …}`. Commands that need confirming are refused (left to the user).
  - `get_run` `{run_id}` → `{run: {id, status, kind, runner_name, repo_name,
    result_excerpt, error, cost_usd, …}}`.
  - `wait_for_runs` `{run_ids, max_seconds}` → `{runs: [{id, status, …}], timed_out}`.
  - `create_task`, `update_task` → `{task: Task}`.
  - `list_projects`, `get_project`, `list_tasks`, `get_task`,
    `comment_on_task`, `list_machines`, `get_overview`, `list_runs` — shown
    generically (arguments and result as JSON).
- A failed tool call is a `tool` message with `is_error: true` and the reason
  in `content`.
