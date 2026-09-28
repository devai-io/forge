# Forge

A self-hosted command center for one developer: your projects, repos, servers
and tasks in one place, with an agent on each of your machines that runs
Claude Code for you, attaches you to its tmux sessions from any browser, opens
VS Code in the browser, and checks every morning that everything still works.

> **Status: early.** Single-user, used daily by its author, APIs may change.

## What it does

- **Projects & tasks** — a kanban board per project, focus list, streaks, activity feed.
- **Machines (agents)** — `forge agent` runs on your laptop, desktop, build box or Mac.
  It dials out to the server (no inbound ports), reports git and CI state of your
  repos, and runs what you queue: Claude Code sessions (`claude -p`, streamed live)
  or named commands you whitelisted in its config.
- **Terminals** — attach to the agents' tmux sessions from the browser or a phone;
  peek and send keys to answer Claude without a full terminal.
- **Claude context** — every Claude Code session in a registered repo starts with
  that project's notes, open tasks and check-up actions, and can read and update
  tasks through the `forge` MCP server.
- **VS Code in the browser** — served from your always-on machine, embedded in the app.
- **Monitoring & daily check-up** — endpoint probes, TLS expiry, and (optional)
  VictoriaMetrics, Grafana alerts and Nomad jobs, turned into a daily action list.
- **Vault** — keys, keystores and service accounts, encrypted with a key kept
  outside the database; revealing needs your password again; everything audited.
- **Security** — password + optional TOTP, step-up confirmation for sensitive
  actions, security log, new-device sign-in e-mails.

The in-app **Docs** page explains exactly what is automated, what is read live,
and what is only configuration.

## Quick start (Docker)

```bash
git clone https://github.com/devai-io/forge && cd forge
cp .env.example .env            # set POSTGRES_PASSWORD; FORGE_DEMO_DATA=true for sample projects
docker compose up -d --build
docker compose exec forge /forge create-user admin you@example.com   # prints a password
open http://localhost:8080
```

The server listens on `127.0.0.1:8080`. To reach it from elsewhere, put a TLS
reverse proxy in front, set `PUBLIC_URL=https://your.domain`, and turn on
two-factor under Settings. **Back up the `data` volume** — it holds
`vault.key`, without which the vault cannot be decrypted.

## Add a machine (agent)

1. In Forge: **Agents → Add runner**, name it, copy the token (shown once).
2. On the machine: install the `forge` binary (build it with `make build`), then
   ```bash
   install -Dm600 deploy/runner.example.json ~/.config/forge/runner.json   # set api_url + token
   forge agent                                                             # or install the service:
   ```
   Linux: `deploy/forge-agent.service` (systemd user unit) · macOS: `deploy/dev.forge.agent.plist`.
3. Optional Claude Code integration on that machine:
   ```bash
   claude mcp add --scope user forge -- ~/.local/bin/forge agent mcp
   ```
   and a SessionStart hook running `~/.local/bin/forge agent context --hook`.

What a machine may do is decided **in its own `runner.json`**: allowed
directories, Claude permission modes, named commands (optionally repo-scoped
and confirm-only), terminals, VS Code. Mark your always-on machine as
**master** on the Agents page: it is the source of repo state and hosts VS Code.

## Configuration

Server: environment variables — see [`.env.example`](.env.example).
Agent: `~/.config/forge/runner.json` — see [`deploy/runner.example.json`](deploy/runner.example.json).

## Build from source

Requirements: Go 1.26+, Node 22+, Postgres 15+.

```bash
make ui build          # web app + bin/forge with the UI embedded
make dev-db run        # local Postgres + server on :8080
make test web-test     # tests (make test-db for the database-backed ones)
```

Layout: `cmd/forge` (the one binary), `internal/` (server, agent, store),
`web/` (React app), `docs/API.md` (the HTTP contract), `deploy/` (service files).

## Security

See [SECURITY.md](SECURITY.md). Please report vulnerabilities privately.
