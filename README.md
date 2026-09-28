# Forge

A self-hosted command center for one developer: your projects, repos, servers
and tasks in one place, with an agent on each of your machines that runs
Claude Code for you, attaches you to its tmux sessions from any browser, opens
VS Code in the browser, and checks every morning that everything still works.

One binary, one SQLite file, one folder. MIT licensed.

> **Status: early.** Single-user, used daily by its author, APIs may change.

## What it does

- **Projects & tasks** — a kanban board per project, focus list, streaks,
  activity feed, and a folder of files per project.
- **Machines (agents)** — `forge agent` runs on your laptop, desktop, build box
  or Mac. It dials out to the server (no inbound ports), reports the git and CI
  state of your repos, and runs what you queue: Claude Code sessions
  (`claude -p`, streamed live) or named commands you allowed in its settings.
  Adding one is a pairing code and one command.
- **Terminals** — attach to the agents' tmux sessions from the browser or a
  phone; peek and send keys to answer Claude without a full terminal.
- **Claude context** — every Claude Code session in a registered repo starts
  with that project's notes, open tasks and check-up actions, and can read and
  update tasks through the `forge` MCP server.
- **VS Code in the browser** — served from your always-on machine, embedded in
  the app, in your theme and accent colour.
- **Monitoring & daily check-up** — endpoint probes, TLS expiry, and (optional)
  VictoriaMetrics, Grafana alerts and Nomad jobs, turned into a daily action list.
- **Vault** — keys, keystores and service accounts, encrypted with a key kept
  outside the database; revealing needs your password again; everything audited.
- **Assistant (optional)** — a chat backed by any OpenAI-compatible model
  (DeepSeek by default) that reads and updates your projects and tasks and
  delegates real work to Claude Code on your machines, then reports back.
- **Token saving (optional)** — with a TypeSafe key, Jev picks cheaper models for
  light agent runs, trims the context Claude sessions start with, and compacts long
  Claude Code sessions on every machine.
- **Security** — password + optional TOTP, step-up confirmation for sensitive
  actions, security log, new-device sign-in e-mails.

The in-app **Docs** page explains exactly what is automated, what is read live,
and what is only configuration.

## Install

The short version — full steps (reverse proxy, backups, upgrades,
troubleshooting) are in **[INSTALL.md](INSTALL.md)**, written so an AI coding
agent can follow it too.

**Server** (Docker):

```bash
curl -fsSLO https://raw.githubusercontent.com/devai-io/forge/main/docker-compose.yml
docker compose up -d
docker compose logs forge        # open the setup link it prints, create your account
```

…or without Docker: install the binary (below) and run `forge server`.

**A machine** — on the Agents page choose **Add machine**, then run the command
it shows on that machine:

```bash
curl -fsSL https://github.com/devai-io/forge/releases/latest/download/install.sh \
  | sh -s -- --pair https://forge.example.com K7QD-M3XP
```

That installs `forge`, pairs the machine, runs the agent in the background
(systemd user unit / launchd) and connects Claude Code.

## The workspace folder

Everything Forge keeps is in one folder, on the server and on each machine:

| Server — `FORGE_HOME` (default `~/.config/forge`, `/data` in Docker) | |
|---|---|
| `config.json` | settings (environment variables override it) |
| `forge.db` | the SQLite database |
| `vault.key` | the vault master key — **back it up separately** |
| `backups/` | nightly database snapshots (14 kept) |
| `projects/<KEY>/` | each project's files |

| Machine — `~/.config/forge` | |
|---|---|
| `agent.json` | this machine's settings and token ([example](deploy/agent.example.json)) |
| `workspaces/`, `vscode/` | VS Code workspace files and server data |
| `logs/` | the background service's log (macOS) |

What a machine may do is decided **in its own `agent.json`**: allowed
directories, Claude permission modes, named commands (optionally repo-scoped
and confirm-only), terminals, VS Code. Mark your always-on machine as
**master** on the Agents page: it is the source of repo state and hosts VS Code.

## Build from source

Requirements: Go 1.26+, Node 22+.

```bash
make ui build          # web app + bin/forge with the UI embedded
make run               # server on :8080, workspace in ./.forge-dev
make test web-test     # Go (SQLite, nothing to set up) and web tests
make dist              # release archives for Linux/macOS × amd64/arm64
```

Layout: `cmd/forge` (the one binary), `internal/` (server, agent, store),
`web/` (React app), `docs/API.md` (the HTTP contract). See
[CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md). Please report vulnerabilities privately.

## License

[MIT](LICENSE)
