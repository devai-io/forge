// Agent-run helpers shared by the agents screens.

import type { CommandDetail, Run, Runner, RunnerCapabilities, RunnerRole } from "@/api/types";

// Mirrors forge_api/deploy/runner.example.json: the keys a runner needs to
// start (api_url, token, allowed_roots) plus safe defaults for the rest. What
// the runner may do is decided in this file on the runner's own machine.
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
