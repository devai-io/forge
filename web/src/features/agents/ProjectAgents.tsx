// The project buttons at the top of Agents: one small square per active
// project. A project has one agent — a Claude Code (or Codex) session in the
// tmux session F-<Project> on the master, Remote Control on for Claude — and
// its button says what that agent is doing. Clicking it asks for an engine,
// a model, an effort and,
// if you like, a request; the session opens if it is not open yet, and the
// request is typed into it. An extra session for the same project is one
// switch away.

import clsx from "clsx";
import { ExternalLink, Play, Plug, WifiOff, X } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useCloseProjectSession, useEngine, useOpenProjectSession, useProjectSessions } from "@/api/hooks";
import type { ProjectSession, ProjectSessions } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Field, Select, Switch, Textarea } from "@/components/ui/Input";
import { useToast } from "@/components/ui/Toast";
import { EFFORT_LABEL, ENGINE_LABEL } from "@/lib/agents";
import { useUser } from "@/lib/auth";
import { shortHome } from "@/lib/paths";
import {
  groupSessions,
  groupStatus,
  isActiveGroup,
  isOpen,
  openCount,
  promptTitle,
  readAgentEngine,
  readAgentPrefs,
  sessionStatus,
  storeAgentPrefs,
  type AgentEngine,
  type AgentGroup,
  type AgentTone,
} from "@/lib/projectAgents";
import { attachPath } from "@/lib/terminal";

const AGENT_MODELS = ["fable", "opus", "sonnet", "haiku"];

const DOT: Record<AgentTone, string> = {
  needs: "bg-warning animate-pulse-soft",
  turn: "bg-warning",
  working: "bg-accent animate-pulse-soft",
  starting: "bg-accent animate-pulse-soft",
  open: "bg-good",
  closed: "",
  offline: "bg-fg-3",
};

const BADGE: Record<AgentTone, "warning" | "accent" | "good" | "outline"> = {
  needs: "warning",
  turn: "warning",
  working: "accent",
  starting: "accent",
  open: "good",
  closed: "outline",
  offline: "outline",
};

/**
 * The grid of project buttons; nothing for an account or a fleet that cannot
 * host project agents. A button is a filter: it narrows the Agents page to
 * that project (again to show everything), and a strip under the grid shows
 * that project's agent sessions — attach, close, or open one.
 */
export function ProjectAgents({ selected = null, onSelect }: { selected?: string | null; onSelect?: (key: string | null) => void }) {
  const member = useUser().role === "member";
  return member ? null : <ProjectButtons selected={selected} onSelect={onSelect ?? (() => {})} />;
}

function ProjectButtons({ selected, onSelect }: { selected: string | null; onSelect: (key: string | null) => void }) {
  const ps = useProjectSessions(5_000);
  const [opening, setOpening] = useState(false);
  const [all, setAll] = useState(false);
  const v = ps.data;
  if (!v?.sessions?.length) return null;
  const groups = groupSessions(v.sessions);
  const active = groups.filter(isActiveGroup);
  // A picked project stays in the grid even when it is not an active one.
  const shown = all ? groups : groups.filter((g) => isActiveGroup(g) || g.key === selected);
  const hidden = groups.length - active.length;
  const current = groups.find((g) => g.key === selected) ?? null;

  return (
    <section aria-labelledby="agents-projects" id="agents-projects-section">
      <div className="mb-2 flex items-baseline gap-2 px-0.5">
        <h2 id="agents-projects" className="text-[13.5px] font-semibold">
          Projects
        </h2>
        <p className="min-w-0 flex-1 truncate text-[12px] text-fg-3">
          {v.host
            ? v.host.online
              ? `Pick one to see only its agents · one agent per project, in tmux on ${v.host.runner_name}`
              : `${v.host.runner_name} is offline`
            : "Needs a master machine with terminals on"}
        </p>
        {hidden > 0 ? (
          <button type="button" onClick={() => setAll((x) => !x)} aria-pressed={all} className="shrink-0 text-[12px] text-fg-3 hover:text-fg">
            {all ? "Active only" : `All projects (${groups.length})`}
          </button>
        ) : null}
      </div>
      {shown.length ? (
        <ul className="flex flex-wrap gap-2" aria-label="Project agents">
          {shown.map((g) => (
            <li key={g.key}>
              <ProjectButton group={g} picked={g.key === selected} onClick={() => onSelect(g.key === selected ? null : g.key)} />
            </li>
          ))}
        </ul>
      ) : (
        <p className="rounded-xl border border-line bg-surface px-3.5 py-2.5 text-[13px] text-fg-3">
          No active project has a repo with a path.{" "}
          <button type="button" className="font-medium text-accent hover:underline" onClick={() => setAll(true)}>
            Show all projects
          </button>
        </p>
      )}
      {current ? <PickedProject group={current} view={v} onOpen={() => setOpening(true)} onClear={() => onSelect(null)} /> : null}
      {current && opening ? <ProjectAgentDialog group={current} view={v} onClose={() => setOpening(false)} /> : null}
    </section>
  );
}

/** The picked project: its agent sessions, a way to open one, and the way back to all projects. */
function PickedProject({ group, view, onOpen, onClear }: { group: AgentGroup; view: ProjectSessions; onOpen: () => void; onClear: () => void }) {
  const sessions = [group.primary, ...group.extras].filter((s) => isOpen(s) || s.run_id !== null);
  const offline = !view.host || !view.host.online;
  return (
    <div aria-label={`${group.name} agent sessions`} role="region" className="mt-2 rounded-xl border border-line bg-surface">
      <div className="flex items-center gap-2 px-3 py-2">
        <ColorDot color={group.color} className="size-2.5" />
        <span className="min-w-0 flex-1 truncate text-[13px]">
          <span className="font-medium">{group.name}</span>
          <span className="text-fg-3">
            {" "}
            · {sessions.length ? `${sessions.length} session${sessions.length === 1 ? "" : "s"}` : "no session open"} · {shortHome(group.primary.dir)}
          </span>
        </span>
        <Button size="sm" variant="subtle" onClick={onOpen} disabled={offline} title="Open its tmux session, with or without a first request">
          <Play className="size-3.5" aria-hidden /> Open session
        </Button>
        <Button size="sm" variant="ghost" onClick={onClear} aria-label="Show all projects">
          <X className="size-3.5" aria-hidden /> All projects
        </Button>
      </div>
      {sessions.length ? (
        <ul aria-label="Sessions" className="divide-y divide-line border-t border-line">
          {sessions.map((s) => (
            <SessionRow key={s.name} s={s} runnerId={view.host?.runner_id ?? null} onNavigate={() => {}} />
          ))}
        </ul>
      ) : null}
    </div>
  );
}

/** One project: a small square — its key and what its agent is doing; the name is in its tooltip and label. Pressed: the page shows only it. */
function ProjectButton({ group, picked, onClick }: { group: AgentGroup; picked: boolean; onClick: () => void }) {
  const st = groupStatus(group);
  const open = openCount(group);
  const lit = st.tone !== "closed" && st.tone !== "offline";
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={picked}
      aria-label={`${group.name}: ${st.tone === "closed" ? "not open" : st.label}${open > 1 ? `, ${open} sessions` : ""}`}
      title={`${group.name} · ${picked ? "showing only its agents — click for all projects" : "show only its agents"}`}
      className={clsx(
        "group relative flex size-[4.25rem] flex-col justify-between rounded-xl border p-1.5 text-left transition-[background,box-shadow,transform] duration-100",
        "hover:shadow-pop active:scale-[0.97]",
        lit ? "border-line-strong" : "border-line bg-surface hover:bg-surface-2",
        picked && "ring-2 ring-accent ring-offset-2 ring-offset-surface",
      )}
      style={lit ? { background: `color-mix(in srgb, ${group.color || "var(--accent)"} 13%, var(--surface))` } : undefined}
    >
      <span className="flex h-3 items-center justify-between">
        <ColorDot color={group.color} className="size-2.5" />
        {open > 1 ? (
          <span className="tabular rounded-full bg-surface px-1 text-[9.5px] leading-3 font-medium text-fg-2 ring-1 ring-line-strong" aria-hidden>
            {open}
          </span>
        ) : null}
      </span>
      <span className="truncate font-mono text-[11.5px] leading-none font-semibold tracking-wide">{group.key}</span>
      <span className={clsx("flex min-w-0 items-center gap-1 text-[9.5px] leading-none", lit ? "text-fg" : "text-fg-3")}>
        {st.tone === "closed" ? (
          <Play className="size-2.5 shrink-0 group-hover:text-accent" aria-hidden />
        ) : (
          <span className={clsx("size-1.5 shrink-0 rounded-full", DOT[st.tone])} aria-hidden />
        )}
        <span className="truncate font-medium">{st.label}</span>
      </span>
    </button>
  );
}

function ProjectAgentDialog({ group, view, onClose }: { group: AgentGroup; view: ProjectSessions; onClose: () => void }) {
  const navigate = useNavigate();
  const toast = useToast();
  const engine = useEngine();
  const open = useOpenProjectSession();
  const host = view.host;
  const primaryOpen = isOpen(group.primary);
  const sessions = [group.primary, ...group.extras].filter((s) => isOpen(s) || s.run_id !== null);

  // Codex when the machine can host it and it was picked last time, or is the default engine.
  const canCodex = !!host?.codex;
  const [agentEngine, setAgentEngine] = useState<AgentEngine>(() => {
    const last = readAgentEngine() || (engine.data?.settings.default === "codex" ? "codex" : "claude");
    return last === "codex" && canCodex ? "codex" : "claude";
  });
  const [model, setModel] = useState(() => readAgentPrefs(agentEngine).model);
  const [effort, setEffort] = useState(() => readAgentPrefs(agentEngine).effort);
  const pickEngine = (e: AgentEngine) => {
    const p = readAgentPrefs(e);
    setAgentEngine(e);
    setModel(p.model);
    setEffort(p.effort);
  };
  const [prompt, setPrompt] = useState("");
  const [extra, setExtra] = useState(false);
  const canSkip = !!host?.skip_permissions;
  const [skip, setSkip] = useState(view.settings.skip_permissions);

  // Claude Code's own aliases (each is the newest model of its family on that
  // machine); for Codex, what the machine's Codex lists for its login.
  const codex = agentEngine === "codex";
  const codexModels = engine.data?.codex_models ?? [];
  const models: { id: string; name: string }[] = codex
    ? codexModels.map((m) => ({ id: m.id, name: m.name && m.name !== m.id ? `${m.name} (${m.id})` : m.id }))
    : AGENT_MODELS.map((m) => ({ id: m, name: m }));
  if (model && !models.some((m) => m.id === model)) models.unshift({ id: model, name: model });
  const pickedCodex = codex ? codexModels.find((m) => m.id === model) : undefined;
  const efforts = codex
    ? (pickedCodex?.efforts?.length ? pickedCodex.efforts : (engine.data?.codex_efforts ?? ["low", "medium", "high", "xhigh"]))
    : (engine.data?.efforts ?? ["low", "medium", "high", "xhigh", "max"]);

  // A session starts (so engine, model, effort and permissions apply) unless
  // the request goes to the open one — which it does only when that one runs
  // the same engine (the other engine gets a session of its own).
  const primaryEngine = group.primary.engine || "claude";
  const otherOpen = primaryOpen && primaryEngine !== agentEngine;
  const starts = !primaryOpen || extra || otherOpen;
  const text = prompt.trim();
  const offline = !host || !host.online;
  const label = starts ? (extra ? "Start an extra session" : text ? "Start with this request" : "Start") : text ? "Send" : "Attach";

  const submit = (e?: React.FormEvent) => {
    e?.preventDefault();
    if (offline || open.isPending || !host) return;
    if (!starts && !text) {
      onClose();
      navigate(attachPath(host.runner_id, group.primary.name));
      return;
    }
    if (starts) storeAgentPrefs(model, effort, agentEngine);
    open.mutate(
      {
        key: group.key,
        engine: starts ? agentEngine : undefined,
        prompt: text || undefined,
        extra: extra || undefined,
        model: starts && model ? model : undefined,
        effort: starts && effort ? effort : undefined,
        skip_permissions: starts && canSkip ? skip : undefined,
      },
      {
        onSuccess: (res) => {
          onClose();
          if (res.run && text) {
            toast.success(res.started ? `Started ${res.session} with your request` : `Sent to ${res.session}`);
            navigate(`/agents/runs/${res.run.id}`);
          } else {
            if (res.started) toast.success(`Started ${res.session}`);
            navigate(attachPath(res.runner_id, res.session));
          }
        },
        onError: (err) => toast.error(err),
      },
    );
  };

  return (
    <Dialog
      open
      onClose={onClose}
      size="md"
      title={
        <span className="flex items-center gap-2">
          <ColorDot color={group.color} className="size-3" /> {group.name}
        </span>
      }
      description={
        <>
          <span className="font-mono">{group.primary.name}</span>
          {host ? ` on ${host.runner_name}` : ""} · {shortHome(group.primary.dir)}
        </>
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="project-agent-form" loading={open.isPending} disabled={offline}>
            {!starts && !text ? <Plug className="size-3.5" aria-hidden /> : null}
            {label}
          </Button>
        </>
      }
    >
      <form id="project-agent-form" onSubmit={submit} className="space-y-4">
        {offline ? (
          <p className="flex items-start gap-2 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-[13px]">
            <WifiOff className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden />
            {host ? `${host.runner_name} is offline — its sessions come back with it.` : "Project agents live on the master machine, with terminals on (\"terminal\": true in its agent.json)."}
          </p>
        ) : null}

        {sessions.length ? (
          <ul aria-label="Sessions" className="divide-y divide-line rounded-lg border border-line">
            {sessions.map((s) => (
              <SessionRow key={s.name} s={s} runnerId={host?.runner_id ?? null} onNavigate={onClose} />
            ))}
          </ul>
        ) : null}

        <Field
          label={starts ? "First request (optional)" : `Request for ${group.primary.name} (optional)`}
          hint={starts ? "Leave it empty to just open the session. ⌘/Ctrl+Enter to start." : "Typed into the open session. Leave it empty to attach. ⌘/Ctrl+Enter to send."}
        >
          {(id, desc) => (
            <Textarea
              id={id}
              aria-describedby={desc}
              data-autofocus
              rows={4}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submit();
              }}
              placeholder="What should it do?"
            />
          )}
        </Field>

        {canCodex ? (
          <Field
            label="Agent"
            hint={
              otherOpen
                ? `${group.primary.name} runs ${ENGINE_LABEL[primaryEngine]}: ${ENGINE_LABEL[agentEngine]} starts in a session of its own.`
                : codex
                  ? "Codex in tmux, with this machine's Codex login."
                  : "Claude Code in tmux, with this machine's Claude login."
            }
          >
            {(id, desc) => (
              <Select
                id={id}
                aria-describedby={desc}
                value={agentEngine}
                onChange={(e) => pickEngine(e.target.value as AgentEngine)}
              >
                <option value="claude">{ENGINE_LABEL.claude}</option>
                <option value="codex">{ENGINE_LABEL.codex}</option>
              </Select>
            )}
          </Field>
        ) : null}

        <div className="grid grid-cols-2 gap-3">
          <Field label="Model">
            {(id) => (
              <Select id={id} value={model} disabled={!starts} onChange={(e) => setModel(e.target.value)}>
                <option value="">Default</option>
                {models.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.name}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label="Effort">
            {(id) => (
              <Select id={id} value={effort} disabled={!starts} onChange={(e) => setEffort(e.target.value)}>
                <option value="">Default</option>
                {efforts.map((x) => (
                  <option key={x} value={x}>
                    {EFFORT_LABEL[x] ?? x}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
        {!starts ? (
          <p className="-mt-2 text-[12px] text-fg-3">
            {group.primary.name} is open: it keeps the model and effort it started with. They apply to a session that starts.
          </p>
        ) : null}

        <div className="flex flex-col gap-2.5">
          {primaryOpen ? <Switch checked={extra} onChange={setExtra} label={`Start an extra session for ${group.name}`} /> : null}
          {starts && canSkip ? <Switch checked={skip} onChange={setSkip} label="Skip permission prompts" /> : null}
        </div>
      </form>
    </Dialog>
  );
}

/** One open session of the project: what it is doing, and the ways into it. */
function SessionRow({ s, runnerId, onNavigate }: { s: ProjectSession; runnerId: number | null; onNavigate: () => void }) {
  const navigate = useNavigate();
  const toast = useToast();
  const close = useCloseProjectSession();
  const [confirming, setConfirming] = useState(false);
  const st = sessionStatus(s);
  const live = isOpen(s);
  return (
    <li className="flex min-w-0 items-center gap-2 px-2.5 py-2">
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span className="truncate font-mono text-[13px]">{s.name}</span>
          <Badge tone={BADGE[st.tone]} dot className="h-4.5 px-1.5 text-[10.5px]">
            {st.tone === "closed" ? "Closed" : st.label}
          </Badge>
        </div>
        {s.run_id !== null ? (
          <Link to={`/agents/runs/${s.run_id}`} onClick={onNavigate} className="block truncate text-[12px] text-fg-3 hover:text-fg">
            #{s.run_id} · {promptTitle(s.run_prompt) || "conversation"}
          </Link>
        ) : (
          <p className="truncate text-[12px] text-fg-3">No conversation yet</p>
        )}
      </div>
      {confirming ? (
        <>
          <Button
            size="sm"
            variant="danger"
            loading={close.isPending}
            onClick={() =>
              close.mutate(
                { key: s.project_key, name: s.name },
                { onSuccess: () => toast.success(`Closed ${s.name}`), onError: (e) => toast.error(e), onSettled: () => setConfirming(false) },
              )
            }
          >
            Close it
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setConfirming(false)}>
            Keep
          </Button>
        </>
      ) : (
        <>
          {s.url ? (
            <a
              href={s.url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex h-7 shrink-0 items-center gap-1 rounded-md border border-line px-2 text-[12.5px] hover:bg-surface-2"
              aria-label={`Open ${s.name} in Claude`}
              title="Open in Claude (Remote Control)"
            >
              Claude <ExternalLink className="size-3" aria-hidden />
            </a>
          ) : null}
          {live && runnerId !== null ? (
            <Button
              size="sm"
              variant="subtle"
              aria-label={`Attach to ${s.name}`}
              title="Attach in Forge: the same session, live"
              onClick={() => {
                onNavigate();
                navigate(attachPath(runnerId, s.name));
              }}
            >
              <Plug className="size-3.5" aria-hidden /> Attach
            </Button>
          ) : null}
          <Button size="icon-sm" variant="ghost" aria-label={`Close ${s.name}`} title="Close the session (its conversation is finished)" onClick={() => setConfirming(true)}>
            <X className="size-3.5" />
          </Button>
        </>
      )}
    </li>
  );
}
