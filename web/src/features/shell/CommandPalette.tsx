// ⌘/Ctrl+K: jump to a page, a project or a task, or start something new.
// Pages and projects filter locally; tasks come from the API's search (title
// and ref), debounced, once two characters are typed.

import clsx from "clsx";
import { Bot, Code, CornerDownLeft, Plug, Plus, Search, Settings, ShieldAlert, Sparkles } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useNavigate } from "react-router-dom";
import { useCodeStatus, useProjects, useTasks, useTerminalHosts } from "@/api/hooks";
import { ColorDot } from "@/components/ui/Badge";
import { StatusIcon } from "@/features/tasks/icons";
import { editorPath } from "@/lib/code";
import { allWindows, attachPath, windowParam, windowTarget } from "@/lib/terminal";
import { NAV } from "./nav";
import { useShell } from "./context";

type Item = { id: string; label: string; hint?: string; icon: React.ReactNode; run: () => void; group: string };

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = window.setTimeout(() => setV(value), ms);
    return () => window.clearTimeout(t);
  }, [value, ms]);
  return v;
}

export function CommandPalette() {
  const { paletteOpen, setPaletteOpen } = useShell();
  if (!paletteOpen) return null;
  return createPortal(<Palette onClose={() => setPaletteOpen(false)} />, document.body);
}

function Palette({ onClose }: { onClose: () => void }) {
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const navigate = useNavigate();
  const { openTask, newTask, newRun, newSession } = useShell();
  const projects = useProjects();
  // Live sessions, quietly: a locked terminal just contributes no entries.
  const terminal = useTerminalHosts({ refetchInterval: false });
  const code = useCodeStatus();
  const debounced = useDebounced(q.trim(), 180);
  const tasks = useTasks({ q: debounced, limit: 12 }, { enabled: debounced.length >= 2 });
  const listRef = useRef<HTMLDivElement>(null);
  const previous = useRef<HTMLElement | null>(document.activeElement as HTMLElement | null);

  useEffect(() => {
    const prev = previous.current;
    return () => prev?.focus?.();
  }, []);

  const items = useMemo<Item[]>(() => {
    const needle = q.trim().toLowerCase();
    const match = (s: string) => !needle || s.toLowerCase().includes(needle);
    const go = (to: string) => () => {
      onClose();
      navigate(to);
    };
    const out: Item[] = [];
    out.push(
      ...[
        {
          id: "new-task",
          label: "Create task",
          icon: <Plus />,
          run: () => {
            onClose();
            newTask();
          },
        },
        {
          id: "new-run",
          label: "Start agent run",
          icon: <Bot />,
          run: () => {
            onClose();
            newRun();
          },
        },
        {
          id: "new-claude-session",
          label: "New Claude session",
          icon: <Sparkles />,
          run: () => {
            onClose();
            newSession({ start: "claude" });
          },
        },
      ]
        .filter((a) => match(a.label))
        .map((a) => ({ ...a, group: "Actions" })),
    );
    for (const p of projects.data ?? []) {
      if (match(p.name) || match(p.key)) {
        out.push({
          id: `p-${p.key}`,
          label: p.name,
          hint: p.key,
          icon: <ColorDot color={p.color} />,
          run: go(`/p/${p.key}`),
          group: "Projects",
        });
      }
    }
    // "Open <project> in VS Code" — only once something is typed, so the
    // default list stays short.
    if (needle && code.data?.available) {
      for (const p of projects.data ?? []) {
        const label = `Open ${p.name} in VS Code`;
        if (match(label) || `vscode code editor ${p.key} ${p.name}`.toLowerCase().includes(needle))
          out.push({
            id: `code-${p.key}`,
            label,
            hint: p.key,
            icon: <Code />,
            run: go(editorPath({ kind: "project", projectKey: p.key, repoIds: [] })),
            group: "VS Code",
          });
      }
    }
    for (const n of NAV) {
      const keywords = "keywords" in n ? n.keywords : "";
      if (match(n.label) || (needle && keywords.includes(needle)))
        out.push({ id: `nav-${n.to}`, label: n.label, icon: <n.icon />, run: go(n.to), group: "Pages" });
    }
    if (match("Settings")) out.push({ id: "nav-settings", label: "Settings", icon: <Settings />, run: go("/settings"), group: "Pages" });
    if (match("Security log") || (needle && "sessions sign-ins audit security".includes(needle)))
      out.push({ id: "nav-security", label: "Security log", icon: <ShieldAlert />, run: go("/settings#security"), group: "Pages" });
    // One entry per Claude window, plus each session that has none running.
    const hosts = (terminal.data?.hosts ?? []).filter((h) => h.terminal && h.online);
    const projectName = (key: string | null) => projects.data?.find((p) => p.key === key)?.name ?? key;
    for (const { host, session, window: w } of allWindows(hosts, (w) => w.claude)) {
      const label = `Attach: ${host.runner_name} · ${windowTarget(session.name, w)} · ${w.repo_name ?? projectName(w.project_key) ?? w.name}`;
      if (match(label) || match("attach claude"))
        out.push({
          id: `attach-${host.runner_id}-${session.name}-${w.index}`,
          label,
          hint: w.project_key ?? undefined,
          icon: <Sparkles />,
          run: go(attachPath(host.runner_id, session.name, windowParam(w))),
          group: "Sessions",
        });
    }
    for (const host of hosts)
      for (const session of host.sessions) {
        const hasClaude = session.window_list?.length ? session.window_list.some((w) => w.claude) : session.claude;
        if (hasClaude) continue;
        const label = `Attach: ${host.runner_name} · ${projectName(session.project_key) ?? session.name}`;
        if (match(label) || match(session.name))
          out.push({
            id: `attach-${host.runner_id}-${session.name}`,
            label,
            hint: session.name,
            icon: <Plug />,
            run: go(attachPath(host.runner_id, session.name)),
            group: "Sessions",
          });
      }
    if (debounced.length >= 2) {
      for (const t of tasks.data ?? []) {
        out.push({
          id: `t-${t.id}`,
          label: t.title,
          hint: t.ref,
          icon: <StatusIcon status={t.status} />,
          run: () => {
            onClose();
            openTask(t.id);
          },
          group: "Tasks",
        });
      }
    }
    return out;
  }, [q, debounced, projects.data, tasks.data, terminal.data, code.data, navigate, onClose, newTask, newRun, newSession, openTask]);

  const clamped = Math.min(active, Math.max(0, items.length - 1));

  useEffect(() => {
    listRef.current?.querySelector(`[data-index="${clamped}"]`)?.scrollIntoView({ block: "nearest" });
  }, [clamped]);

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((a) => Math.min(a + 1, items.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      items[clamped]?.run();
    } else if (e.key === "Escape") {
      e.preventDefault();
      onClose();
    }
  };

  let lastGroup = "";
  return (
    <div className="fixed inset-0 z-[55] flex items-start justify-center p-3 pt-[12vh]">
      <div className="absolute inset-0 bg-overlay" onClick={onClose} aria-hidden />
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        className="animate-in relative w-full max-w-xl overflow-hidden rounded-xl border border-line-strong bg-surface shadow-pop"
        onKeyDown={onKeyDown}
      >
        <div className="flex items-center gap-2 border-b border-line px-3">
          <Search className="size-4 text-fg-3" aria-hidden />
          <input
            autoFocus
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setActive(0);
            }}
            placeholder="Jump to a project, page or task…"
            aria-label="Search"
            aria-controls="palette-list"
            aria-activedescendant={items[clamped] ? `palette-${items[clamped].id}` : undefined}
            className="h-12 flex-1 bg-transparent text-[15px] outline-none placeholder:text-fg-3"
          />
        </div>
        <div ref={listRef} id="palette-list" role="listbox" className="max-h-[55vh] overflow-y-auto p-1.5">
          {items.length === 0 ? (
            <p className="px-3 py-6 text-center text-sm text-fg-3">
              {debounced.length >= 2 && tasks.isFetching ? "Searching…" : "Nothing found"}
            </p>
          ) : (
            items.map((item, i) => {
              const header = item.group !== lastGroup ? item.group : null;
              lastGroup = item.group;
              return (
                <div key={item.id}>
                  {header ? <div className="px-2.5 pt-2 pb-1 text-[11px] font-semibold tracking-wide text-fg-3 uppercase">{header}</div> : null}
                  <button
                    type="button"
                    id={`palette-${item.id}`}
                    role="option"
                    aria-selected={i === clamped}
                    data-index={i}
                    onMouseMove={() => setActive(i)}
                    onClick={item.run}
                    className={clsx(
                      "flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left text-[13.5px]",
                      i === clamped ? "bg-surface-3 text-fg" : "text-fg-2",
                    )}
                  >
                    <span className="grid size-4 place-items-center text-fg-3 [&>svg]:size-4">{item.icon}</span>
                    <span className="min-w-0 flex-1 truncate">{item.label}</span>
                    {item.hint ? <span className="font-mono text-[11.5px] text-fg-3">{item.hint}</span> : null}
                    {i === clamped ? <CornerDownLeft className="size-3.5 text-fg-3" aria-hidden /> : null}
                  </button>
                </div>
              );
            })
          )}
        </div>
      </div>
    </div>
  );
}
