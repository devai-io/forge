// The three widgets at the top of the dashboard: are the services up, what
// is being worked on right now, and one tap into each project agent that is
// open (F-<Project>, Remote Control on).

import clsx from "clsx";
import { Bot, CircleAlert, CircleCheck, ExternalLink, Hammer, Plug, RefreshCw, Sparkles, TriangleAlert, Activity as Pulse } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useMonitoring, useProjectSessions, useServers } from "@/api/hooks";
import type { Dashboard, ProjectSession, Run } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton } from "@/components/ui/Skeleton";
import { RunStatusBadge } from "@/features/agents/RunBits";
import { RefreshWorkDialog } from "@/features/dashboard/RefreshWork";
import { useShell } from "@/features/shell/context";
import { runTitle } from "@/lib/agents";
import { investigationProject, investigationPrompt, prodFirst, serviceIssues, type ServiceIssue } from "@/lib/monitoring";
import { shortHome } from "@/lib/paths";
import { isOpen, promptTitle, sessionStatus, type AgentTone } from "@/lib/projectAgents";
import { attachPath } from "@/lib/terminal";

const SHOWN = 5;

export function TopWidgets({ d }: { d: Dashboard }) {
  return (
    <div className="grid grid-cols-1 items-start gap-4 lg:grid-cols-3">
      <ServicesWidget d={d} />
      <WorkingOnWidget d={d} />
      <ProjectSessionsWidget />
    </div>
  );
}

/**
 * 1) Services: all clear, or which ones are failing or warning — with an
 * agent run one click away that looks into them (all, or one), in the
 * project they belong to.
 */
export function ServicesWidget({ d }: { d: Dashboard }) {
  const mon = useMonitoring();
  const servers = useServers();
  const { newRun } = useShell();
  const issues = prodFirst(serviceIssues({ down: d.endpoints_down, warning: d.endpoints_warning ?? [] }, mon.data), servers.data ?? []);
  const fail = issues.filter((i) => i.level === "fail").length;
  const warn = issues.length - fail;
  const level = fail ? "fail" : warn ? "warn" : "ok";
  const investigate = (list: ServiceIssue[]) =>
    newRun({ project_key: investigationProject(list, servers.data ?? []), prompt: investigationPrompt(list) });

  return (
    <Panel
      title="Services"
      icon={<Pulse />}
      id="dash-services"
      actions={
        <span className="flex items-center gap-2">
          {issues.length ? (
            <Button
              size="sm"
              variant="subtle"
              onClick={() => investigate(issues)}
              title="Start an agent run that looks into these issues"
            >
              <Bot className="size-3.5" aria-hidden />
              Investigate
              {issues.length > 1 ? <span className="hidden sm:inline">all {issues.length}</span> : null}
            </Button>
          ) : null}
          <Link to="/monitoring" className="hidden text-[12px] text-fg-3 hover:text-fg sm:inline">
            Monitoring →
          </Link>
        </span>
      }
    >
      <div className="flex items-center gap-2.5 px-2 pt-1 pb-2" data-level={level}>
        <span
          aria-hidden
          className={clsx(
            "size-2.5 shrink-0 rounded-full",
            level === "fail" ? "glow-critical bg-critical" : level === "warn" ? "bg-warning" : "bg-good",
          )}
        />
        <p className="text-[15px] font-semibold">
          {level === "ok" ? (
            "All services OK"
          ) : (
            <>
              {fail ? `${fail} failing` : null}
              {fail && warn ? " · " : null}
              {warn ? `${warn} warning` : null}
            </>
          )}
        </p>
        {mon.isPending ? <span className="text-[11.5px] text-fg-3">checking hosts…</span> : null}
      </div>
      {issues.length ? (
        <ul aria-label="Services with problems">
          {issues.slice(0, SHOWN).map((i) => (
            <li key={i.key} className="group flex items-start gap-1">
              <Link to={i.to} className="flex min-w-0 flex-1 items-start gap-2 rounded-md px-2 py-1.5 hover:bg-surface-2">
                {i.level === "fail" ? (
                  <CircleAlert className="mt-0.5 size-3.5 shrink-0 text-critical" aria-label="Failing" />
                ) : (
                  <TriangleAlert className="mt-0.5 size-3.5 shrink-0 text-warning" aria-label="Warning" />
                )}
                <span className="min-w-0">
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span className="truncate text-[13px]">{i.title}</span>
                    {i.prod ? (
                      <Badge tone="critical" className="h-4 shrink-0 px-1 text-[10px] font-semibold tracking-wide">
                        PROD
                      </Badge>
                    ) : null}
                  </span>
                  {i.detail ? <span className="block truncate text-[11.5px] text-fg-3">{i.detail}</span> : null}
                </span>
              </Link>
              <Button
                size="icon-sm"
                variant="ghost"
                className="mt-0.5 shrink-0 opacity-60 group-hover:opacity-100 focus-visible:opacity-100"
                aria-label={`Investigate ${i.title}`}
                title="Start an agent run that looks into this"
                onClick={() => investigate([i])}
              >
                <Bot className="size-3.5" />
              </Button>
            </li>
          ))}
          {issues.length > SHOWN ? (
            <li>
              <Link to="/monitoring" className="block px-2 py-1 text-[12px] text-fg-3 hover:text-fg">
                +{issues.length - SHOWN} more →
              </Link>
            </li>
          ) : null}
        </ul>
      ) : (
        <p className="flex items-center gap-1.5 px-2 pb-1 text-[12.5px] text-fg-3">
          <CircleCheck className="size-3.5 text-good" aria-hidden />
          Endpoints up{mon.data ? ", hosts reporting, jobs running, no alerts" : ""}.
        </p>
      )}
    </Panel>
  );
}

/**
 * 2) Working on: every in-progress task, with the agent run on it if any.
 * Refresh asks each task's session to bring the task's status up to date
 * and carry on (resuming or continuing it when it is not running).
 */
export function WorkingOnWidget({ d }: { d: Dashboard }) {
  const { openTask } = useShell();
  const [refreshing, setRefreshing] = useState(false);
  const tasks = d.in_progress ?? [];
  const runByTask = new Map<number, Run>();
  for (const r of d.active_runs) if (r.task_id !== null && !runByTask.has(r.task_id)) runByTask.set(r.task_id, r);
  const loose = d.active_runs.filter((r) => r.task_id === null || !tasks.some((t) => t.id === r.task_id));
  const busy = tasks.length + loose.length;

  return (
    <Panel
      title={
        <>
          Working on {busy ? <span className="font-normal text-fg-3">· {busy}</span> : null}
        </>
      }
      icon={<Hammer />}
      id="dash-working"
      actions={
        <span className="flex items-center gap-2">
          {tasks.length || d.active_runs.some((r) => r.status === "waiting") ? (
            <Button
              size="sm"
              variant="subtle"
              onClick={() => setRefreshing(true)}
              title="Bump every task in progress and every parked run: status update, then keep going (new runs on Opus 5.5 · medium)"
            >
              <RefreshCw className="size-3.5" aria-hidden />
              Refresh
            </Button>
          ) : null}
          <Link to="/tasks?status=in_progress" className="text-[12px] text-fg-3 hover:text-fg">
            Tasks →
          </Link>
        </span>
      }
    >
      {refreshing ? <RefreshWorkDialog tasks={tasks} open={d.active_runs} onClose={() => setRefreshing(false)} /> : null}
      {busy === 0 ? (
        <p className="px-2 py-2 text-[13px] text-fg-3">Nothing in progress right now.</p>
      ) : (
        <ul aria-label="In progress">
          {tasks.slice(0, SHOWN).map((t) => {
            const run = runByTask.get(t.id);
            return (
              <li key={t.id}>
                <button
                  type="button"
                  onClick={() => openTask(t.id)}
                  className="flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-surface-2"
                >
                  <span className="relative grid size-3.5 shrink-0 place-items-center" aria-hidden>
                    <span className="size-2 rounded-full bg-accent animate-pulse-soft" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[13px]">{t.title}</span>
                    <span className="flex items-center gap-1.5 text-[11.5px] text-fg-3">
                      <ColorDot color={t.project_color} className="size-2" />
                      <span className="font-mono">{t.ref}</span>
                      {t.repo_name ? <span className="truncate">· {t.repo_name}</span> : null}
                      <span>
                        · <RelativeTime iso={t.updated_at} />
                      </span>
                    </span>
                  </span>
                  {run ? <RunStatusBadge run={run} /> : null}
                </button>
              </li>
            );
          })}
          {loose.slice(0, Math.max(0, SHOWN - tasks.length)).map((r) => (
            <li key={`r${r.id}`}>
              <Link to={`/agents/runs/${r.id}`} className="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 hover:bg-surface-2">
                <Sparkles className="size-3.5 shrink-0 text-fg-3" aria-hidden />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13px]">{runTitle(r)}</span>
                  <span className="flex items-center gap-1.5 text-[11.5px] text-fg-3">
                    <ColorDot color={r.project_color} className="size-2" />
                    {r.project_key} · agent run on {r.runner_name}
                  </span>
                </span>
                <RunStatusBadge run={r} />
              </Link>
            </li>
          ))}
          {busy > SHOWN ? (
            <li>
              <Link to="/tasks?status=in_progress" className="block px-2 py-1 text-[12px] text-fg-3 hover:text-fg">
                +{busy - SHOWN} more →
              </Link>
            </li>
          ) : null}
        </ul>
      )}
    </Panel>
  );
}

/**
 * 3) Project agents: the F-<Project> sessions that are open on the master —
 * what each is doing, open in Claude or attach. Starting one is the project's
 * button on the Agents page.
 */
export function ProjectSessionsWidget() {
  const ps = useProjectSessions();
  const v = ps.data;
  const open = (v?.sessions ?? []).filter((s) => isOpen(s) || (s.run_id ?? null) !== null);
  const hostDown = !!v?.host && !v.host.online;

  return (
    <Panel
      title={
        <>
          Project agents {open.length ? <span className="font-normal text-fg-3">· {open.length}</span> : null}
        </>
      }
      icon={<Sparkles />}
      id="dash-project-sessions"
      actions={
        <Link to="/agents" className="text-[12px] text-fg-3 hover:text-fg">
          Start one →
        </Link>
      }
    >
      {ps.isPending ? (
        <div className="space-y-2 p-1">
          <Skeleton className="h-7" />
          <Skeleton className="h-7" />
        </div>
      ) : !v ? (
        <p className="px-2 py-2 text-[13px] text-fg-3">Could not load the sessions.</p>
      ) : !v.host ? (
        <p className="px-2 py-2 text-[13px] text-fg-3">
          One Claude Code session per project, with Remote Control on — needs a master machine with terminals on.
        </p>
      ) : hostDown ? (
        <p className="px-2 py-2 text-[13px] text-fg-3">
          <strong className="font-medium text-fg-2">{v.host.runner_name}</strong> is offline — its sessions come back with it.
        </p>
      ) : !open.length ? (
        <p className="px-2 py-2 text-[13px] text-fg-3">
          No project agent is open.{" "}
          <Link to="/agents" className="font-medium text-accent hover:underline">
            Start one from its project's button
          </Link>
          .
        </p>
      ) : (
        <ul aria-label="Project agents">
          {open.map((s) => (
            <SessionLine key={s.name} s={s} runnerId={v.host?.runner_id ?? null} />
          ))}
        </ul>
      )}
    </Panel>
  );
}

const TONE: Record<AgentTone, "good" | "accent" | "warning" | "outline"> = {
  needs: "warning",
  turn: "warning",
  working: "accent",
  starting: "accent",
  open: "good",
  closed: "outline",
  offline: "outline",
};

function SessionLine({ s, runnerId }: { s: ProjectSession; runnerId: number | null }) {
  const navigate = useNavigate();
  const st = sessionStatus(s);
  const canAttach = runnerId !== null && isOpen(s);
  return (
    <li className="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 hover:bg-surface-2/60">
      <ColorDot color={s.project_color} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="truncate font-mono text-[13px]">{s.name}</span>
          <Badge tone={TONE[st.tone]} dot className="h-4.5 px-1.5 text-[10.5px]">
            {st.tone === "closed" ? "Closed" : st.label}
          </Badge>
        </div>
        {s.run_id !== null ? (
          <Link to={`/agents/runs/${s.run_id}`} className="block truncate text-[11px] text-fg-3 hover:text-fg" title={s.run_prompt}>
            #{s.run_id} · {promptTitle(s.run_prompt) || "conversation"}
          </Link>
        ) : (
          <p className="truncate text-[11px] text-fg-3" title={s.dir}>
            {shortHome(s.dir)}
            {s.activity ? (
              <>
                {" "}
                · active <RelativeTime iso={s.activity} />
              </>
            ) : null}
          </p>
        )}
      </div>
      {s.url ? (
        <a
          href={s.url}
          target="_blank"
          rel="noreferrer"
          className="inline-flex h-7 shrink-0 items-center gap-1 rounded-md border border-line px-2 text-[12.5px] hover:bg-surface-2"
          aria-label={`Open ${s.name} in Claude`}
          title="Open in Claude (Remote Control)"
        >
          Open <ExternalLink className="size-3" aria-hidden />
        </a>
      ) : null}
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label={`Attach to ${s.name}`}
        title="Attach in Forge"
        disabled={!canAttach}
        onClick={() => runnerId !== null && navigate(attachPath(runnerId, s.name))}
      >
        <Plug className="size-3.5" />
      </Button>
    </li>
  );
}
