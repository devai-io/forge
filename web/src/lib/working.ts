// "Refresh" in the dashboard's Working on widget: every task in progress —
// and every parked run — gets bumped, so its session brings the task's
// status up to date and carries on. Runs that Refresh starts or continues use
// the chosen model and effort (default Opus 5.5, medium).
//
//   running session    → a status-check message (read when it finishes its step;
//                        a live session keeps its own model)
//   parked session     → resumed in place when it already runs the chosen model
//                        and effort; otherwise continued as a follow-up run on
//                        them, and the parked run is finished
//   session ended      → a follow-up run that continues it, on the chosen model
//   no session yet     → a new run on the default machine, in the task's repo
//   left alone         → queued, ending, waiting for your answer, handed off to
//                        a terminal, or nowhere to run (no machine, no repo)

import type { Run, RunInput, Task } from "@/api/types";

export type RefreshAction = "ask" | "resume" | "continue" | "start" | "skip";

export type BumpSettings = { model: string; effort: string };

export const DEFAULT_BUMP: BumpSettings = { model: "claude-opus-5-5", effort: "medium" };

export type RefreshStep = {
  key: string;
  /** The task in progress; null for a parked run that is on no task. */
  task: Task | null;
  /** The run the step acts on (its open run, or the newest one to continue). */
  run: Run | null;
  action: RefreshAction;
  /** Why, in a few words (shown in the confirmation). */
  why: string;
  /** continue only: the run is parked (waiting) and is finished once the follow-up is queued. */
  parked?: boolean;
};

/** Where a new run for a task would go. */
export type StartTarget = { runner_id: number; repo_id: number; permission_mode: string; interactive: boolean };

/** "claude-opus-5-5" → "Opus 5.5". */
export function modelLabel(id: string): string {
  const m = /^claude-([a-z]+)-(\d+)-(\d+)$/.exec(id);
  if (!m) return id || "default";
  return `${m[1]![0]!.toUpperCase()}${m[1]!.slice(1)} ${m[2]}.${m[3]}`;
}

export const bumpLabel = (s: BumpSettings) => `${modelLabel(s.model)} · ${s.effort}`;

/** Claude runs can switch model on a follow-up; Codex/DeepSeek sessions keep their engine. */
const onClaude = (r: Run) => r.engine === "claude" || r.engine === "";

/**
 * What a refresh does. `open` are the open runs (queued, running, waiting) —
 * the dashboard's `active_runs`; `latest` is each task's newest run when it
 * has no open one; `targets` says where a new run for a task would go
 * (undefined: nowhere — no machine or no repo).
 */
export function planRefresh(
  tasks: Task[],
  open: Run[],
  latest: Map<number, Run | undefined>,
  settings: BumpSettings,
  targets: Map<number, StartTarget | undefined> = new Map(),
): RefreshStep[] {
  const label = bumpLabel(settings);
  const openByTask = new Map<number, Run>();
  for (const r of open) if (r.task_id !== null && r.kind === "agent" && !openByTask.has(r.task_id)) openByTask.set(r.task_id, r);

  const forOpen = (key: string, task: Task | null, run: Run): RefreshStep => {
    if (run.status === "queued") return { key, task, run, action: "skip", why: "about to start" };
    if (run.cancel_requested || run.finish_requested) return { key, task, run, action: "skip", why: "ending" };
    if (run.status === "waiting") {
      const same = run.model === settings.model && run.effort === settings.effort;
      if (same || !onClaude(run)) return { key, task, run, action: "resume", why: same ? "parked: resume it" : `parked: resume it (stays on ${run.engine})` };
      return { key, task, run, action: "continue", parked: true, why: `parked: continue it on ${label}` };
    }
    if (run.awaiting === "answer") return { key, task, run, action: "skip", why: "waiting for your answer" };
    return { key, task, run, action: "ask", why: run.awaiting === "reply" ? "turn ended: ask for status" : "running: ask for status" };
  };

  const steps = tasks.map((task): RefreshStep => {
    const key = `t${task.id}`;
    const run = openByTask.get(task.id);
    if (run) return forOpen(key, task, run);
    const last = latest.get(task.id);
    if (last?.handoff) return { key, task, run: last, action: "skip", why: `continued in ${last.handoff}` };
    if (last && last.kind === "agent" && last.session_id)
      return { key, task, run: last, action: "continue", why: `run #${last.id} ended: continue it on ${onClaude(last) ? label : last.engine}` };
    if (!targets.get(task.id)) return { key, task, run: null, action: "skip", why: "no machine or repo to start a run in" };
    return { key, task, run: null, action: "start", why: `no session yet: start one on ${label}` };
  });

  // Parked runs that are on no task in progress: bump them too.
  const listed = new Set(tasks.map((t) => t.id));
  for (const r of open) {
    if (r.kind !== "agent" || (r.task_id !== null && listed.has(r.task_id))) continue;
    if (r.status !== "waiting") continue; // running ones are already working
    steps.push(forOpen(`r${r.id}`, null, r));
  }
  return steps;
}

/** The message that asks a session for the task's status and to carry on. */
export function refreshText(task: Task | null): string {
  if (!task)
    return [
      "Forge status check: carry on with this run's work from where you left off.",
      "If it is already finished, say so in one line and stop.",
    ].join("\n");
  return [
    `Forge status check for ${task.ref} "${task.title}".`,
    "1. If the task is finished, set it done with forge_update_task and forge_comment what you did (commits, decisions).",
    "2. If you are blocked, set it blocked and say what you need from me.",
    "3. Otherwise forge_comment a one-line status, then keep working on it.",
  ].join("\n");
}

/** The prompt of a fresh run for a task that had no session. */
export function startText(task: Task): string {
  return [
    `Work on ${task.ref} "${task.title}".`,
    "Read it with forge_task (and its comments), check the repo for what has been done so far, and carry the work on from where it stands.",
    "When it is finished, set it done with forge_update_task and forge_comment what you did (commits, decisions).",
    "If you are blocked, set it blocked and say what you need from me.",
  ].join("\n");
}

/** A follow-up run continuing `run`'s session (same machine and folder). */
export function continueInput(run: Run, task: Task | null, settings: BumpSettings): RunInput {
  const claude = onClaude(run);
  return {
    runner_id: run.runner_id,
    repo_id: run.repo_id,
    kind: "agent",
    prompt: refreshText(task),
    permission_mode: run.permission_mode || undefined,
    model: (claude ? settings.model : run.model) || undefined,
    effort: (claude ? settings.effort : run.effort) || undefined,
    interactive: run.interactive || undefined,
    worktree: false,
    task_id: task?.id ?? run.task_id,
    resume_run_id: run.id,
  };
}

/** A new Claude run for a task that had no session. */
export function startInput(task: Task, t: StartTarget, settings: BumpSettings): RunInput {
  return {
    runner_id: t.runner_id,
    repo_id: t.repo_id,
    kind: "agent",
    prompt: startText(task),
    engine: "claude",
    model: settings.model,
    effort: settings.effort,
    permission_mode: t.permission_mode,
    interactive: t.interactive || undefined,
    worktree: false,
    task_id: task.id,
  };
}

/** One line that sums up a plan ("2 to ask for status · 1 to start"). */
export function summarize(steps: Pick<RefreshStep, "action">[], done = false): string {
  const n = (a: RefreshAction) => steps.filter((s) => s.action === a).length;
  const words: Record<RefreshAction, [string, string]> = {
    ask: ["to ask for status", "asked for status"],
    resume: ["to resume", "resumed"],
    continue: ["to continue", "continued"],
    start: ["to start", "started"],
    skip: ["left alone", "left alone"],
  };
  return (["ask", "resume", "continue", "start", "skip"] as const)
    .filter((a) => n(a) > 0)
    .map((a) => `${n(a)} ${words[a][done ? 1 : 0]}`)
    .join(" · ");
}
