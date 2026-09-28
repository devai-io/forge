# Working on Forge (for contributors and AI coding agents)

- **One binary**: `cmd/forge` — `forge server`, `forge agent`, admin commands.
  The web app (`web/`) is embedded via `internal/webui` (`make ui` copies
  `web/dist` there).
- **The contract**: `docs/API.md` is the single source of truth between the
  server and the web app. Change both halves together.
- **Layout**: `internal/api` (HTTP, auth, CSRF, terminal/VS Code relays),
  `internal/store` (all SQL; models mirror the contract), `internal/db`
  (embedded migrations, run at startup), `internal/runner` (the agent),
  `internal/checkup`, `internal/monitoring`, `internal/vault`, `internal/auth`.
- **Docs page**: `web/src/features/docs/content.ts` describes schedules and
  thresholds — update it when you change them in `internal/`.
- **Tests**: `make test` (unit), `make test-db` with `TEST_DATABASE_URL`
  (database-backed API tests; that database is wiped), `make web-test`.
- **Never commit** real hostnames, IP addresses, e-mail addresses, tokens or
  personal project data. Use `example.com`, `192.0.2.0/24`, `198.51.100.0/24`,
  `203.0.113.0/24` and the demo seed (`internal/seed/demo.json`). CI runs
  gitleaks on every push.
