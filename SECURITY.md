# Security

Forge can open shells and editors on your machines and stores signing keys.
Treat an instance like SSH access to every machine that runs its agent.

## Reporting a vulnerability

Please use GitHub's private vulnerability reporting (Security → Report a
vulnerability) on this repository. Do not open a public issue.

## Running it safely

- Serve it over HTTPS only (reverse proxy), set `PUBLIC_URL` to the https URL,
  and enable two-factor authentication right after the first sign-in.
- Keep the database, metrics and Nomad on a private network; the server only
  needs to be reachable by browsers and agents.
- Back up `vault.key` (in `FORGE_DATA_DIR`) separately from database backups.
- Leave `terminal`, `code` and permission modes beyond `plan`/`acceptEdits`
  off on machines that do not need them — they are opt-in per machine.
- Review Settings → Security (the security log) now and then.

## Design notes

- Session cookies are HttpOnly/Secure/SameSite=Lax and stored hashed; they
  slide for 30 days and expire 90 days after sign-in.
- Writes require a custom header and a matching Origin (CSRF); WebSockets
  check Origin.
- Sensitive actions (vault reveal/download/delete, terminals, VS Code,
  two-factor enrolment) need the password (and code) again within 10 minutes.
- Vault values and files: AES-256-GCM, key outside the database, each blob
  bound to its row.
- Agents authenticate with a token (stored hashed) and can only do what their
  local configuration allows.
