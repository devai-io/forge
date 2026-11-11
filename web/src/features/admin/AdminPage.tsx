// Admin: the accounts and what each may do. An owner sees everything; a
// member works only in the projects it was granted, and may have one agent
// of its own (a machine, a model, an effort). From here an owner creates and
// removes accounts, sets their projects and agent, resets a password, opens
// machines to members — and acts as another account to see what it sees.
// Every change asks for the owner's password again.

import { Copy, KeyRound, Pencil, Plus, ShieldCheck, Trash2, UserRound, Users, VenetianMask } from "lucide-react";
import { useState } from "react";
import {
  useAdmin,
  useCreateAccount,
  useDeleteAccount,
  useDisableAccountTotp,
  useRemoveAccountPasskeys,
  useImpersonate,
  useResetAccountPassword,
  useSetMachineMembers,
  useUpdateAccount,
} from "@/api/hooks";
import type { AdminUser, AdminView } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog, Dialog } from "@/components/ui/Dialog";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Checkbox, Field, Input, Select, Switch } from "@/components/ui/Input";
import { Menu } from "@/components/ui/Menu";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { agentLine, EFFORT_LABEL } from "@/lib/agents";
import { useCurrentUser } from "@/lib/auth";

const MODELS = ["fable", "opus", "sonnet", "haiku"];
const EFFORTS = ["low", "medium", "high", "xhigh", "max"];
const MODES = ["acceptEdits", "plan", "auto", "dontAsk"];
const MODE_HELP: Record<string, string> = {
  acceptEdits: "Edits files without asking; asks before it runs commands.",
  plan: "Read-only: explores and proposes, changes nothing.",
  auto: "Decides by itself what is safe to do without asking.",
  dontAsk: "Does what is allowed without asking, skips the rest.",
};

export function AdminPage() {
  const admin = useAdmin();
  const { user: me } = useCurrentUser();
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<number | null>(null);
  const [secret, setSecret] = useState<{ user: string; password: string; created: boolean } | null>(null);
  const v = admin.data;
  const edited = v?.users.find((u) => u.id === editing) ?? null;

  return (
    <div className="mx-auto max-w-[1100px] space-y-4">
      <PageHeader
        title="Admin"
        subtitle="Accounts and what each may do — roles, projects, a member's agent, machines open to members."
        actions={
          <Button variant="primary" size="sm" onClick={() => setCreating(true)} disabled={!v}>
            <Plus className="size-3.5" aria-hidden /> New account
          </Button>
        }
      />
      {admin.isPending ? (
        <SkeletonRows rows={4} />
      ) : admin.error || !v ? (
        <ErrorState error={admin.error} onRetry={() => admin.refetch()} />
      ) : (
        <>
          <Panel title={`Accounts · ${v.users.length}`} icon={<Users />} id="admin-users">
            <ul aria-label="Accounts" className="divide-y divide-line">
              {v.users.map((u) => (
                <UserRow
                  key={u.id}
                  user={u}
                  view={v}
                  self={u.id === me?.id}
                  onEdit={() => setEditing(u.id)}
                  onPassword={(password) => setSecret({ user: u.username, password, created: false })}
                />
              ))}
            </ul>
          </Panel>
          <MachinesPanel view={v} />
        </>
      )}
      {creating && v ? (
        <NewAccountDialog
          view={v}
          onClose={() => setCreating(false)}
          onCreated={(user, password) => {
            setCreating(false);
            setSecret({ user: user.username, password, created: true });
          }}
        />
      ) : null}
      {edited && v ? <EditAccountDialog key={edited.id} user={edited} view={v} self={edited.id === me?.id} onClose={() => setEditing(null)} /> : null}
      {secret ? <PasswordDialog {...secret} onClose={() => setSecret(null)} /> : null}
    </div>
  );
}

function UserRow({
  user,
  view,
  self,
  onEdit,
  onPassword,
}: {
  user: AdminUser;
  view: AdminView;
  self: boolean;
  onEdit: () => void;
  onPassword: (password: string) => void;
}) {
  const toast = useToast();
  const impersonate = useImpersonate();
  const reset = useResetAccountPassword();
  const totp = useDisableAccountTotp();
  const passkeys = useRemoveAccountPasskeys();
  const del = useDeleteAccount();
  const [confirm, setConfirm] = useState<"reset" | "delete" | null>(null);
  const member = user.role === "member";
  const projects = view.projects.filter((p) => user.projects.includes(p.key));

  const actAs = () =>
    impersonate.mutate(user.id, {
      // A full reload: everything on screen was fetched as the owner.
      onSuccess: () => window.location.assign("/"),
      onError: (e) => toast.error(e),
    });

  return (
    <li className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2 px-2 py-3" aria-label={user.username}>
      <span className="grid size-9 shrink-0 place-items-center rounded-full bg-surface-2 text-[13px] font-semibold text-fg-2" aria-hidden>
        {(user.display_name || user.username).slice(0, 1).toUpperCase()}
      </span>
      <div className="min-w-0 flex-1 basis-56">
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span className="truncate text-[14px] font-medium">{user.display_name || user.username}</span>
          <span className="font-mono text-[12px] text-fg-3">{user.username}</span>
          <Badge tone={member ? "outline" : "accent"}>{member ? "Member" : "Owner"}</Badge>
          {self ? <Badge tone="neutral">You</Badge> : null}
          {user.passkeys ? (
            <Badge tone="good" title={`${user.passkeys} passkey(s)`}>
              Passkey
            </Badge>
          ) : null}
          {user.totp_enabled ? (
            <Badge tone="good" title="Two-factor is on">
              2FA
            </Badge>
          ) : null}
          {user.password_generated ? (
            <Badge tone="warning" title="Still the password Forge made up: not changed yet">
              Password not changed
            </Badge>
          ) : null}
        </div>
        <p className="truncate text-[12px] text-fg-3">
          {user.email} ·{" "}
          {user.last_seen_at ? (
            <>
              seen <RelativeTime iso={user.last_seen_at} />
            </>
          ) : (
            "not signed in"
          )}
        </p>
        {member ? (
          <div className="mt-1.5 flex min-w-0 flex-wrap items-center gap-1.5 text-[12px]">
            {projects.length ? (
              projects.map((p) => (
                <span key={p.key} className="inline-flex items-center gap-1 rounded-md border border-line px-1.5 py-0.5 text-fg-2" title={p.name}>
                  <ColorDot color={p.color} className="size-2" /> {p.key}
                </span>
              ))
            ) : (
              <span className="text-fg-3">No projects — it sees nothing yet</span>
            )}
            <span className="text-fg-3">·</span>
            <span className="min-w-0 truncate text-fg-3">{user.agent ? `Agent: ${agentLine(user.agent)} · ${user.agent.permission_mode}` : "No agent of its own"}</span>
          </div>
        ) : (
          <p className="mt-1 text-[12px] text-fg-3">Everything: every project, machine, setting and this page.</p>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        {!self ? (
          <Button size="sm" variant="subtle" onClick={actAs} loading={impersonate.isPending} title={`See Forge as ${user.username} sees it`}>
            <VenetianMask className="size-3.5" aria-hidden /> Act as
          </Button>
        ) : null}
        <Button size="sm" variant="subtle" onClick={onEdit}>
          <Pencil className="size-3.5" aria-hidden /> Edit
        </Button>
        {!self ? (
          <Menu
            label={`More for ${user.username}`}
            triggerClassName="rounded-md p-1.5 text-fg-3 hover:bg-surface-2 hover:text-fg"
            trigger={<span aria-hidden>⋯</span>}
            items={[
              { label: "New password", icon: <KeyRound />, onSelect: () => setConfirm("reset") },
              ...(user.totp_enabled
                ? [
                    {
                      label: "Turn two-factor off",
                      icon: <ShieldCheck />,
                      onSelect: () =>
                        totp.mutate(user.id, {
                          onSuccess: () => toast.success(`Two-factor is off for ${user.username}: set up again at the next sign-in`),
                          onError: (e) => toast.error(e),
                        }),
                    },
                  ]
                : []),
              ...(user.passkeys
                ? [
                    {
                      label: `Remove passkeys (${user.passkeys})`,
                      icon: <KeyRound />,
                      onSelect: () =>
                        passkeys.mutate(user.id, {
                          onSuccess: ({ removed }) => toast.success(`${removed} passkey(s) removed from ${user.username}`),
                          onError: (e) => toast.error(e),
                        }),
                    },
                  ]
                : []),
              "separator" as const,
              { label: "Delete account", icon: <Trash2 />, danger: true, onSelect: () => setConfirm("delete") },
            ]}
          />
        ) : null}
      </div>
      <ConfirmDialog
        open={confirm === "reset"}
        onClose={() => setConfirm(null)}
        title={`New password for ${user.username}?`}
        body="Forge makes up a new one and shows it once. Every session of that account is signed out."
        confirmLabel="Set a new password"
        danger={false}
        loading={reset.isPending}
        onConfirm={() =>
          reset.mutate(user.id, {
            onSuccess: ({ password }) => {
              setConfirm(null);
              onPassword(password);
            },
            onError: (e) => toast.error(e),
          })
        }
      />
      <ConfirmDialog
        open={confirm === "delete"}
        onClose={() => setConfirm(null)}
        title={`Delete ${user.username}?`}
        body="The account, its sessions, its project grants and its agent are removed. The runs it started stay. This cannot be undone."
        loading={del.isPending}
        onConfirm={() => del.mutate(user.id, { onSuccess: () => setConfirm(null), onError: (e) => toast.error(e) })}
      />
    </li>
  );
}

/** Which projects a member works in. */
function ProjectPicker({ view, value, onChange }: { view: AdminView; value: string[]; onChange: (keys: string[]) => void }) {
  return (
    <fieldset>
      <legend className="mb-1.5 block text-[13px] font-medium text-fg-2">Projects it works in</legend>
      {view.projects.length ? (
        <div className="grid max-h-48 grid-cols-1 gap-1 overflow-y-auto rounded-md border border-line p-2 sm:grid-cols-2">
          {view.projects.map((p) => (
            <label key={p.key} className="flex min-w-0 cursor-pointer items-center gap-2 rounded px-1.5 py-1 text-[13px] hover:bg-surface-2">
              <Checkbox
                checked={value.includes(p.key)}
                onChange={(e) => onChange(e.target.checked ? [...value, p.key] : value.filter((k) => k !== p.key))}
              />
              <ColorDot color={p.color} className="size-2" />
              <span className="font-mono text-[12px] text-fg-2">{p.key}</span>
              <span className="truncate">{p.name}</span>
            </label>
          ))}
        </div>
      ) : (
        <p className="text-[13px] text-fg-3">No projects yet.</p>
      )}
      <p className="mt-1 text-[12px] text-fg-3">Their tasks, agent prompts and runs — nothing else. A project's servers, endpoints and repo paths stay hidden.</p>
    </fieldset>
  );
}

function NewAccountDialog({ view, onClose, onCreated }: { view: AdminView; onClose: () => void; onCreated: (user: AdminUser, password: string) => void }) {
  const create = useCreateAccount();
  const toast = useToast();
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState<"member" | "owner">("member");
  const [projects, setProjects] = useState<string[]>([]);
  const ready = username.trim().length >= 2 && email.includes("@");

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!ready || create.isPending) return;
    create.mutate(
      { username: username.trim().toLowerCase(), email: email.trim(), display_name: name.trim() || undefined, role, projects: role === "member" ? projects : undefined },
      { onSuccess: (r) => onCreated(r.user, r.password), onError: (err) => toast.error(err) },
    );
  };

  return (
    <Dialog
      open
      onClose={onClose}
      size="md"
      title="New account"
      description="Forge makes up its password and shows it once; they change it after signing in."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="new-account-form" loading={create.isPending} disabled={!ready}>
            Create account
          </Button>
        </>
      }
    >
      <form id="new-account-form" onSubmit={submit} className="space-y-4">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Username" hint="a–z, 0–9, dot, dash, underscore">
            {(id, desc) => <Input id={id} aria-describedby={desc} data-autofocus value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" />}
          </Field>
          <Field label="Name">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />}</Field>
        </div>
        <Field label="E-mail">{(id) => <Input id={id} type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="off" />}</Field>
        <RoleField role={role} onChange={setRole} />
        {role === "member" ? <ProjectPicker view={view} value={projects} onChange={setProjects} /> : null}
      </form>
    </Dialog>
  );
}

function RoleField({ role, onChange, disabled }: { role: "owner" | "member"; onChange: (r: "owner" | "member") => void; disabled?: boolean }) {
  return (
    <Field
      label="Role"
      hint={role === "owner" ? "Everything, like you: every project, machine, setting, the vault and this page." : "Only the projects below: tasks, agent prompts and runs."}
    >
      {(id, desc) => (
        <Select id={id} aria-describedby={desc} value={role} disabled={disabled} onChange={(e) => onChange(e.target.value as "owner" | "member")}>
          <option value="member">Member — limited to its projects</option>
          <option value="owner">Owner — everything</option>
        </Select>
      )}
    </Field>
  );
}

function EditAccountDialog({ user, view, self, onClose }: { user: AdminUser; view: AdminView; self: boolean; onClose: () => void }) {
  const update = useUpdateAccount();
  const toast = useToast();
  const [name, setName] = useState(user.display_name);
  const [email, setEmail] = useState(user.email);
  const [role, setRole] = useState(user.role);
  const [projects, setProjects] = useState(user.projects);
  const [hasAgent, setHasAgent] = useState(!!user.agent);
  const [machine, setMachine] = useState(user.agent?.runner_name ?? view.machines.find((m) => m.role === "master")?.name ?? view.machines[0]?.name ?? "");
  const [model, setModel] = useState(user.agent?.model ?? "opus");
  const [effort, setEffort] = useState(user.agent?.effort ?? "high");
  const [mode, setMode] = useState(user.agent?.permission_mode ?? "acceptEdits");
  const models = model && !MODELS.includes(model) ? [model, ...MODELS] : MODELS;
  const member = role === "member";

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (update.isPending) return;
    update.mutate(
      {
        id: user.id,
        display_name: name.trim() !== user.display_name ? name.trim() : undefined,
        email: email.trim() !== user.email ? email.trim() : undefined,
        role: role !== user.role ? role : undefined,
        projects: member ? projects : undefined,
        agent: member ? (hasAgent ? { machine, model, effort, permission_mode: mode } : user.agent ? null : undefined) : undefined,
      },
      {
        onSuccess: () => {
          toast.success(`Saved ${user.username}`);
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
      title={
        <span className="flex items-center gap-2">
          <UserRound className="size-4 text-fg-3" aria-hidden /> {user.username}
        </span>
      }
      description={self ? "This is you: your role stays as it is." : undefined}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="edit-account-form" loading={update.isPending} disabled={!name.trim() || !email.includes("@")}>
            Save
          </Button>
        </>
      }
    >
      <form id="edit-account-form" onSubmit={submit} className="space-y-4">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Name">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />}</Field>
          <Field label="E-mail">{(id) => <Input id={id} type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="off" />}</Field>
        </div>
        <RoleField role={role} onChange={setRole} disabled={self} />
        {member ? (
          <>
            <ProjectPicker view={view} value={projects} onChange={setProjects} />
            <fieldset className="space-y-3 rounded-lg border border-line p-3">
              <legend className="px-1 text-[13px] font-medium text-fg-2">Its own agent</legend>
              <Switch checked={hasAgent} onChange={setHasAgent} label="Give this member one agent" />
              <p className="text-[12px] text-fg-3">
                One Claude Code agent with an Agent page, on one machine only, with a set model and effort. Without it, the member queues runs on the
                machines open to members.
              </p>
              {hasAgent ? (
                <>
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                    <Field label="Machine">
                      {(id) => (
                        <Select id={id} value={machine} onChange={(e) => setMachine(e.target.value)}>
                          {view.machines.map((m) => (
                            <option key={m.id} value={m.name}>
                              {m.name}
                              {m.online ? "" : " · offline"}
                            </option>
                          ))}
                        </Select>
                      )}
                    </Field>
                    <Field label="Model">
                      {(id) => (
                        <Select id={id} value={model} onChange={(e) => setModel(e.target.value)}>
                          <option value="">Default</option>
                          {models.map((m) => (
                            <option key={m} value={m}>
                              {m}
                            </option>
                          ))}
                        </Select>
                      )}
                    </Field>
                    <Field label="Effort">
                      {(id) => (
                        <Select id={id} value={effort} onChange={(e) => setEffort(e.target.value)}>
                          <option value="">Default</option>
                          {EFFORTS.map((x) => (
                            <option key={x} value={x}>
                              {EFFORT_LABEL[x] ?? x}
                            </option>
                          ))}
                        </Select>
                      )}
                    </Field>
                  </div>
                  <Field label="Permission mode" hint={`${MODE_HELP[mode] ?? ""} A member's agent never skips permission prompts.`}>
                    {(id, desc) => (
                      <Select id={id} aria-describedby={desc} value={mode} onChange={(e) => setMode(e.target.value)}>
                        {MODES.map((x) => (
                          <option key={x} value={x}>
                            {x}
                          </option>
                        ))}
                      </Select>
                    )}
                  </Field>
                  <p className="text-[12px] text-fg-3">
                    The agent runs as that machine's user, with its files and logins: what the member approves there, it does. Pick the machine with that
                    in mind.
                  </p>
                </>
              ) : null}
            </fieldset>
          </>
        ) : null}
      </form>
    </Dialog>
  );
}

/** A password Forge made up, shown once. */
function PasswordDialog({ user, password, created, onClose }: { user: string; password: string; created: boolean; onClose: () => void }) {
  const toast = useToast();
  return (
    <Dialog
      open
      onClose={onClose}
      size="sm"
      title={created ? `Account ${user} created` : `New password for ${user}`}
      description="Shown once: Forge keeps only its hash. Pass it on over a channel you trust."
      footer={
        <Button variant="primary" onClick={onClose}>
          Done
        </Button>
      }
    >
      <div className="flex items-center gap-2">
        <code className="min-w-0 flex-1 rounded-md border border-line bg-surface-2 px-2.5 py-2 font-mono text-[13.5px] break-all select-all" aria-label="Password">
          {password}
        </code>
        <Button
          size="icon"
          variant="subtle"
          aria-label="Copy the password"
          onClick={() => void navigator.clipboard?.writeText(password).then(() => toast.success("Password copied"))}
        >
          <Copy className="size-4" />
        </Button>
      </div>
      <p className="mt-3 text-[12.5px] text-fg-3">
        They sign in as <span className="font-mono text-fg-2">{user}</span> and change it under their account menu.
      </p>
    </Dialog>
  );
}

function MachinesPanel({ view }: { view: AdminView }) {
  const set = useSetMachineMembers();
  const toast = useToast();
  return (
    <Panel title="Machines and members" icon={<ShieldCheck />} id="admin-machines">
      {view.machines.length ? (
        <ul aria-label="Machines" className="divide-y divide-line">
          {view.machines.map((m) => {
            const agents = view.users.filter((u) => u.agent?.runner_id === m.id);
            return (
              <li key={m.id} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 px-2 py-2.5">
                <div className="min-w-0 flex-1 basis-56">
                  <p className="flex items-center gap-1.5 text-[13.5px] font-medium">
                    <span className={m.online ? "size-1.5 rounded-full bg-good" : "size-1.5 rounded-full bg-fg-3"} aria-hidden />
                    {m.name}
                    {m.role && m.role !== "worker" ? <Badge tone="outline">{m.role}</Badge> : null}
                  </p>
                  <p className="text-[12px] text-fg-3">
                    {agents.length ? `Agent of ${agents.map((u) => u.username).join(", ")}` : "No member has its agent here"}
                    {m.members_allowed ? " · any member may queue runs here" : ""}
                  </p>
                </div>
                <Switch
                  checked={m.members_allowed}
                  disabled={set.isPending}
                  onChange={(members_allowed) => set.mutate({ id: m.id, members_allowed }, { onError: (e) => toast.error(e) })}
                  label="Open to every member"
                />
              </li>
            );
          })}
        </ul>
      ) : (
        <EmptyState compact title="No machines yet" />
      )}
      <p className="px-2 pt-2 pb-1 text-[12px] text-fg-3">
        A member's prompt runs an agent with that machine's files and logins. A member with an agent of its own uses that agent's machine only, open or
        not.
      </p>
    </Panel>
  );
}
