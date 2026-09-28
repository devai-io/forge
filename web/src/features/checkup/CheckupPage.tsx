// The daily check-up as an action list: what failed or warned today, grouped
// by area, each with what to do about it — tick it off or turn it into a task.
// `?id=<n>` shows an older check-up from the history strip.

import clsx from "clsx";
import { ChevronRight, CircleCheck, CircleX, ClipboardCheck, ExternalLink, ListPlus, Mail, Play, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import {
  useCheckItemDone,
  useCheckItemTask,
  useCheckup,
  useCheckups,
  useLatestCheckup,
  useProjects,
  useRunCheckup,
  useSystemFacts,
} from "@/api/hooks";
import type { CheckItem, Checkup, CheckupSummary, CheckSeverity } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Field, Select } from "@/components/ui/Input";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton, SkeletonRows } from "@/components/ui/Skeleton";
import { Spinner } from "@/components/ui/Spinner";
import { useToast } from "@/components/ui/Toast";
import { HelpLink } from "@/features/docs/HelpLink";
import { useShell } from "@/features/shell/context";
import { useUser } from "@/lib/auth";
import { groupCheckItems, isInternalLink, severityLabel } from "@/lib/checkup";
import { formatCalendarDate, formatDateTime, formatDuration, weekdayOf } from "@/lib/format";

export function SeverityIcon({ severity, className }: { severity: CheckSeverity; className?: string }) {
  const cls = clsx("shrink-0", className ?? "size-4");
  if (severity === "fail") return <CircleX className={clsx(cls, "text-critical")} aria-label="Failing" role="img" />;
  if (severity === "warn") return <TriangleAlert className={clsx(cls, "text-warning")} aria-label="Warning" role="img" />;
  return <CircleCheck className={clsx(cls, "text-good")} aria-label="OK" role="img" />;
}

export function CheckupStatusBadge({ status }: { status: CheckSeverity }) {
  return (
    <Badge tone={status === "fail" ? "critical" : status === "warn" ? "warning" : "good"} dot>
      {status === "fail" ? "Needs action" : status === "warn" ? "Warnings" : "All clear"}
    </Badge>
  );
}

/** "Runs daily at 07:30 Europe/Lisbon · next in 9h · e-mail on", from the server's own settings. */
function ScheduleLine() {
  const facts = useSystemFacts();
  const user = useUser();
  if (!facts.data) return <>Every day Forge checks endpoints, certificates, hosts, jobs, alerts, backups, runners, repos, CI and the vault.</>;
  const emailOn = facts.data.features.smtp && user.checkup_email;
  return (
    <>
      Runs daily at <span className="font-medium text-fg-2">{facts.data.intervals.checkup_time}</span> {facts.data.intervals.timezone} · next{" "}
      <RelativeTime iso={facts.data.checkup.next_at} /> ·{" "}
      <span title={facts.data.features.smtp ? "Toggle in Settings → Daily check-up" : "SMTP is not configured on the server"}>
        e-mail {emailOn ? "on" : "off"}
      </span>
    </>
  );
}

export function CheckupPage() {
  const [params, setParams] = useSearchParams();
  const raw = Number(params.get("id"));
  const selectedId = Number.isInteger(raw) && raw > 0 ? raw : null;
  const latest = useLatestCheckup(selectedId === null);
  const selected = useCheckup(selectedId);
  const history = useCheckups(30);
  const run = useRunCheckup();
  const toast = useToast();
  const query = selectedId === null ? latest : selected;
  const checkup = query.data ?? null;

  const runNow = () =>
    run.mutate(undefined, {
      onSuccess: (c) => {
        setParams({});
        toast.success(`Check-up finished — ${c.actions_total} action${c.actions_total === 1 ? "" : "s"}`);
      },
      onError: (e) => toast.error(e),
    });

  return (
    <div className="mx-auto max-w-[1100px] space-y-4">
      <PageHeader
        title="Daily check-up"
        subtitle={<ScheduleLine />}
        actions={
          <>
            <HelpLink anchor="checkup" />
            <Button variant="primary" size="sm" onClick={runNow} disabled={run.isPending}>
            {run.isPending ? <Spinner className="size-3.5 text-accent-fg" /> : <Play className="size-3.5" aria-hidden />}
              {run.isPending ? "Checking…" : "Run now"}
            </Button>
          </>
        }
      />
      {run.isPending ? (
        <p role="status" className="rounded-lg border border-accent/40 bg-accent/8 px-3 py-2 text-[13px] text-fg-2">
          Running every check across the fleet — this usually takes 10–30 seconds.
        </p>
      ) : null}

      <HistoryStrip history={history.data ?? []} selectedId={checkup?.id ?? null} onSelect={(id) => setParams(id ? { id: String(id) } : {})} />

      {query.isPending ? (
        <div className="space-y-3">
          <Skeleton className="h-28 w-full" />
          <SkeletonRows rows={5} />
        </div>
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => query.refetch()} />
      ) : !checkup ? (
        <EmptyState
          icon={<ClipboardCheck />}
          title="No check-up yet — run one"
          action={
            <Button variant="primary" size="sm" onClick={runNow} loading={run.isPending}>
              Run now
            </Button>
          }
        >
          It also runs by itself every day at the time set in Settings.
        </EmptyState>
      ) : (
        <CheckupView checkup={checkup} isLatest={selectedId === null || checkup.id === history.data?.[0]?.id} />
      )}
    </div>
  );
}

function HistoryStrip({
  history,
  selectedId,
  onSelect,
}: {
  history: CheckupSummary[];
  selectedId: number | null;
  onSelect: (id: number | null) => void;
}) {
  if (!history.length) return null;
  const ordered = [...history].reverse(); // oldest left, newest right
  const newest = history[0]?.id;
  return (
    <div className="flex flex-wrap items-center gap-3">
      <span className="text-[12px] text-fg-3">Last {history.length}</span>
      <div role="list" aria-label="Check-up history" className="flex flex-wrap gap-1">
        {ordered.map((c) => (
          <button
            key={c.id}
            type="button"
            role="listitem"
            onClick={() => onSelect(c.id === newest ? null : c.id)}
            aria-current={c.id === selectedId ? "true" : undefined}
            aria-label={`${c.date}: ${severityLabel(c.status)}, ${c.counts.fail} failing, ${c.counts.warn} warnings`}
            title={`${weekdayOf(c.date)} ${formatCalendarDate(c.date)} · ${c.counts.fail} fail · ${c.counts.warn} warn · ${c.actions_done}/${c.actions_total} done`}
            className={clsx(
              "size-4 rounded-[3px] ring-offset-2 ring-offset-bg transition-transform hover:scale-110",
              c.status === "fail" ? "bg-critical" : c.status === "warn" ? "bg-warning" : "bg-good",
              c.id === selectedId && "ring-2 ring-fg",
            )}
          />
        ))}
      </div>
      <span className="flex items-center gap-2 text-[11px] text-fg-3">
        <span className="inline-flex items-center gap-1">
          <span className="size-2 rounded-[2px] bg-good" aria-hidden /> clear
        </span>
        <span className="inline-flex items-center gap-1">
          <span className="size-2 rounded-[2px] bg-warning" aria-hidden /> warnings
        </span>
        <span className="inline-flex items-center gap-1">
          <span className="size-2 rounded-[2px] bg-critical" aria-hidden /> failing
        </span>
      </span>
    </div>
  );
}

function CheckupView({ checkup: c, isLatest }: { checkup: Checkup; isLatest: boolean }) {
  const user = useUser();
  const { groups, ok } = groupCheckItems(c.items);
  const duration = c.finished_at ? new Date(c.finished_at).getTime() - new Date(c.started_at).getTime() : null;
  const pct = c.actions_total ? c.actions_done / c.actions_total : 1;
  return (
    <div className="space-y-4">
      <section aria-label="Summary" className="space-y-3 rounded-xl border border-line bg-surface p-4">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <SeverityIcon severity={c.status} className="size-5" />
          <h2 className="text-[16px] font-semibold">
            {weekdayOf(c.date)} {formatCalendarDate(c.date)}
          </h2>
          <CheckupStatusBadge status={c.status} />
          {!isLatest ? <Badge tone="outline">older check-up</Badge> : null}
          <span className="text-[12px] text-fg-3">
            {c.trigger === "manual" ? "manual" : "scheduled"} · started {formatDateTime(c.started_at, user.timezone)}
            {duration !== null ? ` · took ${formatDuration(duration)}` : " · still running"}
          </span>
          {c.emailed ? (
            <span className="inline-flex items-center gap-1 text-[12px] text-fg-3">
              <Mail className="size-3.5" aria-hidden /> e-mailed
            </span>
          ) : null}
        </div>
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-[13px]">
          <span className="inline-flex items-center gap-1.5">
            <SeverityIcon severity="fail" className="size-3.5" /> <strong className="font-semibold">{c.counts.fail}</strong> failing
          </span>
          <span className="inline-flex items-center gap-1.5">
            <SeverityIcon severity="warn" className="size-3.5" /> <strong className="font-semibold">{c.counts.warn}</strong> warnings
          </span>
          <span className="inline-flex items-center gap-1.5">
            <SeverityIcon severity="ok" className="size-3.5" /> <strong className="font-semibold">{c.counts.ok}</strong> ok
          </span>
        </div>
        {c.actions_total ? (
          <div className="space-y-1">
            <div className="flex items-baseline justify-between text-[12px]">
              <span className="text-fg-2">
                <span className="font-semibold text-fg">{c.actions_done}</span> of {c.actions_total} actions done
              </span>
              <span className="tabular text-fg-3">{Math.round(pct * 100)}%</span>
            </div>
            <div
              role="meter"
              aria-label="Actions done"
              aria-valuemin={0}
              aria-valuemax={c.actions_total}
              aria-valuenow={c.actions_done}
              className="h-1.5 overflow-hidden rounded-full bg-track"
            >
              <div className="h-full rounded-full bg-series-1 transition-[width] duration-500" style={{ width: `${pct * 100}%` }} />
            </div>
          </div>
        ) : null}
      </section>

      {groups.length ? (
        groups.map((g) => (
          <Panel key={g.category} id={`checkup-${g.category}`} title={<>{g.label} <span className="font-normal text-fg-3">· {g.items.length}</span></>} bodyClassName="p-0">
            <ul className="divide-y divide-line">
              {g.items.map((item) => (
                <li key={item.key}>
                  <CheckRow checkupId={c.id} item={item} />
                </li>
              ))}
            </ul>
          </Panel>
        ))
      ) : (
        <p className="flex items-center gap-2 rounded-xl border border-good/40 bg-good/8 px-4 py-3 text-[13.5px]">
          <CircleCheck className="size-4 text-good" aria-hidden /> Nothing to do today — every check passed.
        </p>
      )}

      {ok.length ? (
        <details className="group rounded-xl border border-line bg-surface">
          <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-3 text-[13px] font-medium text-fg-2">
            <ChevronRight className="size-4 text-fg-3 transition-transform group-open:rotate-90" aria-hidden />
            All clear ({ok.length})
          </summary>
          <ul className="divide-y divide-line border-t border-line">
            {ok.map((item) => (
              <li key={item.key} className="flex items-start gap-2.5 px-4 py-2 text-[13px]">
                <SeverityIcon severity="ok" className="mt-0.5 size-3.5" />
                <span className="min-w-0 flex-1">
                  <span className="text-fg">{item.title}</span>
                  {item.detail ? <span className="text-fg-3"> — {item.detail}</span> : null}
                </span>
                {item.project_key ? <span className="shrink-0 font-mono text-[11px] text-fg-3">{item.project_key}</span> : null}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </div>
  );
}

function ItemLink({ link }: { link: string }) {
  const cls = "inline-flex items-center gap-1 text-[12px] text-accent hover:underline";
  return isInternalLink(link) ? (
    <Link to={link} className={cls}>
      Open <ChevronRight className="size-3" aria-hidden />
    </Link>
  ) : (
    <a href={link} target="_blank" rel="noopener noreferrer" className={cls}>
      Open <ExternalLink className="size-3" aria-hidden />
    </a>
  );
}

function CheckRow({ checkupId, item }: { checkupId: number; item: CheckItem }) {
  const done = useCheckItemDone(checkupId);
  const toast = useToast();
  const { openTask } = useShell();
  const [making, setMaking] = useState(false);
  return (
    <div className={clsx("flex items-start gap-3 px-3.5 py-3", item.done && "opacity-60")}>
      <input
        type="checkbox"
        checked={item.done}
        onChange={(e) => done.mutate({ key: item.key, done: e.target.checked }, { onError: (err) => toast.error(err) })}
        aria-label={item.done ? `Mark "${item.title}" not done` : `Mark "${item.title}" done`}
        className="mt-1 size-4 shrink-0 accent-[var(--accent)]"
      />
      <SeverityIcon severity={item.severity} className="mt-0.5 size-4" />
      <div className="min-w-0 flex-1 space-y-0.5">
        <p className={clsx("text-[13.5px] font-medium", item.done && "line-through")}>{item.title}</p>
        {item.detail ? <p className="text-[12.5px] break-words text-fg-2">{item.detail}</p> : null}
        {item.action ? (
          <p className="text-[12.5px] text-fg">
            <span className="text-fg-3">→ </span>
            {item.action}
          </p>
        ) : null}
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 pt-0.5">
          {item.project_key ? (
            <Link to={`/p/${item.project_key}`} className="font-mono text-[11.5px] text-fg-3 hover:text-fg-2">
              {item.project_key}
            </Link>
          ) : null}
          {item.link ? <ItemLink link={item.link} /> : null}
          {item.task_id && item.task_ref ? (
            <button type="button" onClick={() => openTask(item.task_id!)} className="inline-flex items-center gap-1 font-mono text-[12px] text-accent hover:underline">
              <ListPlus className="size-3.5" aria-hidden /> {item.task_ref}
            </button>
          ) : (
            <button type="button" onClick={() => setMaking(true)} className="inline-flex items-center gap-1 text-[12px] text-fg-3 hover:text-fg">
              <ListPlus className="size-3.5" aria-hidden /> Make task
            </button>
          )}
        </div>
      </div>
      {making ? <MakeTaskDialog checkupId={checkupId} item={item} onClose={() => setMaking(false)} /> : null}
    </div>
  );
}

function MakeTaskDialog({ checkupId, item, onClose }: { checkupId: number; item: CheckItem; onClose: () => void }) {
  const projects = useProjects();
  const make = useCheckItemTask(checkupId);
  const toast = useToast();
  const { openTask } = useShell();
  const [projectKey, setProjectKey] = useState(item.project_key ?? "");
  const effective = projectKey || projects.data?.find((p) => p.key === "SRV")?.key || projects.data?.[0]?.key || "";
  return (
    <Dialog
      open
      onClose={onClose}
      size="sm"
      title="Turn into a task"
      description={item.title}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={make.isPending}
            disabled={!effective}
            onClick={() =>
              make.mutate(
                { key: item.key, project_key: effective || undefined },
                {
                  onSuccess: ({ task }) => {
                    toast.success(
                      <span>
                        Created{" "}
                        <button type="button" className="font-mono text-accent hover:underline" onClick={() => openTask(task.id)}>
                          {task.ref}
                        </button>
                      </span>,
                    );
                    onClose();
                  },
                  onError: (e) => toast.error(e),
                },
              )
            }
          >
            Create task
          </Button>
        </>
      }
    >
      <Field label="Project" hint="The task gets the label checkup and the item's detail as its description.">
        {(id, desc) => (
          <Select id={id} aria-describedby={desc} value={effective} onChange={(e) => setProjectKey(e.target.value)}>
            {projects.data?.map((p) => (
              <option key={p.key} value={p.key}>
                {p.key} · {p.name}
              </option>
            ))}
          </Select>
        )}
      </Field>
      {effective ? (
        <p className="mt-2 inline-flex items-center gap-1.5 text-[12px] text-fg-3">
          <ColorDot color={projects.data?.find((p) => p.key === effective)?.color} className="size-2" /> Goes to the {effective} backlog
        </p>
      ) : null}
    </Dialog>
  );
}

/** Dashboard panel: today's check-up at a glance. */
export function CheckupPanel({ summary }: { summary: CheckupSummary | null }) {
  const run = useRunCheckup();
  const toast = useToast();
  if (!summary) {
    return (
      <div className="flex flex-wrap items-center gap-3 rounded-xl border border-dashed border-line-strong bg-surface px-4 py-3 text-[13px]">
        <ClipboardCheck className="size-4 text-fg-3" aria-hidden />
        <span className="text-fg-2">No check-up yet — run one to get today's action list.</span>
        <Button size="sm" variant="primary" className="ml-auto" loading={run.isPending} onClick={() => run.mutate(undefined, { onError: (e) => toast.error(e) })}>
          Run now
        </Button>
      </div>
    );
  }
  const pct = summary.actions_total ? summary.actions_done / summary.actions_total : 1;
  return (
    <Link
      to="/checkup"
      className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-line bg-surface px-4 py-3 transition-colors hover:border-line-strong"
    >
      <span className="flex items-center gap-2">
        <ClipboardCheck className="size-4 text-fg-3" aria-hidden />
        <span className="text-[13.5px] font-semibold">Daily check-up</span>
      </span>
      <CheckupStatusBadge status={summary.status} />
      <span className="flex items-center gap-3 text-[12.5px] text-fg-2">
        <span className="inline-flex items-center gap-1">
          <SeverityIcon severity="fail" className="size-3.5" /> {summary.counts.fail}
        </span>
        <span className="inline-flex items-center gap-1">
          <SeverityIcon severity="warn" className="size-3.5" /> {summary.counts.warn}
        </span>
        <span className="inline-flex items-center gap-1">
          <SeverityIcon severity="ok" className="size-3.5" /> {summary.counts.ok}
        </span>
      </span>
      {summary.actions_total ? (
        <span className="flex min-w-40 flex-1 items-center gap-2 text-[12px] text-fg-3">
          <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-track" aria-hidden>
            <span className="block h-full rounded-full bg-series-1" style={{ width: `${pct * 100}%` }} />
          </span>
          <span className="tabular">
            {summary.actions_done}/{summary.actions_total} done
          </span>
        </span>
      ) : (
        <span className="flex-1 text-[12px] text-fg-3">nothing to act on</span>
      )}
      <span className="text-[11.5px] text-fg-3">
        <RelativeTime iso={summary.finished_at ?? summary.started_at} /> →
      </span>
    </Link>
  );
}
