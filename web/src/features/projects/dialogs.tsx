// Small edit dialogs for a project's repos, endpoints and linked servers.

import { useState } from "react";
import { useSaveEndpoint, useSaveRepo, useServers, useSetProjectServers } from "@/api/hooks";
import type { Endpoint, EndpointKind, ProjectServer, Repo, RepoKind } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Checkbox, Field, Input, Select, Switch, Textarea } from "@/components/ui/Input";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";

const REPO_KINDS: RepoKind[] = ["api", "ui", "mobile", "infra", "lib", "site", "other"];

export function RepoDialog({ projectKey, repo, onClose }: { projectKey: string; repo?: Repo; onClose: () => void }) {
  const save = useSaveRepo(projectKey);
  const toast = useToast();
  const [form, setForm] = useState({
    name: repo?.name ?? "",
    path: repo?.path ?? "",
    remote_url: repo?.remote_url ?? "",
    default_branch: repo?.default_branch ?? "main",
    kind: repo?.kind ?? ("other" as RepoKind),
    deploy: repo?.deploy ?? "",
    notes: repo?.notes ?? "",
  });
  const set = <K extends keyof typeof form>(k: K, v: (typeof form)[K]) => setForm((f) => ({ ...f, [k]: v }));

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    save.mutate(
      { id: repo?.id, ...form, name: form.name.trim(), path: form.path.trim() },
      {
        onSuccess: () => {
          toast.success(repo ? "Repository saved" : "Repository added");
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
      title={repo ? `Edit ${repo.name}` : "Add repository"}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="repo-form" loading={save.isPending} disabled={!form.name.trim()}>
            Save
          </Button>
        </>
      }
    >
      <form id="repo-form" onSubmit={submit} className="space-y-3">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_140px]">
          <Field label="Name">{(id) => <Input id={id} value={form.name} onChange={(e) => set("name", e.target.value)} required />}</Field>
          <Field label="Kind">
            {(id) => (
              <Select id={id} value={form.kind} onChange={(e) => set("kind", e.target.value as RepoKind)}>
                {REPO_KINDS.map((k) => (
                  <option key={k} value={k}>
                    {k}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
        <Field label="Local path" hint="Absolute path on the runner's machine — what agents and git scans use">
          {(id, desc) => (
            <Input
              id={id}
              aria-describedby={desc}
              value={form.path}
              onChange={(e) => set("path", e.target.value)}
              placeholder="/home/you/dev/project/repo"
              className="font-mono text-[13px]"
            />
          )}
        </Field>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_140px]">
          <Field label="Remote URL">
            {(id) => (
              <Input
                id={id}
                value={form.remote_url}
                onChange={(e) => set("remote_url", e.target.value)}
                placeholder="git@github.com:org/repo.git"
                className="font-mono text-[13px]"
              />
            )}
          </Field>
          <Field label="Default branch">
            {(id) => <Input id={id} value={form.default_branch} onChange={(e) => set("default_branch", e.target.value)} />}
          </Field>
        </div>
        <Field label="Deploy" hint="One line: how this reaches production">
          {(id, desc) => (
            <Input
              id={id}
              aria-describedby={desc}
              value={form.deploy}
              onChange={(e) => set("deploy", e.target.value)}
              placeholder="push main → GitHub Actions → Nomad on work"
            />
          )}
        </Field>
        <Field label="Notes">{(id) => <Textarea id={id} rows={3} value={form.notes} onChange={(e) => set("notes", e.target.value)} />}</Field>
      </form>
    </Dialog>
  );
}

export function EndpointDialog({
  projectKey,
  endpoint,
  onClose,
}: {
  projectKey: string;
  endpoint?: Endpoint;
  onClose: () => void;
}) {
  const save = useSaveEndpoint(projectKey);
  const toast = useToast();
  const [name, setName] = useState(endpoint?.name ?? "");
  const [url, setUrl] = useState(endpoint?.url ?? "https://");
  const [kind, setKind] = useState<EndpointKind>(endpoint?.kind ?? "web");
  const [expect, setExpect] = useState(endpoint?.expect_status ?? 200);
  const [enabled, setEnabled] = useState(endpoint?.enabled ?? true);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    save.mutate(
      { id: endpoint?.id, name: name.trim(), url: url.trim(), kind, expect_status: expect, enabled },
      {
        onSuccess: () => {
          toast.success(endpoint ? "Endpoint saved" : "Endpoint added — first check within 2 minutes");
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
      title={endpoint ? `Edit ${endpoint.name}` : "Add endpoint"}
      description="Checked with a GET every 2 minutes; up means the expected status code."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="endpoint-form" loading={save.isPending} disabled={!name.trim() || !/^https?:\/\/.+/.test(url)}>
            Save
          </Button>
        </>
      }
    >
      <form id="endpoint-form" onSubmit={submit} className="space-y-3">
        <Field label="Name">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} required placeholder="Production web" />}</Field>
        <Field label="URL">
          {(id) => <Input id={id} type="url" value={url} onChange={(e) => setUrl(e.target.value)} required className="font-mono text-[13px]" />}
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Kind">
            {(id) => (
              <Select id={id} value={kind} onChange={(e) => setKind(e.target.value as EndpointKind)}>
                <option value="web">web</option>
                <option value="api">api</option>
                <option value="health">health</option>
              </Select>
            )}
          </Field>
          <Field label="Expected status">
            {(id) => (
              <Input id={id} type="number" min={100} max={599} value={expect} onChange={(e) => setExpect(Number(e.target.value))} />
            )}
          </Field>
        </div>
        <Switch checked={enabled} onChange={setEnabled} label="Monitoring enabled" />
      </form>
    </Dialog>
  );
}

export function ServersLinkDialog({
  projectKey,
  linked,
  onClose,
}: {
  projectKey: string;
  linked: ProjectServer[];
  onClose: () => void;
}) {
  const servers = useServers();
  const save = useSetProjectServers(projectKey);
  const toast = useToast();
  const [selection, setSelection] = useState<Record<number, string>>(() =>
    Object.fromEntries(linked.map((s) => [s.server_id, s.role])),
  );

  const toggle = (id: number) =>
    setSelection((sel) => {
      const next = { ...sel };
      if (id in next) delete next[id];
      else next[id] = "";
      return next;
    });

  const submit = () =>
    save.mutate(
      Object.entries(selection).map(([server_id, role]) => ({ server_id: Number(server_id), role: role.trim() })),
      {
        onSuccess: () => {
          toast.success("Servers updated");
          onClose();
        },
        onError: (err) => toast.error(err),
      },
    );

  return (
    <Dialog
      open
      onClose={onClose}
      size="lg"
      title="Linked servers"
      description="Which shared hosts this project runs on, and what it uses each for."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" onClick={submit} loading={save.isPending}>
            Save
          </Button>
        </>
      }
    >
      {servers.isPending ? (
        <SkeletonRows rows={6} />
      ) : (
        <ul className="divide-y divide-line rounded-lg border border-line">
          {servers.data?.map((s) => {
            const checked = s.id in selection;
            return (
              <li key={s.id} className="flex flex-wrap items-center gap-3 px-3 py-2">
                <label className="flex min-w-40 flex-1 items-center gap-2.5 text-[13px]">
                  <Checkbox checked={checked} onChange={() => toggle(s.id)} />
                  <span className="font-medium">{s.name}</span>
                  <Badge tone={s.environment === "production" ? "critical" : "neutral"}>{s.environment}</Badge>
                </label>
                <Input
                  aria-label={`Role on ${s.name}`}
                  placeholder="Role, e.g. app prod1"
                  value={selection[s.id] ?? ""}
                  disabled={!checked}
                  onChange={(e) => setSelection((sel) => ({ ...sel, [s.id]: e.target.value }))}
                  compact
                  className="w-full sm:w-56"
                />
              </li>
            );
          })}
        </ul>
      )}
    </Dialog>
  );
}
