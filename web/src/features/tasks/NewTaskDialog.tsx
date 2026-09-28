import { useState } from "react";
import { useCreateTask, useProject, useProjects } from "@/api/hooks";
import type { Priority, TaskStatus, TaskType } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Field, Input, Select, Switch, Textarea } from "@/components/ui/Input";
import { useToast } from "@/components/ui/Toast";
import type { NewTaskPrefill } from "@/features/shell/context";
import { useShell } from "@/features/shell/context";
import { normalizeLabel, PRIORITIES, STATUSES, TYPES } from "@/lib/tasks";

export function NewTaskDialog({ prefill, onClose }: { prefill: NewTaskPrefill; onClose: () => void }) {
  const projects = useProjects();
  const toast = useToast();
  const { openTask } = useShell();
  const create = useCreateTask();

  const [projectKey, setProjectKey] = useState(prefill.project_key ?? "");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [status, setStatus] = useState<TaskStatus>(prefill.status ?? "todo");
  const [priority, setPriority] = useState<Priority>("medium");
  const [type, setType] = useState<TaskType>("feature");
  const [dueDate, setDueDate] = useState("");
  const [labels, setLabels] = useState("");
  const [focus, setFocus] = useState(prefill.focus ?? false);
  const [repoId, setRepoId] = useState("");
  const [openAfter, setOpenAfter] = useState(false);

  const effectiveKey = projectKey || projects.data?.[0]?.key || "";
  const project = useProject(effectiveKey || undefined);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!title.trim() || !effectiveKey) return;
    create.mutate(
      {
        project_key: effectiveKey,
        title: title.trim(),
        description,
        status,
        priority,
        type,
        due_date: dueDate || null,
        labels: labels
          .split(",")
          .map(normalizeLabel)
          .filter(Boolean),
        focus,
        repo_id: repoId ? Number(repoId) : null,
      },
      {
        onSuccess: (task) => {
          toast.success(`Created ${task.ref}`);
          onClose();
          if (openAfter) openTask(task.id);
        },
        onError: (err) => toast.error(err),
      },
    );
  };

  return (
    <Dialog
      open
      onClose={onClose}
      title="New task"
      size="lg"
      footer={
        <>
          <label className="mr-auto flex items-center gap-2 text-[13px] text-fg-2">
            <input type="checkbox" checked={openAfter} onChange={(e) => setOpenAfter(e.target.checked)} />
            Open after creating
          </label>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="new-task-form" loading={create.isPending} disabled={!title.trim()}>
            Create task
          </Button>
        </>
      }
    >
      <form id="new-task-form" onSubmit={submit} className="space-y-4">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[180px_1fr]">
          <Field label="Project">
            {(id) => (
              <Select id={id} value={effectiveKey} onChange={(e) => { setProjectKey(e.target.value); setRepoId(""); }}>
                {projects.data?.map((p) => (
                  <option key={p.key} value={p.key}>
                    {p.key} · {p.name}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label="Title">
            {(id) => (
              <Input
                id={id}
                data-autofocus
                autoFocus
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                placeholder="What needs to happen?"
                maxLength={300}
                required
              />
            )}
          </Field>
        </div>
        <Field label="Description" hint="Markdown">
          {(id, desc) => (
            <Textarea
              id={id}
              aria-describedby={desc}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={5}
              placeholder="Context, acceptance criteria, links…"
            />
          )}
        </Field>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Field label="Status">
            {(id) => (
              <Select id={id} value={status} onChange={(e) => setStatus(e.target.value as TaskStatus)}>
                {STATUSES.map((s) => (
                  <option key={s.value} value={s.value}>
                    {s.label}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label="Priority">
            {(id) => (
              <Select id={id} value={priority} onChange={(e) => setPriority(e.target.value as Priority)}>
                {PRIORITIES.map((s) => (
                  <option key={s.value} value={s.value}>
                    {s.label}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label="Type">
            {(id) => (
              <Select id={id} value={type} onChange={(e) => setType(e.target.value as TaskType)}>
                {TYPES.map((s) => (
                  <option key={s.value} value={s.value}>
                    {s.label}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label="Due">
            {(id) => <Input id={id} type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} />}
          </Field>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Labels" hint="Comma separated">
            {(id, desc) => (
              <Input id={id} aria-describedby={desc} value={labels} onChange={(e) => setLabels(e.target.value)} placeholder="launch, backend" />
            )}
          </Field>
          <Field label="Repository">
            {(id) => (
              <Select id={id} value={repoId} onChange={(e) => setRepoId(e.target.value)}>
                <option value="">None</option>
                {project.data?.repos.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.name}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
        <Switch checked={focus} onChange={setFocus} label="Add to today's focus" />
      </form>
    </Dialog>
  );
}
