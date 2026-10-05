// Memory: what Forge keeps between sessions — per project (decisions,
// conventions, gotchas, how-tos that Claude sessions start with) or about
// you (project "global": preferences for every project). Claude, the
// Assistant and Telegram add to it; here you read, search, correct and prune.

import clsx from "clsx";
import { Brain, Pencil, Pin, PinOff, Search, Trash2 } from "lucide-react";
import { useState } from "react";
import { useCreateMemory, useDeleteMemory, useMemories, useUpdateMemory } from "@/api/hooks";
import { MEMORY_KINDS, type Memory, type MemoryKind } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Checkbox, Input, Select, Textarea } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { KIND_HINTS, KIND_LABELS, memorySource, savedMessage } from "@/lib/memory";

/** project: a project key, or "global" for the memories about you. */
export function MemoryPanel({ project, title, intro }: { project: string; title: string; intro?: string }) {
  const [q, setQ] = useState("");
  const memories = useMemories(project, q);
  const [deleting, setDeleting] = useState<Memory | null>(null);
  const del = useDeleteMemory();
  const toast = useToast();
  const list = memories.data?.memories ?? [];
  const total = memories.data?.counts[project === "global" ? "" : project] ?? 0;

  return (
    <Panel title={memories.data ? `${title} · ${total}` : title} icon={<Brain />} bodyClassName="space-y-3 p-3.5">
      {intro ? <p className="text-[13px] text-fg-2">{intro}</p> : null}
      <AddMemory project={project} />
      <div className="relative">
        <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-fg-3" aria-hidden />
        <Input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search memory"
          aria-label="Search memory"
          className="pl-8"
          type="search"
        />
      </div>
      {memories.isPending ? (
        <SkeletonRows rows={3} />
      ) : memories.error ? (
        <ErrorState error={memories.error} onRetry={() => memories.refetch()} />
      ) : list.length === 0 ? (
        <EmptyState compact icon={<Brain />} title={q ? "Nothing matches" : "Nothing remembered yet"}>
          {q
            ? "Search looks for any of the words, in any form (deploy finds deploys)."
            : "Claude saves what it learns here as it works (forge_remember); you can add your own."}
        </EmptyState>
      ) : (
        <ul className="divide-y divide-line rounded-lg border border-line">
          {list.map((m) => (
            <MemoryRow key={m.id} memory={m} onDelete={() => setDeleting(m)} />
          ))}
        </ul>
      )}
      <ConfirmDialog
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title="Forget this memory?"
        body="Sessions stop seeing it. There is no undo."
        loading={del.isPending}
        onConfirm={() =>
          deleting &&
          del.mutate(deleting.id, {
            onSuccess: () => {
              setDeleting(null);
              toast.success("Forgotten");
            },
            onError: (e) => toast.error(e),
          })
        }
      />
    </Panel>
  );
}

function KindSelect({ value, onChange, id }: { value: MemoryKind; onChange: (k: MemoryKind) => void; id?: string }) {
  return (
    <Select id={id} compact className="w-36" value={value} onChange={(e) => onChange(e.target.value as MemoryKind)} aria-label="Kind">
      {MEMORY_KINDS.map((k) => (
        <option key={k} value={k} title={KIND_HINTS[k]}>
          {KIND_LABELS[k]}
        </option>
      ))}
    </Select>
  );
}

function AddMemory({ project }: { project: string }) {
  const create = useCreateMemory();
  const toast = useToast();
  const [content, setContent] = useState("");
  const [kind, setKind] = useState<MemoryKind>(project === "global" ? "preference" : "fact");
  const [pinned, setPinned] = useState(false);
  const ok = content.trim().length >= 3;

  return (
    <form
      className="space-y-2"
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate(
          { project_key: project === "global" ? undefined : project, kind, content: content.trim(), pinned },
          {
            onSuccess: (r) => {
              setContent("");
              setPinned(false);
              toast.success(savedMessage(r.duplicate, r.similar.length));
            },
            onError: (err) => toast.error(err),
          },
        );
      }}
    >
      <Textarea
        value={content}
        onChange={(e) => setContent(e.target.value)}
        rows={2}
        className="min-h-16"
        maxLength={2000}
        placeholder={
          project === "global"
            ? "Something about you that holds in every project — e.g. “Prefers small commits with conventional prefixes”"
            : "One durable fact — e.g. “Deploys go through the self-hosted runner; never push to prod on Fridays”"
        }
        aria-label="New memory"
      />
      <div className="flex flex-wrap items-center gap-3">
        <KindSelect value={kind} onChange={setKind} />
        <label className="inline-flex items-center gap-1.5 text-[13px] text-fg-2">
          <Checkbox checked={pinned} onChange={(e) => setPinned(e.target.checked)} />
          Pin (always listed first)
        </label>
        <Button type="submit" size="sm" variant="primary" className="ml-auto" loading={create.isPending} disabled={!ok}>
          Remember
        </Button>
      </div>
    </form>
  );
}

function MemoryRow({ memory: m, onDelete }: { memory: Memory; onDelete: () => void }) {
  const update = useUpdateMemory();
  const toast = useToast();
  const [editing, setEditing] = useState(false);
  const [content, setContent] = useState(m.content);
  const [kind, setKind] = useState<MemoryKind>(m.kind);

  const save = (patch: Parameters<typeof update.mutate>[0]["patch"], done?: () => void) =>
    update.mutate({ id: m.id, patch }, { onSuccess: done, onError: (e) => toast.error(e) });

  return (
    <li className={clsx("space-y-1.5 px-3 py-2.5", m.pinned && "bg-accent/5")}>
      <div className="flex items-start gap-2">
        <Badge tone={m.kind === "gotcha" ? "warning" : m.kind === "decision" ? "accent" : "neutral"} className="mt-0.5 shrink-0">
          {KIND_LABELS[m.kind]}
        </Badge>
        {editing ? (
          <div className="min-w-0 flex-1 space-y-2">
            <Textarea value={content} onChange={(e) => setContent(e.target.value)} rows={3} maxLength={2000} aria-label="Memory" />
            <div className="flex flex-wrap items-center gap-2">
              <KindSelect value={kind} onChange={setKind} />
              <Button
                size="sm"
                variant="primary"
                loading={update.isPending}
                disabled={content.trim().length < 3}
                onClick={() => save({ content: content.trim(), kind }, () => setEditing(false))}
              >
                Save
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  setEditing(false);
                  setContent(m.content);
                  setKind(m.kind);
                }}
              >
                Cancel
              </Button>
            </div>
          </div>
        ) : (
          <p className="min-w-0 flex-1 text-[13.5px] break-words whitespace-pre-wrap">
            <span className="mr-1.5 text-fg-3 tabular">m{m.id}</span>
            {m.content}
          </p>
        )}
        {!editing ? (
          <div className="flex shrink-0 items-center">
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={m.pinned ? "Unpin" : "Pin"}
              title={m.pinned ? "Unpin" : "Pin: always listed first"}
              onClick={() => save({ pinned: !m.pinned })}
            >
              {m.pinned ? <PinOff className="size-3.5" /> : <Pin className="size-3.5" />}
            </Button>
            <Button size="icon-sm" variant="ghost" aria-label="Edit" title="Edit" onClick={() => setEditing(true)}>
              <Pencil className="size-3.5" />
            </Button>
            <Button size="icon-sm" variant="ghost" aria-label="Forget" title="Forget" onClick={onDelete}>
              <Trash2 className="size-3.5" />
            </Button>
          </div>
        ) : null}
      </div>
      <p className="pl-0.5 text-[12px] text-fg-3">
        {m.pinned ? "Pinned · " : ""}
        {m.repo_name ? `${m.repo_name} only · ` : ""}
        by {memorySource(m)} · <RelativeTime iso={m.updated_at} />
        {m.recalls > 0 ? ` · recalled ${m.recalls}×` : ""}
      </p>
    </li>
  );
}
