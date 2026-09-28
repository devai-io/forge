// A live terminal on a tmux session, relayed by the API from the runner.
//
// Frames: binary both ways for terminal bytes; text JSON for control
// ({"type":"resize"} up, {"type":"exit"} down). A WebSocket upgrade cannot
// carry the API's 403 JSON, so elevation is checked *before* connecting, and a
// failed upgrade is diagnosed afterwards with a quiet REST call.
//
// Detaching closes only this socket — tmux keeps the session running.

import "@xterm/xterm/css/xterm.css";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { Terminal } from "@xterm/xterm";
import clsx from "clsx";
import { ArrowLeft, Eye, Minus, Plus, RotateCw, ShieldAlert } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, ApiError, promptElevation } from "@/api/client";
import { useTerminalHosts } from "@/api/hooks";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Spinner } from "@/components/ui/Spinner";
import { useElevatedUntil } from "@/lib/auth";
import { ctrlChar, FONT_MAX, FONT_MIN, KEYBAR, readFontSize, storeFontSize, terminalWsUrl } from "@/lib/terminal";
import { cssVars, terminalTheme } from "@/lib/terminalTheme";
import { useTheme } from "@/lib/theme";
import { ClaudeBadge } from "./bits";
import { PeekPanel } from "./Peek";

type Conn =
  | { kind: "connecting" }
  | { kind: "open" }
  | { kind: "exited"; reason: string }
  | { kind: "closed" }
  | { kind: "failed"; message: string };

const encoder = new TextEncoder();

function useMedia(query: string): boolean {
  const [matches, setMatches] = useState(() => window.matchMedia?.(query).matches ?? false);
  useEffect(() => {
    const mq = window.matchMedia?.(query);
    if (!mq) return;
    const on = () => setMatches(mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, [query]);
  return matches;
}

/** The visual viewport: on phones it shrinks when the soft keyboard opens. */
function useVisualViewport(): { height: number; top: number } | null {
  const [vv, setVv] = useState<{ height: number; top: number } | null>(null);
  useEffect(() => {
    const v = window.visualViewport;
    if (!v) return;
    const on = () => setVv({ height: v.height, top: v.offsetTop });
    on();
    v.addEventListener("resize", on);
    v.addEventListener("scroll", on);
    return () => {
      v.removeEventListener("resize", on);
      v.removeEventListener("scroll", on);
    };
  }, []);
  return vv;
}

async function diagnose(runnerId: number, session: string): Promise<string> {
  try {
    await api.getQuiet(`/terminal/${runnerId}/sessions/${encodeURIComponent(session)}/capture`, { lines: 1 });
    return "The terminal connection was refused.";
  } catch (err) {
    if (!(err instanceof ApiError)) return "Can't reach the Forge API.";
    switch (err.code) {
      case "elevation_required":
        return "Your confirmation has expired.";
      case "runner_offline":
        return "The runner is offline.";
      case "terminal_disabled":
        return "Terminals are disabled on this runner.";
      case "not_found":
        return `There is no session named ${session} on this host any more.`;
      default:
        return err.message;
    }
  }
}

export function TerminalView() {
  const params = useParams();
  const runnerId = Number(params.runnerId);
  const session = params.session ?? "";
  const [search] = useSearchParams();
  const rawWindow = search.get("window");
  const windowIndex = rawWindow !== null && /^\d+$/.test(rawWindow) ? Number(rawWindow) : null;
  const navigate = useNavigate();
  const hosts = useTerminalHosts({ refetchInterval: 15_000 });
  const host = hosts.data?.hosts.find((h) => h.runner_id === runnerId);
  const sessionInfo = host?.sessions.find((s) => s.name === session);
  // What the header describes: the targeted window, else the current one.
  const info =
    sessionInfo?.window_list?.find((w) => (windowIndex !== null ? w.index === windowIndex : w.active)) ?? sessionInfo;

  const { resolved } = useTheme();
  const desktop = useMedia("(min-width: 768px)");
  const coarse = useMedia("(pointer: coarse)");
  const vv = useVisualViewport();
  const elevatedUntil = useElevatedUntil();

  const box = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const sizeRef = useRef<{ cols: number; rows: number }>({ cols: 0, rows: 0 });
  const detaching = useRef(false);
  const ctrlRef = useRef(false);
  const elevatedRef = useRef(elevatedUntil);
  elevatedRef.current = elevatedUntil;

  const [conn, setConn] = useState<Conn>({ kind: "connecting" });
  const [ctrl, setCtrl] = useState(false);
  const [fontSize, setFontSize] = useState(() => readFontSize(window.innerWidth < 640 ? 12 : 13));
  const [peek, setPeek] = useState(false);

  const setSticky = (on: boolean) => {
    ctrlRef.current = on;
    setCtrl(on);
  };

  const send = useCallback((data: string | Uint8Array) => {
    const ws = wsRef.current;
    if (ws?.readyState !== WebSocket.OPEN) return;
    ws.send(typeof data === "string" ? encoder.encode(data) : data);
  }, []);

  const sendResize = useCallback((force = false) => {
    const term = termRef.current;
    const ws = wsRef.current;
    if (!term) return;
    try {
      fitRef.current?.fit();
    } catch {
      /* not laid out yet */
    }
    const { cols, rows } = term;
    if (!force && cols === sizeRef.current.cols && rows === sizeRef.current.rows) return;
    sizeRef.current = { cols, rows };
    if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols, rows }));
  }, []);

  const connect = useCallback(
    async (forceElevation = false) => {
      const term = termRef.current;
      if (!term || !Number.isInteger(runnerId) || !session) return;
      detaching.current = false;
      wsRef.current?.close();
      wsRef.current = null;
      setConn({ kind: "connecting" });

      // The upgrade can't answer 403 elevation_required, so ask first.
      const until = elevatedRef.current ? Date.parse(elevatedRef.current) : 0;
      if (forceElevation || !(until - Date.now() > 15_000)) {
        if (!(await promptElevation())) {
          setConn({ kind: "failed", message: "Attaching to a terminal needs your password again." });
          return;
        }
      }

      try {
        fitRef.current?.fit();
      } catch {
        /* keep the previous size */
      }
      const ws = new WebSocket(terminalWsUrl(window.location, runnerId, session, term.cols, term.rows, windowIndex));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;
      let opened = false;
      let exited = false;

      ws.onopen = () => {
        opened = true;
        setConn({ kind: "open" });
        sendResize(true);
        term.focus();
      };
      ws.onmessage = (ev) => {
        if (typeof ev.data === "string") {
          try {
            const msg = JSON.parse(ev.data) as { type?: string; reason?: string };
            if (msg.type === "exit") {
              exited = true;
              setConn({ kind: "exited", reason: msg.reason || "The session ended." });
            }
          } catch {
            /* ignore malformed control frames */
          }
          return;
        }
        term.write(new Uint8Array(ev.data as ArrayBuffer));
      };
      ws.onclose = () => {
        if (wsRef.current !== ws || detaching.current || exited) return;
        if (!opened) {
          void diagnose(runnerId, session).then((message) => setConn({ kind: "failed", message }));
        } else {
          setConn({ kind: "closed" });
        }
      };
    },
    [runnerId, session, windowIndex, sendResize],
  );

  // Create the terminal once per session; tear everything down on leave.
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const term = new Terminal({
      fontFamily: getComputedStyle(document.documentElement).getPropertyValue("--font-mono") || "monospace",
      fontSize: readFontSize(window.innerWidth < 640 ? 12 : 13),
      cursorBlink: true,
      scrollback: 5000,
      macOptionIsMeta: true,
      theme: terminalTheme(document.documentElement.getAttribute("data-theme") === "light" ? "light" : "dark", cssVars()),
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon((_e, uri) => window.open(uri, "_blank", "noopener,noreferrer")));
    term.open(el);
    termRef.current = term;
    fitRef.current = fit;
    try {
      fit.fit();
    } catch {
      /* container not measured yet; the ResizeObserver will fit it */
    }

    const onData = term.onData((data) => {
      if (ctrlRef.current && data.length === 1) {
        data = ctrlChar(data);
        ctrlRef.current = false;
        setCtrl(false);
      }
      send(data);
    });
    // Mouse reports in X10/binary mode arrive as one char per byte.
    const onBinary = term.onBinary((data) => send(Uint8Array.from(data, (c) => c.charCodeAt(0) & 0xff)));

    let timer: number | undefined;
    const ro = new ResizeObserver(() => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => sendResize(), 120);
    });
    ro.observe(el);

    void connect();

    return () => {
      window.clearTimeout(timer);
      ro.disconnect();
      onData.dispose();
      onBinary.dispose();
      detaching.current = true;
      wsRef.current?.close();
      wsRef.current = null;
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, [connect, send, sendResize]);

  // Follow the app theme.
  useEffect(() => {
    if (termRef.current) termRef.current.options.theme = terminalTheme(resolved, cssVars());
  }, [resolved]);

  // Font size: apply, remember, refit.
  useEffect(() => {
    if (!termRef.current) return;
    termRef.current.options.fontSize = fontSize;
    storeFontSize(fontSize);
    sendResize();
  }, [fontSize, sendResize]);

  // The peek panel and the keyboard change the terminal's box.
  useEffect(() => {
    const t = window.setTimeout(() => sendResize(), 60);
    return () => window.clearTimeout(t);
  }, [peek, vv?.height, sendResize]);

  const detach = () => {
    detaching.current = true;
    wsRef.current?.close();
    navigate("/terminal");
  };

  const key = (seq: string) => {
    send(seq);
    termRef.current?.focus();
  };

  // Phones: the panel tracks the visual viewport so the key bar sits right
  // above the soft keyboard instead of behind it.
  const mobileStyle = !desktop && vv ? { height: vv.height, top: vv.top } : undefined;

  return (
    <div
      className="fixed inset-x-0 top-0 bottom-0 z-50 flex flex-col bg-surface md:top-13 md:left-60 md:z-20"
      style={mobileStyle}
    >
      <header className="flex h-11 shrink-0 items-center gap-2 border-b border-line px-2 pt-[env(safe-area-inset-top)] md:px-3">
        <Button size="sm" variant="ghost" onClick={detach} title="Close this connection — the session keeps running">
          <ArrowLeft className="size-4" aria-hidden /> <span className="hidden sm:inline">Detach</span>
        </Button>
        <div className="flex min-w-0 flex-1 items-center gap-1.5 text-[13px]">
          <span className={clsx("size-1.5 shrink-0 rounded-full", conn.kind === "open" ? "bg-good" : conn.kind === "connecting" ? "bg-warning" : "bg-critical")} aria-hidden />
          <span className="sr-only">{conn.kind === "open" ? "Connected" : conn.kind === "connecting" ? "Connecting" : "Disconnected"}</span>
          <span className="hidden text-fg-3 sm:inline">{host?.runner_name ?? `runner ${runnerId}`} ·</span>
          <span className="truncate font-mono font-medium">
            {windowIndex !== null ? `${session}:${windowIndex}` : session}
          </span>
          {info && "index" in info && info.repo_name ? (
            <span className="hidden truncate text-fg-3 md:inline">{info.repo_name}</span>
          ) : null}
          {/* A wrapper carries the breakpoint: the badges' own inline-flex would
              fight a "hidden" class on them (clsx does not dedupe). */}
          <span className="hidden shrink-0 items-center gap-1.5 sm:flex">
            {info?.project_key ? (
              <Badge tone="outline">
                <ColorDot color={info.project_color} className="size-2" /> {info.project_key}
              </Badge>
            ) : null}
            {info?.claude ? <ClaudeBadge /> : null}
          </span>
        </div>
        <div className="flex items-center gap-0.5">
          <Button size="icon-sm" variant="ghost" aria-label="Smaller text" disabled={fontSize <= FONT_MIN} onClick={() => setFontSize((f) => Math.max(FONT_MIN, f - 1))}>
            <Minus className="size-3.5" />
          </Button>
          <span className="tabular w-6 text-center text-[11px] text-fg-3" aria-label={`Font size ${fontSize}`}>
            {fontSize}
          </span>
          <Button size="icon-sm" variant="ghost" aria-label="Larger text" disabled={fontSize >= FONT_MAX} onClick={() => setFontSize((f) => Math.min(FONT_MAX, f + 1))}>
            <Plus className="size-3.5" />
          </Button>
          <Button size="sm" variant={peek ? "secondary" : "ghost"} aria-pressed={peek} onClick={() => setPeek((p) => !p)}>
            <Eye className="size-3.5" aria-hidden /> <span className="hidden sm:inline">Peek</span>
          </Button>
        </div>
      </header>

      <div className="relative flex min-h-0 flex-1">
        <div className={clsx("relative min-w-0 flex-1", peek && !desktop && "hidden")}>
          <div ref={box} className="absolute inset-0 px-1 py-1" data-testid="terminal" />
          {conn.kind !== "open" ? (
            <ConnOverlay
              conn={conn}
              session={session}
              onReconnect={() => void connect()}
              onElevateRetry={() => void connect(true)}
              onBack={detach}
            />
          ) : null}
        </div>
        {peek ? (
          <aside className="flex min-h-0 w-full flex-col border-l border-line bg-surface md:w-[420px]">
            <PeekPanel
              runnerId={runnerId}
              hostName={host?.runner_name ?? String(runnerId)}
              session={session}
              window={windowIndex}
              onClose={() => setPeek(false)}
              showAttach={false}
            />
          </aside>
        ) : null}
      </div>

      {(coarse || !desktop) && !peek ? (
        <div
          role="toolbar"
          aria-label="Terminal keys"
          className="safe-bottom flex shrink-0 gap-1 border-t border-line bg-surface-2 px-1.5 py-1.5"
        >
          {(
            [
              ["Esc", KEYBAR.esc],
              ["Tab", KEYBAR.tab],
              ["ctrl", ""],
              ["↑", KEYBAR.up],
              ["↓", KEYBAR.down],
              ["←", KEYBAR.left],
              ["→", KEYBAR.right],
              ["^C", KEYBAR.ctrlC],
              ["⏎", KEYBAR.enter],
            ] as const
          ).map(([label, seq]) =>
            label === "ctrl" ? (
              <button
                key={label}
                type="button"
                aria-pressed={ctrl}
                aria-label="Control (applies to the next key)"
                onPointerDown={(e) => e.preventDefault()}
                onClick={() => {
                  setSticky(!ctrlRef.current);
                  termRef.current?.focus();
                }}
                className={clsx(
                  "h-9 min-w-9 flex-1 rounded-md border px-1.5 font-mono text-[13px]",
                  ctrl ? "border-accent bg-accent text-accent-fg" : "border-line-strong bg-surface text-fg",
                )}
              >
                Ctrl
              </button>
            ) : (
              <button
                key={label}
                type="button"
                aria-label={label === "^C" ? "Control-C" : label === "⏎" ? "Enter" : label}
                // Keep focus (and the soft keyboard) in the terminal.
                onPointerDown={(e) => e.preventDefault()}
                onClick={() => key(seq)}
                className="h-9 min-w-9 flex-1 rounded-md border border-line-strong bg-surface px-1.5 font-mono text-[13px] text-fg active:bg-surface-3"
              >
                {label}
              </button>
            ),
          )}
        </div>
      ) : null}
    </div>
  );
}

function ConnOverlay({
  conn,
  session,
  onReconnect,
  onElevateRetry,
  onBack,
}: {
  conn: Exclude<Conn, { kind: "open" }>;
  session: string;
  onReconnect: () => void;
  onElevateRetry: () => void;
  onBack: () => void;
}) {
  return (
    <div className="absolute inset-0 z-10 grid place-items-center bg-surface/80 p-4 backdrop-blur-[2px]" role="status">
      {conn.kind === "connecting" ? (
        <p className="flex items-center gap-2 text-[13px] text-fg-2">
          <Spinner className="size-4" /> Attaching to <span className="font-mono">{session}</span>…
        </p>
      ) : (
        <div className="w-full max-w-sm space-y-3 rounded-xl border border-line-strong bg-surface p-4 text-center shadow-pop">
          {conn.kind === "failed" ? (
            <>
              <ShieldAlert className="mx-auto size-5 text-warning" aria-hidden />
              <p className="text-[14px] font-semibold">Could not attach — confirm again?</p>
              <p className="text-[13px] text-fg-2">{conn.message}</p>
            </>
          ) : conn.kind === "exited" ? (
            <>
              <p className="text-[14px] font-semibold">Session ended</p>
              <p className="text-[13px] text-fg-2">{conn.reason}</p>
            </>
          ) : (
            <>
              <p className="text-[14px] font-semibold">Connection lost</p>
              <p className="text-[13px] text-fg-2">The session is still running on the host.</p>
            </>
          )}
          <div className="flex justify-center gap-2">
            <Button size="sm" variant="ghost" onClick={onBack}>
              Back to sessions
            </Button>
            {conn.kind === "failed" ? (
              <Button size="sm" variant="primary" onClick={onElevateRetry}>
                <ShieldAlert className="size-3.5" aria-hidden /> Confirm and retry
              </Button>
            ) : (
              <Button size="sm" variant="primary" onClick={onReconnect}>
                <RotateCw className="size-3.5" aria-hidden /> Reconnect
              </Button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
