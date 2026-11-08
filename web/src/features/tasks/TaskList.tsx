// Filterable, sortable task list. The caller fetches (by project, focus, open);
// the remaining filters are applied here so typing in the search box never
// waits on the network. Grouped, it leads with what is moving (in progress,
// blocked) and folds the backlog; on a phone the secondary filters sit behind
// one "Filters" button and each row takes two lines instead of cutting titles.

import clsx from "clsx";
import { ChevronRight, ListFilter, Search, SlidersHorizontal, Star } from "lucide-react";
import { useMemo, useState } from "react";
import { useUpdateTask } from "@/api/hooks";
import type { Priority, Task, TaskStatus, TaskType } from "@/api/types";
import { ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { EmptyState } from "@/components/ui/EmptyState";
import { Input, Select } from "@/components/ui/Input";
import { Menu } from "@/components/ui/Menu";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Switch } from "@/components/ui/Input";
import { useToast } from "@/components/ui/Toast";
import { orderAtEnd } from "@/lib/order";
import { defaultListFilters, filterTasks, type ListFilters } from "@/lib/taskFilters";
import {
  LIST_GROUP_ORDER,
  PRIORITIES,
  readCollapsedGroups,
  sortTasks,
  STATUSES,
  statusLabel,
  TYPES,
  writeCollapsedGroups,
  type TaskSort,
} from "@/lib/tasks";
import { PriorityIcon, StatusIcon } from "./icons";
import { DueChip } from "./TaskCard";

export function TaskList({
  tasks,
  today,
  onOpen,
  showProject = false,
  filters,
  onFiltersChange,
  toolbarExtra,
}: {
  tasks: Task[];
  today: string;
  onOpen: (id: number) => void;
  showProject?: boolean;
  filters: ListFilters;
  onFiltersChange: (f: ListFilters) => void;
  toolbarExtra?: React.ReactNode;
}) {
  const labels = useMemo(() => Array.from(new Set(tasks.flatMap((t) => t.labels))).sort(), [tasks]);
  const visible = useMemo(() => sortTasks(filterTasks(tasks, filters), filters.sort), [tasks, filters]);
  const set = <K extends keyof ListFilters>(k: K, v: ListFilters[K]) => onFiltersChange({ ...filters, [k]: v });

  const [collapsed, setCollapsed] = useState<TaskStatus[]>(readCollapsedGroups);
  const toggleGroup = (s: TaskStatus) => {
    const next = collapsed.includes(s) ? collapsed.filter((x) => x !== s) : [...collapsed, s];
    setCollapsed(next);
    writeCollapsedGroups(next);
  };
  // A search shows every match, folded group or not.
  const searching = filters.q.trim() !== "";
  const [filtersOpen, setFiltersOpen] = useState(false);
  // The filters behind the phone's "Filters" button that differ from the defaults.
  const secondary = [
    filters.priority !== "",
    filters.type !== "",
    filters.label !== "",
    filters.sort !== defaultListFilters.sort,
    filters.group !== defaultListFilters.group,
  ].filter(Boolean).length;
  // Rows keep a due column only when some row has a due date.
  const anyDue = visible.some((t) => t.due_date);

  const groups = filters.group
    ? LIST_GROUP_ORDER.map((s) => ({ status: s, tasks: visible.filter((t) => t.status === s) })).filter((g) => g.tasks.length)
    : [{ status: null as TaskStatus | null, tasks: visible }];

  const secondaryControls = (
    <>
      <Select aria-label="Priority" className="w-auto" value={filters.priority} onChange={(e) => set("priority", e.target.value as Priority | "")}>
        <option value="">Any priority</option>
        {PRIORITIES.map((p) => (
          <option key={p.value} value={p.value}>
            {p.label}
          </option>
        ))}
      </Select>
      <Select aria-label="Type" className="w-auto" value={filters.type} onChange={(e) => set("type", e.target.value as TaskType | "")}>
        <option value="">Any type</option>
        {TYPES.map((p) => (
          <option key={p.value} value={p.value}>
            {p.label}
          </option>
        ))}
      </Select>
      {labels.length ? (
        <Select aria-label="Label" className="w-auto" value={filters.label} onChange={(e) => set("label", e.target.value)}>
          <option value="">Any label</option>
          {labels.map((l) => (
            <option key={l} value={l}>
              {l}
            </option>
          ))}
        </Select>
      ) : null}
      <Select aria-label="Sort" className="w-auto" value={filters.sort} onChange={(e) => set("sort", e.target.value as TaskSort)}>
        <option value="priority">Sort: priority</option>
        <option value="due">Sort: due date</option>
        <option value="board">Sort: board order</option>
        <option value="updated">Sort: recently updated</option>
        <option value="created">Sort: newest</option>
      </Select>
      <Switch checked={filters.group} onChange={(v) => set("group", v)} label="Group" />
    </>
  );

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-0 flex-1 basis-40 sm:basis-48">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-fg-3" aria-hidden />
          <Input
            aria-label="Search tasks"
            placeholder="Search title, ref or label"
            value={filters.q}
            onChange={(e) => set("q", e.target.value)}
            className="pl-8"
          />
        </div>
        <Menu
          label="Filter by status"
          align="start"
          triggerClassName="inline-flex h-8.5 items-center gap-1.5 rounded-md border border-line-strong bg-surface px-2.5 text-[13px] text-fg-2 hover:bg-surface-2"
          trigger={
            <>
              <ListFilter className="size-3.5" aria-hidden />
              {filters.statuses.length === 0 || filters.statuses.length === STATUSES.length
                ? "All statuses"
                : filters.statuses.length === 4 && !filters.statuses.includes("done")
                  ? "Open"
                  : filters.statuses.map(statusLabel).join(", ")}
            </>
          }
          items={[
            {
              label: "Open (not done)",
              onSelect: () => set("statuses", ["backlog", "todo", "in_progress", "blocked"]),
            },
            { label: "All", onSelect: () => set("statuses", []) },
            "separator",
            ...STATUSES.map((s) => ({
              label: s.label,
              icon: <StatusIcon status={s.value} />,
              checked: filters.statuses.includes(s.value),
              onSelect: () =>
                set(
                  "statuses",
                  filters.statuses.includes(s.value)
                    ? filters.statuses.filter((x) => x !== s.value)
                    : [...filters.statuses, s.value],
                ),
            })),
          ]}
        />
        <div className="hidden sm:contents">{secondaryControls}</div>
        <Button
          variant="subtle"
          className="sm:hidden"
          onClick={() => setFiltersOpen(true)}
          aria-label={secondary ? `Filters, ${secondary} set` : "Filters"}
        >
          <SlidersHorizontal className="size-3.5" aria-hidden /> Filters{secondary ? ` (${secondary})` : ""}
        </Button>
        {toolbarExtra}
      </div>

      <Dialog
        open={filtersOpen}
        onClose={() => setFiltersOpen(false)}
        title="Filters"
        footer={
          <>
            {secondary ? (
              <Button
                variant="ghost"
                onClick={() =>
                  onFiltersChange({
                    ...filters,
                    priority: "",
                    type: "",
                    label: "",
                    sort: defaultListFilters.sort,
                    group: defaultListFilters.group,
                  })
                }
              >
                Reset
              </Button>
            ) : null}
            <Button variant="primary" onClick={() => setFiltersOpen(false)}>
              Done
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-2.5 [&_select]:w-full">{secondaryControls}</div>
      </Dialog>

      {visible.length === 0 ? (
        <EmptyState compact title={tasks.length ? "No tasks match these filters" : "No tasks yet"} />
      ) : (
        <div className="overflow-hidden rounded-lg border border-line bg-surface">
          {groups.map((g) => (
            <div key={g.status ?? "all"}>
              {g.status ? (
                <button
                  type="button"
                  onClick={() => toggleGroup(g.status!)}
                  aria-expanded={searching || !collapsed.includes(g.status)}
                  className="flex w-full items-center gap-2 border-b border-line bg-surface-2/60 px-3 py-1.5 text-left text-xs font-medium text-fg-2 hover:bg-surface-2"
                >
                  <ChevronRight
                    className={clsx("size-3 text-fg-3 transition-transform", (searching || !collapsed.includes(g.status)) && "rotate-90")}
                    aria-hidden
                  />
                  <StatusIcon status={g.status} />
                  {statusLabel(g.status)}
                  <span className="tabular text-fg-3">{g.tasks.length}</span>
                </button>
              ) : null}
              {!g.status || searching || !collapsed.includes(g.status) ? (
                <ul className="divide-y divide-line">
                  {g.tasks.map((t) => (
                    <li key={t.id}>
                      <TaskRow task={t} today={today} onOpen={onOpen} showProject={showProject} siblings={tasks} dueColumn={anyDue} />
                    </li>
                  ))}
                </ul>
              ) : null}
            </div>
          ))}
        </div>
      )}
      <p className="text-xs text-fg-3">
        {visible.length} of {tasks.length} tasks
      </p>
    </div>
  );
}

function TaskRow({
  task,
  today,
  onOpen,
  showProject,
  siblings,
  dueColumn,
}: {
  task: Task;
  today: string;
  onOpen: (id: number) => void;
  showProject: boolean;
  siblings: Task[];
  dueColumn: boolean;
}) {
  const update = useUpdateTask();
  const toast = useToast();
  return (
    <div className="flex min-w-0 items-start gap-2 px-3 py-2 hover:bg-surface-2/60 sm:items-center">
      <Menu
        label={`Change status of ${task.ref}`}
        align="start"
        triggerClassName="rounded p-0.5 hover:bg-surface-3"
        trigger={<StatusIcon status={task.status} />}
        items={STATUSES.map((s) => ({
          label: s.label,
          icon: <StatusIcon status={s.value} />,
          checked: s.value === task.status,
          onSelect: () =>
            s.value !== task.status &&
            update.mutate(
              {
                id: task.id,
                status: s.value,
                sort_order: orderAtEnd(siblings.filter((x) => x.status === s.value && x.project_id === task.project_id)),
              },
              { onError: (e) => toast.error(e) },
            ),
        }))}
      />
      <PriorityIcon priority={task.priority} className="mt-0.5 size-3.5 sm:mt-0" />
      <button type="button" onClick={() => onOpen(task.id)} className="flex min-w-0 flex-1 flex-col text-left sm:flex-row sm:items-center sm:gap-2">
        <span className="flex min-w-0 items-start gap-2 sm:items-center">
          <span className="hidden shrink-0 font-mono text-[11.5px] text-fg-3 sm:inline">{task.ref}</span>
          <span className={clsx("line-clamp-2 min-w-0 text-[13px] sm:truncate", task.status === "done" ? "text-fg-3" : "text-fg")}>
            {task.title}
          </span>
          {task.focus ? <Star className="mt-0.5 size-3 shrink-0 fill-warning text-warning sm:mt-0" aria-label="In today's focus" /> : null}
        </span>
        {/* Phone: the ref, project and due date on a muted second line. */}
        <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11.5px] text-fg-3 sm:hidden">
          <span className="font-mono">{task.ref}</span>
          {showProject ? (
            <span className="inline-flex items-center gap-1">
              <ColorDot color={task.project_color} className="size-2" />
              {task.project_key}
            </span>
          ) : null}
          <DueChip due={task.due_date} today={today} done={task.status === "done"} />
        </span>
      </button>
      <div className="hidden shrink-0 items-center gap-1 md:flex">
        {task.labels.slice(0, 2).map((l) => (
          <span key={l} className="rounded bg-surface-2 px-1.5 text-[11px] text-fg-2">
            {l}
          </span>
        ))}
      </div>
      {showProject ? (
        <span className="hidden shrink-0 items-center gap-1 text-[11.5px] text-fg-3 sm:inline-flex">
          <ColorDot color={task.project_color} className="size-2" />
          {task.project_key}
        </span>
      ) : null}
      {dueColumn ? (
        <span className="hidden w-20 shrink-0 text-right sm:block">
          <DueChip due={task.due_date} today={today} done={task.status === "done"} />
        </span>
      ) : null}
      <RelativeTime iso={task.updated_at} className="hidden w-14 shrink-0 text-right text-[11px] text-fg-3 lg:block" />
    </div>
  );
}
