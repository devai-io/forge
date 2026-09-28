// Shared infrastructure: the hosts every project runs on, and every endpoint
// being watched, in one place. A server lists the projects that use it, which
// is the question to ask before touching one.

import clsx from "clsx";
import { ChevronDown, Cpu, Globe, Network, Pencil, Plus, Server as ServerIcon, ShieldAlert, Trash2 } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { useDeleteServer, useEndpoints, useSaveServer, useServers } from "@/api/hooks";
import type { Environment, Server } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog, Dialog } from "@/components/ui/Dialog";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Field, Input, Select, Switch, Textarea } from "@/components/ui/Input";
import { Markdown } from "@/components/ui/Markdown";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { Skeleton, SkeletonRows } from "@/components/ui/Skeleton";
import { Segmented } from "@/components/ui/Tabs";
import { useToast } from "@/components/ui/Toast";
import { environmentTone } from "@/lib/infra";
import { EndpointsTable } from "./endpoints";

const ENVIRONMENTS: Environment[] = ["production", "staging", "dev", "infra"];

export function InfraPage() {
  const servers = useServers();
  const endpoints = useEndpoints();
  const [creating, setCreating] = useState(false);
  const [filter, setFilter] = useState<"all" | "down">("all");
  const eps = (endpoints.data ?? []).filter((e) => filter === "all" || e.last_status === "down");
  const down = (endpoints.data ?? []).filter((e) => e.last_status === "down").length;

  return (
    <div className="mx-auto max-w-[1400px] space-y-6">
      <PageHeader
        title="Infrastructure"
        subtitle="Shared hosts across every project, and what is watching the public edges."
        actions={
          <Button variant="primary" size="sm" onClick={() => setCreating(true)}>
            <Plus className="size-3.5" aria-hidden /> Server
          </Button>
        }
      />

      <section aria-labelledby="infra-servers" className="space-y-3">
        <h2 id="infra-servers" className="flex items-center gap-2 text-[13.5px] font-semibold">
          <ServerIcon className="size-4 text-fg-3" aria-hidden /> Servers
          {servers.data ? <span className="text-fg-3">· {servers.data.length}</span> : null}
        </h2>
        {servers.isPending ? (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {Array.from({ length: 6 }, (_, i) => (
              <Skeleton key={i} className="h-40" />
            ))}
          </div>
        ) : servers.error ? (
          <ErrorState error={servers.error} onRetry={() => servers.refetch()} />
        ) : servers.data.length ? (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {servers.data.map((s) => (
              <ServerCard key={s.id} server={s} />
            ))}
          </div>
        ) : (
          <EmptyState icon={<ServerIcon />} title="No servers yet" />
        )}
      </section>

      <Panel
        title={
          <>
            Endpoints{" "}
            {endpoints.data ? (
              <span className="font-normal text-fg-3">
                · {endpoints.data.length - down}/{endpoints.data.length} up
              </span>
            ) : null}
          </>
        }
        icon={<Globe />}
        id="infra-endpoints"
        bodyClassName="p-0"
        actions={
          <Segmented
            size="sm"
            label="Endpoint filter"
            value={filter}
            onChange={setFilter}
            items={[
              { value: "all", label: "All" },
              { value: "down", label: `Down${down ? ` · ${down}` : ""}` },
            ]}
          />
        }
      >
        {endpoints.isPending ? (
          <SkeletonRows rows={5} className="p-3" />
        ) : eps.length ? (
          <>
            <p className="border-b border-line px-3 py-1.5 text-[11.5px] text-fg-3">
              Strip: last 24 hours in half-hour windows — green all checks passed, red any check failed, grey no checks.
            </p>
            <EndpointsTable endpoints={eps} showProject strip />
          </>
        ) : (
          <p className="p-4 text-[13px] text-fg-3">{filter === "down" ? "Nothing is down." : "No endpoints yet — add them from a project's overview."}</p>
        )}
      </Panel>

      {creating ? <ServerDialog onClose={() => setCreating(false)} /> : null}
    </div>
  );
}

function ServerCard({ server: s }: { server: Server }) {
  const [editing, setEditing] = useState(false);
  const [showNotes, setShowNotes] = useState(false);
  return (
    <article className={clsx("flex flex-col gap-2.5 rounded-xl border bg-surface p-4", s.critical ? "border-critical/40" : "border-line")}>
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="font-mono text-[14px] font-semibold">{s.name}</h3>
            <Badge tone={environmentTone(s.environment)} dot>
              {s.environment}
            </Badge>
            {s.critical ? (
              <Badge tone="critical" title="Production-critical: confirm before any change">
                <ShieldAlert className="size-3" aria-hidden /> critical
              </Badge>
            ) : null}
          </div>
          {s.role ? <p className="mt-1 line-clamp-2 text-[12.5px] text-fg-2">{s.role}</p> : null}
        </div>
        <Button size="icon-sm" variant="ghost" aria-label={`Edit ${s.name}`} onClick={() => setEditing(true)}>
          <Pencil className="size-3.5" />
        </Button>
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-[12px]">
        {s.public_address ? (
          <>
            <dt className="text-fg-3">
              <Globe className="inline size-3" aria-hidden /> Public
            </dt>
            <dd className="truncate font-mono text-fg-2">{s.public_address}</dd>
          </>
        ) : null}
        {s.tailscale_ip ? (
          <>
            <dt className="text-fg-3">
              <Network className="inline size-3" aria-hidden /> Tailnet
            </dt>
            <dd className="truncate font-mono text-fg-2">{s.tailscale_ip}</dd>
          </>
        ) : null}
        {s.arch || s.provider ? (
          <>
            <dt className="text-fg-3">
              <Cpu className="inline size-3" aria-hidden /> Host
            </dt>
            <dd className="truncate text-fg-2">{[s.arch, s.provider].filter(Boolean).join(" · ")}</dd>
          </>
        ) : null}
      </dl>
      {s.tags.length ? (
        <div className="flex flex-wrap gap-1">
          {s.tags.map((t) => (
            <span key={t} className="rounded bg-surface-2 px-1.5 text-[11px] text-fg-2">
              {t}
            </span>
          ))}
        </div>
      ) : null}
      {s.projects.length ? (
        <div className="flex flex-wrap gap-x-3 gap-y-1 border-t border-line pt-2">
          {s.projects.map((p) => (
            <Link key={p.key} to={`/p/${p.key}`} className="inline-flex items-center gap-1.5 text-[12px] text-fg-2 hover:text-fg" title={p.role}>
              <ColorDot color={p.color} className="size-2" />
              {p.name}
              {p.role ? <span className="text-fg-3">· {p.role}</span> : null}
            </Link>
          ))}
        </div>
      ) : null}
      {s.notes.trim() ? (
        <div>
          <button
            type="button"
            onClick={() => setShowNotes((v) => !v)}
            aria-expanded={showNotes}
            className="inline-flex items-center gap-1 text-[12px] text-fg-3 hover:text-fg-2"
          >
            <ChevronDown className={clsx("size-3.5 transition-transform", showNotes && "rotate-180")} aria-hidden />
            Notes
          </button>
          {showNotes ? <Markdown className="mt-1.5 text-[12.5px]">{s.notes}</Markdown> : null}
        </div>
      ) : null}
      {editing ? <ServerDialog server={s} onClose={() => setEditing(false)} /> : null}
    </article>
  );
}

function ServerDialog({ server, onClose }: { server?: Server; onClose: () => void }) {
  const save = useSaveServer();
  const del = useDeleteServer();
  const toast = useToast();
  const [confirm, setConfirm] = useState(false);
  const [form, setForm] = useState({
    name: server?.name ?? "",
    role: server?.role ?? "",
    provider: server?.provider ?? "",
    arch: server?.arch ?? "",
    public_address: server?.public_address ?? "",
    tailscale_ip: server?.tailscale_ip ?? "",
    environment: server?.environment ?? ("dev" as Environment),
    critical: server?.critical ?? false,
    tags: server?.tags.join(", ") ?? "",
    notes: server?.notes ?? "",
  });
  const set = <K extends keyof typeof form>(k: K, v: (typeof form)[K]) => setForm((f) => ({ ...f, [k]: v }));

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    save.mutate(
      {
        id: server?.id,
        ...form,
        name: form.name.trim(),
        tags: form.tags
          .split(",")
          .map((t) => t.trim())
          .filter(Boolean),
      },
      {
        onSuccess: () => {
          toast.success(server ? "Server saved" : "Server added");
          onClose();
        },
        onError: (err) => toast.error(err),
      },
    );
  };

  return (
    <Dialog
      open
      onClose={onClose}
      size="lg"
      title={server ? `Edit ${server.name}` : "Add server"}
      footer={
        <>
          {server ? (
            <Button variant="ghost" className="mr-auto text-critical-ink" onClick={() => setConfirm(true)}>
              <Trash2 className="size-3.5" aria-hidden /> Delete
            </Button>
          ) : null}
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="server-form" loading={save.isPending} disabled={!form.name.trim()}>
            Save
          </Button>
        </>
      }
    >
      <form id="server-form" onSubmit={submit} className="space-y-3">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Name">{(id) => <Input id={id} value={form.name} onChange={(e) => set("name", e.target.value)} required className="font-mono" />}</Field>
          <Field label="Environment">
            {(id) => (
              <Select id={id} value={form.environment} onChange={(e) => set("environment", e.target.value as Environment)}>
                {ENVIRONMENTS.map((env) => (
                  <option key={env} value={env}>
                    {env}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
        <Field label="Role">{(id) => <Input id={id} value={form.role} onChange={(e) => set("role", e.target.value)} placeholder="Nomad apps, database primary…" />}</Field>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Public address">
            {(id) => <Input id={id} value={form.public_address} onChange={(e) => set("public_address", e.target.value)} className="font-mono" />}
          </Field>
          <Field label="Tailscale IP">
            {(id) => <Input id={id} value={form.tailscale_ip} onChange={(e) => set("tailscale_ip", e.target.value)} className="font-mono" />}
          </Field>
          <Field label="Provider">{(id) => <Input id={id} value={form.provider} onChange={(e) => set("provider", e.target.value)} placeholder="eu-west-1" />}</Field>
          <Field label="Architecture">{(id) => <Input id={id} value={form.arch} onChange={(e) => set("arch", e.target.value)} placeholder="aarch64" />}</Field>
        </div>
        <Field label="Tags" hint="Comma separated">
          {(id, desc) => <Input id={id} aria-describedby={desc} value={form.tags} onChange={(e) => set("tags", e.target.value)} />}
        </Field>
        <Switch checked={form.critical} onChange={(v) => set("critical", v)} label="Production-critical (confirm before any change)" />
        <Field label="Notes" hint="Markdown">
          {(id, desc) => (
            <Textarea id={id} aria-describedby={desc} rows={6} value={form.notes} onChange={(e) => set("notes", e.target.value)} className="font-mono text-[13px]" />
          )}
        </Field>
      </form>
      {server ? (
        <ConfirmDialog
          open={confirm}
          onClose={() => setConfirm(false)}
          title={`Delete ${server.name}?`}
          body="Removes the server and its links to projects. The machine itself is not touched."
          loading={del.isPending}
          onConfirm={() =>
            del.mutate(server.id, {
              onSuccess: () => {
                toast.success("Server deleted");
                setConfirm(false);
                onClose();
              },
              onError: (err) => toast.error(err),
            })
          }
        />
      ) : null}
    </Dialog>
  );
}
