// List filters applied client-side on top of what the API returned.

import type { Priority, Task, TaskStatus, TaskType } from "@/api/types";
import type { TaskSort } from "./tasks";

export type ListFilters = {
  q: string;
  statuses: TaskStatus[];
  priority: Priority | "";
  type: TaskType | "";
  label: string;
  sort: TaskSort;
  group: boolean;
};

export const defaultListFilters: ListFilters = {
  q: "",
  statuses: ["backlog", "todo", "in_progress", "blocked"],
  priority: "",
  type: "",
  label: "",
  sort: "priority",
  group: true,
};

export function filterTasks(tasks: Task[], f: ListFilters): Task[] {
  const q = f.q.trim().toLowerCase();
  return tasks.filter(
    (t) =>
      (f.statuses.length === 0 || f.statuses.includes(t.status)) &&
      (!f.priority || t.priority === f.priority) &&
      (!f.type || t.type === f.type) &&
      (!f.label || t.labels.includes(f.label)) &&
      (!q || t.title.toLowerCase().includes(q) || t.ref.toLowerCase().includes(q) || t.labels.some((l) => l.includes(q))),
  );
}
