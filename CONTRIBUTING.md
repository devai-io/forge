# Contributing

Thanks for looking. Forge is small and opinionated; issues and pull requests
are welcome, and a short issue first saves work on bigger changes.

- Read [AGENTS.md](AGENTS.md) — the layout, the API contract rule and the
  storage conventions (it is also what AI coding agents read).
- `make ui build test web-test` must pass. CI runs the same, plus gofmt and
  gitleaks.
- Keep changes to `docs/API.md`, `internal/` and `web/src/api/types.ts` in
  the same pull request.
- Never commit real hostnames, IP addresses, e-mail addresses, tokens or
  personal data — use `example.com` and the documentation IP ranges
  (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`).
- Security issues: see [SECURITY.md](SECURITY.md), not a public issue.

By contributing you agree your contribution is licensed under the
[MIT License](LICENSE).
