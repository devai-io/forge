import clsx from "clsx";
import { Bot, SquareTerminal } from "lucide-react";
import { Link } from "react-router-dom";
import type { Run, RunStatus } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { runTitle } from "@/lib/agents";
import { formatCost, formatDuration } from "@/lib/format";

const STATUS: Record<RunStatus, { label: string; tone: "neutral" | "accent" | "good" | "critical" | "outline" }> = {
  queued: { label: "Queued", tone: "neutral" },
  running: { label: "Running", tone: "accent" },
  succeeded: { label: "Succeeded", tone: "good" },
  failed: { label: "Failed", tone: "critical" },
  cancelled: { label: "Cancelled", tone: "outline" },
};

export function RunStatusBadge({ run }: { run: Pick<Run, "status" | "cancel_requested"> }) {
  const s = STATUS[run.status];
  const label = run.status === "running" && run.cancel_requested ? "Cancelling" : s.label;
  return (
    <Badge tone={s.tone} dot className={clsx(run.status === "running" && "[&>span:first-child]:animate-pulse-soft")}>
      {label}
    </Badge>
  );
}

export function RunKindIcon({ kind, className }: { kind: Run["kind"]; className?: string }) {
  return kind === "agent" ? (
    <Bot className={clsx("shrink-0 text-fg-3", className ?? "size-3.5")} aria-label="Agent run" role="img" />
  ) : (
    <SquareTerminal className={clsx("shrink-0 text-fg-3", className ?? "size-3.5")} aria-label="Command run" role="img" />
  );
}

/** One run as a compact, clickable row. */
export function RunRow({ run, showProject = true }: { run: Run; showProject?: boolean }) {
  const duration =
    run.duration_ms ??
    (run.started_at && run.finished_at ? new Date(run.finished_at).getTime() - new Date(run.started_at).getTime() : null);
  return (
    <Link
      to={`/agents/runs/${run.id}`}
      className="group flex min-w-0 items-center gap-2.5 rounded-md px-2 py-2 hover:bg-surface-2"
    >
      <RunKindIcon kind={run.kind} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate text-[13px] text-fg">{runTitle(run)}</span>
        </div>
        <div className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-[11.5px] text-fg-3">
          <span className="font-mono">#{run.id}</span>
          {showProject ? (
            <span className="inline-flex items-center gap-1">
              <ColorDot color={run.project_color} className="size-2" />
              {run.project_key}
            </span>
          ) : null}
          <span className="truncate">{run.repo_name}</span>
          <span>· {run.runner_name}</span>
          {run.task_ref ? <span className="font-mono">· {run.task_ref}</span> : null}
          {run.cost_usd !== null ? <span className="tabular">· {formatCost(run.cost_usd)}</span> : null}
          {duration !== null && run.finished_at ? <span className="tabular">· {formatDuration(duration)}</span> : null}
        </div>
      </div>
      <div className="flex shrink-0 flex-col items-end gap-1">
        <RunStatusBadge run={run} />
        <RelativeTime iso={run.finished_at ?? run.started_at ?? run.created_at} className="text-[11px] text-fg-3" />
      </div>
    </Link>
  );
}
