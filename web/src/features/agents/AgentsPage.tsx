// Agents: the Claude Code sessions and named commands on your machines —
// what is running now (and waiting on you) first, then the history. The
// machines themselves live on their own page (MachinesPage). Nothing open is
// one line, so the history (by day, with what each day cost) starts high.

import clsx from "clsx";
import { Activity, Bot, Cpu, Plus } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useProjects, useRuns } from "@/api/hooks";
import type { Run, RunStatus } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Select } from "@/components/ui/Input";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useShell } from "@/features/shell/context";
import { groupRunsByDay } from "@/lib/agents";
import { useUser } from "@/lib/auth";
import { formatCost } from "@/lib/format";
import { RunRow } from "./RunBits";

export function AgentsPage() {
  const { newRun } = useShell();
  const navigate = useNavigate();
  return (
    <div className="mx-auto max-w-[1200px] space-y-4">
      <PageHeader
        title="Agents"
        subtitle="Claude Code, Codex sessions and commands on your machines — queue them, answer them, follow them from anywhere."
        actions={
          <>
            <Button variant="ghost" size="sm" onClick={() => navigate("/machines")}>
              <Cpu className="size-3.5" aria-hidden /> Machines
            </Button>
            <Button variant="primary" size="sm" onClick={() => newRun()}>
              <Plus className="size-3.5" aria-hidden /> New run
            </Button>
          </>
        }
      />
      <ActivePanel />
      <RunsPanel />
    </div>
  );
}

/** Waiting on you first (live, then parked), then the ones working, then the queue — newest first within each. */
function activeOrder(r: Run): number {
  if (r.status === "running" && r.awaiting) return 0;
  if (r.status === "waiting") return 1;
  return r.status === "running" ? 2 : 3;
}

function ActivePanel() {
  const running = useRuns({ status: "running", limit: 50 }, 3000);
  const queued = useRuns({ status: "queued", limit: 50 }, 3000);
  const waiting = useRuns({ status: "waiting", limit: 50 }, 8000);
  const runs = [...(running.data ?? []), ...(queued.data ?? []), ...(waiting.data ?? [])].sort(
    (a, b) => activeOrder(a) - activeOrder(b) || b.id - a.id,
  );
  const error = running.error ?? queued.error ?? waiting.error;
  const yours = runs.filter((r) => r.status === "waiting" || (r.status === "running" && r.awaiting)).length;
  const { newRun } = useShell();
  const loading = running.isPending || queued.isPending || waiting.isPending;
  if (!loading && !error && !runs.length) {
    return (
      <p id="agents-active" className="flex items-center gap-2 rounded-xl border border-line bg-surface px-3.5 py-2.5 text-[13px] text-fg-3">
        <Activity className="size-4" aria-hidden />
        Nothing open ·
        <button type="button" onClick={() => newRun()} className="font-medium text-accent hover:underline">
          New run
        </button>
      </p>
    );
  }
  return (
    <Panel
      title={runs.length ? `Open · ${runs.length}${yours ? ` · ${yours} waiting for you` : ""}` : "Open"}
      icon={<Activity />}
      id="agents-active"
    >
      {loading ? (
        <SkeletonRows rows={2} />
      ) : error ? (
        <ErrorState
          error={error}
          onRetry={() => {
            void running.refetch();
            void queued.refetch();
            void waiting.refetch();
          }}
        />
      ) : (
        <div>
          {runs.map((r) => (
            <RunRow key={r.id} run={r} />
          ))}
        </div>
      )}
    </Panel>
  );
}

function RunsPanel() {
  const [project, setProject] = useState("");
  const [status, setStatus] = useState<RunStatus | "">("");
  const projects = useProjects();
  const runs = useRuns({ project: project || undefined, status: status || undefined, limit: 100 }, 6000);
  // What is still open is in "Open"; here it only shows when asked for.
  const shown = (runs.data ?? []).filter((r) => status || (r.status !== "running" && r.status !== "queued" && r.status !== "waiting"));
  const user = useUser();
  const days = groupRunsByDay(shown, user.timezone);
  return (
    <Panel
      title="History"
      icon={<Bot />}
      id="agents-runs"
      actions={
        <div className="flex items-center gap-1.5">
          <Select aria-label="Filter by project" compact className="w-auto" value={project} onChange={(e) => setProject(e.target.value)}>
            <option value="">All projects</option>
            {projects.data?.map((p) => (
              <option key={p.key} value={p.key}>
                {p.key}
              </option>
            ))}
          </Select>
          <Select
            aria-label="Filter by status"
            compact
            className="w-auto"
            value={status}
            onChange={(e) => setStatus(e.target.value as RunStatus | "")}
          >
            <option value="">Any status</option>
            <option value="queued">Queued</option>
            <option value="running">Running</option>
            <option value="waiting">Waiting for you</option>
            <option value="succeeded">Succeeded</option>
            <option value="failed">Failed</option>
            <option value="cancelled">Cancelled</option>
          </Select>
        </div>
      }
    >
      {runs.isPending ? (
        <SkeletonRows rows={6} />
      ) : runs.error ? (
        <ErrorState error={runs.error} onRetry={() => runs.refetch()} />
      ) : shown.length ? (
        <div className={clsx("space-y-2", runs.isPlaceholderData && "opacity-60")}>
          {days.map((d) => (
            <section key={d.label} aria-label={d.label}>
              <h3 className="flex items-baseline gap-2 px-2 pt-1 pb-0.5 text-[11px] font-semibold tracking-wide text-fg-3 uppercase">
                {d.label}
                <span className="tabular font-normal tracking-normal normal-case">
                  {d.runs.length} run{d.runs.length === 1 ? "" : "s"}
                  {d.costUsd !== null ? ` · ${formatCost(d.costUsd)}` : ""}
                </span>
              </h3>
              {d.runs.map((r) => (
                <RunRow key={r.id} run={r} />
              ))}
            </section>
          ))}
        </div>
      ) : (
        <EmptyState compact icon={<Bot />} title="No runs match" />
      )}
    </Panel>
  );
}
