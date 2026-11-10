// Project agents: one Claude Code session per project (tmux F-<Project> on the
// master), plus the extra ones a project may have. What the Agents page's
// project buttons show and do is decided here.

import type { ProjectSession } from "@/api/types";

/** A project with its agent session and its extra ones (F-<Project>-2, …). */
export type AgentGroup = {
  key: string;
  name: string;
  color: string;
  primary: ProjectSession;
  extras: ProjectSession[];
};

/** Sessions arrive project by project, the project's own agent first. */
export function groupSessions(sessions: ProjectSession[]): AgentGroup[] {
  const groups = new Map<string, AgentGroup>();
  for (const s of sessions) {
    const g = groups.get(s.project_key);
    if (!g) {
      if (s.extra) continue; // an extra without its project's own entry: nothing to hang it on
      groups.set(s.project_key, { key: s.project_key, name: s.project_name, color: s.project_color, primary: s, extras: [] });
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
  return [g.primary, ...g.extras].map(sessionStatus).sort((a, b) => URGENCY.indexOf(a.tone) - URGENCY.indexOf(b.tone))[0];
}

/** How many of a project's sessions have Claude Code open. */
export const openCount = (g: AgentGroup) => [g.primary, ...g.extras].filter(isOpen).length;

/**
 * The projects that get a button without asking: the ones being worked on
 * (live or building), and any with a session open or a conversation in one.
 */
export function isActiveGroup(g: AgentGroup): boolean {
  if (g.primary.project_status === "live" || g.primary.project_status === "building") return true;
  return [g.primary, ...g.extras].some((s) => isOpen(s) || s.run_id !== null);
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

export const readAgentPrefs = () => ({ model: read(MODEL_KEY), effort: read(EFFORT_KEY) });
export function storeAgentPrefs(model: string, effort: string) {
  store(MODEL_KEY, model);
  store(EFFORT_KEY, effort);
}
