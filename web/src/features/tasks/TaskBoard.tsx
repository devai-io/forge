// Kanban board for one project.
//
// Native HTML5 drag-and-drop: a card's insertion slot is decided by which half
// of a neighbouring card the pointer is over, and the move is one PATCH of
// {status, sort_order} with sort_order the midpoint of the new neighbours
// (lib/order). Touch devices don't get HTML5 DnD, so every card also has a
// status menu — that is the phone's way of moving work.

import clsx from "clsx";
import { Plus } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { useCreateTask, useUpdateTask } from "@/api/hooks";
import type { Task, TaskStatus } from "@/api/types";
import { useToast } from "@/components/ui/Toast";
import { orderAtEnd, orderAtStart, orderForDrop } from "@/lib/order";
import { groupByStatus, STATUSES } from "@/lib/tasks";
import { StatusIcon } from "./icons";
import { TaskCard } from "./TaskCard";

const DONE_VISIBLE = 25;

export function TaskBoard({
  tasks,
  projectKey,
  today,
  onOpen,
}: {
  tasks: Task[];
  projectKey: string;
  today: string;
  onOpen: (id: number) => void;
}) {
  const columns = useMemo(() => groupByStatus(tasks), [tasks]);
  const update = useUpdateTask();
  const toast = useToast();
  const [dragId, setDragId] = useState<number | null>(null);
  const [drop, setDrop] = useState<{ status: TaskStatus; index: number } | null>(null);

  const move = (task: Task, status: TaskStatus, sortOrder: number) =>
    update.mutate({ id: task.id, status, sort_order: sortOrder }, { onError: (e) => toast.error(e) });

  const onDrop = (status: TaskStatus) => {
    const task = tasks.find((t) => t.id === dragId);
    const target = drop;
    setDragId(null);
    setDrop(null);
    if (!task || !target || target.status !== status) return;
    // Within its own column the dragged card is part of `column`; orderForDrop
    // accounts for that (and returns null for a drop back where it was).
    const order = orderForDrop(columns[status], task.id, target.index);
    if (order === null) return;
    move(task, status, order);
  };

  return (
    <div className="-mx-4 overflow-x-auto px-4 pb-4 sm:-mx-6 sm:px-6">
      <div className="grid auto-cols-[minmax(228px,1fr)] grid-flow-col gap-3">
        {STATUSES.map(({ value: status, label }) => (
          <Column
            key={status}
            status={status}
            label={label}
            tasks={columns[status]}
            projectKey={projectKey}
            today={today}
            dragId={dragId}
            drop={drop?.status === status ? drop.index : null}
            onOpen={onOpen}
            onDragOverIndex={(index) => setDrop({ status, index })}
            onDrop={() => onDrop(status)}
            onDragStart={(id) => setDragId(id)}
            onDragEnd={() => {
              setDragId(null);
              setDrop(null);
            }}
            onStatus={(task, next) => move(task, next, orderAtEnd(columns[next]))}
          />
        ))}
      </div>
    </div>
  );
}

function Column({
  status,
  label,
  tasks,
  projectKey,
  today,
  dragId,
  drop,
  onOpen,
  onDragOverIndex,
  onDrop,
  onDragStart,
  onDragEnd,
  onStatus,
}: {
  status: TaskStatus;
  label: string;
  tasks: Task[];
  projectKey: string;
  today: string;
  dragId: number | null;
  drop: number | null;
  onOpen: (id: number) => void;
  onDragOverIndex: (index: number) => void;
  onDrop: () => void;
  onDragStart: (id: number) => void;
  onDragEnd: () => void;
  onStatus: (task: Task, status: TaskStatus) => void;
}) {
  const [adding, setAdding] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const list = useRef<HTMLDivElement>(null);
  const visible = status === "done" && !showAll ? tasks.slice(-DONE_VISIBLE) : tasks;
  const hidden = tasks.length - visible.length;
  const offset = hidden; // visible cards start at this index of the full column

  const indexFromPointer = (clientY: number): number => {
    const cards = Array.from(list.current?.querySelectorAll<HTMLElement>("[data-card]") ?? []);
    for (let i = 0; i < cards.length; i++) {
      const rect = cards[i].getBoundingClientRect();
      if (clientY < rect.top + rect.height / 2) return offset + i;
    }
    return offset + cards.length;
  };

  return (
    <section
      aria-label={`${label} column`}
      className={clsx(
        "flex min-w-0 flex-col rounded-xl border border-line bg-surface-2/40 transition-colors",
        drop !== null && "border-accent/50 bg-accent/5",
      )}
      onDragOver={(e) => {
        if (dragId === null) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = "move";
        onDragOverIndex(indexFromPointer(e.clientY));
      }}
      onDrop={(e) => {
        e.preventDefault();
        onDrop();
      }}
    >
      <header className="flex h-10 items-center gap-2 px-3">
        <StatusIcon status={status} />
        <h3 className="text-[13px] font-medium">{label}</h3>
        <span className="tabular text-xs text-fg-3">{tasks.length}</span>
        <button
          type="button"
          onClick={() => setAdding((a) => !a)}
          className="ml-auto rounded-md p-1 text-fg-3 hover:bg-surface-3 hover:text-fg"
          aria-label={`Add task to ${label}`}
          aria-expanded={adding}
        >
          <Plus className="size-4" />
        </button>
      </header>
      <div ref={list} className="flex min-h-24 flex-1 flex-col gap-1.5 px-2 pb-2">
        {adding ? (
          <QuickAdd projectKey={projectKey} status={status} column={tasks} onDone={() => setAdding(false)} />
        ) : null}
        {hidden > 0 ? (
          <button
            type="button"
            onClick={() => setShowAll(true)}
            className="rounded-md py-1 text-xs text-fg-3 hover:bg-surface-3 hover:text-fg-2"
          >
            Show {hidden} older
          </button>
        ) : null}
        {visible.map((task, i) => (
          <div key={task.id} data-card>
            {drop === offset + i ? <DropLine /> : null}
            <TaskCard
              task={task}
              today={today}
              onOpen={onOpen}
              onStatus={(s) => onStatus(task, s)}
              draggable
              dragging={dragId === task.id}
              onDragStart={(e) => {
                e.dataTransfer.effectAllowed = "move";
                e.dataTransfer.setData("text/plain", task.ref);
                onDragStart(task.id);
              }}
              onDragEnd={onDragEnd}
            />
          </div>
        ))}
        {drop === offset + visible.length ? <DropLine /> : null}
        {!tasks.length && !adding ? <p className="px-1 py-3 text-center text-xs text-fg-3">No tasks</p> : null}
      </div>
    </section>
  );
}

function DropLine() {
  return <div aria-hidden className="my-0.5 h-0.5 rounded-full bg-accent" />;
}

function QuickAdd({
  projectKey,
  status,
  column,
  onDone,
}: {
  projectKey: string;
  status: TaskStatus;
  column: Task[];
  onDone: () => void;
}) {
  const [title, setTitle] = useState("");
  const create = useCreateTask();
  const update = useUpdateTask();
  const toast = useToast();

  const submit = () => {
    const t = title.trim();
    if (!t) return onDone();
    create.mutate(
      { project_key: projectKey, title: t, status },
      {
        onSuccess: (task) => {
          setTitle("");
          // The API appends to the bottom of the column; this box sits at the
          // top, so the new card is moved to where it was typed.
          if (column.length) update.mutate({ id: task.id, sort_order: orderAtStart(column) });
        },
        onError: (e) => toast.error(e),
      },
    );
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
      className="rounded-lg border border-accent/50 bg-surface p-1.5"
    >
      <textarea
        autoFocus
        rows={2}
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            submit();
          } else if (e.key === "Escape") {
            onDone();
          }
        }}
        onBlur={() => !title.trim() && onDone()}
        placeholder="Task title — Enter to add"
        aria-label="New task title"
        className="w-full resize-none bg-transparent px-1 text-[13px] outline-none placeholder:text-fg-3"
      />
    </form>
  );
}
