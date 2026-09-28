// Terminal helpers: session ordering and naming, the WebSocket URL, and the
// byte sequences the phone key bar sends. Pure, so they are tested directly.

import type { TerminalHost, TmuxSession, TmuxWindow } from "@/api/types";

type Sortable = Pick<TmuxSession, "claude" | "activity" | "name">;

/** Claude sessions first (that is what gets attached to), then most recently active. */
export function compareSessions(a: Sortable, b: Sortable): number {
  return (
    Number(b.claude) - Number(a.claude) ||
    (Date.parse(b.activity) || 0) - (Date.parse(a.activity) || 0) ||
    a.name.localeCompare(b.name)
  );
}

export function sortSessions<T extends Sortable>(sessions: T[]): T[] {
  return [...sessions].sort(compareSessions);
}

export const SESSION_NAME_RE = /^[A-Za-z0-9_.-]{1,40}$/;

/** Anything outside tmux-safe characters becomes "-", capped at 40. */
export function sanitizeSessionName(raw: string): string {
  return raw
    .replace(/[^A-Za-z0-9_.-]+/g, "-")
    .replace(/-{2,}/g, "-")
    .replace(/^[-.]+|-+$/g, "")
    .slice(0, 40);
}

/**
 * "CAFE" + "cafe_api" → "CAFE-cafe_api". Suffixes -2, -3… past names
 * already on the host, because giving an existing name reattaches it and a
 * "new session" should be new.
 */
export function deriveSessionName(
  projectKey: string | null | undefined,
  repoName: string | null | undefined,
  existing: string[] = [],
  start: "claude" | "shell" = "claude",
): string {
  const base =
    sanitizeSessionName([projectKey, repoName].filter(Boolean).join("-")) || (start === "claude" ? "claude" : "shell");
  const taken = new Set(existing);
  if (!taken.has(base)) return base;
  for (let i = 2; i < 100; i++) {
    const suffix = `-${i}`;
    const candidate = base.slice(0, 40 - suffix.length) + suffix;
    if (!taken.has(candidate)) return candidate;
  }
  return base.slice(0, 32) + "-" + Date.now().toString(36).slice(-7);
}

/** "/home/ada/dev/shop/shop_api" → "/home/ada/…/shop/shop_api" within `max` chars. */
export function truncateMiddle(text: string, max = 44): string {
  if (text.length <= max) return text;
  const keep = max - 1;
  const head = Math.ceil(keep * 0.35);
  const tail = keep - head;
  return `${text.slice(0, head)}…${text.slice(text.length - tail)}`;
}

/** ws(s)://<host>/api/terminal/<runner>/attach?session=…&cols=…&rows=… on the app's own origin. */
export function terminalWsUrl(
  loc: Pick<Location, "protocol" | "host">,
  runnerId: number,
  session: string,
  cols: number,
  rows: number,
  window?: number | null,
): string {
  const scheme = loc.protocol === "https:" ? "wss" : "ws";
  const q = new URLSearchParams({ session, cols: String(Math.max(1, cols)), rows: String(Math.max(1, rows)) });
  // Attaching to a window makes it the session's current one (tmux sessions share one).
  if (window !== null && window !== undefined) q.set("window", String(window));
  return `${scheme}://${loc.host}/api/terminal/${runnerId}/attach?${q.toString()}`;
}

/**
 * The control character for Ctrl+<key>: letters → 0x01–0x1A, and the
 * punctuation terminals map (@ [ \ ] ^ _ ?). Anything else passes through.
 */
export function ctrlChar(key: string): string {
  if (key.length !== 1) return key;
  const c = key.toUpperCase();
  const code = c.charCodeAt(0);
  if (code >= 65 && code <= 90) return String.fromCharCode(code - 64);
  switch (key) {
    case "@":
    case " ":
    case "2":
      return "\u0000";
    case "[":
    case "3":
      return "\u001b";
    case "\\":
    case "4":
      return "\u001c";
    case "]":
    case "5":
      return "\u001d";
    case "^":
    case "6":
      return "\u001e";
    case "_":
    case "7":
    case "-":
      return "\u001f";
    case "?":
    case "8":
      return "\u007f";
    default:
      return key;
  }
}

/** What the phone key bar sends. Arrows are the normal-mode cursor sequences tmux and Claude expect. */
export const KEYBAR = {
  esc: "\u001b",
  tab: "\t",
  up: "\u001b[A",
  down: "\u001b[B",
  right: "\u001b[C",
  left: "\u001b[D",
  ctrlC: "\u0003",
  enter: "\r",
} as const;

/** Quick answers for Peek's send-keys, as {text, enter}. */
export const PEEK_KEYS: { label: string; text: string; enter: boolean; title: string }[] = [
  { label: "Enter", text: "", enter: true, title: "Press Enter" },
  { label: "Esc", text: "\u001b", enter: false, title: "Press Escape" },
  { label: "y", text: "y", enter: true, title: "Type y and Enter" },
  { label: "1", text: "1", enter: true, title: "Type 1 and Enter (first option)" },
  { label: "Ctrl-C", text: "\u0003", enter: false, title: "Interrupt" },
];

export type HostSession = { host: TerminalHost; session: TmuxSession };
export type HostWindow = { host: TerminalHost; session: TmuxSession; window: TmuxWindow };

/** Every session across hosts, optionally filtered, in display order. */
export function allSessions(
  hosts: TerminalHost[],
  filter: (s: TmuxSession, h: TerminalHost) => boolean = () => true,
): HostSession[] {
  const out: HostSession[] = [];
  for (const host of hosts) for (const session of host.sessions) if (filter(session, host)) out.push({ host, session });
  return out.sort((a, b) => compareSessions(a.session, b.session));
}

/**
 * A session's windows. A runner that predates per-window reporting sends no
 * `window_list`; the session's own (active-window) fields then stand in as
 * its only window so matching and attaching still work.
 */
export function sessionWindows(session: TmuxSession): TmuxWindow[] {
  if (session.window_list?.length) return [...session.window_list].sort((a, b) => a.index - b.index);
  return [
    {
      index: -1, // "whatever is current": no window param is sent for it
      name: session.name,
      active: true,
      command: session.command,
      path: session.path,
      claude: session.claude,
      project_key: session.project_key,
      project_color: session.project_color,
      repo_id: session.repo_id,
      repo_name: session.repo_name,
    },
  ];
}

/** The window param to send: none for the stand-in window of an old runner. */
export const windowParam = (w: Pick<TmuxWindow, "index">): number | null => (w.index >= 0 ? w.index : null);

/**
 * Every window across hosts matching `filter` — the unit Claude work happens
 * in (one session often holds several Claude windows in different repos).
 * Claude windows first, then by their session's activity, then window order.
 */
export function allWindows(
  hosts: TerminalHost[],
  filter: (w: TmuxWindow, s: TmuxSession, h: TerminalHost) => boolean = () => true,
): HostWindow[] {
  const out: HostWindow[] = [];
  for (const host of hosts)
    for (const session of host.sessions)
      for (const window of sessionWindows(session)) if (filter(window, session, host)) out.push({ host, session, window });
  return out.sort(
    (a, b) =>
      Number(b.window.claude) - Number(a.window.claude) ||
      (Date.parse(b.session.activity) || 0) - (Date.parse(a.session.activity) || 0) ||
      a.session.name.localeCompare(b.session.name) ||
      a.window.index - b.window.index,
  );
}

/** "Work:2" — tmux's own session:window target notation. */
export const windowTarget = (session: string, w: Pick<TmuxWindow, "index">) => (w.index >= 0 ? `${session}:${w.index}` : session);

/** Route to the terminal view; `window` becomes ?window=N. */
export const attachPath = (runnerId: number, session: string, window?: number | null) =>
  `/terminal/${runnerId}/${encodeURIComponent(session)}${window !== null && window !== undefined ? `?window=${window}` : ""}`;

// ── Preferences ────────────────────────────────────────────────────────────

const HOST_KEY = "forge.terminal.host";
const FONT_KEY = "forge.terminal.fontSize";
export const FONT_MIN = 9;
export const FONT_MAX = 24;

export function readStoredHost(): number | null {
  try {
    const v = Number(localStorage.getItem(HOST_KEY));
    return Number.isInteger(v) && v > 0 ? v : null;
  } catch {
    return null;
  }
}
export function storeHost(id: number) {
  try {
    localStorage.setItem(HOST_KEY, String(id));
  } catch {
    /* storage blocked: the choice lasts for this visit */
  }
}
export function readFontSize(fallback: number): number {
  try {
    const v = Number(localStorage.getItem(FONT_KEY));
    return v >= FONT_MIN && v <= FONT_MAX ? v : fallback;
  } catch {
    return fallback;
  }
}
export function storeFontSize(size: number) {
  try {
    localStorage.setItem(FONT_KEY, String(size));
  } catch {
    /* ignore */
  }
}

/** Pick the host tab: the remembered one if it still exists, else the API's default, else the first. */
export function pickHost(hostIds: number[], stored: number | null, fallback: number | null): number | null {
  if (stored !== null && hostIds.includes(stored)) return stored;
  if (fallback !== null && hostIds.includes(fallback)) return fallback;
  return hostIds[0] ?? null;
}
