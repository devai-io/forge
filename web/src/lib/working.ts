// "Refresh" in the dashboard's Working on widget: for every task in
// progress, reach the agent session that works on it, so the session brings
// the task's status up to date and carries on.
//
//   running session   → a status-check message (read when it finishes its step)
//   parked session    → the same message, which resumes it in place
//   session ended     → a follow-up run that continues that session
//   no session        → nothing (the widget says so)

import type { Run, RunInput, Task } from "@/api/types";

export type RefreshAction = "ask" | "resume" | "continue" | "skip";

export type RefreshStep = {
  task: Task;
  /** The run the step acts on: the task's open run, or its latest one to continue. */
  run: Run | null;
  action: RefreshAction;
  /** Why, in a few words (shown in the confirmation). */
  why: string;
};

/**
 * What a refresh does for each task. `open` are the open runs (queued,
 * running, waiting) — the dashboard's `active_runs`; `latest` is each task's
 * newest run when it has no open one (undefined: none, or not looked up).
 */
export function planRefresh(tasks: Task[], open: Run[], latest: Map<number, Run | undefined>): RefreshStep[] {
  const openByTask = new Map<number, Run>();
  for (const r of open) if (r.task_id !== null && r.kind === "agent" && !openByTask.has(r.task_id)) openByTask.set(r.task_id, r);

  return tasks.map((task): RefreshStep => {
    const run = openByTask.get(task.id);
    if (run) {
      if (run.status === "queued") return { task, run, action: "skip", why: "about to start" };
      if (run.cancel_requested || run.finish_requested) return { task, run, action: "skip", why: "ending" };
      if (run.status === "waiting") return { task, run, action: "resume", why: "parked: resume it" };
      if (run.awaiting === "answer") return { task, run, action: "skip", why: "waiting for your answer" };
      return { task, run, action: "ask", why: run.awaiting === "reply" ? "turn ended: ask for status" : "running: ask for status" };
    }
    const last = latest.get(task.id);
    if (last && last.kind === "agent" && last.session_id && !last.handoff)
      return { task, run: last, action: "continue", why: `run #${last.id} ended: continue its session` };
    if (last?.handoff) return { task, run: last, action: "skip", why: `continued in ${last.handoff}` };
    return { task, run: null, action: "skip", why: "no agent session" };
  });
}

/** The message that asks a session for the task's status and to carry on. */
export function refreshText(task: Task): string {
  return [
    `Forge status check for ${task.ref} "${task.title}".`,
    "1. If the task is finished, set it done with forge_update_task and forge_comment what you did (commits, decisions).",
    "2. If you are blocked, set it blocked and say what you need from me.",
    "3. Otherwise forge_comment a one-line status, then keep working on it.",
  ].join("\n");
}

/** The body of a follow-up run continuing `run`'s session on its machine. */
export function continueInput(run: Run, task: Task): RunInput {
  return {
    runner_id: run.runner_id,
    repo_id: run.repo_id,
    kind: "agent",
    prompt: refreshText(task),
    permission_mode: run.permission_mode || undefined,
    model: run.model || undefined,
    effort: run.effort || undefined,
    interactive: run.interactive || undefined,
    worktree: run.worktree,
    task_id: task.id,
    resume_run_id: run.id,
  };
}

/** One line that sums up a plan ("2 asked · 1 resumed · 1 skipped"). */
export function summarize(steps: Pick<RefreshStep, "action">[], done = false): string {
  const n = (a: RefreshAction) => steps.filter((s) => s.action === a).length;
  const words: Record<RefreshAction, [string, string]> = {
    ask: ["to ask for status", "asked for status"],
    resume: ["to resume", "resumed"],
    continue: ["to continue", "continued"],
    skip: ["left alone", "left alone"],
  };
  return (["ask", "resume", "continue", "skip"] as const)
    .filter((a) => n(a) > 0)
    .map((a) => `${n(a)} ${words[a][done ? 1 : 0]}`)
    .join(" · ");
}
