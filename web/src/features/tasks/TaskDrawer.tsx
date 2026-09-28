// The task drawer: everything about one task, editable in place.
//
// Opened by `?task=<id>` on any page, so a link to a task works from the
// dashboard, the board or a chat message. Each field saves on its own (one
// PATCH per change) — there is no "Save" button to forget on a phone.

import clsx from "clsx";
import { Bot, Copy, ExternalLink, MoreHorizontal, Star, Trash2, X } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { ApiError } from "@/api/client";
import { useAddComment, useDeleteComment, useDeleteTask, useProject, useTask, useUpdateTask } from "@/api/hooks";
import type { Priority, TaskDetail, TaskInput, TaskStatus, TaskType } from "@/api/types";
import { ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog, Sheet } from "@/components/ui/Dialog";
import { EmptyState } from "@/components/ui/EmptyState";
import { Input, Select, Textarea } from "@/components/ui/Input";
import { Markdown } from "@/components/ui/Markdown";
import { Menu } from "@/components/ui/Menu";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton } from "@/components/ui/Skeleton";
import { Segmented } from "@/components/ui/Tabs";
import { useToast } from "@/components/ui/Toast";
import { RunRow } from "@/features/agents/RunBits";
import { useShell } from "@/features/shell/context";
import { dueInfo, todayInTz } from "@/lib/format";
import { useCurrentUser } from "@/lib/auth";
import { buildAgentPrompt, PRIORITIES, STATUSES, TYPES } from "@/lib/tasks";
import { PriorityIcon, StatusIcon, TypeIcon } from "./icons";
import { LabelsEditor } from "./LabelsEditor";

export function TaskDrawer() {
  const { taskId, closeTask } = useShell();
  return (
    <Sheet open={taskId !== null} onClose={closeTask} label="Task">
      {taskId !== null ? <TaskPanel key={taskId} id={taskId} onClose={closeTask} /> : null}
    </Sheet>
  );
}

function TaskPanel({ id, onClose }: { id: number; onClose: () => void }) {
  const task = useTask(id);
  const notFound = task.error instanceof ApiError && task.error.status === 404;

  return (
    <>
      <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-3">
        {task.data ? (
          <Link
            to={`/p/${task.data.project_key}`}
            onClick={onClose}
            className="flex min-w-0 items-center gap-1.5 text-[13px] text-fg-2 hover:text-fg"
          >
            <ColorDot color={task.data.project_color} />
            <span className="truncate">{task.data.project_name}</span>
            <span className="font-mono text-fg-3">{task.data.ref}</span>
          </Link>
        ) : (
          <Skeleton className="h-4 w-40" />
        )}
        <div className="ml-auto flex items-center gap-1">
          {task.data ? <HeaderActions task={task.data} onClose={onClose} /> : null}
          <button
            type="button"
            onClick={onClose}
            className="rounded-md p-1.5 text-fg-3 hover:bg-surface-2 hover:text-fg"
            aria-label="Close task"
          >
            <X className="size-4" />
          </button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {notFound ? (
          <div className="p-6">
            <EmptyState title="Task not found">It may have been deleted.</EmptyState>
          </div>
        ) : task.data ? (
          <TaskBody task={task.data} />
        ) : (
          <div className="space-y-4 p-5">
            <Skeleton className="h-7 w-3/4" />
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-40 w-full" />
          </div>
        )}
      </div>
    </>
  );
}

function HeaderActions({ task, onClose }: { task: TaskDetail; onClose: () => void }) {
  const update = useUpdateTask();
  const del = useDeleteTask();
  const toast = useToast();
  const { newRun } = useShell();
  const navigate = useNavigate();
  const [confirm, setConfirm] = useState(false);

  return (
    <>
      <button
        type="button"
        onClick={() => update.mutate({ id: task.id, focus: !task.focus }, { onError: (e) => toast.error(e) })}
        className={clsx(
          "inline-flex items-center gap-1 rounded-md px-2 py-1 text-[13px] hover:bg-surface-2",
          task.focus ? "text-fg" : "text-fg-3",
        )}
        aria-pressed={task.focus}
        title={task.focus ? "Remove from today's focus" : "Add to today's focus"}
      >
        <Star className={clsx("size-4", task.focus && "fill-warning text-warning")} aria-hidden />
        <span className="hidden sm:inline">{task.focus ? "Focused" : "Focus"}</span>
      </button>
      <Button
        size="sm"
        variant="subtle"
        onClick={() =>
          newRun({
            project_key: task.project_key,
            repo_id: task.repo_id ?? undefined,
            task_id: task.id,
            task_ref: task.ref,
            prompt: buildAgentPrompt(task),
          })
        }
      >
        <Bot className="size-3.5" aria-hidden />
        <span className="hidden sm:inline">Run agent</span>
      </Button>
      <Menu
        label="Task actions"
        triggerClassName="rounded-md p-1.5 text-fg-3 hover:bg-surface-2 hover:text-fg"
        trigger={<MoreHorizontal className="size-4" />}
        items={[
          {
            label: "Copy link",
            icon: <Copy />,
            onSelect: () => {
              const url = `${window.location.origin}/p/${task.project_key}?task=${task.id}`;
              void navigator.clipboard?.writeText(url).then(
                () => toast.success("Link copied"),
                () => toast.error("Couldn't copy the link"),
              );
            },
          },
          {
            label: "Open project board",
            icon: <ExternalLink />,
            onSelect: () => navigate(`/p/${task.project_key}?tab=board&task=${task.id}`),
          },
          "separator",
          { label: "Delete task", icon: <Trash2 />, danger: true, onSelect: () => setConfirm(true) },
        ]}
      />
      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        title={`Delete ${task.ref}?`}
        body="The task, its comments and its links to runs are removed. This cannot be undone."
        loading={del.isPending}
        onConfirm={() =>
          del.mutate(task.id, {
            onSuccess: () => {
              toast.success(`Deleted ${task.ref}`);
              setConfirm(false);
              onClose();
            },
            onError: (e) => toast.error(e),
          })
        }
      />
    </>
  );
}

function TaskBody({ task }: { task: TaskDetail }) {
  const update = useUpdateTask();
  const toast = useToast();
  const project = useProject(task.project_key);
  const { user } = useCurrentUser();
  const today = todayInTz(user?.timezone);
  const { newRun } = useShell();

  const save = (patch: TaskInput) =>
    update.mutate({ id: task.id, ...patch }, { onError: (e) => toast.error(e) });

  const due = task.due_date ? dueInfo(task.due_date, today) : null;

  return (
    <div className="space-y-6 px-4 py-4 sm:px-5">
      <TitleEditor title={task.title} onSave={(title) => save({ title })} />

      <dl className="grid grid-cols-[96px_1fr] items-center gap-x-3 gap-y-2 text-[13px]">
        <dt className="text-fg-3">Status</dt>
        <dd className="flex items-center gap-2">
          <StatusIcon status={task.status} />
          <Select
            aria-label="Status"
            compact
            className="max-w-44"
            value={task.status}
            onChange={(e) => save({ status: e.target.value as TaskStatus })}
          >
            {STATUSES.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
          </Select>
        </dd>
        <dt className="text-fg-3">Priority</dt>
        <dd className="flex items-center gap-2">
          <PriorityIcon priority={task.priority} />
          <Select
            aria-label="Priority"
            compact
            className="max-w-44"
            value={task.priority}
            onChange={(e) => save({ priority: e.target.value as Priority })}
          >
            {PRIORITIES.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
          </Select>
        </dd>
        <dt className="text-fg-3">Type</dt>
        <dd className="flex items-center gap-2">
          <TypeIcon type={task.type} />
          <Select
            aria-label="Type"
            compact
            className="max-w-44"
            value={task.type}
            onChange={(e) => save({ type: e.target.value as TaskType })}
          >
            {TYPES.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
          </Select>
        </dd>
        <dt className="text-fg-3">Due</dt>
        <dd className="flex flex-wrap items-center gap-2">
          <Input
            aria-label="Due date"
            type="date"
            compact
            className="max-w-44"
            value={task.due_date ?? ""}
            onChange={(e) => save({ due_date: e.target.value || null })}
          />
          {due && task.status !== "done" ? (
            <span className={clsx("text-xs", due.state === "overdue" ? "text-critical-ink" : "text-fg-3")}>{due.label}</span>
          ) : null}
        </dd>
        <dt className="text-fg-3">Repository</dt>
        <dd>
          <Select
            aria-label="Repository"
            compact
            className="max-w-60"
            value={task.repo_id ?? ""}
            onChange={(e) => save({ repo_id: e.target.value ? Number(e.target.value) : null })}
          >
            <option value="">None</option>
            {project.data?.repos.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
            {task.repo_id && !project.data?.repos.some((r) => r.id === task.repo_id) ? (
              <option value={task.repo_id}>{task.repo_name ?? `#${task.repo_id}`}</option>
            ) : null}
          </Select>
        </dd>
        <dt className="text-fg-3">Estimate</dt>
        <dd>
          <EstimateInput value={task.estimate} onSave={(estimate) => save({ estimate })} />
        </dd>
        <dt className="self-start pt-1.5 text-fg-3">Labels</dt>
        <dd>
          <LabelsEditor labels={task.labels} onChange={(labels) => save({ labels })} />
        </dd>
      </dl>

      <DescriptionEditor value={task.description} onSave={(description) => save({ description })} />

      <section aria-labelledby="task-runs" className="space-y-2">
        <div className="flex items-center justify-between">
          <h3 id="task-runs" className="text-[13px] font-semibold text-fg-2">
            Agent runs
          </h3>
          <Button
            size="sm"
            variant="ghost"
            onClick={() =>
              newRun({
                project_key: task.project_key,
                repo_id: task.repo_id ?? undefined,
                task_id: task.id,
                task_ref: task.ref,
                prompt: buildAgentPrompt(task),
              })
            }
          >
            <Bot className="size-3.5" aria-hidden /> Run agent on this task
          </Button>
        </div>
        {task.runs.length ? (
          <div className="-mx-2">
            {task.runs.map((r) => (
              <RunRow key={r.id} run={r} showProject={false} />
            ))}
          </div>
        ) : (
          <p className="text-[13px] text-fg-3">No runs yet.</p>
        )}
      </section>

      <Comments task={task} />

      <p className="border-t border-line pt-3 text-xs text-fg-3">
        Created <RelativeTime iso={task.created_at} /> · updated <RelativeTime iso={task.updated_at} />
        {task.completed_at ? (
          <>
            {" "}
            · completed <RelativeTime iso={task.completed_at} />
          </>
        ) : null}
      </p>
    </div>
  );
}

function TitleEditor({ title, onSave }: { title: string; onSave: (t: string) => void }) {
  const [draft, setDraft] = useState(title);
  useEffect(() => setDraft(title), [title]);
  const commit = () => {
    const t = draft.trim();
    if (t && t !== title) onSave(t);
    else setDraft(title);
  };
  return (
    <textarea
      aria-label="Title"
      value={draft}
      rows={1}
      onChange={(e) => setDraft(e.target.value.replace(/\n/g, ""))}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          (e.target as HTMLTextAreaElement).blur();
        } else if (e.key === "Escape") {
          setDraft(title);
        }
      }}
      className="field-sizing-content w-full resize-none rounded-md border border-transparent bg-transparent px-1 py-0.5 text-lg leading-snug font-semibold hover:border-line focus:border-accent focus:outline-none"
    />
  );
}

function EstimateInput({ value, onSave }: { value: number | null; onSave: (v: number | null) => void }) {
  const [draft, setDraft] = useState(value === null ? "" : String(value));
  useEffect(() => setDraft(value === null ? "" : String(value)), [value]);
  const commit = () => {
    const next = draft.trim() === "" ? null : Number(draft);
    if (next !== null && (!Number.isFinite(next) || next < 0)) {
      setDraft(value === null ? "" : String(value));
      return;
    }
    if (next !== value) onSave(next);
  };
  return (
    <Input
      aria-label="Estimate in points"
      type="number"
      min={0}
      step={0.5}
      inputMode="decimal"
      compact
      className="w-24"
      placeholder="pts"
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
    />
  );
}

function DescriptionEditor({ value, onSave }: { value: string; onSave: (v: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);
  const [view, setView] = useState<"write" | "preview">("write");

  const start = () => {
    setDraft(value);
    setView("write");
    setEditing(true);
  };
  const save = () => {
    if (draft !== value) onSave(draft);
    setEditing(false);
  };

  return (
    <section aria-labelledby="task-description" className="space-y-2">
      <div className="flex items-center justify-between">
        <h3 id="task-description" className="text-[13px] font-semibold text-fg-2">
          Description
        </h3>
        {editing ? (
          <Segmented
            size="sm"
            label="Description view"
            value={view}
            onChange={setView}
            items={[
              { value: "write", label: "Write" },
              { value: "preview", label: "Preview" },
            ]}
          />
        ) : (
          <Button size="sm" variant="ghost" onClick={start}>
            Edit
          </Button>
        )}
      </div>
      {editing ? (
        <div className="space-y-2">
          {view === "write" ? (
            <Textarea
              aria-label="Description"
              autoFocus
              rows={10}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) save();
                if (e.key === "Escape") {
                  e.preventDefault();
                  setEditing(false);
                }
              }}
              className="font-mono text-[13px]"
            />
          ) : (
            <div className="min-h-24 rounded-md border border-line p-3">
              {draft.trim() ? <Markdown>{draft}</Markdown> : <p className="text-[13px] text-fg-3">Nothing to preview.</p>}
            </div>
          )}
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>
              Cancel
            </Button>
            <Button size="sm" variant="primary" onClick={save}>
              Save
            </Button>
          </div>
        </div>
      ) : value.trim() ? (
        <div onDoubleClick={start}>
          <Markdown>{value}</Markdown>
        </div>
      ) : (
        <button type="button" onClick={start} className="text-[13px] text-fg-3 hover:text-fg-2">
          Add a description…
        </button>
      )}
    </section>
  );
}

function Comments({ task }: { task: TaskDetail }) {
  const add = useAddComment(task.id);
  const del = useDeleteComment(task.id);
  const toast = useToast();
  const [body, setBody] = useState("");

  const submit = () => {
    if (!body.trim()) return;
    add.mutate(body.trim(), {
      onSuccess: () => setBody(""),
      onError: (e) => toast.error(e),
    });
  };

  return (
    <section aria-labelledby="task-comments" className="space-y-3">
      <h3 id="task-comments" className="text-[13px] font-semibold text-fg-2">
        Comments {task.comments.length ? <span className="text-fg-3">· {task.comments.length}</span> : null}
      </h3>
      {task.comments.map((c) => (
        <article key={c.id} className="group rounded-md border border-line bg-surface-2/50 px-3 py-2">
          <div className="mb-1 flex items-center justify-between text-xs text-fg-3">
            <RelativeTime iso={c.created_at} />
            <button
              type="button"
              onClick={() => del.mutate(c.id, { onError: (e) => toast.error(e) })}
              className="rounded p-0.5 opacity-0 group-hover:opacity-100 hover:text-critical-ink focus:opacity-100"
              aria-label="Delete comment"
            >
              <Trash2 className="size-3.5" />
            </button>
          </div>
          <Markdown>{c.body}</Markdown>
        </article>
      ))}
      <div className="space-y-2">
        <Textarea
          aria-label="New comment"
          rows={3}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submit();
          }}
          placeholder="Leave a note… (⌘/Ctrl+Enter)"
        />
        <div className="flex justify-end">
          <Button size="sm" variant="primary" onClick={submit} loading={add.isPending} disabled={!body.trim()}>
            Comment
          </Button>
        </div>
      </div>
    </section>
  );
}
