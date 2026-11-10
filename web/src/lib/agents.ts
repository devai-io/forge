// Agent-run helpers shared by the agents screens.

import type { CommandDetail, Engine, Pairing, Run, Runner, RunnerCapabilities, RunnerRole } from "@/api/types";
import { addDays, todayInTz } from "./format";

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

export const ENGINE_LABEL: Record<Engine, string> = { deepseek: "DeepSeek (OpenRouter)", claude: "Claude", codex: "Codex" };

/** Effort levels (claude --effort; the Assistant's DeepSeek loop adds "off"). */
export const EFFORT_LABEL: Record<string, string> = {
  off: "Thinking off",
  low: "Low",
  medium: "Medium",
  high: "High",
  xhigh: "Extra high",
  max: "Max",
};

export const PERMISSION_MODE_HELP: Record<string, string> = {
  plan: "Read-only: explores and proposes a plan, changes nothing.",
  acceptEdits: "May edit files; shell commands that need approval are denied.",
  auto: "Claude decides per action using its safety classifier.",
  dontAsk: "Only pre-approved tools run; everything else is denied.",
  bypassPermissions: "Everything runs without asking (--dangerously-skip-permissions). Use with a worktree.",
};

type TitledRun = Pick<Run, "kind" | "prompt" | "command"> & Partial<Pick<Run, "task_ref" | "task_title" | "status" | "result">>;

export const RUN_TITLE_MAX = 90;

/** Cut at a word boundary with an ellipsis; one long word is cut where it must be. */
function clipTitle(text: string, max = RUN_TITLE_MAX): string {
  const s = text.replace(/\s+/g, " ").trim();
  if (s.length <= max) return s;
  const cut = s.slice(0, max - 1);
  const space = cut.lastIndexOf(" ");
  return `${(space > max / 2 ? cut.slice(0, space) : cut).replace(/[\s,;:.—-]+$/, "")}…`;
}

/** Markdown emphasis and code marks off a one-line heading. */
// Emphasis markers go, but an underscore inside a word (alpha_app, snake_case) stays.
const plainInline = (s: string) =>
  s
    .replace(/[*`]+/g, "")
    .replace(/(^|[^\p{L}\p{N}])_+|_+(?=$|[^\p{L}\p{N}])/gu, "$1")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .trim();

/**
 * Prompts Forge sends to carry a session on ("Forge status check: …",
 * "continue"). They say nothing about the work, so lists show them as a
 * small badge and title the run by its task or its answer instead.
 */
export function runTag(run: Pick<Run, "kind" | "prompt">): "status check" | "continue" | null {
  if (run.kind !== "agent") return null;
  const p = run.prompt.trim();
  if (/^Forge status check\b/i.test(p)) return "status check";
  if (/^(continue|carry on|go on|keep going|resume)[.!]?$/i.test(p)) return "continue";
  return null;
}

/**
 * A run's title: "REF task title" for a task's run; for an answered run whose
 * result opens with a markdown heading, that heading; else the prompt's first
 * line (or, for a status check / continue, the answer's), clipped.
 */
export function runTitle(run: TitledRun): string {
  if (run.kind === "command") return run.command || "command";
  if (run.task_ref && run.task_title) return clipTitle(`${run.task_ref} ${run.task_title}`);
  const result = run.result?.trim() ?? "";
  const answered = !!result && run.status !== "queued" && run.status !== "running";
  if (answered) {
    const heading = /^#{1,6}\s+(.+)/.exec(result.split("\n")[0] ?? "");
    if (heading && plainInline(heading[1])) return clipTitle(plainInline(heading[1]));
  }
  const tag = runTag(run);
  if (tag) {
    const line = answered ? result.split("\n").find((l) => l.trim()) : "";
    return line ? clipTitle(plainInline(line.replace(/^[#>\-*\s]+/, ""))) : tag === "continue" ? "Continue" : "Status check";
  }
  const first = run.prompt.split("\n").find((l) => l.trim()) ?? "";
  return clipTitle(first) || "(empty prompt)";
}

export type RunDay = { label: "Today" | "Yesterday" | "Earlier"; runs: Run[]; costUsd: number | null };

/**
 * History under Today / Yesterday / Earlier (by when each run last moved, in
 * the user's time zone), order kept, empty buckets left out. `costUsd` is the
 * bucket's total, or null when no run in it reported a cost.
 */
export function groupRunsByDay(runs: Run[], timeZone: string | undefined, now: Date = new Date()): RunDay[] {
  const today = todayInTz(timeZone, now);
  const yesterday = addDays(today, -1);
  const days: RunDay[] = [
    { label: "Today", runs: [], costUsd: null },
    { label: "Yesterday", runs: [], costUsd: null },
    { label: "Earlier", runs: [], costUsd: null },
  ];
  for (const r of runs) {
    const day = todayInTz(timeZone, new Date(r.finished_at ?? r.started_at ?? r.created_at));
    const d = days[day === today ? 0 : day === yesterday ? 1 : 2];
    d.runs.push(r);
    if (r.cost_usd !== null && r.cost_usd !== undefined) d.costUsd = (d.costUsd ?? 0) + r.cost_usd;
  }
  return days.filter((d) => d.runs.length);
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

/** A member's agent in a line: "Claude Code · opus · High effort · on desk". */
export function agentLine(a: { model: string; effort: string; runner_name: string }): string {
  return ["Claude Code", a.model || "default model", a.effort ? `${EFFORT_LABEL[a.effort] ?? a.effort} effort` : "", `on ${a.runner_name}`]
    .filter(Boolean)
    .join(" · ");
}

/**
 * Whether a member's request about a task belongs in a new conversation of its
 * agent: yes when the open one is about something else (it is finished first).
 * With none open there is nothing to finish: it simply starts.
 */
export function startsFresh(view: { run: { task_id: number | null } | null }, taskId: number | null | undefined): boolean {
  if (!view.run) return false;
  return !!taskId && view.run.task_id !== taskId;
}

