import { Plus, Trash2, X } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useCreateProject, useDeleteProject, useUpdateProject } from "@/api/hooks";
import type { Category, Link, Project, ProjectStatus } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog, Dialog } from "@/components/ui/Dialog";
import { Field, Input, Select, Textarea } from "@/components/ui/Input";
import { useToast } from "@/components/ui/Toast";
import { PROJECT_COLORS } from "@/lib/projects";
import { PROJECT_STATUSES } from "@/lib/tasks";

const KEY_RE = /^[A-Z][A-Z0-9]{1,9}$/;

export function ProjectDialog({ project, onClose }: { project?: Project; onClose: () => void }) {
  const editing = !!project;
  const toast = useToast();
  const navigate = useNavigate();
  const create = useCreateProject();
  const update = useUpdateProject(project?.key ?? "");
  const del = useDeleteProject();

  const [key, setKey] = useState(project?.key ?? "");
  const [name, setName] = useState(project?.name ?? "");
  const [category, setCategory] = useState<Category>(project?.category ?? "personal");
  const [status, setStatus] = useState<ProjectStatus>(project?.status ?? "building");
  const [priority, setPriority] = useState(project?.priority ?? 3);
  const [color, setColor] = useState(project?.color ?? PROJECT_COLORS[0]);
  const [summary, setSummary] = useState(project?.summary ?? "");
  const [targetDate, setTargetDate] = useState(project?.target_date ?? "");
  const [links, setLinks] = useState<Link[]>(project?.links ?? []);
  const [description, setDescription] = useState(project?.description ?? "");
  const [infraNotes, setInfraNotes] = useState(project?.infra_notes ?? "");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [typedKey, setTypedKey] = useState("");

  const keyError = !editing && key && !KEY_RE.test(key) ? "2–10 letters/digits, starting with a letter" : undefined;
  const pending = create.isPending || update.isPending;

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const body = {
      name: name.trim(),
      category,
      status,
      priority,
      color,
      summary: summary.trim(),
      target_date: targetDate || null,
      links: links.filter((l) => l.url.trim()).map((l) => ({ label: l.label.trim() || l.url.trim(), url: l.url.trim() })),
      description,
      infra_notes: infraNotes,
    };
    if (editing) {
      update.mutate(body, {
        onSuccess: () => {
          toast.success("Project saved");
          onClose();
        },
        onError: (err) => toast.error(err),
      });
    } else {
      create.mutate(
        { ...body, key },
        {
          onSuccess: (p) => {
            toast.success(`Created ${p.name}`);
            onClose();
            navigate(`/p/${p.key}`);
          },
          onError: (err) => toast.error(err),
        },
      );
    }
  };

  return (
    <Dialog
      open
      onClose={onClose}
      size="lg"
      title={editing ? `Edit ${project.name}` : "New project"}
      footer={
        <>
          {editing ? (
            <Button variant="ghost" className="mr-auto text-critical-ink" onClick={() => setConfirmDelete(true)}>
              <Trash2 className="size-3.5" aria-hidden /> Delete
            </Button>
          ) : null}
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            type="submit"
            form="project-form"
            loading={pending}
            disabled={!name.trim() || (!editing && !KEY_RE.test(key))}
          >
            {editing ? "Save" : "Create project"}
          </Button>
        </>
      }
    >
      <form id="project-form" onSubmit={submit} className="space-y-4">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[140px_1fr]">
          <Field label="Key" error={keyError} hint={editing ? "Keys are permanent" : "Prefix for task refs"}>
            {(id, desc) => (
              <Input
                id={id}
                aria-describedby={desc}
                aria-invalid={!!keyError}
                value={key}
                disabled={editing}
                onChange={(e) => setKey(e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, "").slice(0, 10))}
                placeholder="SHOP"
                className="font-mono"
              />
            )}
          </Field>
          <Field label="Name">
            {(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} required maxLength={80} />}
          </Field>
        </div>
        <Field label="Summary" hint="One line, shown on cards">
          {(id, desc) => (
            <Input id={id} aria-describedby={desc} value={summary} onChange={(e) => setSummary(e.target.value)} maxLength={200} />
          )}
        </Field>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Field label="Status">
            {(id) => (
              <Select id={id} value={status} onChange={(e) => setStatus(e.target.value as ProjectStatus)}>
                {PROJECT_STATUSES.map((s) => (
                  <option key={s.value} value={s.value}>
                    {s.label}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label="Category">
            {(id) => (
              <Select id={id} value={category} onChange={(e) => setCategory(e.target.value as Category)}>
                <option value="work">Work</option>
                <option value="personal">Personal</option>
              </Select>
            )}
          </Field>
          <Field label="Priority" hint="1 = top focus">
            {(id, desc) => (
              <Select id={id} aria-describedby={desc} value={priority} onChange={(e) => setPriority(Number(e.target.value))}>
                {[1, 2, 3, 4, 5].map((p) => (
                  <option key={p} value={p}>
                    P{p}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label="Target date">
            {(id) => <Input id={id} type="date" value={targetDate} onChange={(e) => setTargetDate(e.target.value)} />}
          </Field>
        </div>
        <fieldset>
          <legend className="mb-1.5 text-[13px] font-medium text-fg-2">Colour</legend>
          <div className="flex flex-wrap items-center gap-2">
            {PROJECT_COLORS.map((c) => (
              <button
                key={c}
                type="button"
                onClick={() => setColor(c)}
                aria-label={`Colour ${c}`}
                aria-pressed={color.toLowerCase() === c}
                className="size-6 rounded-md ring-offset-2 ring-offset-surface aria-pressed:ring-2 aria-pressed:ring-fg"
                style={{ background: c }}
              />
            ))}
            <input
              type="color"
              aria-label="Custom colour"
              value={color}
              onChange={(e) => setColor(e.target.value)}
              className="h-6 w-8 cursor-pointer rounded border border-line-strong bg-transparent"
            />
          </div>
        </fieldset>
        <fieldset className="space-y-2">
          <legend className="mb-1.5 text-[13px] font-medium text-fg-2">Links</legend>
          {links.map((l, i) => (
            <div key={i} className="flex gap-2">
              <Input
                aria-label="Link label"
                placeholder="Label"
                value={l.label}
                className="w-36"
                onChange={(e) => setLinks(links.map((x, j) => (j === i ? { ...x, label: e.target.value } : x)))}
              />
              <Input
                aria-label="Link URL"
                placeholder="https://"
                type="url"
                value={l.url}
                onChange={(e) => setLinks(links.map((x, j) => (j === i ? { ...x, url: e.target.value } : x)))}
              />
              <Button size="icon" variant="ghost" aria-label="Remove link" onClick={() => setLinks(links.filter((_, j) => j !== i))}>
                <X className="size-4" />
              </Button>
            </div>
          ))}
          <Button size="sm" variant="ghost" onClick={() => setLinks([...links, { label: "", url: "" }])}>
            <Plus className="size-3.5" aria-hidden /> Add link
          </Button>
        </fieldset>
        <Field label="Description" hint="Markdown">
          {(id, desc) => (
            <Textarea
              id={id}
              aria-describedby={desc}
              rows={6}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              className="font-mono text-[13px]"
            />
          )}
        </Field>
        <Field label="Infrastructure notes" hint="Markdown — deploy workflow, hosts, gotchas">
          {(id, desc) => (
            <Textarea
              id={id}
              aria-describedby={desc}
              rows={6}
              value={infraNotes}
              onChange={(e) => setInfraNotes(e.target.value)}
              className="font-mono text-[13px]"
            />
          )}
        </Field>
      </form>
      {editing ? (
        <ConfirmDialog
          open={confirmDelete}
          onClose={() => setConfirmDelete(false)}
          title={`Delete ${project.name}?`}
          confirmLabel="Delete project"
          loading={del.isPending}
          body={
            <div className="space-y-3">
              <p>
                Deletes every task, repo, endpoint and run of this project. There is no undo — consider setting the
                status to Archived instead.
              </p>
              <Input
                aria-label={`Type ${project.key} to confirm`}
                placeholder={`Type ${project.key} to confirm`}
                value={typedKey}
                onChange={(e) => setTypedKey(e.target.value.toUpperCase())}
                className="font-mono"
              />
            </div>
          }
          onConfirm={() => {
            if (typedKey !== project.key) {
              toast.error(`Type ${project.key} to confirm`);
              return;
            }
            del.mutate(project.key, {
              onSuccess: () => {
                toast.success(`Deleted ${project.name}`);
                onClose();
                navigate("/projects");
              },
              onError: (err) => toast.error(err),
            });
          }}
        />
      ) : null}
    </Dialog>
  );
}
