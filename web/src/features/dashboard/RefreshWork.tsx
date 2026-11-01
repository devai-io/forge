// The Working on widget's Refresh: shows what it will do for each task in
// progress (ask a running session for status, resume a parked one, continue
// an ended one), then does it. See lib/working.ts for the rules.

import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, api, errorMessage } from "@/api/client";
import type { Run, Task } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Skeleton } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { continueInput, planRefresh, refreshText, summarize, type RefreshStep } from "@/lib/working";

const VERB: Record<RefreshStep["action"], string> = { ask: "Ask", resume: "Resume", continue: "Continue", skip: "Skip" };

export function RefreshWorkDialog({ tasks, open, onClose }: { tasks: Task[]; open: Run[]; onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [steps, setSteps] = useState<RefreshStep[] | null>(null);
  const [busy, setBusy] = useState(false);

  // Tasks with no open agent run: look up their newest run, to continue it.
  useEffect(() => {
    let live = true;
    const withOpen = new Set(open.filter((r) => r.kind === "agent").map((r) => r.task_id));
    const lookups = tasks
      .filter((t) => !withOpen.has(t.id))
      .map((t) =>
        api
          .get<{ runs: Run[] }>("/runs", { task: t.id, limit: 1 })
          .then((r) => [t.id, r.runs[0]] as const)
          .catch(() => [t.id, undefined] as const),
      );
    void Promise.all(lookups).then((pairs) => {
      if (live) setSteps(planRefresh(tasks, open, new Map(pairs)));
    });
    return () => {
      live = false;
    };
  }, [tasks, open]);

  const todo = (steps ?? []).filter((s) => s.action !== "skip");

  const run = async () => {
    setBusy(true);
    const failed: string[] = [];
    const done: RefreshStep[] = [];
    for (const s of todo) {
      const r = s.run!;
      try {
        if (s.action === "continue") {
          await api.post<Run>("/runs", continueInput(r, s.task));
        } else {
          try {
            await api.post<Run>(`/runs/${r.id}/messages`, { text: refreshText(s.task) });
          } catch (err) {
            // A parked session on a machine that cannot resume in place: continue it instead.
            if (s.action === "resume" && err instanceof ApiError && err.status === 422) {
              await api.post<Run>("/runs", continueInput(r, s.task));
            } else throw err;
          }
        }
        done.push(s);
      } catch (err) {
        failed.push(`${s.task.ref}: ${errorMessage(err)}`);
      }
    }
    void qc.invalidateQueries({ queryKey: ["runs"] });
    void qc.invalidateQueries({ queryKey: ["dashboard"] });
    void qc.invalidateQueries({ queryKey: ["tasks"] });
    setBusy(false);
    onClose();
    if (done.length) toast.success(`Refreshed: ${summarize(done, true)}`);
    if (failed.length) toast.error(new Error(`Could not refresh ${failed.join("; ")}`));
  };

  return (
    <Dialog
      open
      onClose={busy ? () => {} : onClose}
      title="Refresh what is in progress"
      description="Each task's agent session updates the task's status (done, blocked, or a one-line note) and keeps going."
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="primary" data-autofocus onClick={run} loading={busy} disabled={!steps || todo.length === 0}>
            {todo.length ? `Refresh ${todo.length}` : "Nothing to refresh"}
          </Button>
        </>
      }
    >
      {!steps ? (
        <Skeleton className="h-16" />
      ) : (
        <>
          <ul aria-label="Refresh plan" className="divide-y divide-line">
            {steps.map((s) => (
              <li key={s.task.id} className="flex items-baseline gap-2 py-1.5 text-[13px]">
                <span className={s.action === "skip" ? "w-16 shrink-0 text-fg-3" : "w-16 shrink-0 font-medium text-accent"}>
                  {VERB[s.action]}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="font-mono text-[12px] text-fg-3">{s.task.ref}</span> {s.task.title}
                  <span className="block text-[11.5px] text-fg-3">
                    {s.why}
                    {s.run ? (
                      <>
                        {" · "}
                        <Link to={`/agents/runs/${s.run.id}`} className="hover:text-fg" onClick={onClose}>
                          run #{s.run.id}
                        </Link>
                      </>
                    ) : null}
                  </span>
                </span>
              </li>
            ))}
          </ul>
          <p className="mt-2 text-[12px] text-fg-3">{summarize(steps)}.</p>
        </>
      )}
    </Dialog>
  );
}
