// Terminal sessions surfaced where the work is: the project page (windows in
// that project's repos, on any host) and the dashboard (every Claude window).
// Both match per *window* — one tmux session often holds several Claude
// windows, each in a different repo.

import { Eye, Plug, Plus, Sparkles, SquareTerminal } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { isElevationError } from "@/api/client";
import { useTerminalHosts } from "@/api/hooks";
import { ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { useShell } from "@/features/shell/context";
import { allWindows, attachPath, windowParam, windowTarget, type HostWindow } from "@/lib/terminal";
import { ClaudeBadge, UnlockTerminals } from "./bits";
import { PeekSheet } from "./Peek";

function WindowLine({ item, onPeek, showProject = false }: { item: HostWindow; onPeek: () => void; showProject?: boolean }) {
  const navigate = useNavigate();
  const { host, session, window: w } = item;
  return (
    <li className="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 hover:bg-surface-2/60">
      <SquareTerminal className="size-3.5 shrink-0 text-fg-3" aria-hidden />
      <button
        type="button"
        className="min-w-0 flex-1 text-left disabled:cursor-default"
        disabled={!host.online}
        onClick={() => navigate(attachPath(host.runner_id, session.name, windowParam(w)))}
        tabIndex={-1} // the Attach button below is the keyboard path; this is the big touch target
      >
        <div className="flex min-w-0 flex-wrap items-center gap-1.5 text-[13px]">
          <span className="font-mono">{windowTarget(session.name, w)}</span>
          {w.claude ? <ClaudeBadge className="h-4.5 px-1.5 text-[10.5px]" /> : null}
          {showProject && w.project_key ? (
            <span className="inline-flex items-center gap-1 text-[11.5px] text-fg-2">
              <ColorDot color={w.project_color} className="size-2" />
              {w.project_key}
            </span>
          ) : null}
          {w.repo_name ? <span className="text-[12px] text-fg-2">{w.repo_name}</span> : null}
        </div>
        <p className="truncate text-[11px] text-fg-3">
          {host.runner_name}
          {host.online ? "" : " (offline)"} · {w.command || "—"} · active <RelativeTime iso={session.activity} />
        </p>
      </button>
      <Button size="icon-sm" variant="ghost" aria-label={`Peek at ${windowTarget(session.name, w)} on ${host.runner_name}`} title="Peek" onClick={onPeek}>
        <Eye className="size-3.5" />
      </Button>
      <Button
        size="icon-sm"
        variant="subtle"
        aria-label={`Attach to ${windowTarget(session.name, w)} on ${host.runner_name}`}
        title="Attach"
        disabled={!host.online}
        onClick={() => navigate(attachPath(host.runner_id, session.name, windowParam(w)))}
      >
        <Plug className="size-3.5" />
      </Button>
    </li>
  );
}

function usePeek() {
  const [peek, setPeek] = useState<HostWindow | null>(null);
  const sheet = peek ? (
    <PeekSheet
      runnerId={peek.host.runner_id}
      hostName={peek.host.runner_name}
      session={peek.session.name}
      window={windowParam(peek.window)}
      windowLabel={peek.window.repo_name ?? peek.window.name}
      onClose={() => setPeek(null)}
    />
  ) : null;
  return { open: setPeek, sheet };
}

/** Project overview: tmux windows in this project's repos, on every host. */
export function ProjectSessions({ projectKey }: { projectKey: string }) {
  const hosts = useTerminalHosts({ refetchInterval: 15_000 });
  const { newSession } = useShell();
  const peek = usePeek();
  const locked = isElevationError(hosts.error);
  const terminalHosts = (hosts.data?.hosts ?? []).filter((h) => h.terminal);
  // Nothing to show on a fleet with no terminal runners (or an older API).
  if (!locked && !terminalHosts.length) return null;
  const windows = allWindows(terminalHosts, (w) => w.project_key === projectKey);

  return (
    <Panel
      title={
        <>
          Sessions {windows.length ? <span className="font-normal text-fg-3">· {windows.length}</span> : null}
        </>
      }
      icon={<SquareTerminal />}
      id="proj-sessions"
      actions={
        locked ? null : (
          <Button size="sm" variant="ghost" onClick={() => newSession({ project_key: projectKey, start: "claude" })}>
            <Plus className="size-3.5" aria-hidden /> New Claude session
          </Button>
        )
      }
    >
      {locked ? (
        <div className="flex items-center justify-between gap-2 px-2 py-1.5 text-[13px] text-fg-3">
          Sessions are hidden until you confirm it's you.
          <UnlockTerminals compact />
        </div>
      ) : windows.length ? (
        <ul>
          {windows.map((item) => (
            <WindowLine key={`${item.host.runner_id}-${item.session.name}-${item.window.index}`} item={item} onPeek={() => peek.open(item)} />
          ))}
        </ul>
      ) : (
        <p className="px-2 py-2 text-[13px] text-fg-3">No tmux windows in this project's repos right now.</p>
      )}
      {peek.sheet}
    </Panel>
  );
}

/** Dashboard: every window running Claude, and how busy each host is. */
export function ClaudeSessionsPanel() {
  const hosts = useTerminalHosts({ refetchInterval: 15_000 });
  const peek = usePeek();
  const locked = isElevationError(hosts.error);
  const terminalHosts = (hosts.data?.hosts ?? []).filter((h) => h.terminal);
  if (!locked && !terminalHosts.length) return null;
  const claude = allWindows(terminalHosts, (w) => w.claude);

  return (
    <Panel
      title={
        <>
          Claude sessions {claude.length ? <span className="font-normal text-fg-3">· {claude.length}</span> : null}
        </>
      }
      icon={<Sparkles />}
      id="dash-claude"
      actions={
        <Link to="/terminal" className="text-[12px] text-fg-3 hover:text-fg">
          Terminal →
        </Link>
      }
    >
      {locked ? (
        <div className="flex flex-wrap items-center justify-between gap-2 px-2 py-1.5 text-[13px] text-fg-3">
          Locked until you confirm it's you.
          <UnlockTerminals compact />
        </div>
      ) : (
        <>
          <div className="mb-1 flex flex-wrap gap-x-3 gap-y-1 px-2 pt-1 text-[12px] text-fg-3">
            {terminalHosts.map((h) => {
              const n = h.sessions.reduce((acc, s) => acc + (s.window_list?.length ? s.window_list.filter((w) => w.claude).length : Number(s.claude)), 0);
              return (
                <span key={h.runner_id} className="inline-flex items-center gap-1.5">
                  <span className={h.online ? "size-1.5 rounded-full bg-good" : "size-1.5 rounded-full bg-fg-3"} aria-hidden />
                  {h.runner_name}
                  <span className="tabular text-fg-2">
                    {n} Claude · {h.sessions.length} session{h.sessions.length === 1 ? "" : "s"}
                  </span>
                </span>
              );
            })}
          </div>
          {claude.length ? (
            <ul>
              {claude.map((item) => (
                <WindowLine
                  key={`${item.host.runner_id}-${item.session.name}-${item.window.index}`}
                  item={item}
                  showProject
                  onPeek={() => peek.open(item)}
                />
              ))}
            </ul>
          ) : (
            <p className="px-2 py-2 text-[13px] text-fg-3">No Claude sessions running.</p>
          )}
        </>
      )}
      {peek.sheet}
    </Panel>
  );
}
