// One project: overview (what, where it runs, how healthy), its board, a
// filterable list, its agent runs, its files and its activity. The tab lives in the URL
// (?tab=board) so a bookmark or a phone home-screen link lands on it.

import clsx from "clsx";
import {
  AlertTriangle,
  Bot,
  ExternalLink,
  FolderGit2,
  GitBranch,
  Globe,
  MoreHorizontal,
  Pencil,
  Plus,
  Server as ServerIcon,
  Target,
  Trash2,
} from "lucide-react";
import { useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { ApiError } from "@/api/client";
import { useDeleteRepo, useProject, useProjectActivity, useRuns, useTasks } from "@/api/hooks";
import type { ProjectDetail, Repo } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Markdown } from "@/components/ui/Markdown";
import { Menu } from "@/components/ui/Menu";
import { Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton, SkeletonRows } from "@/components/ui/Skeleton";
import { Tabs } from "@/components/ui/Tabs";
import { useToast } from "@/components/ui/Toast";
import { RunRow } from "@/features/agents/RunBits";
import { ActivityFeed } from "@/features/dashboard/ActivityFeed";
import { EndpointsTable } from "@/features/infra/endpoints";
import { OpenInCodeButton } from "@/features/editor/OpenInCode";
import { ProjectDashboards } from "@/features/monitoring/ProjectDashboards";
import { ProjectSessions } from "@/features/terminal/SessionStrips";
import { useShell } from "@/features/shell/context";
import { ProjectStatusBadge } from "@/features/tasks/icons";
import { TaskBoard } from "@/features/tasks/TaskBoard";
import { TaskList } from "@/features/tasks/TaskList";
import { defaultListFilters, type ListFilters } from "@/lib/taskFilters";
import { useUser } from "@/lib/auth";
import { countdown, todayInTz } from "@/lib/format";
import { EndpointDialog, RepoDialog, ServersLinkDialog } from "./dialogs";
import { CIBadge } from "./CIBadge";
import { ProgressMeter } from "./ProjectCard";
import { ProjectDialog } from "./ProjectDialog";
import { ProjectFiles } from "./ProjectFiles";

type Tab = "overview" | "board" | "list" | "agents" | "files" | "activity";
const TABS: Tab[] = ["overview", "board", "list", "agents", "files", "activity"];

export function ProjectPage() {
  const { key = "" } = useParams();
  const project = useProject(key.toUpperCase());
  const [params, setParams] = useSearchParams();
  const tabParam = params.get("tab") as Tab | null;
  const tab: Tab = tabParam && TABS.includes(tabParam) ? tabParam : "overview";
  const setTab = (t: Tab) =>
    setParams((p) => {
      const next = new URLSearchParams(p);
      if (t === "overview") next.delete("tab");
      else next.set("tab", t);
      return next;
    });

  if (project.isPending) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-16 w-full max-w-xl" />
        <Skeleton className="h-9 w-full" />
        <SkeletonRows rows={6} />
      </div>
    );
  }
  if (project.error) {
    if (project.error instanceof ApiError && project.error.status === 404) {
      return <EmptyState title="Project not found">There is no project with key {key.toUpperCase()}.</EmptyState>;
    }
    return <ErrorState error={project.error} onRetry={() => project.refetch()} />;
  }
  const p = project.data;
  const s = p.stats;

  return (
    <div className="mx-auto max-w-[1400px]">
      <ProjectHeader project={p} />
      <Tabs
        label="Project views"
        value={tab}
        onChange={setTab}
        className="mb-4"
        items={[
          { value: "overview", label: "Overview" },
          { value: "board", label: "Board", count: s.total - s.done },
          { value: "list", label: "List", count: s.total },
          { value: "agents", label: "Agents", count: s.active_runs || undefined },
          { value: "files", label: "Files" },
          { value: "activity", label: "Activity" },
        ]}
      />
      {tab === "overview" ? <Overview project={p} /> : null}
      {tab === "board" ? <BoardTab project={p} /> : null}
      {tab === "list" ? <ListTab project={p} /> : null}
      {tab === "agents" ? <AgentsTab project={p} /> : null}
      {tab === "files" ? <ProjectFiles projectKey={p.key} /> : null}
      {tab === "activity" ? <ActivityTab project={p} /> : null}
    </div>
  );
}

function ProjectHeader({ project: p }: { project: ProjectDetail }) {
  const { newTask, newRun } = useShell();
  const user = useUser();
  const today = todayInTz(user.timezone);
  const [editing, setEditing] = useState(false);
  return (
    <div className="mb-4 flex flex-wrap items-start gap-4">
      <div className="flex min-w-0 grow basis-80 items-start gap-3">
        <span aria-hidden className="mt-1 h-9 w-1.5 shrink-0 rounded-full" style={{ background: p.color }} />
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold tracking-tight">{p.name}</h1>
            <span className="font-mono text-[12px] text-fg-3">{p.key}</span>
            <ProjectStatusBadge status={p.status} />
            <Badge tone="outline">P{p.priority}</Badge>
            <Badge tone="outline">{p.category}</Badge>
            {p.target_date ? (
              <Badge tone="outline" title={`Target ${p.target_date}`}>
                <Target className="size-3" aria-hidden /> {countdown(p.target_date, today)}
              </Badge>
            ) : null}
          </div>
          {p.summary ? <p className="mt-1 text-[13px] text-fg-2">{p.summary}</p> : null}
        </div>
      </div>
      {/* One row on a phone: icons only, with the label as the accessible name and tooltip. */}
      <div className="flex flex-nowrap items-center gap-1.5 sm:gap-2">
        <OpenInCodeButton size="sm" variant="subtle" responsiveLabel target={{ kind: "project", projectKey: p.key, repoIds: [] }} />
        <Button size="sm" variant="subtle" onClick={() => setEditing(true)} aria-label="Edit project" title="Edit project">
          <Pencil className="size-3.5" aria-hidden /> <span className="hidden sm:inline">Edit</span>
        </Button>
        <Button size="sm" variant="subtle" onClick={() => newRun({ project_key: p.key })} aria-label="Run agent" title="Run agent">
          <Bot className="size-3.5" aria-hidden /> <span className="hidden sm:inline">Run agent</span>
        </Button>
        <Button size="sm" variant="primary" onClick={() => newTask({ project_key: p.key })} aria-label="New task" title="New task (C)">
          <Plus className="size-3.5" aria-hidden /> <span className="hidden sm:inline">Task</span>
        </Button>
      </div>
      {editing ? <ProjectDialog project={p} onClose={() => setEditing(false)} /> : null}
    </div>
  );
}

function Overview({ project: p }: { project: ProjectDetail }) {
  const s = p.stats;
  const [serversOpen, setServersOpen] = useState(false);
  const [endpointOpen, setEndpointOpen] = useState(false);
  const [repoEdit, setRepoEdit] = useState<Repo | "new" | null>(null);

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-5">
      <div className="space-y-4 lg:col-span-3">
        <section className="rounded-xl border border-line bg-surface p-4">
          <div className="mb-2 flex items-baseline justify-between text-[13px]">
            <span className="text-fg-2">
              <span className="font-semibold text-fg">{s.done}</span> of {s.total} tasks done
            </span>
            <span className="tabular text-fg-3">{Math.round(s.progress * 100)}%</span>
          </div>
          <ProgressMeter value={s.progress} color={p.color} label={`${p.name} progress`} />
          <dl className="mt-3 grid grid-cols-3 gap-2 text-center sm:grid-cols-6">
            {[
              ["Backlog", s.backlog],
              ["To do", s.todo],
              ["In progress", s.in_progress],
              ["Blocked", s.blocked],
              ["Overdue", s.overdue],
              ["Done 7d", s.done_7d],
            ].map(([label, value]) => (
              <div key={label} className="flex flex-col-reverse rounded-md bg-surface-2/70 py-1.5">
                <dt className="text-[10.5px] text-fg-3">{label}</dt>
                <dd className="text-[15px] font-semibold">{value}</dd>
              </div>
            ))}
          </dl>
        </section>

        <ProjectSessions projectKey={p.key} />

        <Panel
          title={`Repositories · ${p.repos.length}`}
          icon={<FolderGit2 />}
          id="proj-repos"
          bodyClassName="p-0"
          actions={
            <Button size="sm" variant="ghost" onClick={() => setRepoEdit("new")}>
              <Plus className="size-3.5" aria-hidden /> Add
            </Button>
          }
        >
          {p.repos.length ? (
            <ul className="divide-y divide-line">
              {p.repos.map((r) => (
                <RepoRow key={r.id} repo={r} projectKey={p.key} onEdit={() => setRepoEdit(r)} />
              ))}
            </ul>
          ) : (
            <p className="p-4 text-[13px] text-fg-3">No repositories yet.</p>
          )}
        </Panel>

        {p.description.trim() ? (
          <Panel title="About" id="proj-about" bodyClassName="px-4 py-3">
            <Markdown>{p.description}</Markdown>
          </Panel>
        ) : null}
      </div>

      <div className="space-y-4 lg:col-span-2">
        {p.links.length ? (
          <Panel title="Links" icon={<ExternalLink />} id="proj-links">
            <ul>
              {p.links.map((l) => (
                <li key={l.url}>
                  <a
                    href={l.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="flex items-center gap-2 rounded-md px-2 py-1.5 text-[13px] hover:bg-surface-2"
                  >
                    <ExternalLink className="size-3.5 text-fg-3" aria-hidden />
                    <span className="font-medium">{l.label}</span>
                    <span className="min-w-0 truncate font-mono text-[11.5px] text-fg-3">{l.url.replace(/^https?:\/\//, "")}</span>
                  </a>
                </li>
              ))}
            </ul>
          </Panel>
        ) : null}

        <ProjectDashboards project={p} />

        <Panel
          title={`Endpoints · ${s.endpoints_up}/${s.endpoints_total} up`}
          icon={<Globe />}
          id="proj-endpoints"
          bodyClassName="p-0"
          actions={
            <Button size="sm" variant="ghost" onClick={() => setEndpointOpen(true)}>
              <Plus className="size-3.5" aria-hidden /> Add
            </Button>
          }
        >
          {p.endpoints.length ? (
            <EndpointsTable endpoints={p.endpoints} />
          ) : (
            <p className="p-4 text-[13px] text-fg-3">No monitored endpoints.</p>
          )}
        </Panel>

        <Panel
          title={`Servers · ${p.servers.length}`}
          icon={<ServerIcon />}
          id="proj-servers"
          actions={
            <Button size="sm" variant="ghost" onClick={() => setServersOpen(true)}>
              <Pencil className="size-3.5" aria-hidden /> Edit
            </Button>
          }
        >
          {p.servers.length ? (
            <ul>
              {p.servers.map((sv) => (
                <li key={sv.server_id} className="flex items-center gap-2 rounded-md px-2 py-1.5 text-[13px]">
                  <ServerIcon className="size-3.5 text-fg-3" aria-hidden />
                  <span className="font-medium">{sv.name}</span>
                  <span className="min-w-0 flex-1 truncate text-fg-2">{sv.role}</span>
                  {sv.critical ? (
                    <Badge tone="critical" dot title="Production-critical host">
                      critical
                    </Badge>
                  ) : null}
                  <Badge tone={sv.environment === "production" ? "serious" : "neutral"}>{sv.environment}</Badge>
                </li>
              ))}
            </ul>
          ) : (
            <p className="px-2 py-2 text-[13px] text-fg-3">Not linked to any server.</p>
          )}
        </Panel>

        {p.infra_notes.trim() ? (
          <Panel title="Infrastructure notes" id="proj-infra" bodyClassName="px-4 py-3">
            <Markdown>{p.infra_notes}</Markdown>
          </Panel>
        ) : null}
      </div>

      {serversOpen ? <ServersLinkDialog projectKey={p.key} linked={p.servers} onClose={() => setServersOpen(false)} /> : null}
      {endpointOpen ? <EndpointDialog projectKey={p.key} onClose={() => setEndpointOpen(false)} /> : null}
      {repoEdit ? (
        <RepoDialog projectKey={p.key} repo={repoEdit === "new" ? undefined : repoEdit} onClose={() => setRepoEdit(null)} />
      ) : null}
    </div>
  );
}

function RepoRow({ repo: r, projectKey, onEdit }: { repo: Repo; projectKey: string; onEdit: () => void }) {
  const del = useDeleteRepo(projectKey);
  const toast = useToast();
  const { newRun } = useShell();
  const [confirm, setConfirm] = useState(false);
  const g = r.git;
  return (
    <li className="px-3.5 py-3">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[13.5px] font-medium">{r.name}</span>
            <Badge tone="outline">{r.kind}</Badge>
            {g ? (
              <span className="inline-flex items-center gap-1 font-mono text-[11.5px] text-fg-2">
                <GitBranch className="size-3 text-fg-3" aria-hidden />
                {g.branch}
              </span>
            ) : null}
            {g && g.dirty > 0 ? (
              <Badge tone="warning" dot title="Uncommitted changes to tracked files">
                {g.dirty} changed
              </Badge>
            ) : null}
            {g && (g.untracked ?? 0) > 0 ? (
              <Badge tone="outline" title="Untracked files (not in git)">
                {g.untracked} untracked
              </Badge>
            ) : null}
            {g && (g.ahead > 0 || g.behind > 0) ? (
              <span className="tabular text-[11.5px] text-fg-2" title="Ahead / behind the upstream">
                ↑{g.ahead} ↓{g.behind}
              </span>
            ) : null}
          </div>
          {r.path ? <p className="mt-0.5 truncate font-mono text-[11.5px] text-fg-3">{r.path}</p> : null}
          {g?.error ? (
            <p className="mt-1 flex items-center gap-1 text-[12px] text-critical-ink">
              <AlertTriangle className="size-3.5" aria-hidden /> {g.error}
            </p>
          ) : g?.head ? (
            <p className="mt-1 truncate text-[12.5px] text-fg-2">
              <span className="font-mono text-[11.5px] text-fg-3">{g.head.hash.slice(0, 7)}</span> {g.head.subject}{" "}
              <span className="text-fg-3">
                · {g.head.author} · <RelativeTime iso={g.head.at} />
              </span>
            </p>
          ) : !g ? (
            <p className="mt-1 text-[12px] text-fg-3">Not scanned yet — a runner reports git status every few minutes.</p>
          ) : null}
          {g?.ci ? (
            <div className="mt-1">
              <CIBadge ci={g.ci} />
            </div>
          ) : null}
          <div className="mt-1 flex flex-wrap gap-x-3 text-[11.5px] text-fg-3">
            {g ? (
              <span>
                <span className="tabular text-fg-2">{g.commits_7d}</span> commits 7d
              </span>
            ) : null}
            {g ? (
              <span>
                scanned <RelativeTime iso={g.scanned_at} /> on {g.runner_name}
              </span>
            ) : null}
            {r.deploy ? <span className="truncate">deploy: {r.deploy}</span> : null}
          </div>
        </div>
        <OpenInCodeButton
          size="icon-sm"
          variant="ghost"
          iconOnly
          label={`Open ${r.name} in VS Code`}
          target={{ kind: "project", projectKey, repoIds: [r.id] }}
        />
        <Menu
          label={`Actions for ${r.name}`}
          triggerClassName="grid size-7 place-items-center rounded-md text-fg-3 hover:bg-surface-2 hover:text-fg"
          trigger={<MoreHorizontal className="size-4" />}
          items={[
            { label: "Run agent here", icon: <Bot />, disabled: !r.path, onSelect: () => newRun({ project_key: projectKey, repo_id: r.id }) },
            { label: "Edit", icon: <Pencil />, onSelect: onEdit },
            "separator",
            { label: "Remove", icon: <Trash2 />, danger: true, onSelect: () => setConfirm(true) },
          ]}
        />
      </div>
      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        title={`Remove ${r.name}?`}
        body="Removes the repository from this project (nothing on disk is touched)."
        confirmLabel="Remove"
        loading={del.isPending}
        onConfirm={() =>
          del.mutate(r.id, {
            onSuccess: () => {
              toast.success("Repository removed");
              setConfirm(false);
            },
            onError: (e) => toast.error(e),
          })
        }
      />
    </li>
  );
}

function BoardTab({ project }: { project: ProjectDetail }) {
  const tasks = useTasks({ project: project.key });
  const user = useUser();
  const { openTask } = useShell();
  if (tasks.isPending) return <SkeletonRows rows={6} />;
  if (tasks.error) return <ErrorState error={tasks.error} onRetry={() => tasks.refetch()} />;
  return <TaskBoard tasks={tasks.data} projectKey={project.key} today={todayInTz(user.timezone)} onOpen={openTask} />;
}

function ListTab({ project }: { project: ProjectDetail }) {
  const tasks = useTasks({ project: project.key });
  const user = useUser();
  const { openTask } = useShell();
  const [filters, setFilters] = useState<ListFilters>(defaultListFilters);
  if (tasks.isPending) return <SkeletonRows rows={6} />;
  if (tasks.error) return <ErrorState error={tasks.error} onRetry={() => tasks.refetch()} />;
  return (
    <TaskList
      tasks={tasks.data}
      today={todayInTz(user.timezone)}
      onOpen={openTask}
      filters={filters}
      onFiltersChange={setFilters}
    />
  );
}

function AgentsTab({ project }: { project: ProjectDetail }) {
  const runs = useRuns({ project: project.key, limit: 100 }, 8000);
  const { newRun } = useShell();
  return (
    <Panel
      title="Runs"
      icon={<Bot />}
      id="proj-runs"
      actions={
        <Button size="sm" variant="primary" onClick={() => newRun({ project_key: project.key })}>
          <Plus className="size-3.5" aria-hidden /> New run
        </Button>
      }
    >
      {runs.isPending ? (
        <SkeletonRows rows={4} />
      ) : runs.data?.length ? (
        <div className={clsx(runs.isPlaceholderData && "opacity-60")}>
          {runs.data.map((r) => (
            <RunRow key={r.id} run={r} showProject={false} />
          ))}
        </div>
      ) : (
        <EmptyState compact icon={<Bot />} title="No agent runs for this project yet" />
      )}
    </Panel>
  );
}

function ActivityTab({ project }: { project: ProjectDetail }) {
  const activity = useProjectActivity(project.key, 100);
  return (
    <Panel title="Activity" id="proj-activity">
      {activity.isPending ? (
        <SkeletonRows rows={6} />
      ) : (
        <ActivityFeed
          items={activity.data ?? []}
          showProject={false}
          empty="No activity yet — task and run events will show up here."
        />
      )}
    </Panel>
  );
}
