// The Documentation page's content: what Forge automates, what it reads live,
// and what is only configuration somebody typed in. Kept as data so the page
// can render it consistently and the facts strip can overlay live numbers
// from GET /api/system. Every interval and threshold here mirrors the server
// (internal/monitor, internal/checkup, internal/runner) — change both together.
// The same goes for the workspace layout and the backup schedule below.

export type Tier = "automated" | "live" | "stored" | "on-demand";

export const TIERS: Record<Tier, { label: string; blurb: string }> = {
  automated: { label: "Automated", blurb: "Runs on its own on a schedule or an event and writes results into Forge." },
  live: { label: "Live", blurb: "Read from an outside system when you open the page; nothing is stored." },
  "on-demand": { label: "On demand", blurb: "Happens only when you (or Claude through the MCP server) ask for it." },
  stored: { label: "Stored", blurb: "Configuration or notes that were typed in — by you, an import, or an AI agent. Changes only when edited." },
};

export type Source = {
  name: string;
  tier: Tier;
  who: string; // which process does it
  when: string; // schedule / trigger
  what: string; // what it reads and writes
  factKey?: string; // key into SystemFacts.intervals for the live value
};

export const SOURCES: Source[] = [
  { name: "Endpoint monitor", tier: "automated", who: "the Forge server", when: "Every 2 minutes (and when you press ⟳ on an endpoint)", factKey: "endpoint_check",
    what: "GET on every enabled endpoint URL, 10 s timeout, redirects are not followed. Up = the status code you expect. Each probe is kept 7 days (the 24 h uptime figure, latency and the check strips come from them). A change between up and down lands in the activity feed." },
  { name: "Repository scan", tier: "automated", who: "the agent on the master machine", when: "Every 5 minutes, and right after an agent run finishes", factKey: "repo_scan",
    what: "`git status --porcelain --branch` and `git log` in every registered repo path that exists on that machine: branch, changed and untracked files, ahead/behind the last fetch, last commit, commits in the last 7 days. Only the master's view is kept; other machines are told not to scan." },
  { name: "Repository sync", tier: "automated", who: "the agent on the master machine, with that machine's own git credentials", when: "Every 30 minutes, before the repo scan (`pull_interval` in its agent.json; \"0\" turns it off)", factKey: "repo_sync",
    what: "`git fetch --prune` in every registered repo, then `git merge --ff-only` from the branch's upstream — only when nothing can be lost: no uncommitted tracked changes, no unpushed local commits, no merge or rebase in progress, no agent run working there. Otherwise it only fetches (so \"behind\" is current) and says why it did not pull. Pulled commits land in the activity feed; a failing fetch shows up in the check-up." },
  { name: "Jev decisions (token saving)", tier: "on-demand", who: "the Forge server, with the TypeSafe key from the vault", when: "Only when Settings → Token saving is on: as an agent run is queued, and as a Claude session starts (cached 30 minutes)",
    what: "Short typed questions to TypeSafe's Jev model instead of Claude: whether a run queued without a model is light or ordinary enough for Haiku or Sonnet (only above 70% confidence; otherwise the machine's default), and which open tasks matter for the repo a session opened in (with more than 8 open). Any error or unsure answer changes nothing. With compaction on, every machine's Claude Code also gets the pinned fast-jev-compaction plugin and the key." },
  { name: "CI status", tier: "automated", who: "the agent on the master, using that machine's own `gh` login", when: "With the repo scan, at most every 15 minutes per repo", factKey: "ci_status",
    what: "The latest GitHub Actions run on each repo's default branch (workflow, conclusion, link). Forge never holds a GitHub token." },
  { name: "Runner heartbeat", tier: "automated", who: "the agent on every machine", when: "Every 10 seconds", factKey: "runner_heartbeat",
    what: "Says the machine is alive, what it can do (Claude, permission modes, commands, terminals, VS Code) and — when terminals are on — the tmux sessions and windows it currently has. A runner silent for 90 s shows offline; a run it was executing is failed after 10 minutes of silence." },
  { name: "Daily check-up", tier: "automated", who: "the Forge server", when: "Once a day at your check-up time (Settings), and whenever you press Run now", factKey: "checkup_time",
    what: "Runs every check listed below and stores the result as the day's action list. E-mails you the summary when SMTP is configured and the toggle is on." },
  { name: "Monitoring page", tier: "live", who: "the Forge server, over your private network", when: "When the page is open, refreshed every 30 s (cached for 30 s server-side)", factKey: "monitoring_cache",
    what: "When configured: host CPU/memory/disk/load from VictoriaMetrics (vector's `host_*` metrics), blackbox probes with certificate expiry, every Nomad host's job list (anonymous read), and Grafana's firing alerts and dashboard list when a Grafana token is in the vault. Every source is optional. Nothing on this page is stored by Forge." },
  { name: "Grafana alerts", tier: "live", who: "the Forge server, with the vault item tagged integration:grafana", when: "With the Monitoring page and the check-up",
    what: "Rule states from Grafana's alerting API. Without a token, alerts are not read." },
  { name: "Activity feed", tier: "automated", who: "the Forge server", when: "On the event",
    what: "Written when a task is created, moved, completed or commented, a project changes, a run is queued or finishes, an endpoint goes down or comes back." },
  { name: "Claude context", tier: "on-demand", who: "the agent (`forge agent context --hook` and `forge agent mcp`) with the machine's token", when: "At the start of every Claude Code session in a registered repo, and whenever Claude calls a forge_* tool",
    what: "Injects the project's summary, infra and change-control notes, repo state, open tasks and open check-up actions. The MCP tools let Claude list, create, update and comment on tasks and read the check-up — nothing else." },
  { name: "Agent runs and commands", tier: "on-demand", who: "the agent on the machine you pick", when: "When you queue one",
    what: "Claude Code (`claude -p`, streamed back as a transcript) or a named command from that machine's `~/.config/forge/agent.json`, inside a repo directory. What a machine may do is decided in its own `agent.json`." },
  { name: "Terminals and VS Code", tier: "on-demand", who: "the agent on the master (terminals on every machine that allows them)", when: "When you attach or open",
    what: "tmux sessions attached through a relay in the server; VS Code's own web build served from the master. Terminals need no inbound connection — the agents dial out; the VS Code gateway must be reachable from the server over a private network." },
  { name: "Database backup", tier: "automated", who: "the Forge server", when: "Every night",
    what: "A consistent snapshot of `forge.db` into `backups/` in the Forge workspace; the newest 14 are kept, older ones deleted. `vault.key` is not part of it — back that file up separately." },
  { name: "E-mail", tier: "automated", who: "the Forge server, through your SMTP account", when: "Password-reset links, the daily check-up, sign-ins from a new device",
    what: "Sent only when SMTP is configured." },
];

export type Check = { category: string; rule: string; severity: string; source: string };

export const CHECKS: Check[] = [
  { category: "Endpoints", rule: "An endpoint is down at the last probe", severity: "fail", source: "endpoint monitor" },
  { category: "Endpoints", rule: "Uptime over the last 24 h below 98 %, or last latency above 3 s", severity: "warn", source: "endpoint monitor" },
  { category: "TLS", rule: "Certificate of any https endpoint host expires in under 7 days (own TLS handshake at check time)", severity: "fail", source: "check-up itself" },
  { category: "TLS", rule: "…in under 21 days (ACME clients usually renew at 30, so this means renewal is not happening)", severity: "warn", source: "check-up itself" },
  { category: "Hosts", rule: "A host has sent no metrics for 3 minutes", severity: "fail", source: "VictoriaMetrics" },
  { category: "Hosts", rule: "Root disk ≥ 90 %", severity: "fail", source: "VictoriaMetrics" },
  { category: "Hosts", rule: "Root disk ≥ 80 %, memory ≥ 90 %, or CPU ≥ 90 % over 5 minutes", severity: "warn", source: "VictoriaMetrics" },
  { category: "Nomad", rule: "A service/system job is dead or has no running allocation; Nomad itself unreachable", severity: "fail", source: "each host's Nomad API" },
  { category: "Nomad", rule: "A job has allocations waiting for placement", severity: "warn", source: "each host's Nomad API" },
  { category: "Alerts", rule: "A Grafana rule is firing with severity critical (or none)", severity: "fail", source: "Grafana (needs the token)" },
  { category: "Alerts", rule: "Any other firing or pending rule; Grafana not connected", severity: "warn", source: "Grafana" },
  { category: "Backups", rule: "A monitored host's last successful database backup is older than 26 h (only when the `pgbackup_*` metrics exist)", severity: "fail", source: "VictoriaMetrics" },
  { category: "Runners", rule: "The master machine has been offline for more than 10 minutes; or no master is elected", severity: "fail / warn", source: "heartbeats" },
  { category: "Repos", rule: "Uncommitted tracked changes, unpushed commits, behind origin (with why it was not pulled), a failed automatic pull, or a git error", severity: "warn", source: "master's repo scan" },
  { category: "CI", rule: "The latest run on a repo's default branch failed", severity: "fail", source: "master's `gh`" },
  { category: "Tasks", rule: "Overdue, due today, or blocked for more than 3 days", severity: "warn", source: "Forge tasks" },
  { category: "Vault", rule: "A credential expired; expires within 30 days", severity: "fail / warn", source: "vault expiry dates you entered" },
  { category: "Security", rule: "Two-factor is off; a password Forge generated (on the command line) was never changed; sign-ins from new devices in the last 7 days", severity: "warn", source: "Forge's own security log" },
];

export const STORED: { name: string; origin: string; note: string }[] = [
  { name: "Projects", origin: "Created in the app, imported with `forge import`, or loaded as demo data", note: "Key, status, priority, colour, summary, description, infra & change-control notes, links, target date. Forge never rewrites these — edit them on the project page. The Claude context injects them verbatim." },
  { name: "Repositories", origin: "Same as projects", note: "Name, local path (on the master machine), remote, kind, deploy notes. Only the git/CI state next to them is automated." },
  { name: "Servers", origin: "Same as projects", note: "Role, provider, addresses, environment, critical flag, tags, notes. Forge does not SSH anywhere and does not discover hosts: the cards are documentation you keep, while the Monitoring page shows the live numbers for the same names." },
  { name: "Endpoints", origin: "Same as projects", note: "The URL list and the expected status are yours; the probing is automated." },
  { name: "Tasks", origin: "Created by you, imported, turned into tasks from the check-up, or created by Claude through the MCP server", note: "Nothing completes a task automatically." },
  { name: "Vault items", origin: "Added in the app or with `forge import`", note: "An item can hold values and a file, or be a reference only (where the original lives) — useful for secrets that belong in another secret manager. Expiry dates are what you enter." },
  { name: "Grafana dashboards", origin: "Read from Grafana", note: "Listed once a Grafana token is connected." },
  { name: "Project files", origin: "Uploaded on a project's Files tab", note: "Kept as they are in `projects/<KEY>/` in the Forge workspace, up to 100 MB each. Forge does not read or index them." },
  { name: "Machine commands, permission modes, allowed roots, terminal and VS Code settings", origin: "Each machine's `~/.config/forge/agent.json`", note: "Pairing writes the address and token; the rest is edited on the machine. Reported at every heartbeat; Forge only chooses among what a machine advertises." },
  { name: "Appearance", origin: "Settings → Appearance", note: "Light, dark or system is per browser. The accent colour is saved to your account, so every browser — and the embedded VS Code — follows it. Chart and status colours never change with it." },
];

export const SECURITY: { title: string; body: string }[] = [
  { title: "Jev key", body: "The TypeSafe key is sealed in the vault (tag integration:jev) and only read by the server — and, while compaction is on, handed to your paired machines for their Claude Code. Jev receives run prompts (routing), task titles (context trimming) and, through the plugin, Claude Code transcripts (compaction)." },
  { title: "Who can get in", body: "One account. Password (bcrypt) plus, when enabled, a 6-digit authenticator code that can be used once. Wrong passwords are limited to 8 per address and 30 overall per 15 minutes; a sign-in from a device Forge has not seen e-mails you." },
  { title: "Sessions", body: "An HttpOnly, Secure cookie whose value is only stored hashed. It slides for 30 days of use and ends 90 days after sign-in regardless. Every session is listed under Settings with its address and browser, and can be revoked; changing the password revokes the others." },
  { title: "Step-up for the dangerous things", body: "Revealing or downloading a credential, deleting one, attaching a terminal, sending keys, creating a session, opening VS Code, and enrolling two-factor all need the password (and code) again within the last 10 minutes on that session." },
  { title: "Cross-site protection", body: "Every write must carry a header a foreign site cannot add, and the browser's Origin must be the app's own; the terminal and VS Code WebSockets check the Origin too. The app's Content-Security-Policy allows only its own scripts." },
  { title: "Vault", body: "Secret values and files are sealed with AES-256-GCM under a key kept outside the database (`vault.key` in the Forge workspace, or one you provide) — the database and its nightly snapshots hold ciphertext. Lists never return values; every reveal, download, edit and delete is in the audit log." },
  { title: "Machines", body: "A machine gets its token by pairing: a one-time code, valid 15 minutes, that only the Agents page hands out; only its hash is stored. The token only lets it take runs, report repos and sessions, provide terminals, read project context and write tasks. Machines never accept connections. Store uploads and deploys are commands that must be confirmed by name; permission modes above plan/acceptEdits exist only if written into that machine's `agent.json` by hand." },
  { title: "Network", body: "Put Forge behind a TLS reverse proxy and keep Nomad and metrics on a private network. VS Code runs on the master and is reachable only through Forge, over your private network, with a per-open secret." },
  { title: "The setup token", body: "Until the one account exists, the setup page needs the setup token the server prints in its log (or `forge setup-token` prints). Once the account is created, setup is closed for good." },
  { title: "Recovery, from the server", body: "On the server: `forge reset-password <user>` · `forge disable-2fa <user>` · `forge create-runner <name>` (e.g. through `docker compose exec forge …`). Container logs and the security log keep the record." },
];

export const NOT_AUTOMATED: string[] = [
  "Nothing on a server is changed by Forge itself: no deploys, restarts, rotations or SSH. It observes, and it runs what you queue through an agent on your own machines.",
  "Server records, project notes and vault expiry dates do not update themselves — the check-up warns from what you entered.",
  "Tasks are never completed automatically, including the ones created from a check-up item.",
  "Machines sleep; anything that needs one (e.g. iOS builds on a Mac) waits until it wakes. The daily check-up ignores sleeping non-master machines on purpose.",
  "Grafana alerts appear only once a Viewer service-account token is stored in the vault.",
];

// ── Setup and storage ──────────────────────────────────────────────────────

export const SETUP: { title: string; body: string }[] = [
  { title: "First run", body: "A new server has no account. On first start it prints a setup link — `<public URL>/setup?token=…` — in its log; `forge setup-token` on the server prints it again (Docker: `docker compose exec forge forge setup-token`). The setup page creates the one account, optionally with demo data to explore, and signs you in." },
  { title: "Adding a machine", body: "Agents → Add machine gives a one-time pairing code (like `K7QD-M3XP`, valid 15 minutes). On a machine without Forge, one command installs `forge` into `~/.local/bin`, pairs, installs the background service (a systemd user unit on Linux, launchd on macOS) and connects Claude Code: `curl -fsSL …/install.sh | sh -s -- --pair <url> <code>`. With Forge already there: `forge agent pair <url> <code>`, then `forge agent install`, and optionally `forge agent setup-claude`. Pair again gives a reinstalled or moved machine a new code; its old token keeps working until that code is used." },
  { title: "Backups", body: "Every night the server snapshots `forge.db` into `backups/` and keeps the newest 14. Copy that folder off the machine now and then. `vault.key` is not in it — back it up separately: without it the vault cannot be decrypted." },
  { title: "Accent colour", body: "Twelve presets or any colour you pick, under Settings → Appearance. It is saved to your account and passed on to the embedded VS Code; text on it is chosen for contrast." },
];

export type WorkspaceDir = { where: string; root: string; entries: { path: string; what: string }[] };

export const WORKSPACE: WorkspaceDir[] = [
  {
    where: "Server",
    root: "`FORGE_HOME` — default `~/.config/forge`, `/data` in Docker",
    entries: [
      { path: "config.json", what: "Server settings" },
      { path: "forge.db", what: "The database: one SQLite file holds everything" },
      { path: "vault.key", what: "The vault master key — keep a copy somewhere else" },
      { path: "backups/", what: "Nightly database snapshots, the newest 14 kept" },
      { path: "projects/<KEY>/", what: "Each project's files" },
    ],
  },
  {
    where: "Each machine",
    root: "`~/.config/forge`",
    entries: [
      { path: "agent.json", what: "Machine settings and its token: commands, permission modes, allowed roots, terminals, VS Code" },
      { path: "workspaces/", what: "VS Code workspace files for projects" },
      { path: "vscode/", what: "VS Code server data" },
      { path: "logs/", what: "The agent's logs" },
    ],
  },
];
