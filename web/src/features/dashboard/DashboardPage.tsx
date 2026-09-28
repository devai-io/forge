// The daily view: what to do today, what is on fire, how every project is
// moving, and whether the week is on track.

import {
  Activity as ActivityIcon,
  AlarmClock,
  Ban,
  Bot,
  CalendarCheck,
  CircleAlert,
  CircleCheck,
  Circle,
  Rocket,
  X,
  Flame,
  FolderKanban,
  ListTodo,
  Plus,
  Star,
  TrendingUp,
  WifiOff,
} from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { useDashboard, useUpdateTask } from "@/api/hooks";
import type { Dashboard, Task } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Panel } from "@/components/ui/Panel";
import { Skeleton } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { RunRow } from "@/features/agents/RunBits";
import { roleOf } from "@/lib/agents";
import { CheckupPanel } from "@/features/checkup/CheckupPage";
import { ProjectCard } from "@/features/projects/ProjectCard";
import { useShell } from "@/features/shell/context";
import { FocusRow } from "@/features/tasks/TaskCard";
import { ClaudeSessionsPanel } from "@/features/terminal/SessionStrips";
import { useUser } from "@/lib/auth";
import { greeting, hourInTz, longDate } from "@/lib/format";
import { dismissGettingStarted, docsRead, gettingStartedDismissed, gettingStartedSteps } from "@/lib/gettingStarted";
import { ActivityFeed } from "./ActivityFeed";
import { CompletionsChart, GoalRing, StatTile } from "./charts";

export function DashboardPage() {
  const dash = useDashboard();
  const user = useUser();

  if (dash.isPending) return <DashboardSkeleton />;
  if (dash.error) return <ErrorState error={dash.error} onRetry={() => dash.refetch()} />;
  const d = dash.data;
  const name = (user.display_name || user.username).split(" ")[0];

  return (
    <div className="mx-auto max-w-[1400px] space-y-5">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">
            {greeting(hourInTz(user.timezone))}, {name}
          </h1>
          <p className="text-[13px] text-fg-3">{longDate(d.today)}</p>
        </div>
      </div>

      {d.runners.length === 0 ? <GettingStarted d={d} /> : null}
      <Alerts d={d} />
      <CheckupPanel summary={d.checkup ?? null} />
      <Stats d={d} />

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          <FocusPanel d={d} />
          {d.overdue.length ? <OverduePanel d={d} /> : null}
        </div>
        <Panel title="Completions" icon={<TrendingUp />} id="dash-completions" bodyClassName="p-3">
          <p className="mb-2 text-[12px] text-fg-3">Tasks done per day, last {d.daily.length} days</p>
          <CompletionsChart days={d.daily} today={d.today} />
        </Panel>
      </div>

      <section aria-labelledby="dash-projects" className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 id="dash-projects" className="flex items-center gap-2 text-[13.5px] font-semibold">
            <FolderKanban className="size-4 text-fg-3" aria-hidden /> Projects
          </h2>
          <Link to="/projects" className="text-[12.5px] text-fg-3 hover:text-fg">
            All projects →
          </Link>
        </div>
        {d.projects.length ? (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {d.projects.map((p) => (
              <ProjectCard key={p.id} project={p} today={d.today} />
            ))}
          </div>
        ) : (
          <EmptyState title="No projects yet" />
        )}
      </section>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <div className="space-y-4">
          <ClaudeSessionsPanel />
          <AgentsPanel d={d} />
        </div>
        <Panel title="Activity" icon={<ActivityIcon />} id="dash-activity" className="lg:col-span-2">
          <ActivityFeed items={d.activity} />
        </Panel>
      </div>
    </div>
  );
}

/** A small checklist for a fresh account (no machines yet); dismissible per browser. */
function GettingStarted({ d }: { d: Dashboard }) {
  const user = useUser();
  const [hidden, setHidden] = useState(gettingStartedDismissed);
  if (hidden) return null;
  const steps = gettingStartedSteps({
    machines: d.runners.length,
    projects: d.projects.length,
    totp: user.totp_enabled,
    docsRead: docsRead(),
  });
  const done = steps.filter((s) => s.done).length;
  return (
    <section aria-labelledby="dash-start" className="rounded-xl border border-line bg-surface p-3.5">
      <div className="mb-2.5 flex items-center gap-2">
        <Rocket className="size-4 text-fg-3" aria-hidden />
        <h2 id="dash-start" className="text-[13.5px] font-semibold">
          Get started
        </h2>
        <span className="tabular text-[12px] text-fg-3">
          {done} of {steps.length}
        </span>
        <button
          type="button"
          onClick={() => {
            dismissGettingStarted();
            setHidden(true);
          }}
          className="ml-auto grid size-7 place-items-center rounded-md text-fg-3 hover:bg-surface-2 hover:text-fg"
          aria-label="Dismiss getting started"
          title="Dismiss"
        >
          <X className="size-4" />
        </button>
      </div>
      <ol className="grid grid-cols-1 gap-2 sm:grid-cols-2 xl:grid-cols-4">
        {steps.map((s, i) => (
          <li key={s.id}>
            <Link
              to={s.to}
              className="flex h-full items-start gap-2.5 rounded-lg border border-line px-3 py-2.5 hover:bg-surface-2"
            >
              {s.done ? (
                <CircleCheck className="mt-0.5 size-4 shrink-0 text-good" aria-hidden />
              ) : (
                <Circle className="mt-0.5 size-4 shrink-0 text-fg-3" aria-hidden />
              )}
              <span className="min-w-0">
                <span className={s.done ? "block text-[13px] text-fg-3 line-through" : "block text-[13px] font-medium"}>
                  {i + 1}. {s.label}
                </span>
                <span className="block text-[12px] text-fg-3">{s.hint}</span>
                <span className="sr-only">{s.done ? " (done)" : " (to do)"}</span>
              </span>
            </Link>
          </li>
        ))}
      </ol>
    </section>
  );
}

function Alerts({ d }: { d: Dashboard }) {
  // Only the master is expected to be always on; the Mac and laptops sleep.
  const offline = d.runners.filter((r) => !r.online && roleOf(r) === "master");
  const items: { key: string; icon: React.ReactNode; text: React.ReactNode; to: string }[] = [];
  for (const e of d.endpoints_down) {
    items.push({
      key: `e${e.id}`,
      icon: <CircleAlert className="size-4 text-critical" aria-hidden />,
      text: (
        <>
          <strong className="font-semibold">{e.name}</strong> is down
          {e.last_code ? ` (HTTP ${e.last_code})` : e.last_error ? ` — ${e.last_error}` : ""} · {e.project_key}
        </>
      ),
      to: "/infra",
    });
  }
  if (offline.length) {
    items.push({
      key: "runners",
      icon: <WifiOff className="size-4 text-warning" aria-hidden />,
      text: (
        <>
          Master offline: <strong className="font-semibold">{offline.map((r) => r.name).join(", ")}</strong> — nothing runs, scans or attaches until it is back
        </>
      ),
      to: "/agents",
    });
  }
  if (d.stats.blocked > 0) {
    items.push({
      key: "blocked",
      icon: <Ban className="size-4 text-warning" aria-hidden />,
      text: (
        <>
          <strong className="font-semibold">{d.stats.blocked}</strong> blocked task{d.stats.blocked > 1 ? "s" : ""}
        </>
      ),
      to: "/tasks?status=blocked",
    });
  }
  if (!items.length) return null;
  return (
    <ul aria-label="Alerts" className="flex flex-wrap gap-2">
      {items.map((i) => (
        <li key={i.key}>
          <Link
            to={i.to}
            className="flex items-center gap-2 rounded-lg border border-line-strong bg-surface px-3 py-1.5 text-[13px] hover:bg-surface-2"
          >
            {i.icon}
            <span>{i.text}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}

function Stats({ d }: { d: Dashboard }) {
  const s = d.stats;
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
      <StatTile
        label="Streak"
        icon={<Flame />}
        value={`${s.streak_days}d`}
        sub={<span>best {s.best_streak}d</span>}
      />
      <StatTile label="Done today" icon={<CalendarCheck />} value={s.done_today} sub={<span>{s.due_soon} due soon</span>} />
      <div className="col-span-2 flex items-center gap-3 rounded-xl border border-line bg-surface p-3.5 sm:col-span-1">
        <GoalRing done={s.done_week} goal={s.weekly_goal} />
        <div className="min-w-0">
          <div className="text-[12px] text-fg-2">Weekly goal</div>
          <div className="text-[22px] leading-tight font-semibold">
            {s.done_week}
            <span className="text-[14px] font-normal text-fg-3"> / {s.weekly_goal}</span>
          </div>
          <div className="text-[11.5px] text-fg-3">
            {s.done_week - s.done_prev_week === 0 ? (
              "= last week"
            ) : (
              <span className={s.done_week >= s.done_prev_week ? "font-medium text-good-ink" : "font-medium text-critical-ink"}>
                {s.done_week > s.done_prev_week ? "▲" : "▼"} {Math.abs(s.done_week - s.done_prev_week)} vs last week
              </span>
            )}
          </div>
        </div>
      </div>
      <StatTile label="Open" icon={<ListTodo />} value={s.open_tasks} sub={<span>{s.in_progress} in progress</span>} />
      <StatTile
        label="Overdue"
        icon={<AlarmClock />}
        tone={s.overdue > 0 ? "critical" : undefined}
        value={s.overdue}
        sub={<span>{s.overdue > 0 ? "needs a date or a push" : "nothing late"}</span>}
      />
      <StatTile
        label="Blocked"
        icon={<Ban />}
        tone={s.blocked > 0 ? "warning" : undefined}
        value={s.blocked}
        sub={<span>{s.blocked > 0 ? "waiting on something" : "all clear"}</span>}
      />
    </div>
  );
}

function useCompleteWithUndo() {
  const update = useUpdateTask();
  const toast = useToast();
  return (task: Task) => {
    if (task.status === "done") return;
    const previous = task.status;
    update.mutate(
      { id: task.id, status: "done" },
      {
        onSuccess: () =>
          toast.success(
            <span>
              Completed <span className="font-mono">{task.ref}</span>{" "}
              <button
                type="button"
                className="ml-1 font-medium text-accent hover:underline"
                onClick={() => update.mutate({ id: task.id, status: previous })}
              >
                Undo
              </button>
            </span>,
          ),
        onError: (e) => toast.error(e),
      },
    );
  };
}

function FocusPanel({ d }: { d: Dashboard }) {
  const { openTask, newTask } = useShell();
  const complete = useCompleteWithUndo();
  return (
    <Panel
      title="Today's focus"
      icon={<Star />}
      id="dash-focus"
      actions={
        <Button size="sm" variant="ghost" onClick={() => newTask({ focus: true })}>
          <Plus className="size-3.5" aria-hidden /> Add
        </Button>
      }
    >
      {d.focus.length ? (
        d.focus.map((t) => <FocusRow key={t.id} task={t} today={d.today} onOpen={openTask} onComplete={complete} />)
      ) : (
        <EmptyState compact icon={<Star />} title="No focus tasks">
          Star a task (or add one here) to put it on today's list.
        </EmptyState>
      )}
    </Panel>
  );
}

function OverduePanel({ d }: { d: Dashboard }) {
  const { openTask } = useShell();
  const complete = useCompleteWithUndo();
  return (
    <Panel title={`Overdue · ${d.overdue.length}`} icon={<AlarmClock />} id="dash-overdue">
      {d.overdue.map((t) => (
        <FocusRow key={t.id} task={t} today={d.today} onOpen={openTask} onComplete={complete} />
      ))}
    </Panel>
  );
}

function AgentsPanel({ d }: { d: Dashboard }) {
  const { newRun } = useShell();
  const runs = [...d.active_runs, ...d.recent_runs];
  return (
    <Panel
      title="Agents"
      icon={<Bot />}
      id="dash-agents"
      actions={
        <Button size="sm" variant="ghost" onClick={() => newRun()}>
          <Plus className="size-3.5" aria-hidden /> Run
        </Button>
      }
    >
      <div className="mb-1 flex flex-wrap gap-x-3 gap-y-1 px-2 pt-1 text-[12px] text-fg-3">
        {d.runners.length ? (
          d.runners.filter((r) => r.online || roleOf(r) === "master").map((r) => (
            <span key={r.id} className="inline-flex items-center gap-1.5">
              <span className={r.online ? "size-1.5 rounded-full bg-good" : "size-1.5 rounded-full bg-fg-3"} aria-hidden />
              {r.name} <span className="sr-only">{r.online ? "online" : "offline"}</span>
              {r.running ? <span className="text-fg-2">· {r.running} running</span> : null}
            </span>
          ))
        ) : (
          <Link to="/agents" className="hover:text-fg-2">
            No machines yet — add one →
          </Link>
        )}
      </div>
      {runs.length ? (
        runs.map((r) => <RunRow key={r.id} run={r} />)
      ) : (
        <p className="px-2 py-3 text-[13px] text-fg-3">No runs yet.</p>
      )}
    </Panel>
  );
}

function DashboardSkeleton() {
  return (
    <div className="mx-auto max-w-[1400px] space-y-5" role="status" aria-label="Loading dashboard">
      <Skeleton className="h-8 w-64" />
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
        {Array.from({ length: 6 }, (_, i) => (
          <Skeleton key={i} className="h-24" />
        ))}
      </div>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Skeleton className="h-64 lg:col-span-2" />
        <Skeleton className="h-64" />
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {Array.from({ length: 6 }, (_, i) => (
          <Skeleton key={i} className="h-52" />
        ))}
      </div>
    </div>
  );
}
