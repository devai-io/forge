import clsx from "clsx";
import { useQueryClient } from "@tanstack/react-query";
import { Code, Eye, Lock, Plug, Sparkles, SquareTerminal, Trash2, Users } from "lucide-react";
import { useState } from "react";
import { promptElevation } from "@/api/client";
import { keys, useKillSession } from "@/api/hooks";
import type { TmuxSession, TmuxWindow } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { Input } from "@/components/ui/Input";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { useToast } from "@/components/ui/Toast";
import { truncateMiddle } from "@/lib/terminal";

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

function WindowRow({
  window: w,
  onAttach,
  onPeek,
  edit,
}: {
  window: TmuxWindow;
  onAttach: () => void;
  onPeek: () => void;
  edit?: WindowEdit;
}) {
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
          {w.claude ? <ClaudeBadge className="h-4.5 px-1.5 text-[10.5px]" /> : null}
          {w.project_key ? (
            <span className="inline-flex items-center gap-1 text-[11px] text-fg-2">
              <ColorDot color={w.project_color} className="size-2" />
              {w.project_key}
            </span>
          ) : null}
          {w.repo_name ? <span className="text-[11px] text-fg-2">{w.repo_name}</span> : null}
        </div>
        <p className="truncate font-mono text-[11px] text-fg-3" title={w.path}>
          {w.command ? `${w.command} · ` : ""}
          {truncateMiddle(w.path, 44)}
        </p>
      </div>
      {edit && w.path ? (
        <span title={edit.disabledReason ?? `Edit ${w.path} in VS Code`} className="inline-flex">
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={`Edit here: open window ${w.index} (${w.name}) in VS Code`}
            disabled={!!edit.disabledReason}
            onClick={() => edit.onEdit(w)}
          >
            <Code className="size-3.5" />
          </Button>
        </span>
      ) : null}
      <Button size="icon-sm" variant="ghost" aria-label={`Peek at window ${w.index} (${w.name})`} title="Peek" onClick={onPeek}>
        <Eye className="size-3.5" />
      </Button>
      <Button size="icon-sm" variant="subtle" aria-label={`Attach to window ${w.index} (${w.name})`} title="Attach to this window" onClick={onAttach}>
        <Plug className="size-3.5" />
      </Button>
    </li>
  );
}

/**
 * One tmux session: the session's own facts, then a row per window — each
 * window is often its own Claude session in a different repo, so attach and
 * peek work per window too. `onAttach()`/`onPeek()` without a window target
 * the session's current window.
 */
/** "Edit here" on window rows: open the window's folder in VS Code (only on the machine that serves it). */
export type WindowEdit = { onEdit: (window: TmuxWindow) => void; disabledReason?: string | null };

export function SessionCard({
  session: s,
  hostName,
  onAttach,
  onPeek,
  onKill,
  edit,
  compact = false,
}: {
  session: TmuxSession;
  hostName?: string;
  onAttach: (window?: TmuxWindow) => void;
  onPeek: (window?: TmuxWindow) => void;
  onKill?: () => void;
  edit?: WindowEdit;
  compact?: boolean;
}) {
  const windows = s.window_list?.length ? [...s.window_list].sort((a, b) => a.index - b.index) : [];
  const claude = windows.length ? windows.some((w) => w.claude) : s.claude;
  return (
    <article
      aria-label={`Session ${s.name}`}
      className={clsx(
        "flex min-w-0 flex-col gap-2 rounded-xl border bg-surface p-3.5",
        claude ? "border-accent/45" : "border-line",
      )}
    >
      <div className="flex min-w-0 items-start gap-2">
        <SquareTerminal className="mt-0.5 size-4 shrink-0 text-fg-3" aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <h3 className="truncate font-mono text-[13.5px] font-semibold">{s.name}</h3>
            {claude ? <ClaudeBadge /> : null}
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
              {truncateMiddle(s.path, compact ? 36 : 52)}
            </p>
          ) : null}
        </div>
      </div>
      {windows.length ? (
        <ul aria-label={`Windows of ${s.name}`} className="divide-y divide-line rounded-lg border border-line px-1.5">
          {windows.map((w) => (
            <WindowRow key={w.index} window={w} onAttach={() => onAttach(w)} onPeek={() => onPeek(w)} edit={edit} />
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
        {s.attached > 0 ? (
          <span className="inline-flex items-center gap-1 text-fg-2" title="Clients attached right now, on any machine">
            <Users className="size-3" aria-hidden /> {s.attached} attached
          </span>
        ) : null}
        <span className="ml-auto">
          active <RelativeTime iso={s.activity} />
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        <Button size="sm" variant="primary" onClick={() => onAttach()} title="Attach to the session's current window">
          <Plug className="size-3.5" aria-hidden /> Attach
        </Button>
        <Button size="sm" variant="subtle" onClick={() => onPeek()}>
          <Eye className="size-3.5" aria-hidden /> Peek
        </Button>
        {onKill ? (
          <Button size="sm" variant="ghost" className="ml-auto text-fg-3 hover:text-critical-ink" onClick={onKill} aria-label={`Kill ${s.name}`}>
            <Trash2 className="size-3.5" aria-hidden /> Kill
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
            {session.claude ? ", including the running Claude session and its unsaved conversation" : ""}
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
