// Machines (runners: where work executes) and runs (the work).
//
// A machine runs `forge agent`: it polls the API with its own token,
// advertises what it will accept, and streams output back. It gets that token
// by pairing — a one-time code from "Add machine" or "Pair again" — so the
// token itself never has to be copied around. Rotating by hand (the token is
// shown once) stays for manual setups.

import clsx from "clsx";
import {
  Bot,
  ChevronRight,
  Cpu,
  Crown,
  KeyRound,
  Link2,
  MoreHorizontal,
  Plus,
  RotateCw,
  ShieldAlert,
  Smartphone,
  SquareTerminal,
  Trash2,
} from "lucide-react";
import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  useCreateRunner,
  useDeleteRunner,
  usePairRunner,
  useProjects,
  useRotateRunner,
  useRunners,
  useRuns,
  useSetRunnerRole,
} from "@/api/hooks";
import type { CommandDetail, Pairing, Runner, RunnerRole, RunStatus } from "@/api/types";
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
import {
  awaitingPairing,
  commandDetails,
  defaultNewRole,
  ROLE_LABEL,
  ROLE_ORDER,
  roleOf,
  runnerConfigSnippet,
} from "@/lib/agents";
import { useNow } from "@/lib/now";
import { CopyButton, PairingDialog } from "./PairingDialog";
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

type Issued = { runner: Runner; pairing: Pairing };

function RunnersPanel() {
  const runners = useRunners(5000);
  // /agents?add=1 (the dashboard's getting-started link) opens "Add machine".
  const [params, setParams] = useSearchParams();
  const [adding, setAdding] = useState(() => params.get("add") === "1");
  const [pairing, setPairing] = useState<Issued | null>(null);
  const [issued, setIssued] = useState<{ runner: Runner; token: string } | null>(null);

  const closeAdd = () => {
    setAdding(false);
    if (params.has("add"))
      setParams(
        (p) => {
          const next = new URLSearchParams(p);
          next.delete("add");
          return next;
        },
        { replace: true },
      );
  };

  return (
    <Panel
      title="Machines"
      icon={<Cpu />}
      id="agents-runners"
      actions={
        <Button size="sm" variant="ghost" onClick={() => setAdding(true)}>
          <Plus className="size-3.5" aria-hidden /> Add machine
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
              <RunnerRow key={r.id} runner={r} onToken={setIssued} onPair={setPairing} />
            ))}
        </ul>
      ) : (
        <EmptyState
          compact
          icon={<Cpu />}
          title="No machines yet"
          action={
            <Button size="sm" variant="primary" onClick={() => setAdding(true)}>
              <Plus className="size-3.5" aria-hidden /> Add machine
            </Button>
          }
        >
          Add your desktop, a laptop or a server, then paste one command there. Runs, terminals and VS Code happen on
          your machines.
        </EmptyState>
      )}

      {adding ? (
        <AddMachineDialog
          runners={runners.data ?? []}
          onClose={closeAdd}
          onCreated={(res) => {
            closeAdd();
            setPairing(res);
          }}
        />
      ) : null}
      {pairing ? <PairingDialog runner={pairing.runner} pairing={pairing.pairing} onClose={() => setPairing(null)} /> : null}
      {issued ? <TokenDialog runner={issued.runner} token={issued.token} onClose={() => setIssued(null)} /> : null}
    </Panel>
  );
}

function AddMachineDialog({
  runners,
  onClose,
  onCreated,
}: {
  runners: Runner[];
  onClose: () => void;
  onCreated: (res: Issued) => void;
}) {
  const create = useCreateRunner();
  const toast = useToast();
  const [name, setName] = useState("");
  const [role, setRole] = useState<RunnerRole>(() => defaultNewRole(runners));
  const hasMaster = runners.some((r) => roleOf(r) === "master");
  return (
    <Dialog
      open
      onClose={onClose}
      size="sm"
      title="Add machine"
      description="A name for it, e.g. desk or laptop. Next you get a code to pair it."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="runner-form" loading={create.isPending} disabled={!name.trim()}>
            Add and pair
          </Button>
        </>
      }
    >
      <form
        id="runner-form"
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (!name.trim()) return;
          create.mutate({ name: name.trim(), role }, { onSuccess: onCreated, onError: (err) => toast.error(err) });
        }}
      >
        <Field label="Name">
          {(id) => (
            <Input
              id={id}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="desk"
              autoCapitalize="none"
              spellCheck={false}
              required
            />
          )}
        </Field>
        <Field
          label="Role"
          hint={
            role === "master"
              ? hasMaster
                ? "Becomes the master; the current one turns into a worker."
                : "Your always-on machine: default for runs and terminals, serves VS Code, scans repos."
              : role === "ios"
                ? "A Mac that takes iOS builds and uploads."
                : "Anything else — a laptop, a build box. Takes runs you send it."
          }
        >
          {(id, desc) => (
            <Select id={id} aria-describedby={desc} value={role} onChange={(e) => setRole(e.target.value as RunnerRole)}>
              <option value="master">{ROLE_LABEL.master}</option>
              <option value="worker">{ROLE_LABEL.worker}</option>
              <option value="ios">{ROLE_LABEL.ios}</option>
            </Select>
          )}
        </Field>
      </form>
    </Dialog>
  );
}

function RunnerRow({
  runner: r,
  onToken,
  onPair,
}: {
  runner: Runner;
  onToken: (x: { runner: Runner; token: string }) => void;
  onPair: (x: Issued) => void;
}) {
  const rotate = useRotateRunner();
  const pair = usePairRunner();
  const del = useDeleteRunner();
  const toast = useToast();
  const now = useNow();
  const [confirm, setConfirm] = useState<"rotate" | "delete" | "master" | null>(null);
  const setRole = useSetRunnerRole();
  const caps = r.capabilities;
  const neverSeen = !r.last_seen_at;
  const waiting = awaitingPairing(r, now);
  const pairAgain = () => pair.mutate(r.id, { onSuccess: onPair, onError: (e) => toast.error(e) });
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
            ) : waiting ? (
              <Badge tone="accent" dot title="A pairing code is out and unused">
                waiting for pairing
              </Badge>
            ) : neverSeen ? (
              <Badge tone="outline" title="This machine has not connected yet">
                not paired
              </Badge>
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
            {neverSeen ? (
              "never connected"
            ) : (
              <>
                {r.hostname}
                {r.os ? ` · ${r.os}` : ""}
                {r.version ? ` · v${r.version}` : ""} · seen <RelativeTime iso={r.last_seen_at} />
              </>
            )}
          </p>
          {neverSeen ? (
            <Button size="sm" variant="subtle" className="mt-2" onClick={pairAgain} loading={pair.isPending}>
              <Link2 className="size-3.5" aria-hidden /> {waiting ? "Show a new code" : "Pair it"}
            </Button>
          ) : null}
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
            { label: "Pair again (new code)", icon: <Link2 />, onSelect: pairAgain },
            { label: "Rotate token (manual setup)", icon: <RotateCw />, onSelect: () => setConfirm("rotate") },
            { label: "Delete machine", icon: <Trash2 />, danger: true, onSelect: () => setConfirm("delete") },
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
        body="The current token stops working immediately, and you put the new one on the machine by hand. To move or reinstall a machine, Pair again is easier."
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
              toast.success("Machine deleted");
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
        <p className="text-fg-2">
          On {runner.name}, put it in <code className="font-mono">~/.forge/agent.json</code> as{" "}
          <code className="font-mono">"token"</code>, then run <code className="font-mono">forge agent install</code> (or
          restart the agent if it is already installed).
        </p>
        <details className="group">
          <summary className="inline-flex cursor-pointer list-none items-center gap-1 text-fg-2 hover:text-fg">
            <ChevronRight className="size-3.5 text-fg-3 transition-transform group-open:rotate-90" aria-hidden />
            A complete <code className="font-mono">agent.json</code> for a new machine
          </summary>
          <div className="mt-2 space-y-1.5">
            <div className="flex justify-end">
              <CopyButton text={config} label="Copy config" />
            </div>
            <pre className="overflow-x-auto rounded-md border border-line bg-surface-2 px-3 py-2 font-mono text-[12px]">{config}</pre>
            <p className="text-fg-3">
              Commands, permission modes (add <code className="font-mono">bypassPermissions</code> only by hand), allowed
              roots, terminals and VS Code are all decided in this file, on the machine.
            </p>
          </div>
        </details>
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
