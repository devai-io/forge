# Security

Forge can open shells and editors on your machines and stores signing keys.
Treat an instance like SSH access to every machine that runs its agent.

## Reporting a vulnerability

Please use GitHub's private vulnerability reporting (Security → Report a
vulnerability) on this repository. Do not open a public issue.

## Running it safely

- Serve it over HTTPS only (reverse proxy), set `PUBLIC_URL` to the https URL,
  and enable two-factor authentication right after the first sign-in.
- Keep metrics, Nomad and VS Code gateways on a private network; the server
  only needs to be reachable by browsers and agents.
- Back up `vault.key` (in the workspace, `FORGE_HOME`) separately from the
  database backups in `backups/` — together they decrypt the vault.
- Keep the workspace folder private (Forge creates it `0700`, its files
  `0600`).
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
  local configuration (`~/.config/forge/agent.json`) allows.
- Machines join with a one-time pairing code (valid 15 minutes, stored
  hashed, rate-limited); the first account is created with a one-time setup
  token printed by the server. Neither is ever shown again.
- The Assistant (off unless configured) sends your chat, and what its tools
  read (projects, tasks, run results), to the LLM provider you configure. It
  queues runs with the same rules as the web app and cannot use
  `bypassPermissions` or confirm-only commands.
- Project files are always served as downloads (`application/octet-stream`,
  sandboxed), never rendered in the app's origin.
