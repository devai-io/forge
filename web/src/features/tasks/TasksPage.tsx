import { ListTodo, Star } from "lucide-react";
import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useProjects, useTasks } from "@/api/hooks";
import type { TaskStatus } from "@/api/types";
import { ErrorState } from "@/components/ui/EmptyState";
import { Select } from "@/components/ui/Input";
import { PageHeader } from "@/components/ui/Panel";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { Segmented } from "@/components/ui/Tabs";
import { useShell } from "@/features/shell/context";
import { useUser } from "@/lib/auth";
import { todayInTz } from "@/lib/format";
import { STATUSES } from "@/lib/tasks";
import { defaultListFilters, type ListFilters } from "@/lib/taskFilters";
import { TaskList } from "./TaskList";

type Scope = "all" | "focus" | "overdue";

export function TasksPage() {
  const [params, setParams] = useSearchParams();
  const project = params.get("project") ?? "";
  const scope = (
    ["focus", "overdue"].includes(params.get("scope") ?? "")
      ? params.get("scope")
      : params.get("focus") === "1"
        ? "focus"
        : params.get("overdue") === "1"
          ? "overdue"
          : "all"
  ) as Scope;
  const initialStatus = params.get("status");
  const [filters, setFilters] = useState<ListFilters>(() => ({
    ...defaultListFilters,
    statuses:
      initialStatus && STATUSES.some((s) => s.value === initialStatus)
        ? [initialStatus as TaskStatus]
        : defaultListFilters.statuses,
  }));

  const projects = useProjects();
  const user = useUser();
  const { openTask } = useShell();
  const tasks = useTasks({
    project: project || undefined,
    focus: scope === "focus" || undefined,
    overdue: scope === "overdue" || undefined,
  });
  const today = todayInTz(user.timezone);

  const setParam = (key: string, value: string) =>
    setParams((p) => {
      const next = new URLSearchParams(p);
      if (value) next.set(key, value);
      else next.delete(key);
      return next;
    });

  const counts = useMemo(() => {
    const open = (tasks.data ?? []).filter((t) => t.status !== "done").length;
    return { open };
  }, [tasks.data]);

  return (
    <div className="mx-auto max-w-[1200px]">
      <PageHeader
        title="Tasks"
        subtitle={tasks.data ? `${counts.open} open across ${project ? "this project" : "all projects"}` : " "}
        actions={
          <>
            <Segmented
              label="Scope"
              value={scope}
              onChange={(v) => setParam("scope", v === "all" ? "" : v)}
              items={[
                { value: "all", label: <><ListTodo className="size-3.5" aria-hidden /> All</> },
                { value: "focus", label: <><Star className="size-3.5" aria-hidden /> Focus</> },
                { value: "overdue", label: "Overdue" },
              ]}
            />
            <Select aria-label="Project" className="w-auto" value={project} onChange={(e) => setParam("project", e.target.value)}>
              <option value="">All projects</option>
              {projects.data?.map((p) => (
                <option key={p.key} value={p.key}>
                  {p.key} · {p.name}
                </option>
              ))}
            </Select>
          </>
        }
      />
      {tasks.isPending ? (
        <SkeletonRows rows={8} />
      ) : tasks.error ? (
        <ErrorState error={tasks.error} onRetry={() => tasks.refetch()} />
      ) : (
        <TaskList
          tasks={tasks.data}
          today={today}
          onOpen={openTask}
          showProject={!project}
          filters={filters}
          onFiltersChange={setFilters}
        />
      )}
    </div>
  );
}
