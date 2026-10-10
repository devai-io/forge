// Agents: the Claude Code sessions and named commands on your machines —
// what is running now (and waiting on you) first, then the history. The
// machines themselves live on their own page (MachinesPage).

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
import { RunRow } from "./RunBits";

export function AgentsPage() {
  const { newRun } = useShell();
  const navigate = useNavigate();
  return (
    <div className="mx-auto max-w-5xl space-y-4">
      <PageHeader
        title="Agents"
        subtitle="Claude Code sessions and commands on your machines — queue them, answer them, follow them from anywhere."
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

/** Waiting on you first, then the ones working, then the queue — newest first within each. */
function activeOrder(r: Run): number {
  if (r.status === "running" && r.awaiting) return 0;
  return r.status === "running" ? 1 : 2;
}

function ActivePanel() {
  const running = useRuns({ status: "running", limit: 50 }, 3000);
  const queued = useRuns({ status: "queued", limit: 50 }, 3000);
  const runs = [...(running.data ?? []), ...(queued.data ?? [])].sort((a, b) => activeOrder(a) - activeOrder(b) || b.id - a.id);
  const error = running.error ?? queued.error;
  return (
    <Panel title={runs.length ? `Running now · ${runs.length}` : "Running now"} icon={<Activity />} id="agents-active">
      {running.isPending || queued.isPending ? (
        <SkeletonRows rows={2} />
      ) : error ? (
        <ErrorState
          error={error}
          onRetry={() => {
            void running.refetch();
            void queued.refetch();
          }}
        />
      ) : runs.length ? (
        <div>
          {runs.map((r) => (
            <RunRow key={r.id} run={r} />
          ))}
        </div>
      ) : (
        <EmptyState compact icon={<Bot />} title="Nothing running">
          Start one with New run, from a task, or from the Assistant.
        </EmptyState>
      )}
    </Panel>
  );
}

function RunsPanel() {
  const [project, setProject] = useState("");
  const [status, setStatus] = useState<RunStatus | "">("");
  const projects = useProjects();
  const runs = useRuns({ project: project || undefined, status: status || undefined, limit: 100 }, 6000);
  // What is still going is in "Running now"; here it only shows when asked for.
  const shown = (runs.data ?? []).filter((r) => status || (r.status !== "running" && r.status !== "queued"));
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
        <div className={clsx(runs.isPlaceholderData && "opacity-60")}>
          {shown.map((r) => (
            <RunRow key={r.id} run={r} />
          ))}
        </div>
      ) : (
        <EmptyState compact icon={<Bot />} title="No runs match" />
      )}
    </Panel>
  );
}
