# Installing Forge

This guide is written to be followed step by step — by you, or by an AI coding
agent acting for you. Every step says how to check that it worked. Replace
`forge.example.com` with your own address throughout.

Forge has two parts, both in the same `forge` binary:

- **the server** — the web app, the API and the database (one SQLite file).
  Run it once, somewhere always on (a small VPS, a home server, a NAS).
- **the agent** — runs on each machine whose repos, terminals and Claude Code
  sessions you want in Forge (your desktop, laptop, a build box, a Mac). It
  only makes outbound connections to the server.

---

## 1. The server

### 1a. With Docker (recommended)

Requirements: Docker with the compose plugin.

```bash
mkdir -p ~/forge && cd ~/forge
curl -fsSLO https://raw.githubusercontent.com/devai-io/forge/main/docker-compose.yml
# The address people will open (set it now if you will use a domain — see 1c):
echo 'PUBLIC_URL=https://forge.example.com' > .env
docker compose up -d
```

Check: `curl -s http://127.0.0.1:8080/api/status` prints
`{"db":"ok","status":"ok","version":"…"}`.

Everything the server keeps is in the `data` volume, mounted at `/data`
(the workspace — see [section 4](#4-the-workspace-backups-and-restore)).

### 1b. Without Docker

Requirements: Linux or macOS, amd64 or arm64.

```bash
curl -fsSL https://github.com/devai-io/forge/releases/latest/download/install.sh | sh
forge server        # workspace: ~/.config/forge (set FORGE_HOME to change it)
```

Check: as in 1a.

To run it as a system service on Linux, create a user and a unit:

```bash
sudo useradd --system --home /var/lib/forge --create-home forge
sudo install -m 755 ~/.local/bin/forge /usr/local/bin/forge
sudo tee /etc/systemd/system/forge.service >/dev/null <<'UNIT'
[Unit]
Description=Forge server
After=network-online.target
Wants=network-online.target

[Service]
User=forge
Environment=FORGE_HOME=/var/lib/forge
Environment=PUBLIC_URL=https://forge.example.com
Environment=LISTEN_ADDR=127.0.0.1:8080
ExecStart=/usr/local/bin/forge server
Restart=always
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/forge
PrivateTmp=true

[Install]
WantedBy=multi-user.target
UNIT
sudo systemctl daemon-reload && sudo systemctl enable --now forge
```

Check: `systemctl status forge` is `active (running)`, and the status URL
answers as in 1a.

### 1c. HTTPS and a domain

The server listens on `127.0.0.1:8080`. To use it from other devices, put a
TLS reverse proxy in front and set `PUBLIC_URL` to the https address (it
decides secure cookies and the one origin allowed to make changes — a
mismatch shows up as `cross-origin request refused`).

Caddy (automatic certificates, WebSockets included):

```caddyfile
forge.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

nginx:

```nginx
server {
    server_name forge.example.com;
    listen 443 ssl;  # plus your certificate lines
    client_max_body_size 110m;               # project file uploads (100 MB)
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;  # terminals and VS Code
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;             # VS Code needs the real host
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_read_timeout 1h;
    }
}
```

If the proxy does not connect from the same host (127.0.0.1), list its address
in `TRUSTED_PROXIES` so sign-in rate limits and the security log see real
client addresses.

Check: `curl -s https://forge.example.com/api/status` answers.

### 1d. Create your account

On first start the server prints a one-time setup link:

```bash
docker compose logs forge | grep -A2 "Create your account"    # Docker
journalctl -u forge | grep setup                               # systemd
forge setup-token                                              # prints it again
docker compose exec forge forge setup-token                    # …in Docker
```

Open it, choose a username and password (12+ characters), your time zone,
and whether to load demo data. Then, in **Settings**, turn on **two-factor
authentication** — Forge can open shells on your machines.

Check: you land on the dashboard, signed in.

(Without a browser: `forge create-user <username> <email>` prints a generated
password; the daily check-up reminds you to change it.)

---

## 2. Machines (agents)

Requirements on each machine: Linux or macOS, `git`; for agent runs,
[Claude Code](https://docs.anthropic.com/en/docs/claude-code) signed in
(`claude` on PATH); for terminals, `tmux`; for CI status, `gh` signed in; for
VS Code in the browser (master only), the `code` CLI.

### 2a. Pair it

1. In Forge: **Agents → Add machine**. Name it (e.g. `desk`), pick its role —
   **master** for the always-on machine that hosts VS Code and reports repo
   state, **ios** for a Mac that does Xcode builds, **worker** otherwise.
2. Run the command the dialog shows, on that machine:

   ```bash
   curl -fsSL https://github.com/devai-io/forge/releases/latest/download/install.sh \
     | sh -s -- --pair https://forge.example.com K7QD-M3XP
   ```

   This installs `forge` into `~/.local/bin`, trades the code (valid 15
   minutes, once) for the machine's token, writes `~/.config/forge/agent.json`,
   installs the background service and connects Claude Code.

   If `forge` is already installed, the same in three commands:

   ```bash
   forge agent pair https://forge.example.com K7QD-M3XP
   forge agent install        # systemd user unit (Linux) / launchd agent (macOS)
   forge agent setup-claude   # MCP server + SessionStart hook for Claude Code
   ```

Check: the dialog turns to "connected" within ~10 seconds; on the machine,
`systemctl --user status forge-agent` (Linux) or
`tail ~/.config/forge/logs/agent.log` (macOS) shows it running.

On Linux, to keep the agent running while you are logged out:
`sudo loginctl enable-linger $USER`.

No browser handy? On the server: `forge add-machine desk master` prints a
code and the command.

### 2b. Decide what it may do

Everything a machine accepts is in **its own** `~/.config/forge/agent.json`, never
on the server. Edit it, then `forge agent install` (restarts the service).
Full reference: [`deploy/agent.example.json`](deploy/agent.example.json).

| Key | Default | Meaning |
|---|---|---|
| `allowed_roots` | the existing ones of `~/dev`, `~/code`, `~/src`, `~/projects`, `~/work` (else `~/dev`) | runs only happen inside these folders |
| `permission_modes` | `["plan", "acceptEdits"]` | Claude Code modes the UI may choose; add `bypassPermissions` only by hand |
| `commands` | a few git commands | named shell commands: `"name": "cmd"` or `{"run", "description", "repos": [...], "confirm": true}` |
| `terminal` | `false` | let Forge attach to this machine's tmux sessions |
| `code` | off | `{"enabled": true, "listen": "10.0.0.5:7422"}` — VS Code in the browser; `listen` must be an address the **server** can reach (a private network/VPN), never a public one |
| `path_map` | `{}` | where repos registered with other machines' paths live here, e.g. `{"/home/me/": "/Users/me/"}` |
| `max_concurrent`, `max_run_minutes` | 2, 120 | run limits |
| `ci_interval` | `15m` | how often GitHub Actions status is read with `gh` (`0` = off) |
| `pull_interval` | `30m` | master only: fetch every repo, fast-forward the ones with no local changes or unpushed commits (`0` = off). Uses this machine's git credentials — SSH keys must work without a prompt (no passphrase, or an agent the service can reach) |

### 2c. Register your repos

In Forge, create a project and add its repos with their **path on the master**
(e.g. `/home/you/dev/shop/shop_api`). The master reports each repo's branch,
changes, ahead/behind and CI; runs and terminals open there.

Check: the project page shows git state for each repo within 5 minutes.

### 2d. Optional: the Assistant

A chat in Forge that answers from your projects, tasks and machines and
delegates work to Claude Code (it queues runs exactly like the New run dialog,
in `plan` or `acceptEdits` mode — never `bypassPermissions`; commands that need
confirmation are left to you). It needs an OpenAI-compatible API key —
[DeepSeek](https://platform.deepseek.com) by default. **Settings → Assistant**,
or on the server:

```bash
forge assistant set-key < key.txt            # stored sealed in the vault
forge assistant model deepseek-flash          # optional: model [base-url]
forge assistant on
```

### 2e. Agent engine: DeepSeek by default

Agent runs start Claude Code on a machine. By default it talks to
[DeepSeek](https://platform.deepseek.com)'s Anthropic-compatible API
(`deepseek-flash`; `deepseek-v4-pro` when Jev judges a task heavy) instead of
Anthropic. Claude is used for a run only when you pick it in the New run dialog
(or ask the Assistant for Claude), or when there is no DeepSeek key (the run
says so). **Settings → Agent engine**, or on the server:

```bash
forge engine set-key < key.txt   # sealed in the vault (tag integration:deepseek)
forge engine status              # the Assistant's key is used when it points at DeepSeek
forge engine claude              # make Claude the default instead (forge engine deepseek to undo)
```

The machine receives the key with each DeepSeek run and sets
`ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN` for that `claude` process only (any
Anthropic credentials in its environment are dropped for it). Machines need
Forge ≥ the release that added this; older agents fail DeepSeek runs.

### 2f. Optional: token saving with Jev

With a [TypeSafe](https://jevtypesafeai.com) API key, Forge uses Jev (a small
decision model) instead of Claude for routine judgements: **Settings → Token
saving**, paste the key, switch on. Or on the server:

```bash
forge jev set-key < key.txt     # stored sealed in the vault
forge jev on
forge jev status
```

With "compaction" on, each machine's agent installs the pinned
`fast-jev-compaction` Claude Code plugin within a heartbeat or two. Opt a
machine out with `"claude_jev": false` in its `agent.json`.

---

## 3. Configuration reference (server)

Settings live in `<workspace>/config.json` (created with defaults on first
start); an environment variable with the same meaning wins.

| config.json | Environment | Default | |
|---|---|---|---|
| `public_url` | `PUBLIC_URL` | `http://localhost:8080` | the address people open |
| `listen_addr` | `LISTEN_ADDR` / `PORT` | `0.0.0.0:8080` | |
| `timezone` | `FORGE_TIMEZONE` | `UTC` | for new accounts |
| `demo_data` | `FORGE_DEMO_DATA` | `false` | sample projects in an empty database |
| `backup_keep` | `FORGE_BACKUP_KEEP` | `14` | nightly snapshots kept (`0` = off) |
| `trusted_proxies` | `TRUSTED_PROXIES` (comma-separated) | — | proxies allowed to set X-Forwarded-For |
| `smtp.host`, `.port`, `.user`, `.password` / `.password_file`, `.from` | `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASSWORD` / `SMTP_PASSWORD_FILE`, `SMTP_FROM` | off | password resets, daily check-up, new-device alerts |
| `monitoring.victoriametrics_url`, `.grafana_url`, `.grafana_public_url` | `VICTORIAMETRICS_URL`, `GRAFANA_URL`, `GRAFANA_PUBLIC_URL` | off | optional fleet monitoring |
| — | `FORGE_HOME` | `~/.config/forge` (`/data` in Docker) | the workspace |
| — | `VAULT_KEY_FILE` | `<workspace>/vault.key` | bring your own vault key |

---

## 4. The workspace, backups and restore

```
<FORGE_HOME>/
  config.json        settings
  forge.db           the database (forge.db-wal / -shm while running)
  vault.key          the vault master key
  setup-token        only until the first account exists
  backups/           forge-YYYY-MM-DD.db, one a day, 14 kept
  projects/<KEY>/    project files
```

- **Back up `vault.key` separately** (a password manager is fine). Without it
  the vault's contents cannot be decrypted — database backups alone are not
  enough, and that is on purpose.
- The server writes a consistent snapshot to `backups/` every day;
  `forge backup` writes one now. Copy `backups/` and `projects/` off the
  machine with whatever you already use (restic, borg, rsync).
- **Restore:** stop the server, copy a snapshot over `forge.db` (and remove
  any `forge.db-wal` / `forge.db-shm`), put `vault.key` back, start it.

---

## 5. Upgrading

- Docker: `docker compose pull && docker compose up -d`.
- Binary: run the install script again (the server: then restart it).
- Machines: run the install script again without `--pair`, then
  `forge agent install` to restart the agent.

**From v0.1.x:** the default folder moved from `~/.forge` to
`~/.config/forge`. Nothing breaks — Forge keeps using `~/.forge` while that
is where the database (server) or `agent.json` (machine) is. To move, stop
the server or agent, `mv ~/.forge ~/.config/forge`, then start it again
(machines: `forge agent install`, which also rewrites the service).

Database migrations run automatically at start. Check: the status URL shows
the new version; each machine's version is on the Agents page.

---

## 6. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `cross-origin request refused` on every change | `PUBLIC_URL` differs from the address in the browser (scheme, host or port) |
| Signed out right after signing in | serving over plain http while `PUBLIC_URL` is https (secure cookie), or the other way round |
| Terminal / VS Code never connect behind nginx | missing `Upgrade`/`Connection` headers or a short `proxy_read_timeout` |
| `unknown or expired pairing code` | codes last 15 minutes and work once — **Pair again** on the Agents page |
| Machine shows "never connected" | the agent is not running (`systemctl --user status forge-agent`), or it cannot reach `api_url` in `~/.config/forge/agent.json` |
| Runs fail with "outside allowed roots" | add the repo's folder to `allowed_roots` in that machine's `agent.json` |
| "VS Code is not enabled on …" | set `code.enabled` + `code.listen` on the master and install the `code` CLI |
| Lost the password | `forge reset-password <username>` on the server (Docker: `docker compose exec forge forge reset-password <username>`) |
| Lost the authenticator | `forge disable-2fa <username>` |

---

## 7. Checklist (for AI agents doing this for someone)

1. Ask for: the domain (or "local only"), the server host, which machines to
   add and which one is always on (master).
2. Server: 1a or 1b, then 1c if there is a domain. Verify
   `curl -s <PUBLIC_URL>/api/status` → `"status":"ok"`.
3. Give the person the setup link (`forge setup-token`) — they create the
   account themselves; do not choose their password.
4. Remind them to turn on two-factor and to store `vault.key` somewhere safe.
5. For each machine: they create the pairing code on the Agents page; you run
   the install command on that machine. Verify it shows as connected.
6. On the master: set `"terminal": true` if they want terminals, and `code`
   if they want VS Code (needs a private address the server reaches). Run
   `forge agent install` after editing.
7. Never paste tokens, pairing codes or `vault.key` into chats, issues or
   commits.
