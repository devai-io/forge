import clsx from "clsx";
import { useQueryClient } from "@tanstack/react-query";
import { Bot, Code, Eye, Lock, Plug, Sparkles, SquareTerminal, Trash2, Users } from "lucide-react";
import { useState, type ReactNode } from "react";
import { promptElevation } from "@/api/client";
import { keys, useKillSession } from "@/api/hooks";
import type { TmuxSession, TmuxWindow } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { Input } from "@/components/ui/Input";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { useToast } from "@/components/ui/Toast";
import { commandLabel, isAgent, shortPath } from "@/lib/terminal";

/** The prominent marker for a session running Claude Code. */
export function ClaudeBadge({ className }: { className?: string }) {
  return (
    <span
      className={clsx(
        "inline-flex h-5 shrink-0 items-center gap-1 rounded-full bg-accent px-2 text-[11px] font-semibold text-accent-fg",
        className,
      )}
    >
      <Sparkles className="size-3" aria-hidden /> Claude
    </span>
  );
}

/** The same marker for a session running Codex. */
export function CodexBadge({ className }: { className?: string }) {
  return (
    <span
      className={clsx(
        "inline-flex h-5 shrink-0 items-center gap-1 rounded-full bg-accent px-2 text-[11px] font-semibold text-accent-fg",
        className,
      )}
    >
      <Bot className="size-3" aria-hidden /> Codex
    </span>
  );
}

/** Claude's or Codex's badge for a window or session running one, else nothing. */
export function AgentBadge({ item, className }: { item?: { claude: boolean; codex?: boolean }; className?: string }) {
  if (item?.claude) return <ClaudeBadge className={className} />;
  if (item?.codex) return <CodexBadge className={className} />;
  return null;
}

/** Keeps a disabled button's reason hoverable (a disabled <button> gets no pointer events in some browsers). */
function Gate({ reason, className, children }: { reason?: string | null; className?: string; children: ReactNode }) {
  return reason ? (
    <span title={reason} className={clsx("inline-flex", className)}>
      {children}
    </span>
  ) : (
    <>{children}</>
  );
}

/** VS Code's "edit here" for a window — hidden on phones, where VS Code is no use. */
function EditButton({ window: w, edit, offline }: { window: TmuxWindow; edit: WindowEdit; offline?: string | null }) {
  const reason = offline ?? edit.disabledReason;
  return (
    <span title={reason ?? `Edit ${w.path} in VS Code`} className="hidden sm:inline-flex">
      <Button size="icon-sm" variant="ghost" aria-label={`Edit here: open window ${w.index} (${w.name}) in VS Code`} disabled={!!reason} onClick={() => edit.onEdit(w)}>
        <Code className="size-3.5" />
      </Button>
    </span>
  );
}

function WindowRow({
  window: w,
  onAttach,
  onPeek,
  edit,
  offline,
}: {
  window: TmuxWindow;
  onAttach: () => void;
  onPeek: () => void;
  edit?: WindowEdit;
  offline?: string | null;
}) {
  const cmd = commandLabel(w);
  return (
    <li className="flex min-w-0 items-center gap-2 py-1.5">
      <span
        className={clsx("w-4 shrink-0 text-center text-[10px]", w.active ? "text-accent" : "text-transparent")}
        title={w.active ? "Current window" : undefined}
        aria-label={w.active ? "current window" : undefined}
      >
        ●
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span className="font-mono text-[12.5px] text-fg">
            {w.index}:{w.name}
          </span>
          <AgentBadge item={w} className={clsx("h-4.5 px-1.5 text-[10.5px]", offline && "opacity-60")} />
          {w.project_key ? (
            <span className="inline-flex items-center gap-1 text-[11px] text-fg-2">
              <ColorDot color={w.project_color} className="size-2" />
              {w.project_key}
            </span>
          ) : null}
          {w.repo_name ? <span className="text-[11px] text-fg-2">{w.repo_name}</span> : null}
        </div>
        <p className="truncate font-mono text-[11px] text-fg-3" title={w.path}>
          {cmd ? `${cmd} · ` : ""}
          {shortPath(w.path, 44)}
        </p>
      </div>
      {edit && w.path ? <EditButton window={w} edit={edit} offline={offline} /> : null}
      <Gate reason={offline}>
        <Button size="icon-sm" variant="ghost" aria-label={`Peek at window ${w.index} (${w.name})`} title={offline ? undefined : "Peek"} disabled={!!offline} onClick={onPeek}>
          <Eye className="size-3.5" />
        </Button>
      </Gate>
      <Gate reason={offline}>
        <Button
          size="icon-sm"
          variant="subtle"
          aria-label={`Attach to window ${w.index} (${w.name})`}
          title={offline ? undefined : "Attach to this window"}
          disabled={!!offline}
          onClick={onAttach}
        >
          <Plug className="size-3.5" />
        </Button>
      </Gate>
    </li>
  );
}

/** "Edit here" on window rows: open the window's folder in VS Code (only on the machine that serves it). */
export type WindowEdit = { onEdit: (window: TmuxWindow) => void; disabledReason?: string | null };

type SessionCardProps = {
  session: TmuxSession;
  hostName?: string;
  onAttach: (window?: TmuxWindow) => void;
  onPeek: (window?: TmuxWindow) => void;
  onKill?: () => void;
  edit?: WindowEdit;
  compact?: boolean;
  /** Set (to "<machine> is offline") when the machine is offline: attach, peek and edit are off, and say why. */
  offline?: string | null;
};

/**
 * One tmux session: the session's own facts, then a row per window — each
 * window is often its own Claude session in a different repo, so attach and
 * peek work per window too. `onAttach()`/`onPeek()` without a window target
 * the session's current window. A session with exactly one window is one
 * compact row instead (most are: one Claude in one repo).
 */
export function SessionCard(props: SessionCardProps) {
  const { session: s, hostName, onAttach, onPeek, onKill, edit, compact = false, offline } = props;
  const windows = s.window_list?.length ? [...s.window_list].sort((a, b) => a.index - b.index) : [];
  if (windows.length === 1) return <SingleWindowCard {...props} window={windows[0]} />;
  const agent = windows.length ? { claude: windows.some((w) => w.claude), codex: windows.some((w) => !!w.codex) } : s;
  return (
    <article
      aria-label={`Session ${s.name}`}
      className={clsx(
        "flex min-w-0 flex-col gap-2 rounded-xl border bg-surface p-3.5",
        isAgent(agent) && !offline ? "border-accent/45" : "border-line",
      )}
    >
      <div className="flex min-w-0 items-start gap-2">
        <SquareTerminal className="mt-0.5 size-4 shrink-0 text-fg-3" aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <h3 className="truncate font-mono text-[13.5px] font-semibold">{s.name}</h3>
            <AgentBadge item={agent} className={offline ? "opacity-60" : undefined} />
            {!windows.length && s.project_key ? (
              <Badge tone="outline">
                <ColorDot color={s.project_color} className="size-2" />
                {s.project_key}
              </Badge>
            ) : null}
            {hostName ? <span className="text-[11.5px] text-fg-3">on {hostName}</span> : null}
          </div>
          {!windows.length ? (
            <p className="mt-0.5 truncate font-mono text-[11.5px] text-fg-3" title={s.path}>
              {s.repo_name ? <span className="text-fg-2">{s.repo_name} · </span> : null}
              {shortPath(s.path, compact ? 36 : 52)}
            </p>
          ) : null}
        </div>
      </div>
      {windows.length ? (
        <ul aria-label={`Windows of ${s.name}`} className="divide-y divide-line rounded-lg border border-line px-1.5">
          {windows.map((w) => (
            <WindowRow key={w.index} window={w} onAttach={() => onAttach(w)} onPeek={() => onPeek(w)} edit={edit} offline={offline} />
          ))}
        </ul>
      ) : null}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11.5px] text-fg-3">
        {!windows.length ? (
          <span>
            running <span className="font-mono text-fg-2">{s.command || "—"}</span>
          </span>
        ) : null}
        <span>
          {s.windows} window{s.windows === 1 ? "" : "s"}
        </span>
        <Attached count={s.attached} />
        <span className="ml-auto">
          active <RelativeTime iso={s.activity} />
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        <Gate reason={offline}>
          <Button size="sm" variant="primary" onClick={() => onAttach()} title={offline ? undefined : "Attach to the session's current window"} disabled={!!offline}>
            <Plug className="size-3.5" aria-hidden /> Attach
          </Button>
        </Gate>
        <Gate reason={offline}>
          <Button size="sm" variant="subtle" onClick={() => onPeek()} disabled={!!offline}>
            <Eye className="size-3.5" aria-hidden /> Peek
          </Button>
        </Gate>
        {onKill ? (
          <Button size="sm" variant="ghost" className="ml-auto text-fg-3 hover:text-critical-ink" onClick={onKill} aria-label={`Kill ${s.name}`}>
            <Trash2 className="size-3.5" aria-hidden /> Kill
          </Button>
        ) : null}
      </div>
    </article>
  );
}

function Attached({ count, short = false }: { count: number; short?: boolean }) {
  if (count <= 0) return null;
  return (
    <span className="inline-flex items-center gap-1 text-fg-2" title="Clients attached right now, on any machine">
      <Users className="size-3" aria-hidden /> {count}
      {short ? <span className="sr-only"> attached</span> : " attached"}
    </span>
  );
}

/** A one-window session as a single row: project, name, agent badge once, path, activity, then Peek and Attach once. */
function SingleWindowCard({ session: s, window: w, hostName, onAttach, onPeek, onKill, edit, offline }: SessionCardProps & { window: TmuxWindow }) {
  const agent = { claude: w.claude || s.claude, codex: !!w.codex || !!s.codex };
  const cmd = commandLabel(w);
  return (
    <article
      aria-label={`Session ${s.name}`}
      className={clsx(
        "flex min-w-0 items-center gap-2.5 rounded-xl border bg-surface px-3 py-2.5",
        isAgent(agent) && !offline ? "border-accent/45" : "border-line",
      )}
    >
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5">
          {w.project_key ? (
            <span className="inline-flex items-center gap-1 text-[11.5px] font-medium text-fg-2">
              <ColorDot color={w.project_color} className="size-2" />
              {w.project_key}
            </span>
          ) : (
            <SquareTerminal className="size-3.5 shrink-0 text-fg-3" aria-hidden />
          )}
          <h3 className="min-w-0 truncate font-mono text-[13px] font-semibold">{s.name}</h3>
          <AgentBadge item={agent} className={clsx("h-4.5 px-1.5 text-[10.5px]", offline && "opacity-60")} />
          {hostName ? <span className="text-[11px] text-fg-3">on {hostName}</span> : null}
        </div>
        <p className="flex min-w-0 items-center gap-1.5 text-[11px] text-fg-3">
          <span className="min-w-0 truncate font-mono" title={w.path}>
            {w.repo_name ? <span className="text-fg-2">{w.repo_name} · </span> : null}
            {cmd ? `${cmd} · ` : ""}
            {shortPath(w.path, 36)}
          </span>
          <span className="shrink-0">
            · active <RelativeTime iso={s.activity} />
          </span>
          <span className="shrink-0">
            <Attached count={s.attached} short />
          </span>
        </p>
      </div>
      <div className="flex shrink-0 items-center gap-1">
        {edit && w.path ? <EditButton window={w} edit={edit} offline={offline} /> : null}
        <Gate reason={offline}>
          <Button size="sm" variant="subtle" aria-label={`Peek at ${s.name}`} onClick={() => onPeek(w)} disabled={!!offline}>
            <Eye className="size-3.5" aria-hidden /> <span className="max-sm:sr-only">Peek</span>
          </Button>
        </Gate>
        <Gate reason={offline}>
          <Button size="sm" variant="primary" aria-label={`Attach to ${s.name}`} onClick={() => onAttach(w)} disabled={!!offline}>
            <Plug className="size-3.5" aria-hidden /> Attach
          </Button>
        </Gate>
        {onKill ? (
          <Button size="icon-sm" variant="ghost" className="text-fg-3 hover:text-critical-ink" onClick={onKill} aria-label={`Kill ${s.name}`} title={`Kill ${s.name}`}>
            <Trash2 className="size-3.5" />
          </Button>
        ) : null}
      </div>
    </article>
  );
}

/** Kill a session, confirmed by typing its name — tmux has no undo. */
export function KillSessionDialog({
  runnerId,
  hostName,
  session,
  onClose,
  onKilled,
}: {
  runnerId: number;
  hostName: string;
  session: TmuxSession;
  onClose: () => void;
  onKilled?: () => void;
}) {
  const kill = useKillSession();
  const toast = useToast();
  const [typed, setTyped] = useState("");
  return (
    <ConfirmDialog
      open
      onClose={onClose}
      title={`Kill ${session.name} on ${hostName}?`}
      confirmLabel="Kill session"
      loading={kill.isPending}
      body={
        <div className="space-y-3">
          <p>
            Ends every window and process in it
            {isAgent(session) ? `, including the running ${session.claude ? "Claude" : "Codex"} session and its unsaved conversation` : ""}
            {session.attached ? `, and disconnects ${session.attached} attached client${session.attached > 1 ? "s" : ""}` : ""}.
          </p>
          <Input
            aria-label={`Type ${session.name} to confirm`}
            placeholder={`Type ${session.name} to confirm`}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            className="font-mono"
            autoComplete="off"
          />
        </div>
      }
      onConfirm={() => {
        if (typed.trim() !== session.name) {
          toast.error(`Type ${session.name} to confirm`);
          return;
        }
        kill.mutate(
          { runnerId, name: session.name },
          {
            onSuccess: () => {
              toast.success(`Killed ${session.name}`);
              onKilled?.();
              onClose();
            },
            onError: (e) => toast.error(e),
          },
        );
      }}
    />
  );
}

/**
 * Terminals need a recent password confirmation. Background polling does not
 * prompt; this is the explicit way in.
 */
export function UnlockTerminals({ compact = false }: { compact?: boolean }) {
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const unlock = async () => {
    setBusy(true);
    try {
      if (await promptElevation()) await qc.invalidateQueries({ queryKey: keys.terminalHosts });
    } finally {
      setBusy(false);
    }
  };
  if (compact) {
    return (
      <Button size="sm" variant="subtle" onClick={unlock} loading={busy}>
        <Lock className="size-3.5" aria-hidden /> Unlock sessions
      </Button>
    );
  }
  return (
    <div className="flex flex-col items-center gap-2 rounded-xl border border-dashed border-line-strong px-6 py-10 text-center">
      <Lock className="size-5 text-fg-3" aria-hidden />
      <p className="text-sm font-medium text-fg-2">Terminals are locked</p>
      <p className="max-w-sm text-[13px] text-fg-3">
        A terminal is a shell on your machines, so seeing and attaching to sessions needs your password (and code) from
        the last 10 minutes.
      </p>
      <Button variant="primary" size="sm" onClick={unlock} loading={busy}>
        Confirm it's you
      </Button>
    </div>
  );
}
