// Agent-run helpers shared by the agents screens.

import type { CommandDetail, Engine, Pairing, Run, Runner, RunnerCapabilities, RunnerRole } from "@/api/types";

// Mirrors deploy/runner.example.json, which is the machine's ~/.config/forge/agent.json:
// the keys an agent needs to start (api_url, token, allowed_roots) plus safe
// defaults for the rest. What the machine may do is decided in this file on
// the machine itself. Pairing writes it; this is for a manual setup.
export function runnerConfigSnippet(origin: string, token: string): string {
  return JSON.stringify(
    {
      api_url: origin,
      token,
      allowed_roots: ["~/dev"],
      permission_modes: ["plan", "acceptEdits"],
      commands: {
        "git-status": "git status -sb",
        "git-log": "git log --oneline --decorate -15",
        "git-pull": "git pull --ff-only",
        "git-fetch": "git fetch --prune",
      },
      max_concurrent: 2,
      scan_interval: "5m",
    },
    null,
    2,
  );
}

export const ENGINE_LABEL: Record<Engine, string> = { deepseek: "DeepSeek", claude: "Claude" };

export const PERMISSION_MODE_HELP: Record<string, string> = {
  plan: "Read-only: explores and proposes a plan, changes nothing.",
  acceptEdits: "May edit files; shell commands that need approval are denied.",
  auto: "Claude decides per action using its safety classifier.",
  dontAsk: "Only pre-approved tools run; everything else is denied.",
  bypassPermissions: "Everything runs without asking. Use with a worktree.",
};

export function runTitle(run: Pick<Run, "kind" | "prompt" | "command">): string {
  if (run.kind === "command") return run.command || "command";
  const first = run.prompt.split("\n").find((l) => l.trim()) ?? "";
  return first.trim() || "(empty prompt)";
}

/**
 * Every command a runner advertises, with its metadata. `commands` is the
 * authoritative list of names; a name without a detail entry (an older runner)
 * gets an unscoped, unconfirmed default rather than disappearing.
 */
export function commandDetails(caps: Pick<RunnerCapabilities, "commands" | "command_details"> | undefined): CommandDetail[] {
  if (!caps) return [];
  const details = caps.command_details ?? [];
  const names = Array.from(new Set([...(caps.commands ?? []), ...details.map((d) => d.name)]));
  return names.map(
    (name) => details.find((d) => d.name === name) ?? { name, description: "", repos: [], confirm: false },
  );
}

/** Commands that may run in a repo: unscoped ones, and ones scoped to it by name. */
export function commandsForRepo(details: CommandDetail[], repoName: string | undefined): CommandDetail[] {
  return details.filter((d) => d.repos.length === 0 || (!!repoName && d.repos.includes(repoName)));
}

// ── Runner roles ───────────────────────────────────────────────────────────

export const ROLE_LABEL: Record<RunnerRole, string> = { master: "Master", ios: "iOS", worker: "Worker" };
export const ROLE_ORDER: Record<RunnerRole, number> = { master: 0, ios: 1, worker: 2 };

/** Older APIs send no role: treat that runner as a worker. */
export const roleOf = (r: { role?: RunnerRole | null }): RunnerRole => r.role ?? "worker";

type RunnerLike = Pick<Runner, "id" | "online" | "role"> & { capabilities?: Pick<RunnerCapabilities, "commands" | "command_details"> };

/** The runner a new run defaults to: the master when it is up, else the first online one. */
export function defaultRunner<T extends RunnerLike>(runners: T[]): T | undefined {
  return (
    runners.find((r) => roleOf(r) === "master" && r.online) ??
    runners.find((r) => r.online) ??
    runners.find((r) => roleOf(r) === "master") ??
    runners[0]
  );
}

/**
 * Where a named command should run. iOS commands go to the iOS machine (the
 * Mac); anything else stays on the current runner if it has the command, then
 * the master, then any online runner that has it.
 */
export function runnerForCommand<T extends RunnerLike>(runners: T[], name: string, currentId: number | undefined): T | undefined {
  const has = runners.filter((r) => commandDetails(r.capabilities).some((c) => c.name === name));
  if (!has.length) return undefined;
  if (/ios/i.test(name)) {
    const ios = has.find((r) => roleOf(r) === "ios");
    if (ios) return ios;
  }
  return (
    has.find((r) => r.id === currentId) ??
    has.find((r) => roleOf(r) === "master") ??
    has.find((r) => r.online) ??
    has[0]
  );
}

export type CommandOption = CommandDetail & { runners: string[] };

/**
 * Every command any runner offers for this repo, so a command that only the
 * Mac has (an iOS upload) is still pickable while the master is selected.
 * Details come from the current runner when it has the command.
 */
export function commandOptions(
  runners: (RunnerLike & { name: string })[],
  repoName: string | undefined,
  currentId: number | undefined,
): CommandOption[] {
  const ordered = [...runners].sort((a, b) => Number(b.id === currentId) - Number(a.id === currentId));
  const out = new Map<string, CommandOption>();
  for (const r of ordered) {
    for (const c of commandsForRepo(commandDetails(r.capabilities), repoName)) {
      const existing = out.get(c.name);
      if (existing) existing.runners.push(r.name);
      else out.set(c.name, { ...c, runners: [r.name] });
    }
  }
  return Array.from(out.values());
}

// ── Pairing ────────────────────────────────────────────────────────────────
//
// Adding a machine hands out a one-time code; `forge agent pair <origin>
// <code>` on that machine swaps it for the token. Codes live 15 minutes.

export const PAIRING_TTL_MS = 15 * 60_000;
export const INSTALL_SCRIPT_URL = "https://github.com/devai-io/forge/releases/latest/download/install.sh";

/** The commands to run on the machine, for both starting points. */
export function pairCommands(origin: string, code: string) {
  return {
    /** No Forge there yet: installs the binary, pairs, installs the service, wires Claude Code. */
    install: `curl -fsSL ${INSTALL_SCRIPT_URL} | sh -s -- --pair ${origin} ${code}`,
    pair: `forge agent pair ${origin} ${code}`,
    service: "forge agent install",
    claude: "forge agent setup-claude",
  };
}

/** A new machine is the master when there is none yet; otherwise a worker. */
export function defaultNewRole(runners: Pick<Runner, "role">[]): RunnerRole {
  return runners.some((r) => roleOf(r) === "master") ? "worker" : "master";
}

/** "12:34" until `expiresAt`; null once it has passed. */
export function pairingCountdown(expiresAt: string, now: number): string | null {
  const left = Math.ceil((Date.parse(expiresAt) - now) / 1000);
  if (!(left > 0)) return null;
  return `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`;
}

export type PairingState = "waiting" | "paired" | "connected" | "expired";

/**
 * Where a code handed out in this dialog stands, from the polled runner row:
 *   - the code is used once the runner's `pair_expires_at` is cleared;
 *   - it is connected once the runner has been heard from since the code was
 *     issued (server time: expires_at − 15 min, so browser clock skew is moot);
 *   - unused past its expiry → expired.
 */
export function pairingState(
  runner: Pick<Runner, "last_seen_at" | "pair_expires_at"> | undefined,
  pairing: Pairing,
  now: number,
): PairingState {
  const expires = Date.parse(pairing.expires_at);
  const used = !!runner && runner.pair_expires_at === null;
  if (used) {
    const issued = expires - PAIRING_TTL_MS;
    const seen = runner.last_seen_at ? Date.parse(runner.last_seen_at) : NaN;
    return seen >= issued ? "connected" : "paired";
  }
  return now >= expires ? "expired" : "waiting";
}

/** A machine with an unused, unexpired code that has never connected. */
export function awaitingPairing(r: Pick<Runner, "last_seen_at" | "pair_expires_at">, now: number): boolean {
  return !r.last_seen_at && !!r.pair_expires_at && Date.parse(r.pair_expires_at) > now;
}
