import clsx from "clsx";
import { CircleAlert, CircleCheck, CircleDashed, EyeOff, GitBranch, GitCommitHorizontal, Hourglass, Target, TriangleAlert } from "lucide-react";
import { Link } from "react-router-dom";
import type { Project } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { ProjectStatusBadge } from "@/features/tasks/icons";
import { countdown, daysBetween } from "@/lib/format";
import { isStale } from "@/lib/projects";

/** Progress as a meter: the project's colour on a wash of itself. */
export function ProgressMeter({ value, color, label }: { value: number; color: string; label: string }) {
  const pct = Math.round(Math.max(0, Math.min(1, value)) * 100);
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={pct}
      className="h-1.5 w-full overflow-hidden rounded-full"
      style={{ background: `color-mix(in srgb, ${color} 18%, transparent)` }}
    >
      <div className="h-full rounded-full transition-[width] duration-500" style={{ width: `${pct}%`, background: color }} />
    </div>
  );
}

export function EndpointHealth({ up, total, down, warning = 0 }: { up: number; total: number; down: number; warning?: number }) {
  if (!total) return null;
  const allUp = down === 0 && up === total;
  return (
    <span className="inline-flex items-center gap-1 text-[11.5px] text-fg-2" title={`${up} of ${total} endpoints up`}>
      {allUp ? (
        <CircleCheck className="size-3.5 text-good" aria-hidden />
      ) : down > 0 ? (
        <CircleAlert className="size-3.5 text-critical" aria-hidden />
      ) : warning > 0 ? (
        <TriangleAlert className="size-3.5 text-warning" aria-hidden />
      ) : (
        <CircleDashed className="size-3.5 text-fg-3" aria-hidden />
      )}
      <span className="tabular">
        {up}/{total} up
      </span>
      {down > 0 ? <span className="sr-only">, {down} down</span> : null}
      {warning > 0 ? <span className="sr-only">, {warning} failing (being rechecked)</span> : null}
    </span>
  );
}

/** `compact` (the dashboard): on a phone the card drops the summary, so thirteen projects fit in a few screens. */
export function ProjectCard({ project, today, compact = false }: { project: Project; today: string; compact?: boolean }) {
  const s = project.stats;
  // Backlog + to do: in progress and blocked get their own counts, so "open" would count them twice.
  const toDo = s.total - s.done - s.in_progress - s.blocked;
  const stale = isStale(project);
  const targetDays = project.target_date ? daysBetween(today, project.target_date) : null;
  return (
    <Link
      to={`/p/${project.key}`}
      className={clsx(
        "group relative flex flex-col gap-3 overflow-hidden rounded-xl border border-line bg-surface p-4 pl-5 transition-colors hover:border-line-strong",
        compact && "max-sm:gap-2 max-sm:p-3 max-sm:pl-4",
      )}
    >
      <span aria-hidden className="absolute inset-y-0 left-0 w-1" style={{ background: project.color }} />
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="truncate text-[15px] font-semibold">{project.name}</h3>
            <span className="font-mono text-[11px] text-fg-3">{project.key}</span>
          </div>
          {project.summary ? <p className={clsx("mt-0.5 line-clamp-2 text-[12.5px] text-fg-2", compact && "max-sm:hidden")}>{project.summary}</p> : null}
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1">
          <ProjectStatusBadge status={project.status} />
          <span className="text-[11px] text-fg-3" title="Priority (1 = top focus)">
            P{project.priority} · {project.category}
          </span>
          {project.hide_on_dashboard ? (
            <span className="inline-flex items-center gap-1 text-[11px] text-fg-3" title="Hidden from the dashboard (Edit project to show it)">
              <EyeOff className="size-3" aria-hidden /> not on dashboard
            </span>
          ) : null}
        </div>
      </div>

      <div className="space-y-1.5">
        <div className="flex items-baseline justify-between text-[12px]">
          <span className="text-fg-2">
            <span className="font-semibold text-fg">{s.done}</span> of {s.total} done
          </span>
          <span className="tabular text-fg-3">{Math.round(s.progress * 100)}%</span>
        </div>
        <ProgressMeter value={s.progress} color={project.color} label={`${project.name} progress`} />
      </div>

      <p className="flex flex-wrap items-center gap-x-2.5 gap-y-1 text-[12.5px] text-fg-2">
        <span>
          <span className="font-semibold text-fg tabular">{toDo}</span> to do
        </span>
        {s.in_progress ? (
          <span>
            <span className="font-semibold text-fg tabular">{s.in_progress}</span> in progress
          </span>
        ) : null}
        {s.blocked ? (
          <span className="text-critical-ink">
            <CircleAlert className="mr-0.5 inline size-3.5 -translate-y-px" aria-hidden />
            <span className="font-semibold tabular">{s.blocked}</span> blocked
          </span>
        ) : null}
        {s.overdue ? (
          <span className="text-critical-ink">
            <span className="font-semibold tabular">{s.overdue}</span> overdue
          </span>
        ) : null}
        {s.ci_failing ? (
          <Badge tone="critical" title="The latest CI run of a repo in this project did not succeed">
            CI failing{s.ci_failing > 1 ? ` · ${s.ci_failing}` : ""}
          </Badge>
        ) : null}
      </p>

      <div className="mt-auto flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[11.5px] text-fg-2">
        <span className="inline-flex items-center gap-1" title="Tasks completed in the last 7 days">
          <CircleCheck className="size-3.5 text-fg-3" aria-hidden />
          <span className="tabular">{s.done_7d}</span> done 7d
        </span>
        <span className="inline-flex items-center gap-1" title="Commits in the last 7 days (latest runner scan)">
          <GitCommitHorizontal className="size-3.5 text-fg-3" aria-hidden />
          <span className="tabular">{s.commits_7d}</span> commits 7d
        </span>
        <EndpointHealth up={s.endpoints_up} total={s.endpoints_total} down={s.endpoints_down} warning={s.endpoints_warning} />
        {s.dirty_repos > 0 ? (
          <span className="inline-flex items-center gap-1" title="Repos with uncommitted changes">
            <GitBranch className="size-3.5 text-fg-3" aria-hidden />
            {s.dirty_repos} dirty
          </span>
        ) : null}
        {project.target_date && targetDays !== null ? (
          <span
            className={clsx("inline-flex items-center gap-1", targetDays < 0 ? "text-critical-ink" : undefined)}
            title={`Target ${project.target_date}`}
          >
            <Target className="size-3.5 text-fg-3" aria-hidden />
            {countdown(project.target_date, today)}
          </span>
        ) : null}
        {stale ? (
          <Badge tone="warning" className="ml-auto" title="No activity for over a week">
            <Hourglass className="size-3" aria-hidden /> stale
          </Badge>
        ) : s.last_activity_at ? (
          <span className="ml-auto text-fg-3">
            <RelativeTime iso={s.last_activity_at} />
          </span>
        ) : null}
      </div>
    </Link>
  );
}
