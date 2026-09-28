# Working on Forge (for contributors and AI coding agents)

- **One binary**: `cmd/forge` — `forge server`, `forge agent`, admin commands.
  The web app (`web/`) is embedded via `internal/webui` (`make ui` copies
  `web/dist` there).
- **The contract**: `docs/API.md` is the single source of truth between the
  server and the web app. Change both halves together.
- **Layout**: `internal/api` (HTTP, auth, CSRF, terminal/VS Code relays,
  setup, pairing, project files), `internal/store` (all SQL; models mirror
  the contract), `internal/db` (SQLite + embedded migrations, run at
  startup), `internal/config` (the server workspace, `config.json`),
  `internal/runner` (the agent, incl. `pair`/`install`/`setup-claude`),
  `internal/backup`, `internal/checkup`, `internal/monitoring`,
  `internal/vault`, `internal/auth`.
- **Storage**: one SQLite file via the pure-Go `modernc.org/sqlite` driver
  (no cgo — the binary cross-compiles). Store SQL uses `$1` placeholders and
  the conventions documented at the top of `internal/db/db.go`: timestamps
  are fixed-format UTC text (use `now()`, `ts_add(ts, seconds)`,
  `local_date(ts, zone)`), lists are JSON text (`json_each` instead of
  `ANY`). A schema change is a new file in `internal/db/migrations/`.
- **Docs page**: `web/src/features/docs/content.ts` describes schedules and
  thresholds — update it when you change them in `internal/`.
- **Tests**: `make test` (everything in Go, each test on a fresh SQLite file),
  `make web-test`. New SQL gets exercised in `internal/store/sqlite_test.go`
  or an API test.
- **Never commit** real hostnames, IP addresses, e-mail addresses, tokens or
  personal project data. Use `example.com`, `192.0.2.0/24`, `198.51.100.0/24`,
  `203.0.113.0/24` and the demo seed (`internal/seed/demo.json`). CI runs
  gitleaks on every push.
