// Add or edit a server (Infrastructure and the server's own page).

import { Trash2 } from "lucide-react";
import { useState } from "react";
import { useDeleteServer, useSaveServer } from "@/api/hooks";
import type { Environment, Server } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog, Dialog } from "@/components/ui/Dialog";
import { Field, Input, Select, Switch, Textarea } from "@/components/ui/Input";
import { useToast } from "@/components/ui/Toast";

const ENVIRONMENTS: Environment[] = ["production", "staging", "dev", "infra"];

export function ServerDialog({ server, onClose, onDeleted }: { server?: Server; onClose: () => void; onDeleted?: () => void }) {
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
    ssh_target: server?.ssh_target ?? "",
  });
  const set = <K extends keyof typeof form>(k: K, v: (typeof form)[K]) => setForm((f) => ({ ...f, [k]: v }));

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    save.mutate(
      {
        id: server?.id,
        ...form,
        name: form.name.trim(),
        ssh_target: form.ssh_target.trim(),
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
        <Field label="SSH target" hint="What Open shell runs ssh to: user@host, an address or an ~/.ssh/config alias. Empty: the server's name.">
          {(id, desc) => (
            <Input
              id={id}
              aria-describedby={desc}
              value={form.ssh_target}
              onChange={(e) => set("ssh_target", e.target.value)}
              placeholder={form.name.trim() || "root@host"}
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
            />
          )}
        </Field>
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
                onDeleted?.();
              },
              onError: (err) => toast.error(err),
            })
          }
        />
      ) : null}
    </Dialog>
  );
}
