// The daily check-up as an action list: what failed or warned today, grouped
// by area, each with what to do about it — and, since items recur day after
// day, since when it has been there, whether it is still there (Re-check),
// the task it already became, and a way to put it aside (snooze). Items that
// were open last time and are gone now are listed as fixed.
// `?id=<n>` shows an older check-up from the history strip.

import clsx from "clsx";
import {
  BellOff,
  ChevronRight,
  CircleCheck,
  CircleX,
  ClipboardCheck,
  ExternalLink,
  ListPlus,
  Mail,
  Play,
  RefreshCw,
  TriangleAlert,
} from "lucide-react";
import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import {
  useCheckItemDone,
  useCheckItemSnooze,
  useFixCheckItem,
  useCheckItemTask,
  useCheckup,
  useCheckups,
  useLatestCheckup,
  useProjects,
  useRecheck,
  useRunCheckup,
  useSystemFacts,
} from "@/api/hooks";
import type { CheckItem, Checkup, CheckupSummary, CheckSeverity, ResolvedItem } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Field, Select } from "@/components/ui/Input";
import { Menu } from "@/components/ui/Menu";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton, SkeletonRows } from "@/components/ui/Skeleton";
import { Spinner } from "@/components/ui/Spinner";
import { useToast } from "@/components/ui/Toast";
import { HelpLink } from "@/features/docs/HelpLink";
import { useShell } from "@/features/shell/context";
import { useUser } from "@/lib/auth";
import { ageDays, groupCheckItems, isInternalLink, isNew, isSettled, severityLabel } from "@/lib/checkup";
import { addDays, formatCalendarDate, formatDateTime, formatDuration, todayInTz, weekdayOf } from "@/lib/format";

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

/** What a re-check changed, for the toast: how many of `keys` are now resolved vs still open. */
function recheckOutcome(c: Checkup, keys: string[]) {
  let resolved = 0;
  let open = 0;
  for (const it of c.items) {
    if (!keys.includes(it.key)) continue;
    if (it.resolved_at) resolved++;
    else open++;
  }
  return { resolved, open };
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
  const isLatest = selectedId === null || checkup?.id === history.data?.[0]?.id;
  const recheck = useRecheck(checkup?.id ?? 0);
  const openKeys = checkup ? checkup.items.filter((i) => i.severity !== "ok" && !i.snoozed && !isSettled(i)).map((i) => i.key) : [];

  const runNow = () =>
    run.mutate(undefined, {
      onSuccess: (c) => {
        setParams({});
        toast.success(`Check-up finished — ${c.actions_total} action${c.actions_total === 1 ? "" : "s"}, ${c.actions_new} new`);
      },
      onError: (e) => toast.error(e),
    });
  const recheckAll = () =>
    recheck.mutate(
      {},
      {
        onSuccess: (c) => {
          const { resolved, open } = recheckOutcome(c, openKeys);
          toast.success(
            resolved === 0
              ? `Re-checked ${open} item${open === 1 ? "" : "s"} — all still there`
              : `Re-checked ${resolved + open}: ${resolved} resolved, ${open} still open`,
          );
        },
        onError: (e) => toast.error(e),
      },
    );

  return (
    <div className="mx-auto max-w-[1100px] space-y-4">
      <PageHeader
        title="Daily check-up"
        subtitle={<ScheduleLine />}
        actions={
          <>
            <HelpLink anchor="checkup" />
            {checkup && isLatest && openKeys.length > 0 ? (
              <Button
                variant="secondary"
                size="sm"
                onClick={recheckAll}
                disabled={recheck.isPending || run.isPending}
                title="Run the check behind every open item again, right now"
              >
                {recheck.isPending ? <Spinner className="size-3.5" /> : <RefreshCw className="size-3.5" aria-hidden />}
                {recheck.isPending ? "Re-checking…" : `Re-check ${openKeys.length} open`}
              </Button>
            ) : null}
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
        <CheckupView checkup={checkup} isLatest={isLatest} rechecking={recheck.isPending} />
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

function CheckupView({ checkup: c, isLatest, rechecking }: { checkup: Checkup; isLatest: boolean; rechecking: boolean }) {
  const user = useUser();
  const { groups, ok, snoozed } = groupCheckItems(c.items, c.date);
  const duration = c.finished_at ? new Date(c.finished_at).getTime() - new Date(c.started_at).getTime() : null;
  const pct = c.actions_total ? c.actions_done / c.actions_total : 1;
  const recurring = c.actions_total - c.actions_new;
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
          {c.actions_total ? (
            <span className="text-fg-2" title="New: first seen in this check-up. Recurring: already in the previous one.">
              <strong className="font-semibold text-fg">{c.actions_new}</strong> new · <strong className="font-semibold text-fg">{recurring}</strong> recurring
            </span>
          ) : null}
          {c.actions_snoozed ? (
            <a href="#checkup-snoozed" className="text-fg-3 hover:text-fg-2">
              {c.actions_snoozed} snoozed
            </a>
          ) : null}
          {c.fixed_total ? (
            <a href="#checkup-fixed" className="inline-flex items-center gap-1 text-fg-2 hover:text-fg">
              <CircleCheck className="size-3.5 text-good" aria-hidden /> {c.fixed_total} fixed since last check-up
            </a>
          ) : null}
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
        {isLatest && c.actions_total ? (
          <p className="text-[12px] text-fg-3">
            Findings are as of <RelativeTime iso={c.finished_at ?? c.started_at} />. Re-check an item to see whether it is still there now.
          </p>
        ) : null}
      </section>

      {groups.length ? (
        groups.map((g) => (
          <Panel key={g.category} id={`checkup-${g.category}`} title={<>{g.label} <span className="font-normal text-fg-3">· {g.items.length}</span></>} bodyClassName="p-0">
            <ul className="divide-y divide-line">
              {g.items.map((item) => (
                <li key={item.key}>
                  <CheckRow checkup={c} item={item} canAct={isLatest} rechecking={rechecking} />
                </li>
              ))}
            </ul>
          </Panel>
        ))
      ) : (
        <p className="flex items-center gap-2 rounded-xl border border-good/40 bg-good/8 px-4 py-3 text-[13.5px]">
          <CircleCheck className="size-4 text-good" aria-hidden />
          {c.actions_snoozed ? "Nothing to do today — the only findings are snoozed." : "Nothing to do today — every check passed."}
        </p>
      )}

      {c.resolved.length ? <FixedPanel checkup={c} /> : null}

      {snoozed.length ? (
        <details id="checkup-snoozed" className="group rounded-xl border border-line bg-surface">
          <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-3 text-[13px] font-medium text-fg-2">
            <ChevronRight className="size-4 text-fg-3 transition-transform group-open:rotate-90" aria-hidden />
            <BellOff className="size-3.5 text-fg-3" aria-hidden /> Snoozed ({snoozed.length})
          </summary>
          <ul className="divide-y divide-line border-t border-line">
            {snoozed.map((item) => (
              <li key={item.key}>
                <SnoozedRow checkup={c} item={item} canAct={isLatest} />
              </li>
            ))}
          </ul>
        </details>
      ) : null}

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

/** "new today" or "since N days", from first_seen. Nothing on rows that predate the field. */
function AgeBadge({ item, date }: { item: { first_seen: string }; date: string }) {
  if (!item.first_seen) return null;
  if (isNew(item, date)) return <Badge tone="accent">new today</Badge>;
  const days = ageDays(item, date);
  return (
    <Badge tone="outline" title={`First seen ${formatCalendarDate(item.first_seen)}`}>
      since {days === 1 ? "yesterday" : `${days} days`}
    </Badge>
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

/** The item's task, when it has one: ref + status, or "task done" when the item outlived it. */
function TaskLink({ item }: { item: CheckItem }) {
  const { openTask } = useShell();
  if (!item.task_id || !item.task_ref) return null;
  const done = item.task_status === "done";
  return (
    <button
      type="button"
      onClick={() => openTask(item.task_id!)}
      className={clsx("inline-flex items-center gap-1 font-mono text-[12px] hover:underline", done ? "text-fg-3" : "text-accent")}
      title={done ? "The task was closed, but the finding is still here" : `Task ${item.task_status.replace("_", " ")}`}
    >
      <ListPlus className="size-3.5" aria-hidden /> {item.task_ref}
      {item.task_status ? <span className="font-sans text-[11px] text-fg-3">· {done ? "done" : item.task_status.replace("_", " ")}</span> : null}
    </button>
  );
}

function CheckRow({ checkup, item, canAct, rechecking }: { checkup: Checkup; item: CheckItem; canAct: boolean; rechecking: boolean }) {
  const user = useUser();
  const done = useCheckItemDone(checkup.id);
  const snooze = useCheckItemSnooze(checkup.id);
  const recheck = useRecheck(checkup.id);
  const fix = useFixCheckItem(checkup.id);
  const toast = useToast();
  const [making, setMaking] = useState(false);
  const [fixing, setFixing] = useState<string | null>(null);
  const settled = isSettled(item);
  const resolved = item.resolved_at !== null;
  const taskDone = item.task_id !== null && item.task_status === "done";
  const today = todayInTz(user.timezone);

  const recheckOne = () =>
    recheck.mutate(
      { key: item.key },
      {
        onSuccess: (c) => {
          const fresh = c.items.find((i) => i.key === item.key);
          if (!fresh) return;
          if (fresh.resolved_at) toast.success(`Resolved — "${item.title}" is gone`);
          else toast.info(`Still there: ${fresh.title}`);
        },
        onError: (e) => toast.error(e),
      },
    );
  const snoozeFor = (until: string | null) =>
    snooze.mutate(
      { key: item.key, until },
      {
        onSuccess: () => toast.success(until ? `Snoozed until ${formatCalendarDate(until)}` : "Snoozed until it changes"),
        onError: (e) => toast.error(e),
      },
    );
  const busy = recheck.isPending || rechecking || fix.isPending;
  const applyFix = (id: string, label: string) => {
    setFixing(id);
    fix.mutate(
      { key: item.key, fix: id },
      {
        onSuccess: ({ checkup: c, note, run }) => {
          const fresh = c.items.find((i) => i.key === item.key);
          if (note) toast.error(`${label}: ${note}${run ? ` (run #${run.id})` : ""}`);
          else if (fresh?.resolved_at) toast.success(`${label} — resolved`);
          else toast.info(`${label} — done, but it is still there: ${fresh?.title ?? item.title}`);
        },
        onError: (e) => toast.error(e),
        onSettled: () => setFixing(null),
      },
    );
  };

  return (
    <div className={clsx("flex items-start gap-3 px-3.5 py-3", settled && "opacity-60")}>
      <input
        type="checkbox"
        checked={settled}
        disabled={resolved}
        onChange={(e) => done.mutate({ key: item.key, done: e.target.checked }, { onError: (err) => toast.error(err) })}
        aria-label={resolved ? `"${item.title}" resolved` : item.done ? `Mark "${item.title}" not done` : `Mark "${item.title}" done`}
        title={resolved ? "Verified gone by a re-check" : undefined}
        className="mt-1 size-4 shrink-0 accent-[var(--accent)]"
      />
      <SeverityIcon severity={item.severity} className="mt-0.5 size-4" />
      <div className="min-w-0 flex-1 space-y-0.5">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <p className={clsx("text-[13.5px] font-medium", settled && "line-through")}>{item.title}</p>
          <AgeBadge item={item} date={checkup.date} />
          {resolved ? (
            <Badge tone="good" dot title={formatDateTime(item.resolved_at, user.timezone)}>
              resolved · verified <RelativeTime iso={item.resolved_at} />
            </Badge>
          ) : item.checked_at ? (
            <span className="text-[11.5px] text-fg-3" title={formatDateTime(item.checked_at, user.timezone)}>
              still there · re-checked <RelativeTime iso={item.checked_at} />
            </span>
          ) : null}
          {taskDone ? <Badge tone="warning">task done, still here</Badge> : null}
        </div>
        {item.detail ? <p className="text-[12.5px] break-words text-fg-2">{item.detail}</p> : null}
        {item.action && !resolved ? (
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
          <TaskLink item={item} />
          {item.task_id === null || taskDone ? (
            <button type="button" onClick={() => setMaking(true)} className="inline-flex items-center gap-1 text-[12px] text-fg-3 hover:text-fg">
              <ListPlus className="size-3.5" aria-hidden /> {taskDone ? "Make another task" : "Make task"}
            </button>
          ) : null}
          {canAct && !resolved
            ? item.fixes?.map((f) => (
                <Button
                  key={f.id}
                  size="sm"
                  variant="subtle"
                  className="h-6 px-2 text-[12px]"
                  disabled={busy}
                  loading={fixing === f.id}
                  onClick={() => applyFix(f.id, f.label)}
                  title={`${f.label}, then re-check`}
                >
                  {f.label}
                </Button>
              ))
            : null}
          {canAct && !resolved ? (
            <>
              <button
                type="button"
                onClick={recheckOne}
                disabled={busy}
                className="inline-flex items-center gap-1 text-[12px] text-fg-3 hover:text-fg disabled:opacity-60"
                title="Run this check again now"
              >
                {recheck.isPending ? <Spinner className="size-3.5" /> : <RefreshCw className="size-3.5" aria-hidden />} Re-check
              </button>
              <Menu
                label={`Snooze "${item.title}"`}
                trigger={
                  <span className="inline-flex items-center gap-1 text-[12px] text-fg-3 hover:text-fg">
                    <BellOff className="size-3.5" aria-hidden /> Snooze
                  </span>
                }
                items={[
                  { label: "Hide for 7 days", onSelect: () => snoozeFor(addDays(today, 7)) },
                  { label: "Hide for 30 days", onSelect: () => snoozeFor(addDays(today, 30)) },
                  "separator",
                  { label: "Hide until it changes", onSelect: () => snoozeFor(null) },
                ]}
              />
            </>
          ) : null}
        </div>
      </div>
      {making ? <MakeTaskDialog checkupId={checkup.id} item={item} onClose={() => setMaking(false)} /> : null}
    </div>
  );
}

function SnoozedRow({ checkup, item, canAct }: { checkup: Checkup; item: CheckItem; canAct: boolean }) {
  const snooze = useCheckItemSnooze(checkup.id);
  const toast = useToast();
  return (
    <div className="flex items-start gap-3 px-4 py-2.5 text-[13px]">
      <SeverityIcon severity={item.severity} className="mt-0.5 size-3.5" />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-fg">{item.title}</span>
          <AgeBadge item={item} date={checkup.date} />
          <span className="text-[11.5px] text-fg-3">{item.snoozed_until ? `until ${formatCalendarDate(item.snoozed_until)}` : "until it changes"}</span>
        </div>
        <div className="flex flex-wrap items-center gap-x-3 pt-0.5">
          {item.project_key ? <span className="font-mono text-[11.5px] text-fg-3">{item.project_key}</span> : null}
          <TaskLink item={item} />
          {canAct ? (
            <button
              type="button"
              onClick={() => snooze.mutate({ key: item.key, until: "off" }, { onError: (e) => toast.error(e) })}
              disabled={snooze.isPending}
              className="text-[12px] text-fg-3 hover:text-fg"
            >
              Unsnooze
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}

/** Items open in the previous check-up and gone in this one. */
function FixedPanel({ checkup: c }: { checkup: Checkup }) {
  const user = useUser();
  const { openTask } = useShell();
  const since = (r: ResolvedItem) => {
    const days = r.first_seen ? Math.max(0, ageDays(r, c.date)) : 0;
    return days > 1 ? `was open ${days} days` : days === 1 ? "was open since yesterday" : "appeared in the previous check-up";
  };
  return (
    <Panel
      id="checkup-fixed"
      title={
        <>
          Fixed since last check-up <span className="font-normal text-fg-3">· {c.resolved.length}</span>
        </>
      }
      bodyClassName="p-0"
    >
      <ul className="divide-y divide-line">
        {c.resolved.map((r) => (
          <li key={r.key} className="flex items-start gap-2.5 px-4 py-2.5 text-[13px]">
            <CircleCheck className="mt-0.5 size-3.5 shrink-0 text-good" aria-label="Fixed" role="img" />
            <span className="min-w-0 flex-1">
              <span className="text-fg line-through decoration-fg-3">{r.title}</span>
              <span className="text-fg-3" title={`Last seen in the check-up of ${formatDateTime(r.since_at, user.timezone)}`}>
                {" "}
                — {since(r)}
              </span>
            </span>
            {r.task_ref ? (
              <button type="button" onClick={() => void openTask} className="shrink-0 font-mono text-[11.5px] text-fg-3" title="The task made from it">
                {r.task_ref}
              </button>
            ) : null}
            {r.project_key ? <span className="shrink-0 font-mono text-[11px] text-fg-3">{r.project_key}</span> : null}
          </li>
        ))}
      </ul>
    </Panel>
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
      <Field label="Project" hint="The task gets the label checkup and the item's detail as its description. Tomorrow's check-up keeps the link while the task is open.">
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
        {summary.actions_new ? <Badge tone="accent">{summary.actions_new} new</Badge> : null}
        {summary.fixed_total ? (
          <span className="inline-flex items-center gap-1 text-fg-3">
            <CircleCheck className="size-3.5 text-good" aria-hidden /> {summary.fixed_total} fixed
          </span>
        ) : null}
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
