import clsx from "clsx";
import { CalendarClock, Check, MessageSquare, Star } from "lucide-react";
import type { Task, TaskStatus } from "@/api/types";
import { ColorDot } from "@/components/ui/Badge";
import { Menu } from "@/components/ui/Menu";
import { dueInfo } from "@/lib/format";
import { STATUSES } from "@/lib/tasks";
import { PriorityIcon, StatusIcon, TypeIcon } from "./icons";

export function DueChip({ due, today, done }: { due: string | null; today: string; done?: boolean }) {
  if (!due) return null;
  const info = dueInfo(due, today);
  const urgent = !done && (info.state === "overdue" || info.state === "today");
  return (
    <span
      className={clsx(
        "inline-flex items-center gap-1 text-[11.5px] whitespace-nowrap",
        done ? "text-fg-3" : urgent ? "text-critical-ink" : info.state === "soon" ? "text-fg-2" : "text-fg-3",
      )}
      title={`Due ${due}`}
    >
      <CalendarClock className="size-3" aria-hidden />
      {info.label}
    </span>
  );
}

/** Board card. Draggable on pointer devices; the status menu does the same job on touch. */
export function TaskCard({
  task,
  today,
  onOpen,
  onStatus,
  showProject = false,
  draggable = false,
  dragging = false,
  onDragStart,
  onDragEnd,
}: {
  task: Task;
  today: string;
  onOpen: (id: number) => void;
  onStatus?: (status: TaskStatus) => void;
  showProject?: boolean;
  draggable?: boolean;
  dragging?: boolean;
  onDragStart?: (e: React.DragEvent) => void;
  onDragEnd?: () => void;
}) {
  return (
    <div
      role="button"
      tabIndex={0}
      draggable={draggable}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onClick={() => onOpen(task.id)}
      onKeyDown={(e) => {
        // Only the card itself: Enter on the status menu inside must not open the drawer.
        if (e.target !== e.currentTarget) return;
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onOpen(task.id);
        }
      }}
      aria-label={`${task.ref}: ${task.title}`}
      className={clsx(
        "group rounded-lg border border-line bg-surface px-2.5 py-2 text-left shadow-[0_1px_0_rgba(0,0,0,0.03)] transition-[border,opacity]",
        "hover:border-line-strong",
        dragging && "opacity-40",
      )}
    >
      <div className="flex items-center gap-1.5 text-[11.5px] text-fg-3">
        {showProject ? <ColorDot color={task.project_color} className="size-2" /> : null}
        <span className="font-mono">{task.ref}</span>
        <TypeIcon type={task.type} className="size-3" />
        {task.focus ? <Star className="size-3 fill-warning text-warning" aria-label="In today's focus" /> : null}
        <span className="ml-auto flex items-center gap-1">
          <PriorityIcon priority={task.priority} />
          {onStatus ? (
            <Menu
              label={`Change status of ${task.ref}`}
              triggerClassName="-m-0.5 rounded p-0.5 hover:bg-surface-2"
              trigger={<StatusIcon status={task.status} />}
              items={STATUSES.map((s) => ({
                label: s.label,
                icon: <StatusIcon status={s.value} />,
                checked: s.value === task.status,
                onSelect: () => s.value !== task.status && onStatus(s.value),
              }))}
            />
          ) : null}
        </span>
      </div>
      <p className={clsx("mt-1 line-clamp-3 text-[13px] leading-snug", task.status === "done" ? "text-fg-2" : "text-fg")}>
        {task.title}
      </p>
      {task.labels.length || task.due_date || task.comment_count || task.repo_name ? (
        <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1">
          <DueChip due={task.due_date} today={today} done={task.status === "done"} />
          {task.labels.slice(0, 3).map((l) => (
            <span key={l} className="rounded bg-surface-2 px-1.5 text-[11px] text-fg-2">
              {l}
            </span>
          ))}
          {task.repo_name ? <span className="truncate text-[11px] text-fg-3">{task.repo_name}</span> : null}
          {task.comment_count ? (
            <span className="ml-auto inline-flex items-center gap-0.5 text-[11px] text-fg-3">
              <MessageSquare className="size-3" aria-hidden />
              {task.comment_count}
            </span>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/** Dashboard focus / overdue row: tick to complete, click to open. */
export function FocusRow({
  task,
  today,
  onOpen,
  onComplete,
}: {
  task: Task;
  today: string;
  onOpen: (id: number) => void;
  onComplete: (task: Task) => void;
}) {
  const done = task.status === "done";
  return (
    <div className="group flex items-start gap-2.5 rounded-md px-2 py-1.5 hover:bg-surface-2">
      <button
        type="button"
        onClick={() => onComplete(task)}
        aria-label={done ? `${task.ref} is done` : `Mark ${task.ref} done`}
        aria-pressed={done}
        className={clsx(
          "mt-0.5 grid size-4 shrink-0 place-items-center rounded-full border transition-colors",
          done ? "border-good bg-good text-white" : "border-line-strong hover:border-good",
        )}
      >
        {done ? <Check className="size-3" strokeWidth={3} /> : null}
      </button>
      <button type="button" onClick={() => onOpen(task.id)} className="min-w-0 flex-1 text-left">
        <span className={clsx("block text-[13px] leading-snug", done ? "text-fg-3 line-through" : "text-fg")}>
          {task.title}
        </span>
        <span className="mt-0.5 flex flex-wrap items-center gap-x-2 text-[11.5px] text-fg-3">
          <span className="inline-flex items-center gap-1">
            <ColorDot color={task.project_color} className="size-2" />
            <span className="font-mono">{task.ref}</span>
          </span>
          <DueChip due={task.due_date} today={today} done={done} />
        </span>
      </button>
      <PriorityIcon priority={task.priority} className="mt-0.5 size-3.5" />
    </div>
  );
}
