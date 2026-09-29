// The contract's types, verbatim from docs/API.md at the repo root. When that file
// changes, this one changes in the same session — the two halves agree by
// copying, not by generation, because the contract is small and read by people.

/* eslint-disable @typescript-eslint/no-explicit-any */

export type User = {
  id: number;
  username: string;
  email: string;
  display_name: string;
  timezone: string; // IANA, e.g. "Europe/Lisbon"; drives "today"
  weekly_goal: number; // tasks to finish per week (dashboard ring)
  created_at: string;
  totp_enabled: boolean;
  checkup_time: string; // "HH:MM" in the user's timezone
  checkup_email: boolean; // e-mail the daily check-up result
  accent: string; // "" = default, a preset id ("teal") or a custom "#rrggbb"
};

/** GET /api/auth/me, POST /api/auth/login and POST /api/setup. */
export type MeResponse = { user: User; elevated_until: string | null };

/** GET /api/setup (no auth): true until the server has its one account. */
export type SetupStatus = { needed: boolean };

/** POST /api/setup — the setup token is printed in the server log on first start. */
export type SetupInput = {
  token: string;
  username: string;
  email: string;
  password: string;
  display_name?: string;
  timezone: string;
  demo_data: boolean;
};

export type Session = {
  id: number;
  current: boolean;
  user_agent: string;
  ip: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
};

export type ProjectStatus = "live" | "building" | "radar" | "paused" | "archived";
export type Category = "work" | "personal";

export type ProjectStats = {
  total: number;
  backlog: number;
  todo: number;
  in_progress: number;
  blocked: number;
  done: number;
  overdue: number;
  done_7d: number;
  created_7d: number;
  commits_7d: number;
  dirty_repos: number;
  endpoints_total: number;
  endpoints_up: number;
  endpoints_down: number;
  active_runs: number;
  last_activity_at: string | null;
  progress: number; // 0..1
};

export type Link = { label: string; url: string };

export type Project = {
  id: number;
  key: string;
  name: string;
  category: Category;
  status: ProjectStatus;
  priority: number; // 1 (top focus) .. 5
  color: string; // "#rrggbb"
  summary: string;
  description: string;
  infra_notes: string;
  target_date: string | null;
  links: Link[];
  created_at: string;
  updated_at: string;
  stats: ProjectStats;
};

export type GitHead = { hash: string; subject: string; author: string; at: string };
export type GitStatus = {
  branch: string;
  dirty: number; // tracked changes (modified, staged, deleted, renamed)
  untracked?: number; // "??" files, reported separately since phase 4
  ahead: number;
  behind: number;
  head: GitHead | null;
  commits_7d: number;
  last_commit_at: string | null;
  scanned_at: string;
  runner_name: string;
  error: string;
  ci: CIStatus | null;
  sync?: SyncStatus | null; // the master's latest fetch + fast-forward
};

export type SyncStatus = {
  at: string;
  result: "up_to_date" | "pulled" | "skipped" | "error";
  detail: string;
  pulled: number;
};

export type CIStatus = {
  status: string; // queued | in_progress | completed
  conclusion: string; // success | failure | cancelled | … ("" while running)
  workflow: string;
  title: string;
  url: string;
  sha: string;
  at: string;
};

export type RepoKind = "api" | "ui" | "mobile" | "infra" | "lib" | "site" | "other";
export type Repo = {
  id: number;
  project_id: number;
  name: string;
  path: string;
  remote_url: string;
  default_branch: string;
  kind: RepoKind;
  deploy: string;
  notes: string;
  sort_order: number;
  git: GitStatus | null;
};

export type Environment = "production" | "staging" | "dev" | "infra";
export type Server = {
  id: number;
  name: string;
  role: string;
  provider: string;
  arch: string;
  public_address: string;
  tailscale_ip: string;
  environment: Environment;
  critical: boolean;
  tags: string[];
  notes: string;
  projects: { key: string; name: string; color: string; role: string }[];
  created_at: string;
  updated_at: string;
};
export type ProjectServer = {
  server_id: number;
  name: string;
  role: string;
  environment: Environment;
  critical: boolean;
};

export type EndpointKind = "web" | "api" | "health";
export type Endpoint = {
  id: number;
  project_id: number;
  project_key: string;
  project_name: string;
  project_color: string;
  name: string;
  url: string;
  kind: EndpointKind;
  expect_status: number;
  enabled: boolean;
  last_status: "up" | "down" | "unknown";
  last_code: number | null;
  last_latency_ms: number | null;
  last_error: string;
  last_checked_at: string | null;
  last_change_at: string | null;
  uptime_24h: number | null;
};
export type EndpointCheck = {
  at: string;
  ok: boolean;
  code: number | null;
  latency_ms: number | null;
  error: string;
};

/** A file in the project's folder on the server. */
export type ProjectFile = { name: string; size: number; modified_at: string };

export type ProjectDetail = Project & {
  repos: Repo[];
  servers: ProjectServer[];
  endpoints: Endpoint[];
};

export type TaskStatus = "backlog" | "todo" | "in_progress" | "blocked" | "done";
export type Priority = "urgent" | "high" | "medium" | "low";
export type TaskType = "feature" | "bug" | "chore" | "research" | "ops";
export type Task = {
  id: number;
  project_id: number;
  project_key: string;
  project_name: string;
  project_color: string;
  number: number;
  ref: string;
  title: string;
  description: string;
  status: TaskStatus;
  priority: Priority;
  type: TaskType;
  labels: string[];
  due_date: string | null;
  focus: boolean;
  repo_id: number | null;
  repo_name: string | null;
  estimate: number | null;
  sort_order: number;
  completed_at: string | null;
  created_at: string;
  updated_at: string;
  comment_count: number;
};
export type Comment = { id: number; task_id: number; body: string; created_at: string };
export type TaskDetail = Task & { comments: Comment[]; runs: Run[] };

export type Activity = {
  id: number;
  project_id: number | null;
  project_key: string | null;
  project_color: string | null;
  task_id: number | null;
  task_ref: string | null;
  run_id: number | null;
  kind: string;
  summary: string;
  created_at: string;
};

export type CommandDetail = {
  name: string;
  description: string;
  repos: string[]; // repo names it applies to; [] = any repo
  confirm: boolean; // destructive/outward-facing: UI must ask, API needs `confirmed: true`
};

export type RunnerRole = "master" | "ios" | "worker";

export type RunnerCapabilities = {
  claude: boolean;
  permission_modes: string[];
  commands: string[];
  max_concurrent: number;
  command_details: CommandDetail[];
  ci: boolean;
  terminal: boolean;
  code: boolean; // runner can serve VS Code (web)
};
export type Runner = {
  id: number;
  name: string;
  hostname: string;
  os: string;
  version: string;
  online: boolean;
  last_seen_at: string | null;
  capabilities: RunnerCapabilities;
  running: number;
  created_at: string;
  role: RunnerRole;
  pair_expires_at: string | null; // non-null = a pairing code is outstanding and unused
};

/** A one-time code a machine swaps for its token (`forge agent pair <origin> <code>`). */
export type Pairing = { code: string; expires_at: string };

export type RunStatus = "queued" | "running" | "succeeded" | "failed" | "cancelled";
export type RunKind = "agent" | "command";
/** Where an agent run's Claude Code sends its requests. */
export type Engine = "deepseek" | "claude";
export type Run = {
  id: number;
  runner_id: number;
  runner_name: string;
  project_id: number;
  project_key: string;
  project_color: string;
  repo_id: number;
  repo_name: string;
  task_id: number | null;
  task_ref: string | null;
  kind: RunKind;
  prompt: string;
  command: string;
  permission_mode: string;
  model: string;
  model_note: string; // why Forge chose the model ("Jev: …"), else ""
  engine: Engine | ""; // agent runs: the backend Claude Code talks to; "" for commands
  worktree: boolean;
  resume_run_id: number | null;
  status: RunStatus;
  cancel_requested: boolean;
  session_id: string;
  result: string;
  error: string;
  exit_code: number | null;
  cost_usd: number | null;
  num_turns: number | null;
  duration_ms: number | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
};
export type RunEvent = {
  seq: number;
  at: string;
  kind: "claude" | "stdout" | "stderr" | "system";
  data: any;
};

export type DayCount = { date: string; done: number; created: number };
export type DashboardStats = {
  streak_days: number;
  best_streak: number;
  done_today: number;
  done_week: number;
  done_prev_week: number;
  weekly_goal: number;
  open_tasks: number;
  in_progress: number;
  blocked: number;
  overdue: number;
  due_soon: number;
};
export type Dashboard = {
  today: string;
  stats: DashboardStats;
  daily: DayCount[];
  focus: Task[];
  overdue: Task[];
  projects: Project[];
  endpoints_down: Endpoint[];
  runners: Runner[];
  active_runs: Run[];
  recent_runs: Run[];
  activity: Activity[];
  checkup: CheckupSummary | null;
};

// ── Vault ───────────────────────────────────────────────────────────────────

export type VaultKind =
  | "password"
  | "api_key"
  | "private_key"
  | "keystore"
  | "certificate"
  | "provisioning_profile"
  | "service_account"
  | "token"
  | "env_file"
  | "note"
  | "reference";

export type VaultItem = {
  id: number;
  name: string;
  kind: VaultKind;
  project_key: string | null;
  project_color: string | null;
  platform: string; // "ios" | "android" | "web" | "ci" | "infra" | ""
  host: string; // where the original lives: "desk" | "mac" | "sops" | "github" | …
  identifier: string; // non-secret handle
  fields: Record<string, string>; // non-secret metadata
  secret_keys: string[];
  has_file: boolean;
  file_name: string;
  file_size: number;
  location: string;
  expires_at: string | null;
  notes: string;
  tags: string[];
  created_at: string;
  updated_at: string;
  last_revealed_at: string | null;
};

export type VaultAudit = {
  id: number;
  item_id: number | null;
  item_name: string;
  action: "create" | "update" | "reveal" | "download" | "delete";
  ip: string;
  at: string;
};

export type VaultInput = {
  name?: string;
  kind?: VaultKind;
  project_key?: string | null;
  platform?: string;
  host?: string;
  identifier?: string;
  fields?: Record<string, string>;
  secret?: Record<string, string> | null; // replaces wholesale; null removes
  file?: { name: string; content_base64: string } | null; // replaces; null removes
  location?: string;
  expires_at?: string | null;
  notes?: string;
  tags?: string[];
};

// ── Monitoring ──────────────────────────────────────────────────────────────

export type HostMetrics = {
  name: string;
  server_id: number | null;
  up: boolean;
  last_seen: string | null;
  uptime_seconds: number | null;
  cpu_pct: number | null;
  mem_pct: number | null;
  disk_pct: number | null;
  load1: number | null;
};
export type Probe = { url: string; up: boolean; ssl_days_left: number | null };
export type NomadJob = { id: string; type: string; status: string; running: number; failed: number; queued: number };
export type NomadHost = { host: string; reachable: boolean; error: string; jobs: NomadJob[] };
export type Alert = {
  name: string;
  state: "firing" | "pending" | "normal";
  severity: string;
  summary: string;
  labels: Record<string, string>;
  since: string | null;
};
export type GrafanaDashboard = { uid: string; title: string; folder: string; url: string };
export type Monitoring = {
  fetched_at: string;
  hosts: HostMetrics[];
  probes: Probe[];
  nomad: NomadHost[];
  grafana: {
    url: string;
    connected: boolean;
    error: string;
    alerts: Alert[];
    dashboards: GrafanaDashboard[];
  };
};

// ── Daily check-up ──────────────────────────────────────────────────────────

export type CheckSeverity = "ok" | "warn" | "fail";
export type CheckCategory =
  | "endpoints"
  | "tls"
  | "hosts"
  | "nomad"
  | "alerts"
  | "runners"
  | "repos"
  | "ci"
  | "tasks"
  | "vault"
  | "backups";
export type CheckItem = {
  key: string;
  category: CheckCategory;
  severity: CheckSeverity;
  title: string;
  detail: string;
  action: string;
  link: string | null;
  project_key: string | null;
  done: boolean;
  task_id: number | null;
  task_ref: string | null;
};
export type CheckupSummary = {
  id: number;
  date: string;
  trigger: "schedule" | "manual";
  started_at: string;
  finished_at: string | null;
  status: CheckSeverity;
  counts: { ok: number; warn: number; fail: number };
  actions_total: number;
  actions_done: number;
  emailed: boolean;
};
export type Checkup = CheckupSummary & { items: CheckItem[] };

// ── Request bodies ──────────────────────────────────────────────────────────

export type ProjectInput = Partial<
  Pick<
    Project,
    | "key"
    | "name"
    | "category"
    | "status"
    | "priority"
    | "color"
    | "summary"
    | "description"
    | "infra_notes"
    | "target_date"
    | "links"
  >
>;

export type RepoInput = Partial<
  Pick<Repo, "name" | "path" | "remote_url" | "default_branch" | "kind" | "deploy" | "notes" | "sort_order">
>;

export type EndpointInput = Partial<Pick<Endpoint, "name" | "url" | "kind" | "expect_status" | "enabled">>;

export type ServerInput = Partial<
  Pick<
    Server,
    | "name"
    | "role"
    | "provider"
    | "arch"
    | "public_address"
    | "tailscale_ip"
    | "environment"
    | "critical"
    | "tags"
    | "notes"
  >
>;

export type TaskInput = Partial<
  Pick<
    Task,
    | "title"
    | "description"
    | "status"
    | "priority"
    | "type"
    | "labels"
    | "due_date"
    | "focus"
    | "repo_id"
    | "estimate"
    | "sort_order"
  >
> & { project_key?: string };

export type RunInput = {
  runner_id: number;
  repo_id: number;
  kind: RunKind;
  prompt?: string;
  command?: string;
  permission_mode?: string;
  model?: string;
  worktree?: boolean;
  task_id?: number | null;
  resume_run_id?: number | null;
  confirmed?: boolean;
  engine?: Engine; // omitted = the server's default (Settings → Agent engine)
};

export type TaskFilters = {
  project?: string;
  status?: TaskStatus[];
  focus?: boolean;
  q?: string;
  priority?: Priority;
  type?: TaskType;
  label?: string;
  overdue?: boolean;
  open?: boolean;
  limit?: number;
};

export type RunFilters = {
  project?: string;
  task?: number;
  status?: RunStatus;
  limit?: number;
};

// ── Terminal (tmux sessions via runners) ────────────────────────────────────

export type TmuxWindow = {
  index: number;
  name: string;
  active: boolean; // the session's current window
  command: string;
  path: string;
  claude: boolean;
  project_key: string | null;
  project_color: string | null;
  repo_id: number | null;
  repo_name: string | null;
};

/** Session-level path/command/project fields describe its active window. */
export type TmuxSession = {
  name: string;
  windows: number;
  window_list: TmuxWindow[];
  attached: number; // clients currently attached (any machine)
  created: string;
  activity: string;
  path: string; // active pane's working directory
  command: string; // active pane's foreground command
  claude: boolean; // some pane in the session runs claude
  project_key: string | null;
  project_color: string | null;
  repo_id: number | null;
  repo_name: string | null;
};

export type TerminalHost = {
  runner_id: number;
  runner_name: string;
  hostname: string;
  online: boolean;
  terminal: boolean; // the runner allows terminals
  sessions: TmuxSession[];
  updated_at: string | null;
  role: RunnerRole;
};

export type TerminalHosts = { hosts: TerminalHost[]; default_runner_id: number | null };

export type NewSessionInput = {
  name?: string;
  repo_id?: number;
  project_key?: string;
  start: "claude" | "shell";
  prompt?: string;
};

// ── VS Code (web) on the master ────────────────────────────────────────────

export type CodeStatus = { available: boolean; runner_name: string | null; reason: string };
export type CodeOpenInput = { project_key?: string; repo_ids?: number[]; folder?: string };
export type CodeOpenResult = { url: string; workspace: string; expires_at: string };

// ── Security log & system facts ────────────────────────────────────────────

export type SecurityKind =
  | "login"
  | "login_failed"
  | "login_new_device"
  | "logout"
  | "elevate"
  | "elevate_failed"
  | "password_changed"
  | "password_reset"
  | "totp_enabled"
  | "totp_disabled"
  | "session_revoked"
  | "sessions_revoked_others"
  | "vault_reveal"
  | "vault_download"
  | "terminal_attach"
  | "terminal_keys"
  | "terminal_create"
  | "code_open"
  | "setup"
  | "runner_created"
  | "runner_pair_code"
  | "runner_paired"
  | "runner_rotated"
  | "jev_settings"
  | "jev_key"
  | "assistant_settings"
  | "assistant_key"
  | "engine_settings"
  | "deepseek_key";

export type SecurityEvent = {
  id: number;
  kind: SecurityKind;
  detail: string;
  ip: string;
  user_agent: string;
  session_id: number | null;
  at: string;
};

export type SystemFacts = {
  version: string;
  started_at: string;
  public_url: string;
  intervals: {
    endpoint_check: string;
    repo_scan: string;
    repo_sync: string;
    ci_status: string;
    runner_heartbeat: string;
    monitoring_cache: string;
    checkup_time: string;
    timezone: string;
    session_ttl: string;
    session_max: string;
    elevation: string;
    code_session: string;
  };
  features: {
    smtp: boolean;
    vault: boolean;
    totp_enabled: boolean;
    grafana_connected: boolean;
    victoriametrics: boolean;
    monitoring_sources: string[];
  };
  master: { name: string; online: boolean; code: boolean; terminal: boolean } | null;
  runners: { name: string; role: string; online: boolean; commands: number; terminal: boolean }[];
  counts: {
    projects: number;
    repos: number;
    repos_scanned: number;
    servers: number;
    endpoints: number;
    tasks_open: number;
    tasks_done: number;
    vault_items: number;
    vault_with_values: number;
    checkups: number;
    runs: number;
    security_events: number;
  };
  checkup: { last_at: string | null; last_trigger: string | null; next_at: string; emailed_last: boolean };
  seeded_at: string | null;
};

// ── Agent engine ──────────────────────────────────────────────────────────

export type EngineSettings = {
  default: Engine; // used by runs that do not pick an engine
  model: string; // DeepSeek model for ordinary runs
  heavy_model: string; // DeepSeek model when Jev judges a task heavy
};

export type EngineStatus = { settings: EngineSettings; deepseek_key: boolean };

// ── Jev (token saving) ────────────────────────────────────────────────────

export type JevSettings = {
  enabled: boolean;
  routing: boolean; // pick haiku/sonnet for agent runs queued without a model
  context: boolean; // session context lists only tasks relevant to the repo
  compaction: boolean; // machines wire Jev compaction into Claude Code
};

export type JevStatus = {
  settings: JevSettings;
  key_configured: boolean;
  stats: {
    calls: number;
    errors: number;
    input_tokens: number;
    last_at: string | null;
    last_error: string;
    last_model: string;
  };
};

// ── Assistant (a chat that runs Forge and delegates to Claude Code) ────────

export type AssistantSettings = {
  enabled: boolean;
  base_url: string; // OpenAI-compatible API base, default "https://api.deepseek.com"
  model: string; // default "deepseek-flash"
};

export type AssistantStatus = {
  settings: AssistantSettings;
  key_configured: boolean; // the key itself is never returned
  models: string[]; // what the provider lists for this key ([] if unknown/unreachable)
};

export type ChatUsage = { input_tokens: number; output_tokens: number; cached_tokens: number };

export type Chat = {
  id: number;
  title: string;
  busy: boolean; // the agent is working on this chat right now
  last_error: string; // "" or why the last turn failed
  usage: ChatUsage; // totals for the chat
  created_at: string;
  updated_at: string;
};

export type ChatToolCall = { id: string; name: string; arguments: Record<string, unknown> };

export type ChatMessage = {
  seq: number; // increasing per chat, starts at 1
  role: "user" | "assistant" | "tool";
  content: string; // markdown for user/assistant; for tool: a short human summary line
  tool_calls: ChatToolCall[]; // assistant messages that call tools ([] otherwise)
  tool_call_id: string; // tool messages: which call this answers ("" otherwise)
  tool_name: string; // tool messages: the tool's name ("" otherwise)
  result: unknown; // tool messages: the structured result (object), else null
  is_error: boolean; // tool messages: the tool failed
  created_at: string;
};

/** GET /api/chats/{id}, POST /api/chats and POST /api/chats/{id}/messages. */
export type ChatThread = { chat: Chat; messages: ChatMessage[] };
