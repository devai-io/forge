// Project agents: one Claude Code session per project (tmux F-<Project> on the
// master), its bot session (F-<Project>-Bot) when the project is kept open,
// plus the extra ones a project may have. What the Agents page's
// project buttons show and do is decided here.

import type { ProjectSession } from "@/api/types";

/** A project with its agent session and its extra ones (F-<Project>-2, …). */
export type AgentGroup = {
  key: string;
  name: string;
  color: string;
  primary: ProjectSession;
  bot: ProjectSession | null;
  extras: ProjectSession[];
};

/** All of a project's sessions: its agent, its bot session, the extras. */
export const groupAll = (g: AgentGroup): ProjectSession[] => [g.primary, ...(g.bot ? [g.bot] : []), ...g.extras];

/** The project is kept open (its agent and bot session start again whenever they are gone). */
export const isKept = (g: AgentGroup) => !!g.primary.kept;

/** Sessions arrive project by project, the project's own agent first. */
export function groupSessions(sessions: ProjectSession[]): AgentGroup[] {
  const groups = new Map<string, AgentGroup>();
  for (const s of sessions) {
    const g = groups.get(s.project_key);
    if (!g) {
      if (s.extra || s.bot) continue; // nothing to hang it on without its project's own entry
      groups.set(s.project_key, { key: s.project_key, name: s.project_name, color: s.project_color, primary: s, bot: null, extras: [] });
    } else if (s.bot) {
      g.bot = s;
    } else if (s.extra) {
      g.extras.push(s);
    }
  }
  return [...groups.values()];
}

/** Claude Code is open in it, or on its way. */
export const isOpen = (s: ProjectSession) => s.state === "running" || s.state === "starting";

export type AgentTone = "needs" | "turn" | "working" | "starting" | "open" | "closed" | "offline";

/** What one session is doing, in a word. */
export function sessionStatus(s: ProjectSession): { tone: AgentTone; label: string } {
  if (s.state === "offline") return { tone: "offline", label: "Offline" };
  if (s.state === "starting" || s.run_status === "queued") return { tone: "starting", label: "Starting…" };
  if (s.state === "running") {
    if (s.run_status === "running") {
      if (s.awaiting === "answer") return { tone: "needs", label: "Needs you" };
      if (s.awaiting === "reply") return { tone: "turn", label: "Your turn" };
      return { tone: "working", label: "Working" };
    }
    return { tone: "open", label: "Open" };
  }
  return { tone: "closed", label: "Start" };
}

const URGENCY: AgentTone[] = ["needs", "turn", "working", "starting", "open", "closed", "offline"];

/** A project's button shows its most pressing session. */
export function groupStatus(g: AgentGroup): { tone: AgentTone; label: string } {
  return groupAll(g).map(sessionStatus).sort((a, b) => URGENCY.indexOf(a.tone) - URGENCY.indexOf(b.tone))[0];
}

/** How many of a project's sessions have Claude Code open. */
export const openCount = (g: AgentGroup) => groupAll(g).filter(isOpen).length;

/**
 * The projects that get a button without asking: the ones being worked on
 * (live or building), and any with a session open or a conversation in one.
 */
export function isActiveGroup(g: AgentGroup): boolean {
  if (isKept(g) || g.primary.project_status === "live" || g.primary.project_status === "building") return true;
  return groupAll(g).some((s) => isOpen(s) || s.run_id !== null);
}

/** The first line of a request, for a row. */
export function promptTitle(prompt: string, max = 90): string {
  const line = prompt.trim().split("\n")[0] ?? "";
  return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}

// ── What the dialog last started a session with ────────────────────────────

const MODEL_KEY = "forge.agent.model";
const EFFORT_KEY = "forge.agent.effort";

function read(key: string): string {
  try {
    return window.localStorage.getItem(key) ?? "";
  } catch {
    return "";
  }
}

function store(key: string, value: string) {
  try {
    if (value) window.localStorage.setItem(key, value);
    else window.localStorage.removeItem(key);
  } catch {
    /* private mode: not remembered */
  }
}

const ENGINE_KEY = "forge.agent.engine";

export type AgentEngine = "claude" | "codex";

/** Model and effort are remembered per engine (Claude's under the original keys). */
const keys = (engine: AgentEngine) =>
  engine === "codex" ? [`${MODEL_KEY}.codex`, `${EFFORT_KEY}.codex`] : [MODEL_KEY, EFFORT_KEY];

/** The engine last started ("" = none yet: the server's default engine decides). */
export const readAgentEngine = (): AgentEngine | "" => {
  const e = read(ENGINE_KEY);
  return e === "claude" || e === "codex" ? e : "";
};

export function readAgentPrefs(engine: AgentEngine = "claude") {
  const [m, e] = keys(engine);
  return { model: read(m!), effort: read(e!) };
}

export function storeAgentPrefs(model: string, effort: string, engine: AgentEngine = "claude") {
  const [m, e] = keys(engine);
  store(ENGINE_KEY, engine);
  store(m!, model);
  store(e!, effort);
}
