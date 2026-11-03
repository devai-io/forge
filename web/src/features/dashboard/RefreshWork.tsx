// The Working on widget's Refresh: shows what it will do for every task in
// progress and every parked run (ask a running session for status, resume or
// continue a parked or ended one, start a run where there is none), then does
// it. Runs it starts or continues use the chosen model and effort, Opus 5.5 at
// medium by default. See lib/working.ts for the rules.

import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, api, errorMessage } from "@/api/client";
import type { EngineStatus, ProjectDetail, Run, Runner, Task } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Field, Select } from "@/components/ui/Input";
import { Skeleton } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { defaultRunner } from "@/lib/agents";
import {
  DEFAULT_BUMP,
  continueInput,
  modelLabel,
  planRefresh,
  refreshText,
  startInput,
  summarize,
  type BumpSettings,
  type RefreshStep,
  type StartTarget,
} from "@/lib/working";

const VERB: Record<RefreshStep["action"], string> = { ask: "Ask", resume: "Resume", continue: "Continue", start: "Start", skip: "Skip" };

type Lookups = {
  latest: Map<number, Run | undefined>;
  targets: Map<number, StartTarget | undefined>;
  models: string[];
  efforts: string[];
};

/** Where a new run for each task goes: the default Claude machine, in the task's repo (or the project's first checkout). */
async function resolveTargets(tasks: Task[]): Promise<Map<number, StartTarget | undefined>> {
  const out = new Map<number, StartTarget | undefined>();
  if (tasks.length === 0) return out;
  const runners = await api.get<{ runners: Runner[] }>("/runners").then((r) => r.runners).catch(() => [] as Runner[]);
  const runner = defaultRunner(runners.filter((r) => r.capabilities.claude));
  if (!runner) return out;
  const modes = runner.capabilities.permission_modes.length ? runner.capabilities.permission_modes : ["plan"];
  const mode = modes.includes("bypassPermissions") ? "bypassPermissions" : modes.includes("acceptEdits") ? "acceptEdits" : modes[0]!;
  const keys = [...new Set(tasks.map((t) => t.project_key))];
  const projects = new Map(
    await Promise.all(
      keys.map((k) =>
        api
          .get<ProjectDetail>(`/projects/${encodeURIComponent(k)}`)
          .then((p) => [k, p] as const)
          .catch(() => [k, undefined] as const),
      ),
    ),
  );
  for (const t of tasks) {
    const repos = (projects.get(t.project_key)?.repos ?? []).filter((r) => r.path);
    const repo = repos.find((r) => r.id === t.repo_id) ?? repos[0];
    out.set(t.id, repo ? { runner_id: runner.id, repo_id: repo.id, permission_mode: mode, interactive: !!runner.capabilities.interactive } : undefined);
  }
  return out;
}

export function RefreshWorkDialog({ tasks, open, onClose }: { tasks: Task[]; open: Run[]; onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [data, setData] = useState<Lookups | null>(null);
  const [settings, setSettings] = useState<BumpSettings>(DEFAULT_BUMP);
  const [busy, setBusy] = useState(false);

  // Look up, once: each task's newest run (when it has no open one), where a
  // new run would go, and what models and efforts Claude runs offer.
  useEffect(() => {
    let live = true;
    const withOpen = new Set(open.filter((r) => r.kind === "agent").map((r) => r.task_id));
    const idle = tasks.filter((t) => !withOpen.has(t.id));
    void (async () => {
      const [pairs, targets, engine] = await Promise.all([
        Promise.all(
          idle.map((t) =>
            api
              .get<{ runs: Run[] }>("/runs", { task: t.id, limit: 1 })
              .then((r) => [t.id, r.runs[0]] as const)
              .catch(() => [t.id, undefined] as const),
          ),
        ),
        resolveTargets(idle),
        api.get<EngineStatus>("/engine").catch(() => null),
      ]);
      if (!live) return;
      const models = engine?.models?.claude?.length ? engine.models.claude : [DEFAULT_BUMP.model];
      const efforts = engine?.efforts?.length ? engine.efforts : [DEFAULT_BUMP.effort];
      setData({ latest: new Map(pairs), targets, models, efforts });
      setSettings((s) => ({ model: models.includes(s.model) ? s.model : models[0]!, effort: efforts.includes(s.effort) ? s.effort : efforts[0]! }));
    })();
    return () => {
      live = false;
    };
  }, [tasks, open]);

  const steps = useMemo(() => (data ? planRefresh(tasks, open, data.latest, settings, data.targets) : null), [data, tasks, open, settings]);
  const todo = (steps ?? []).filter((s) => s.action !== "skip");

  const run = async () => {
    setBusy(true);
    const failed: string[] = [];
    const done: RefreshStep[] = [];
    for (const s of todo) {
      try {
        if (s.action === "start") {
          await api.post<Run>("/runs", startInput(s.task!, data!.targets.get(s.task!.id)!, settings));
        } else if (s.action === "continue") {
          await api.post<Run>("/runs", continueInput(s.run!, s.task, settings));
          // The follow-up carries the session on; the parked run is done.
          if (s.parked) await api.post<Run>(`/runs/${s.run!.id}/finish`).catch(() => undefined);
        } else {
          try {
            await api.post<Run>(`/runs/${s.run!.id}/messages`, { text: refreshText(s.task) });
          } catch (err) {
            // A parked session on a machine that cannot resume in place: continue it instead.
            if (s.action === "resume" && err instanceof ApiError && err.status === 422) {
              await api.post<Run>("/runs", continueInput(s.run!, s.task, settings));
              await api.post<Run>(`/runs/${s.run!.id}/finish`).catch(() => undefined);
            } else throw err;
          }
        }
        done.push(s);
      } catch (err) {
        failed.push(`${s.task?.ref ?? `run #${s.run?.id}`}: ${errorMessage(err)}`);
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
      description="Every task's agent session updates the task's status (done, blocked, or a one-line note) and keeps going; a task with no session gets one."
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
      {!steps || !data ? (
        <Skeleton className="h-16" />
      ) : (
        <>
          <div className="mb-3 grid grid-cols-2 gap-3">
            <Field label="Model" hint="For runs Refresh starts or continues; a live session keeps its own.">
              {(id, desc) => (
                <Select id={id} aria-describedby={desc} value={settings.model} onChange={(e) => setSettings({ ...settings, model: e.target.value })}>
                  {data.models.map((m) => (
                    <option key={m} value={m}>
                      {modelLabel(m)}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <Field label="Effort">
              {(id) => (
                <Select id={id} value={settings.effort} onChange={(e) => setSettings({ ...settings, effort: e.target.value })}>
                  {data.efforts.map((x) => (
                    <option key={x} value={x}>
                      {x}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          </div>
          <ul aria-label="Refresh plan" className="divide-y divide-line">
            {steps.map((s) => (
              <li key={s.key} className="flex items-baseline gap-2 py-1.5 text-[13px]">
                <span className={s.action === "skip" ? "w-16 shrink-0 text-fg-3" : "w-16 shrink-0 font-medium text-accent"}>{VERB[s.action]}</span>
                <span className="min-w-0 flex-1">
                  {s.task ? (
                    <>
                      <span className="font-mono text-[12px] text-fg-3">{s.task.ref}</span> {s.task.title}
                    </>
                  ) : (
                    <>Parked run on no task</>
                  )}
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
