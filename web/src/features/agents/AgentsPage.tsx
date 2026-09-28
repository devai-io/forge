// Runners (the machines that execute work) and runs (the work).
//
// A runner is `forge_runner` on a desktop or server: it polls the API with
// its own token, advertises what it will accept, and streams output back.
// Its token is shown exactly once, at creation or rotation.

import clsx from "clsx";
import {
  Bot,
  Check,
  ChevronRight,
  Copy,
  Cpu,
  Crown,
  KeyRound,
  MoreHorizontal,
  Plus,
  RotateCw,
  ShieldAlert,
  Smartphone,
  SquareTerminal,
  Trash2,
} from "lucide-react";
import { useState } from "react";
import {
  useCreateRunner,
  useDeleteRunner,
  useProjects,
  useRotateRunner,
  useRunners,
  useRuns,
  useSetRunnerRole,
} from "@/api/hooks";
import type { CommandDetail, Runner, RunStatus } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog, Dialog } from "@/components/ui/Dialog";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Field, Input, Select } from "@/components/ui/Input";
import { Menu } from "@/components/ui/Menu";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { useShell } from "@/features/shell/context";
import { commandDetails, ROLE_LABEL, ROLE_ORDER, roleOf, runnerConfigSnippet } from "@/lib/agents";
import { RoleBadge } from "./RoleBadge";
import { RunRow } from "./RunBits";

export function AgentsPage() {
  const { newRun } = useShell();
  return (
    <div className="mx-auto max-w-[1400px]">
      <PageHeader
        title="Agents"
        subtitle="Queue Claude Code or named commands on your machines, from anywhere."
        actions={
          <Button variant="primary" size="sm" onClick={() => newRun()}>
            <Plus className="size-3.5" aria-hidden /> New run
          </Button>
        }
      />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-5">
        <div className="lg:col-span-2">
          <RunnersPanel />
        </div>
        <div className="lg:col-span-3">
          <RunsPanel />
        </div>
      </div>
    </div>
  );
}

function RunnersPanel() {
  const runners = useRunners(5000);
  const create = useCreateRunner();
  const toast = useToast();
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [issued, setIssued] = useState<{ runner: Runner; token: string } | null>(null);

  return (
    <Panel
      title="Runners"
      icon={<Cpu />}
      id="agents-runners"
      actions={
        <Button size="sm" variant="ghost" onClick={() => setAdding(true)}>
          <Plus className="size-3.5" aria-hidden /> Add runner
        </Button>
      }
    >
      {runners.isPending ? (
        <SkeletonRows rows={3} />
      ) : runners.error ? (
        <ErrorState error={runners.error} onRetry={() => runners.refetch()} />
      ) : runners.data.length ? (
        <ul className="space-y-1">
          {[...runners.data]
            .sort((a, b) => ROLE_ORDER[roleOf(a)] - ROLE_ORDER[roleOf(b)] || a.name.localeCompare(b.name))
            .map((r) => (
              <RunnerRow key={r.id} runner={r} onToken={setIssued} />
            ))}
        </ul>
      ) : (
        <EmptyState compact icon={<Cpu />} title="No runners yet">
          Add one, then start <code className="font-mono">forge_runner</code> on your desktop or a server.
        </EmptyState>
      )}

      <Dialog
        open={adding}
        onClose={() => setAdding(false)}
        size="sm"
        title="Add runner"
        description="A name for the machine, e.g. desktop or buildbox."
        footer={
          <>
            <Button variant="ghost" onClick={() => setAdding(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              type="submit"
              form="runner-form"
              loading={create.isPending}
              disabled={!name.trim()}
            >
              Create
            </Button>
          </>
        }
      >
        <form
          id="runner-form"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate(name.trim(), {
              onSuccess: (res) => {
                setAdding(false);
                setName("");
                setIssued(res);
              },
              onError: (err) => toast.error(err),
            });
          }}
        >
          <Field label="Name">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} placeholder="desktop" required />}</Field>
        </form>
      </Dialog>
      {issued ? <TokenDialog runner={issued.runner} token={issued.token} onClose={() => setIssued(null)} /> : null}
    </Panel>
  );
}

function RunnerRow({ runner: r, onToken }: { runner: Runner; onToken: (x: { runner: Runner; token: string }) => void }) {
  const rotate = useRotateRunner();
  const del = useDeleteRunner();
  const toast = useToast();
  const [confirm, setConfirm] = useState<"rotate" | "delete" | "master" | null>(null);
  const setRole = useSetRunnerRole();
  const caps = r.capabilities;
  return (
    <li className="rounded-lg border border-line px-3 py-2.5">
      <div className="flex items-start gap-2.5">
        <span
          className={clsx("mt-1.5 size-2 shrink-0 rounded-full", r.online ? "bg-good" : "bg-fg-3")}
          aria-hidden
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[13.5px] font-medium">{r.name}</span>
            <RoleBadge role={roleOf(r)} />
            {r.online ? (
              <Badge tone="good">online</Badge>
            ) : roleOf(r) === "master" ? (
              <Badge tone="critical" dot title="The master is expected to be always on">
                offline
              </Badge>
            ) : (
              <Badge tone="outline" title="Laptops and the Mac sleep; nothing is wrong">
                asleep
              </Badge>
            )}
            {r.running ? <Badge tone="accent">{r.running} running</Badge> : null}
          </div>
          <p className="mt-0.5 truncate text-[11.5px] text-fg-3">
            {r.hostname || "never connected"}
            {r.os ? ` · ${r.os}` : ""}
            {r.version ? ` · v${r.version}` : ""} · seen <RelativeTime iso={r.last_seen_at} />
          </p>
          {r.last_seen_at ? (
            <div className="mt-1.5 flex flex-wrap gap-1">
              <Badge tone={caps.claude ? "neutral" : "warning"}>{caps.claude ? "claude ✓" : "no claude"}</Badge>
              {caps.permission_modes.map((m) => (
                <Badge key={m} tone="outline">
                  {m}
                </Badge>
              ))}
              <Badge tone="outline">max {caps.max_concurrent}</Badge>
              {caps.ci ? <Badge tone="outline">CI status</Badge> : null}
            </div>
          ) : null}
          {r.last_seen_at ? <RunnerCommands details={commandDetails(caps)} /> : null}
        </div>
        <Menu
          label={`Actions for ${r.name}`}
          triggerClassName="grid size-7 place-items-center rounded-md text-fg-3 hover:bg-surface-2 hover:text-fg"
          trigger={<MoreHorizontal className="size-4" />}
          items={[
            ...(roleOf(r) !== "master"
              ? [{ label: "Make master", icon: <Crown />, onSelect: () => setConfirm("master") }]
              : []),
            ...(["ios", "worker"] as const)
              .filter((role) => role !== roleOf(r))
              .map((role) => ({
                label: `Set role → ${ROLE_LABEL[role]}`,
                icon: role === "ios" ? <Smartphone /> : <Cpu />,
                onSelect: () =>
                  setRole.mutate(
                    { id: r.id, role },
                    { onSuccess: () => toast.success(`${r.name} is now ${ROLE_LABEL[role]}`), onError: (e) => toast.error(e) },
                  ),
              })),
            "separator" as const,
            { label: "Rotate token", icon: <RotateCw />, onSelect: () => setConfirm("rotate") },
            { label: "Delete runner", icon: <Trash2 />, danger: true, onSelect: () => setConfirm("delete") },
          ]}
        />
      </div>
      <ConfirmDialog
        open={confirm === "master"}
        onClose={() => setConfirm(null)}
        title={`Make ${r.name} the master?`}
        confirmLabel="Make master"
        danger={false}
        loading={setRole.isPending}
        body={
          <div className="space-y-2">
            <p>
              The master is the primary machine: {r.name} stays the default for new runs and terminals, and it becomes
              the only machine whose repo scans (git status, CI) are stored.
            </p>
            <p>The current master becomes a worker. The daily check-up fails when the master is offline for 10 minutes.</p>
          </div>
        }
        onConfirm={() =>
          setRole.mutate(
            { id: r.id, role: "master" },
            {
              onSuccess: () => {
                toast.success(`${r.name} is now the master`);
                setConfirm(null);
              },
              onError: (e) => toast.error(e),
            },
          )
        }
      />
      <ConfirmDialog
        open={confirm === "rotate"}
        onClose={() => setConfirm(null)}
        title={`Rotate ${r.name}'s token?`}
        body="The current token stops working immediately; the runner must be given the new one."
        confirmLabel="Rotate"
        danger={false}
        loading={rotate.isPending}
        onConfirm={() =>
          rotate.mutate(r.id, {
            onSuccess: (res) => {
              setConfirm(null);
              onToken(res);
            },
            onError: (e) => toast.error(e),
          })
        }
      />
      <ConfirmDialog
        open={confirm === "delete"}
        onClose={() => setConfirm(null)}
        title={`Delete ${r.name}?`}
        body="Its token is revoked. Past runs stay in the history."
        loading={del.isPending}
        onConfirm={() =>
          del.mutate(r.id, {
            onSuccess: () => {
              toast.success("Runner deleted");
              setConfirm(null);
            },
            onError: (e) => toast.error(e),
          })
        }
      />
    </li>
  );
}

function RunnerCommands({ details }: { details: CommandDetail[] }) {
  if (!details.length) return <p className="mt-1.5 text-[11.5px] text-fg-3">No named commands.</p>;
  return (
    <details className="group mt-2">
      <summary className="inline-flex cursor-pointer list-none items-center gap-1 text-[12px] text-fg-2 hover:text-fg">
        <ChevronRight className="size-3.5 text-fg-3 transition-transform group-open:rotate-90" aria-hidden />
        <SquareTerminal className="size-3.5 text-fg-3" aria-hidden />
        {details.length} command{details.length === 1 ? "" : "s"}
        {details.some((d) => d.confirm) ? <span className="text-fg-3"> · {details.filter((d) => d.confirm).length} need confirmation</span> : null}
      </summary>
      <ul className="mt-1.5 space-y-1.5 border-l border-line pl-3">
        {details.map((d) => (
          <li key={d.name} className="text-[12px]">
            <div className="flex flex-wrap items-center gap-1.5">
              <code className="font-mono font-medium text-fg">{d.name}</code>
              {d.confirm ? (
                <Badge tone="warning" title="Destructive or outward-facing: asks before running">
                  <ShieldAlert className="size-3" aria-hidden /> confirm
                </Badge>
              ) : null}
              <span className="text-[11px] text-fg-3">{d.repos.length ? `only ${d.repos.join(", ")}` : "any repo"}</span>
            </div>
            {d.description ? <p className="text-fg-3">{d.description}</p> : null}
          </li>
        ))}
      </ul>
    </details>
  );
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      size="sm"
      variant="subtle"
      onClick={() =>
        void navigator.clipboard?.writeText(text).then(() => {
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1500);
        })
      }
    >
      {copied ? <Check className="size-3.5 text-good" aria-hidden /> : <Copy className="size-3.5" aria-hidden />}
      {copied ? "Copied" : label}
    </Button>
  );
}

function TokenDialog({ runner, token, onClose }: { runner: Runner; token: string; onClose: () => void }) {
  const config = runnerConfigSnippet(window.location.origin, token);
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Token for ${runner.name}`}
      description="Shown once. Store it now — Forge keeps only a hash."
      footer={
        <Button variant="primary" onClick={onClose}>
          Done
        </Button>
      }
    >
      <div className="space-y-4 text-[13px]">
        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <span className="flex items-center gap-1.5 font-medium text-fg-2">
              <KeyRound className="size-3.5" aria-hidden /> Token
            </span>
            <CopyButton text={token} label="Copy token" />
          </div>
          <code className="block rounded-md border border-line bg-surface-2 px-3 py-2 font-mono text-[12px] break-all">{token}</code>
        </div>
        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <span className="font-medium text-fg-2">
              <code className="font-mono">~/.config/forge/runner.json</code>
            </span>
            <CopyButton text={config} label="Copy config" />
          </div>
          <pre className="overflow-x-auto rounded-md border border-line bg-surface-2 px-3 py-2 font-mono text-[12px]">{config}</pre>
          <p className="text-fg-3">
            Then on that machine: <code className="font-mono text-fg-2">make install-runner</code> in forge_api and{" "}
            <code className="font-mono text-fg-2">systemctl --user enable --now forge-runner</code>. Commands, permission
            modes (add <code className="font-mono">bypassPermissions</code> only by hand) and roots live in this file.
          </p>
        </div>
      </div>
    </Dialog>
  );
}

function RunsPanel() {
  const [project, setProject] = useState("");
  const [status, setStatus] = useState<RunStatus | "">("");
  const projects = useProjects();
  const runs = useRuns({ project: project || undefined, status: status || undefined, limit: 100 }, 6000);
  return (
    <Panel
      title="Runs"
      icon={<Bot />}
      id="agents-runs"
      actions={
        <div className="flex items-center gap-1.5">
          <Select aria-label="Filter by project" compact className="w-auto" value={project} onChange={(e) => setProject(e.target.value)}>
            <option value="">All projects</option>
            {projects.data?.map((p) => (
              <option key={p.key} value={p.key}>
                {p.key}
              </option>
            ))}
          </Select>
          <Select
            aria-label="Filter by status"
            compact
            className="w-auto"
            value={status}
            onChange={(e) => setStatus(e.target.value as RunStatus | "")}
          >
            <option value="">Any status</option>
            <option value="queued">Queued</option>
            <option value="running">Running</option>
            <option value="succeeded">Succeeded</option>
            <option value="failed">Failed</option>
            <option value="cancelled">Cancelled</option>
          </Select>
        </div>
      }
    >
      {runs.isPending ? (
        <SkeletonRows rows={6} />
      ) : runs.error ? (
        <ErrorState error={runs.error} onRetry={() => runs.refetch()} />
      ) : runs.data.length ? (
        <div className={clsx(runs.isPlaceholderData && "opacity-60")}>
          {runs.data.map((r) => (
            <RunRow key={r.id} run={r} />
          ))}
        </div>
      ) : (
        <EmptyState compact icon={<Bot />} title="No runs match" />
      )}
    </Panel>
  );
}
