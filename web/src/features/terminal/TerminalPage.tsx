// Every tmux session on every runner that allows terminals, one host per tab.
// Attach opens the full terminal (/terminal/<runner>/<session>); Peek reads
// and answers a session without one — enough to reply to Claude from a phone.

import clsx from "clsx";
import { Plus, SquareTerminal, WifiOff } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { isElevationError, promptElevation } from "@/api/client";
import { useTerminalHosts } from "@/api/hooks";
import type { TerminalHost, TmuxSession } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { PageHeader } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton } from "@/components/ui/Skeleton";
import { useShell } from "@/features/shell/context";
import { attachPath, pickHost, readStoredHost, sortSessions, storeHost, windowParam } from "@/lib/terminal";
import { RoleBadge } from "@/features/agents/RoleBadge";
import { useEditorLauncher } from "@/features/editor/launcher";
import { KillSessionDialog, SessionCard, UnlockTerminals } from "./bits";
import { PeekSheet } from "./Peek";

export function TerminalPage() {
  const hosts = useTerminalHosts();
  const { newSession } = useShell();
  const [chosen, setChosen] = useState<number | null>(null);
  const locked = isElevationError(hosts.error);

  // Coming here is asking for terminals: offer the password once, up front.
  // Polling after that stays quiet.
  const prompted = useRef(false);
  useEffect(() => {
    if (locked && !prompted.current) {
      prompted.current = true;
      void promptElevation().then((ok) => {
        if (ok) void hosts.refetch();
      });
    }
  }, [locked, hosts]);

  const list = hosts.data?.hosts ?? [];
  const selectedId = chosen ?? pickHost(list.map((h) => h.runner_id), readStoredHost(), hosts.data?.default_runner_id ?? null);
  const selected = list.find((h) => h.runner_id === selectedId) ?? null;

  const choose = (id: number) => {
    setChosen(id);
    storeHost(id);
  };

  return (
    <div className="mx-auto max-w-[1200px]">
      <PageHeader
        title="Terminal"
        subtitle="tmux sessions on your machines — attach from any browser, or peek and answer from your phone."
        actions={
          <Button variant="primary" size="sm" onClick={() => newSession({ runner_id: selected?.runner_id, start: "claude" })} disabled={locked}>
            <Plus className="size-3.5" aria-hidden /> New session
          </Button>
        }
      />
      {hosts.isPending ? (
        <div className="space-y-3">
          <Skeleton className="h-10 w-full max-w-xl" />
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <Skeleton className="h-36" />
            <Skeleton className="h-36" />
          </div>
        </div>
      ) : locked ? (
        <UnlockTerminals />
      ) : hosts.error ? (
        <ErrorState error={hosts.error} onRetry={() => hosts.refetch()} />
      ) : list.length === 0 ? (
        <EmptyState icon={<SquareTerminal />} title="No runners yet">
          Terminals come from forge_runner on your machines. Add a runner on the Agents page and set "terminal": true in
          its runner.json.
        </EmptyState>
      ) : (
        <>
          <HostTabs hosts={list} selected={selectedId} onSelect={choose} />
          {selected ? <HostSessions host={selected} /> : null}
        </>
      )}
    </div>
  );
}

function HostTabs({ hosts, selected, onSelect }: { hosts: TerminalHost[]; selected: number | null; onSelect: (id: number) => void }) {
  return (
    <div role="tablist" aria-label="Hosts" className="mb-4 flex gap-1 overflow-x-auto border-b border-line [scrollbar-width:none]">
      {hosts.map((h) => {
        const active = h.runner_id === selected;
        const claude = h.sessions.filter((s) => s.claude).length;
        return (
          <button
            key={h.runner_id}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onSelect(h.runner_id)}
            className={clsx(
              "relative -mb-px flex h-10 shrink-0 items-center gap-2 border-b-2 px-3 text-sm transition-colors",
              active ? "border-accent font-medium text-fg" : "border-transparent hover:text-fg-2",
              !h.online && "opacity-55",
              !active && "text-fg-3",
            )}
          >
            <span className={clsx("size-1.5 rounded-full", h.online ? "bg-good" : "bg-fg-3")} aria-hidden />
            {h.runner_name}
            {h.role && h.role !== "worker" ? <RoleBadge role={h.role} className="h-4.5 px-1.5 text-[10px]" /> : null}
            {!h.online ? <span className="text-[11px] font-normal">{h.role === "master" ? "offline" : "asleep"}</span> : null}
            {h.online && h.terminal ? (
              <span className="tabular rounded bg-surface-2 px-1 text-[11px] text-fg-3" title={`${h.sessions.length} sessions, ${claude} running Claude`}>
                {h.sessions.length}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

function HostSessions({ host }: { host: TerminalHost }) {
  const navigate = useNavigate();
  const code = useEditorLauncher();
  // VS Code runs on one machine (the master): only its windows' folders exist there.
  const servesCode = code.runnerName ? code.runnerName === host.runner_name : host.role === "master";
  const { newSession } = useShell();
  const [peek, setPeek] = useState<{ session: string; window: number | null; label?: string } | null>(null);
  const [killing, setKilling] = useState<TmuxSession | null>(null);

  if (!host.terminal) {
    return (
      <EmptyState icon={<SquareTerminal />} title={`Terminals disabled on ${host.runner_name}`}>
        terminals disabled on this runner — set <code className="font-mono">"terminal": true</code> in its runner.json and
        restart forge_runner.
      </EmptyState>
    );
  }

  const sessions = sortSessions(host.sessions);
  return (
    <div className="space-y-3">
      {!host.online ? (
        <p className="flex items-center gap-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-[13px]">
          <WifiOff className="size-4 shrink-0 text-warning" aria-hidden />
          {host.runner_name} is offline — last seen <RelativeTime iso={host.updated_at} />. The list below is what it last
          reported.
        </p>
      ) : (
        <p className="text-[12px] text-fg-3">
          {host.hostname || host.runner_name} · {sessions.length} session{sessions.length === 1 ? "" : "s"} · reported{" "}
          <RelativeTime iso={host.updated_at} />
        </p>
      )}
      {sessions.length ? (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          {sessions.map((s) => (
            <SessionCard
              key={s.name}
              session={s}
              onAttach={(w) => navigate(attachPath(host.runner_id, s.name, w ? windowParam(w) : null))}
              onPeek={(w) => setPeek({ session: s.name, window: w ? windowParam(w) : null, label: w?.repo_name ?? w?.name })}
              onKill={host.online ? () => setKilling(s) : undefined}
              edit={
                servesCode
                  ? {
                      onEdit: (w) => code.open({ kind: "folder", path: w.path }),
                      disabledReason: code.available ? null : code.reason,
                    }
                  : undefined
              }
            />
          ))}
        </div>
      ) : (
        <EmptyState
          icon={<SquareTerminal />}
          title={`No tmux sessions on ${host.runner_name}`}
          action={
            host.online ? (
              <Button size="sm" variant="primary" onClick={() => newSession({ runner_id: host.runner_id, start: "claude" })}>
                Start a Claude session
              </Button>
            ) : null
          }
        />
      )}
      {peek ? (
        <PeekSheet
          runnerId={host.runner_id}
          hostName={host.runner_name}
          session={peek.session}
          window={peek.window}
          windowLabel={peek.label}
          onClose={() => setPeek(null)}
        />
      ) : null}
      {killing ? (
        <KillSessionDialog runnerId={host.runner_id} hostName={host.runner_name} session={killing} onClose={() => setKilling(null)} />
      ) : null}
    </div>
  );
}
