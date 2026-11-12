import clsx from "clsx";
import { Archive, ArchiveRestore, Bot, SquareTerminal } from "lucide-react";
import { Link } from "react-router-dom";
import { useArchiveRun } from "@/api/hooks";
import type { Run, RunStatus } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { useToast } from "@/components/ui/Toast";
import { runTag, runTitle } from "@/lib/agents";
import { formatCost, formatDuration } from "@/lib/format";

const STATUS: Record<RunStatus, { label: string; tone: "neutral" | "accent" | "good" | "warning" | "critical" | "outline" }> = {
  queued: { label: "Queued", tone: "neutral" },
  running: { label: "Running", tone: "accent" },
  waiting: { label: "Waiting for you", tone: "warning" },
  succeeded: { label: "Succeeded", tone: "good" },
  failed: { label: "Failed", tone: "critical" },
  cancelled: { label: "Cancelled", tone: "outline" },
};

export function RunStatusBadge({
  run,
}: {
  run: Pick<Run, "status" | "cancel_requested"> & { awaiting?: Run["awaiting"]; tmux_session?: string };
}) {
  const s = STATUS[run.status];
  // A session in tmux whose Claude exited is not waiting on anyone: a message starts it again.
  if (run.tmux_session && run.status === "waiting") {
    return (
      <Badge tone="outline" dot title="Claude Code is not open in its tmux session; a message (or Open in terminal) starts it again">
        Closed
      </Badge>
    );
  }
  // An interactive session waiting on the user says so.
  const waiting = run.status === "running" && !run.cancel_requested && run.awaiting;
  if (waiting) {
    return (
      <Badge tone="warning" dot className="[&>span:first-child]:animate-pulse-soft">
        {run.awaiting === "answer" ? "Needs you" : "Your turn"}
      </Badge>
    );
  }
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

/** "status check" / "continue": what a run Forge sent to carry a session on was, beside its real title. */
export function RunTagBadge({ run }: { run: Pick<Run, "kind" | "prompt"> }) {
  const tag = runTag(run);
  return tag ? (
    <Badge tone="outline" className="shrink-0 text-[10.5px]" title={run.prompt}>
      {tag}
    </Badge>
  ) : null;
}

/** One run as a compact, clickable row. */
/**
 * A run in a list. `archive` adds a button that takes it off the list
 * ("archive", with Undo in the toast) or puts an archived one back ("restore").
 */
export function RunRow({ run, showProject = true, archive }: { run: Run; showProject?: boolean; archive?: "archive" | "restore" }) {
  const row = <RunLink run={run} showProject={showProject} className={archive ? "min-w-0 flex-1" : undefined} />;
  if (!archive) return row;
  return (
    <div className="group/row flex min-w-0 items-center gap-0.5">
      {row}
      <ArchiveRunButton run={run} mode={archive} />
    </div>
  );
}

function ArchiveRunButton({ run, mode }: { run: Run; mode: "archive" | "restore" }) {
  const archive = useArchiveRun();
  const toast = useToast();
  const restore = mode === "restore";
  const label = `${restore ? "Restore" : "Archive"} run #${run.id}`;
  return (
    <button
      type="button"
      aria-label={label}
      title={restore ? "Put it back on the Agents page" : "Archive: take it off this page (nothing stops; it comes back if it needs you)"}
      disabled={archive.isPending}
      onClick={() =>
        archive.mutate(
          { id: run.id, archived: !restore },
          {
            onSuccess: () =>
              restore
                ? toast.success(`Run #${run.id} is back`)
                : toast.success(
                    <span className="inline-flex items-center gap-2">
                      Run #{run.id} archived
                      <button
                        type="button"
                        className="font-medium text-accent hover:underline"
                        onClick={() => archive.mutate({ id: run.id, archived: false })}
                      >
                        Undo
                      </button>
                    </span>,
                    { duration: 6000 },
                  ),
            onError: (e) => toast.error(e),
          },
        )
      }
      className={clsx(
        "grid size-8 shrink-0 place-items-center rounded-md text-fg-3 hover:bg-surface-2 hover:text-fg focus-visible:opacity-100 disabled:opacity-50",
        !restore && "md:opacity-0 md:group-hover/row:opacity-100",
      )}
    >
      {restore ? <ArchiveRestore className="size-4" aria-hidden /> : <Archive className="size-4" aria-hidden />}
    </button>
  );
}

function RunLink({ run, showProject, className }: { run: Run; showProject: boolean; className?: string }) {
  const duration =
    run.duration_ms ??
    (run.started_at && run.finished_at ? new Date(run.finished_at).getTime() - new Date(run.started_at).getTime() : null);
  return (
    <Link
      to={`/agents/runs/${run.id}`}
      className={clsx("group flex min-w-0 items-center gap-2.5 rounded-md px-2 py-2 hover:bg-surface-2", className)}
    >
      <RunKindIcon kind={run.kind} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate text-[13px] text-fg">{runTitle(run)}</span>
          <RunTagBadge run={run} />
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
          {run.tmux_session ? (
            <span className="inline-flex items-center gap-1 font-mono" title="Lives in this tmux session on its machine">
              · <SquareTerminal className="size-3" aria-hidden /> {run.tmux_session}
            </span>
          ) : null}
          {run.task_ref && !run.task_title ? <span className="font-mono">· {run.task_ref}</span> : null}
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
