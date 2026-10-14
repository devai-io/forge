// The project's focus: what to do now, what comes next, the roadmap and what
// blocks it. Forge recomputes it a couple of minutes after tasks, runs or the
// check-up change the project (by the model when a provider key is set, by
// rules otherwise); "Recalculate" does it on the spot.

import clsx from "clsx";
import {
  Bot,
  Check,
  CircleDot,
  ClipboardCheck,
  Compass,
  GitBranch,
  Globe,
  ListTodo,
  OctagonAlert,
  RefreshCw,
  Sparkles,
} from "lucide-react";
import { useProjectFocus, useRefreshFocus } from "@/api/hooks";
import type { FocusItem, FocusKind, ProjectFocus, RoadmapStep } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { ErrorState } from "@/components/ui/EmptyState";
import { Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { useShell } from "@/features/shell/context";
import { formatSpend, usageCost } from "@/lib/assistant";

const KIND_ICON: Record<FocusKind, typeof ListTodo> = {
  task: ListTodo,
  checkup: ClipboardCheck,
  repo: GitBranch,
  endpoint: Globe,
  run: Bot,
  other: CircleDot,
};

export function FocusPanel({ projectKey, className }: { projectKey: string; className?: string }) {
  const focus = useProjectFocus(projectKey);
  const refresh = useRefreshFocus(projectKey);
  const toast = useToast();
  return (
    <Panel
      title="Focus"
      icon={<Compass />}
      id="proj-focus"
      className={className}
      bodyClassName="p-0"
      actions={
        <Button
          size="sm"
          variant="ghost"
          loading={refresh.isPending}
          onClick={() => refresh.mutate(undefined, { onError: (e) => toast.error(e) })}
          title="Work out the focus again now"
        >
          {!refresh.isPending ? <RefreshCw className="size-3.5" aria-hidden /> : null} Recalculate
        </Button>
      }
    >
      {focus.isPending ? (
        <SkeletonRows rows={4} className="p-3" />
      ) : focus.error ? (
        <ErrorState error={focus.error} onRetry={() => focus.refetch()} />
      ) : (
        <FocusBody f={focus.data} />
      )}
    </Panel>
  );
}

function FocusBody({ f }: { f: ProjectFocus }) {
  const { focus } = f;
  return (
    <div className="divide-y divide-line">
      {focus.headline ? <p className="px-4 py-3 text-[14px] font-medium text-fg">{focus.headline}</p> : null}

      {focus.blockers.length ? (
        <section aria-label="Blockers" className="bg-critical/6 px-4 py-2.5">
          <h3 className="mb-1 flex items-center gap-1.5 text-[11px] font-semibold tracking-wide text-critical-ink uppercase">
            <OctagonAlert className="size-3.5" aria-hidden /> Blocked
          </h3>
          <ul className="space-y-1">
            {focus.blockers.map((it, i) => (
              <ItemRow key={i} item={it} f={f} />
            ))}
          </ul>
        </section>
      ) : null}

      <div className="grid grid-cols-1 md:grid-cols-2 md:divide-x md:divide-line">
        <section aria-label="Now" className="px-4 py-3">
          <h3 className="mb-1.5 text-[11px] font-semibold tracking-wide text-fg-3 uppercase">Now</h3>
          {focus.now.length ? (
            <ol className="space-y-2">
              {focus.now.map((it, i) => (
                <ItemRow key={i} item={it} f={f} index={i + 1} />
              ))}
            </ol>
          ) : (
            <p className="text-[13px] text-fg-3">Nothing needs you right now.</p>
          )}
        </section>
        <section aria-label="Next" className="border-t border-line px-4 py-3 md:border-t-0">
          <h3 className="mb-1.5 text-[11px] font-semibold tracking-wide text-fg-3 uppercase">Next</h3>
          {focus.next.length ? (
            <ul className="space-y-1.5">
              {focus.next.map((it, i) => (
                <ItemRow key={i} item={it} f={f} compact />
              ))}
            </ul>
          ) : (
            <p className="text-[13px] text-fg-3">Nothing queued after that.</p>
          )}
        </section>
      </div>

      {focus.roadmap.length ? <Roadmap steps={focus.roadmap} f={f} /> : null}

      <footer className="flex flex-wrap items-center gap-x-2 gap-y-1 px-4 py-2 text-[11.5px] text-fg-3">
        {f.source === "ai" ? <Sparkles className="size-3" aria-hidden /> : null}
        <span>
          Updated <RelativeTime iso={f.generated_at} />
          {f.trigger ? <> · {f.trigger}</> : null} · {f.source === "ai" ? `AI${f.model ? ` (${f.model})` : ""}` : "rules"}
        </span>
        {usageCost(f.usage) !== null ? (
          <span title="What the model calls for this project's focus cost: the latest computation, and in all">
            · {usageCost(f.last_usage) !== null ? `${formatSpend(usageCost(f.last_usage))} this time, ` : ""}
            {formatSpend(usageCost(f.usage))} in all
          </span>
        ) : null}
        {f.dirty ? (
          <span className="inline-flex items-center gap-1 text-fg-2" title={f.dirty_reason ? `Changed: ${f.dirty_reason}` : undefined}>
            · <span className="size-1.5 rounded-full bg-warning" aria-hidden /> stale — recalculating soon
          </span>
        ) : null}
        {f.error ? <span className="w-full text-critical-ink">{f.error}</span> : null}
      </footer>
    </div>
  );
}

function RefChip({ refName, f }: { refName: string; f: ProjectFocus }) {
  const { openTask } = useShell();
  const t = f.tasks[refName];
  if (!t) return <span className="font-mono text-[11px] text-fg-3">{refName}</span>;
  return (
    <button
      type="button"
      onClick={() => openTask(t.id)}
      title={`${t.title} (${t.status.replace("_", " ")})`}
      className={clsx(
        "rounded border border-line px-1 font-mono text-[11px] hover:border-accent hover:text-fg",
        t.status === "done" ? "text-fg-3 line-through" : "text-fg-2",
      )}
    >
      {refName}
    </button>
  );
}

function ItemRow({ item, f, index, compact }: { item: FocusItem; f: ProjectFocus; index?: number; compact?: boolean }) {
  const Icon = KIND_ICON[item.kind] ?? CircleDot;
  const done = item.ref ? f.tasks[item.ref]?.status === "done" : false;
  return (
    <li className="flex min-w-0 gap-2">
      {index !== undefined ? (
        <span
          className={clsx(
            "mt-px flex size-5 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold",
            done ? "bg-good/15 text-good-ink" : "bg-accent/12 text-fg",
          )}
          aria-hidden
        >
          {done ? <Check className="size-3" /> : index}
        </span>
      ) : (
        <Icon className="mt-0.5 size-3.5 shrink-0 text-fg-3" aria-label={item.kind} />
      )}
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className={clsx("text-[13px]", !compact && "font-medium", done && "text-fg-3 line-through")}>{item.title}</span>
          {item.ref ? <RefChip refName={item.ref} f={f} /> : null}
          {index !== undefined && item.kind !== "task" ? <Icon className="size-3 text-fg-3" aria-label={item.kind} /> : null}
        </div>
        {item.why && !compact ? <p className="text-[12px] text-fg-2">{item.why}</p> : null}
      </div>
    </li>
  );
}

const STEP_DOT: Record<RoadmapStep["status"], string> = {
  done: "bg-good border-good",
  active: "bg-accent border-accent ring-3 ring-accent/20",
  planned: "bg-surface border-line-strong",
};

function Roadmap({ steps, f }: { steps: RoadmapStep[]; f: ProjectFocus }) {
  return (
    <section aria-label="Roadmap" className="px-4 py-3">
      <h3 className="mb-2 text-[11px] font-semibold tracking-wide text-fg-3 uppercase">Roadmap</h3>
      <ol className="relative space-y-2.5 before:absolute before:top-1.5 before:bottom-1.5 before:left-[5px] before:w-px before:bg-line">
        {steps.map((s, i) => (
          <li key={i} className="relative flex min-w-0 items-start gap-2.5 pl-0">
            <span className={clsx("relative mt-1 size-[11px] shrink-0 rounded-full border-2", STEP_DOT[s.status])} aria-hidden />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-1.5">
                <span className={clsx("text-[13px]", s.status === "active" && "font-semibold", s.status === "done" && "text-fg-3")}>
                  {s.title}
                </span>
                <span className="sr-only">({s.status})</span>
                {s.refs.map((r) => (
                  <RefChip key={r} refName={r} f={f} />
                ))}
              </div>
            </div>
          </li>
        ))}
      </ol>
    </section>
  );
}
