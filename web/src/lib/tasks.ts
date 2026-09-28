// Task vocabulary: the fixed orders and labels the board, lists and filters
// share. Status order is the board's column order and the API's list order.

import type { Priority, ProjectStatus, Task, TaskStatus, TaskType } from "@/api/types";

export const STATUSES: { value: TaskStatus; label: string }[] = [
  { value: "backlog", label: "Backlog" },
  { value: "todo", label: "To do" },
  { value: "in_progress", label: "In progress" },
  { value: "blocked", label: "Blocked" },
  { value: "done", label: "Done" },
];

export const PRIORITIES: { value: Priority; label: string }[] = [
  { value: "urgent", label: "Urgent" },
  { value: "high", label: "High" },
  { value: "medium", label: "Medium" },
  { value: "low", label: "Low" },
];

export const TYPES: { value: TaskType; label: string }[] = [
  { value: "feature", label: "Feature" },
  { value: "bug", label: "Bug" },
  { value: "chore", label: "Chore" },
  { value: "research", label: "Research" },
  { value: "ops", label: "Ops" },
];

export const PROJECT_STATUSES: { value: ProjectStatus; label: string }[] = [
  { value: "live", label: "Live" },
  { value: "building", label: "Building" },
  { value: "radar", label: "On radar" },
  { value: "paused", label: "Paused" },
  { value: "archived", label: "Archived" },
];

export const statusLabel = (s: TaskStatus) => STATUSES.find((x) => x.value === s)?.label ?? s;
export const priorityLabel = (p: Priority) => PRIORITIES.find((x) => x.value === p)?.label ?? p;
export const typeLabel = (t: TaskType) => TYPES.find((x) => x.value === t)?.label ?? t;
export const projectStatusLabel = (s: ProjectStatus) => PROJECT_STATUSES.find((x) => x.value === s)?.label ?? s;

const STATUS_RANK: Record<TaskStatus, number> = { backlog: 0, todo: 1, in_progress: 2, blocked: 3, done: 4 };
const PRIORITY_RANK: Record<Priority, number> = { urgent: 0, high: 1, medium: 2, low: 3 };

export const statusRank = (s: TaskStatus) => STATUS_RANK[s];
export const priorityRank = (p: Priority) => PRIORITY_RANK[p];

/** "SHOP-12". The API sends `ref`, but optimistic rows are built client-side. */
export function formatRef(projectKey: string, number: number): string {
  return `${projectKey.toUpperCase()}-${number}`;
}

/** Parse "shop-12" / "SHOP-12" into its parts, or null. */
export function parseRef(text: string): { key: string; number: number } | null {
  const m = /^\s*([A-Za-z][A-Za-z0-9]{1,9})-(\d+)\s*$/.exec(text);
  return m ? { key: m[1].toUpperCase(), number: Number(m[2]) } : null;
}

export function isOverdue(task: Pick<Task, "due_date" | "status">, today: string): boolean {
  return task.status !== "done" && !!task.due_date && task.due_date < today;
}

export function groupByStatus<T extends Pick<Task, "status" | "sort_order">>(tasks: T[]): Record<TaskStatus, T[]> {
  const groups: Record<TaskStatus, T[]> = { backlog: [], todo: [], in_progress: [], blocked: [], done: [] };
  for (const t of tasks) groups[t.status]?.push(t);
  for (const s of Object.keys(groups) as TaskStatus[]) groups[s].sort((a, b) => a.sort_order - b.sort_order);
  return groups;
}

export type TaskSort = "board" | "priority" | "due" | "updated" | "created";

export function sortTasks<T extends Task>(tasks: T[], sort: TaskSort): T[] {
  const copy = [...tasks];
  const byDue = (a: T, b: T) => (a.due_date ?? "9999") < (b.due_date ?? "9999") ? -1 : (a.due_date ?? "9999") > (b.due_date ?? "9999") ? 1 : 0;
  switch (sort) {
    case "priority":
      return copy.sort((a, b) => priorityRank(a.priority) - priorityRank(b.priority) || byDue(a, b) || a.id - b.id);
    case "due":
      return copy.sort((a, b) => byDue(a, b) || priorityRank(a.priority) - priorityRank(b.priority));
    case "updated":
      return copy.sort((a, b) => b.updated_at.localeCompare(a.updated_at));
    case "created":
      return copy.sort((a, b) => b.created_at.localeCompare(a.created_at));
    default:
      return copy.sort((a, b) => statusRank(a.status) - statusRank(b.status) || a.sort_order - b.sort_order);
  }
}

/** Normalise a free-typed label: trimmed, lower-case, inner spaces as dashes. */
export function normalizeLabel(raw: string): string {
  return raw.trim().toLowerCase().replace(/\s+/g, "-").slice(0, 32);
}

/** The prompt a "Run agent on this task" starts from: ref, title, description. */
export function buildAgentPrompt(task: Pick<Task, "ref" | "title" | "description">): string {
  return `${task.ref}: ${task.title}\n\n${task.description.trim()}`.trim();
}
