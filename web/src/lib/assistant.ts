// Assistant display helpers: pairing tool calls with their results, and the
// one-line summaries the conversation shows for them. (The cache merging the
// hooks need lives in lib/chats, so this stays out of the main bundle.)
//
// Tool arguments and results are whatever the server's agent loop produced,
// typed `unknown` in the contract — every read here is defensive, so an
// unexpected shape degrades to a plainer line instead of breaking the page.

import { ApiError } from "@/api/client";
import type { ChatEngine, ChatMessage, ChatToolCall, ChatTurn, ChatUsage, RunStatus, UsageEntry } from "@/api/types";

/** Where the Settings panel lives, for links from the page's "off" state. */
export const ASSISTANT_SETTINGS_PATH = "/settings#settings-assistant";

export const EXAMPLE_PROMPTS = [
  "What needs my attention across my projects today?",
  "Review the open SHOP bugs and fix the easiest one on desk",
  "Run the tests for GAME on laptop and tell me what fails",
  "Create a GAME task for a pause menu, then have Claude Code plan it",
];

/** The API says the assistant is off (not enabled, or no key stored). */
export const isAssistantOff = (err: unknown) => err instanceof ApiError && err.code === "assistant_off";

// ── Display model ──────────────────────────────────────────────────────────

export type ChatItem =
  | { kind: "user"; key: string; message: ChatMessage }
  | { kind: "assistant"; key: string; message: ChatMessage }
  // `result` is null while the tool is still running.
  | { kind: "tool"; key: string; call: ChatToolCall; result: ChatMessage | null };

/**
 * Messages → what the conversation renders. An assistant turn becomes its
 * prose (when it has any) followed by one row per tool call, each paired with
 * the tool message that answers it; tool messages are never shown on their own
 * unless nothing called them.
 */
export function buildChatItems(messages: ChatMessage[]): ChatItem[] {
  const results = new Map<string, ChatMessage>();
  for (const m of messages) if (m.role === "tool" && m.tool_call_id) results.set(m.tool_call_id, m);
  const called = new Set<string>();
  const items: ChatItem[] = [];
  for (const m of messages) {
    if (m.role === "user") {
      items.push({ kind: "user", key: `m${m.seq}`, message: m });
    } else if (m.role === "assistant") {
      if (m.content.trim()) items.push({ kind: "assistant", key: `m${m.seq}`, message: m });
      for (const call of m.tool_calls ?? []) {
        called.add(call.id);
        items.push({ kind: "tool", key: `t${m.seq}-${call.id}`, call, result: results.get(call.id) ?? null });
      }
    } else if (!called.has(m.tool_call_id)) {
      items.push({
        kind: "tool",
        key: `m${m.seq}`,
        call: { id: m.tool_call_id, name: m.tool_name, arguments: {} },
        result: m,
      });
    }
  }
  return items;
}

// ── Tool rows ──────────────────────────────────────────────────────────────

export type ToolView = {
  // delegate / command: a card with a live run badge; runs / task / generic: one line.
  kind: "delegate" | "command" | "runs" | "task" | "generic";
  text: string;
  runId: number | null; // the run a delegate / command queued
  task: { id: number; ref: string } | null; // create / update: the task to open
  prompt: string;
  permissionMode: string;
  model: string;
  modelNote: string;
};

type Obj = Record<string, unknown>;
const obj = (v: unknown): Obj => (v && typeof v === "object" && !Array.isArray(v) ? (v as Obj) : {});
const str = (v: unknown): string => (typeof v === "string" ? v : typeof v === "number" ? String(v) : "");
const num = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) ? v : null);

const RUN_STATUS: Record<RunStatus, string> = {
  queued: "queued",
  running: "still running",
  succeeded: "succeeded",
  failed: "failed",
  cancelled: "cancelled",
};
const statusWord = (s: unknown) => RUN_STATUS[s as RunStatus] ?? (str(s) || "unknown");

// Tools with nothing to link to: past tense for the row, the bare verb for
// "Couldn't …" when the tool failed.
const GENERIC: Record<string, (a: Obj) => [past: string, verb: string]> = {
  list_projects: () => ["Looked at projects", "look at projects"],
  get_project: (a) => {
    const p = str(a.project ?? a.key);
    return p ? [`Looked at project ${p}`, `look at project ${p}`] : ["Looked at a project", "look at a project"];
  },
  list_tasks: (a) => {
    const p = str(a.project ?? a.project_key);
    return p ? [`Listed tasks in ${p}`, `list tasks in ${p}`] : ["Listed tasks", "list tasks"];
  },
  get_task: (a) => {
    const ref = taskRefArg(a);
    return ref ? [`Looked at ${ref}`, `look at ${ref}`] : ["Looked at a task", "look at a task"];
  },
  comment_on_task: (a) => {
    const ref = taskRefArg(a);
    return ref ? [`Commented on ${ref}`, `comment on ${ref}`] : ["Commented on a task", "comment on a task"];
  },
  list_machines: () => ["Checked the machines", "check the machines"],
  get_overview: () => ["Looked at the overview", "look at the overview"],
  list_runs: () => ["Listed runs", "list runs"],
};

function taskRefArg(a: Obj): string {
  const ref = str(a.ref ?? a.task_ref ?? a.task);
  if (ref) return ref;
  const id = num(a.id ?? a.task_id);
  return id !== null ? `task #${id}` : "";
}

const humanName = (name: string) => name.replace(/_/g, " ").trim() || "a tool";

const runList = (ids: unknown): string =>
  (Array.isArray(ids) ? ids : [])
    .map((id) => num(id))
    .filter((id): id is number => id !== null)
    .map((id) => `#${id}`)
    .join(", ");

/**
 * The one line (and, for delegations and commands, the card facts) a tool call
 * shows in the conversation. `result` is undefined while the call is still
 * running; `isError` marks a failed call, whose message the row shows next to it.
 */
export function describeToolCall(name: string, args: Obj, result: unknown, isError = false): ToolView {
  const pending = result === undefined;
  const r = obj(result);
  const view: ToolView = {
    kind: "generic",
    text: "",
    runId: null,
    task: null,
    prompt: "",
    permissionMode: "",
    model: "",
    modelNote: "",
  };

  switch (name) {
    case "delegate_to_claude": {
      const machine = str(r.machine) || str(args.machine);
      const where = [str(r.project) || str(args.project), str(r.repo) || str(args.repo)].filter(Boolean).join("/");
      const on = machine ? ` on ${machine}` : "";
      const tail = where ? ` · ${where}` : "";
      return {
        ...view,
        kind: "delegate",
        text: isError
          ? `Couldn't delegate to Claude Code${tail}`
          : `${pending ? "Delegating" : "Delegated"} to Claude Code${on}${tail}`,
        runId: num(r.run_id),
        prompt: str(args.prompt),
        permissionMode: str(r.permission_mode) || str(args.permission_mode),
        model: str(r.model) || str(args.model),
        modelNote: str(r.model_note),
      };
    }
    case "run_command": {
      const command = str(r.command) || str(args.command) || "a command";
      const machine = str(r.machine) || str(args.machine);
      const on = machine ? ` on ${machine}` : "";
      return {
        ...view,
        kind: "command",
        text: isError ? `Couldn't run ${command}${on}` : `${pending ? "Running" : "Ran"} ${command}${on}`,
        runId: num(r.run_id),
      };
    }
    case "get_run": {
      const run = obj(r.run);
      const id = num(run.id) ?? num(args.run_id);
      const label = id !== null ? `run #${id}` : "a run";
      if (isError) return { ...view, kind: "runs", text: `Couldn't check ${label}` };
      if (pending) return { ...view, kind: "runs", text: `Checking ${label}…` };
      return { ...view, kind: "runs", text: `Checked ${label} — ${statusWord(run.status)}` };
    }
    case "wait_for_runs": {
      const ids = runList(args.run_ids);
      const label = ids ? `${ids.includes(",") ? "runs" : "run"} ${ids}` : "runs";
      if (isError) return { ...view, kind: "runs", text: `Couldn't check ${label}` };
      if (pending) return { ...view, kind: "runs", text: `Waiting for ${label}…` };
      const runs = (Array.isArray(r.runs) ? r.runs : []).map(obj);
      const parts = runs.map((run) => `#${str(run.id)} — ${statusWord(run.status)}`);
      const text = parts.length ? `Checked ${parts.length === 1 ? "run" : "runs"} ${parts.join(", ")}` : `Checked ${label}`;
      return { ...view, kind: "runs", text: r.timed_out === true ? `${text} · stopped waiting` : text };
    }
    case "create_task":
    case "update_task": {
      const task = obj(r.task);
      const id = num(task.id);
      const ref = str(task.ref) || taskRefArg(args);
      const title = str(task.title) || str(args.title);
      const creating = name === "create_task";
      if (isError) {
        return { ...view, kind: "task", text: creating ? `Couldn't create task${title ? ` “${title}”` : ""}` : `Couldn't update ${ref || "a task"}` };
      }
      if (pending) {
        return { ...view, kind: "task", text: creating ? `Creating task${title ? ` “${title}”` : ""}…` : `Updating ${ref || "a task"}…` };
      }
      const status = !creating ? str(args.status) : "";
      return {
        ...view,
        kind: "task",
        text: `${creating ? "Created" : "Updated"} ${ref || "a task"}${title ? ` · ${title}` : ""}${status ? ` → ${status.replace(/_/g, " ")}` : ""}`,
        task: id !== null && ref ? { id, ref } : null,
      };
    }
  }

  const [past, verb] = GENERIC[name]?.(args) ?? [`Used ${humanName(name)}`, `use ${humanName(name)}`];
  return { ...view, text: isError ? `Couldn't ${verb}` : past };
}

// ── Numbers ────────────────────────────────────────────────────────────────

/** 950 / 12.3k / 1.1M */
export function formatTokens(n: number): string {
  if (!Number.isFinite(n) || n < 1000) return String(Math.max(0, Math.round(n || 0)));
  if (n < 999_950) return `${(n / 1000).toFixed(1).replace(/\.0$/, "")}k`;
  return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
}

/** "12.3k in / 1.1k out tokens" */
export function usageLabel(usage: ChatUsage | null | undefined): string {
  if (!usage) return "";
  return `${formatTokens(usage.input_tokens)} in / ${formatTokens(usage.output_tokens)} out tokens`;
}

// ── Engines and spend ──────────────────────────────────────────────────────

export const ENGINE_NAME: Record<ChatEngine, string> = { deepseek: "DeepSeek API", claude: "Claude Code" };

export const EFFORT_LABEL: Record<string, string> = {
  off: "Thinking off",
  low: "Low",
  medium: "Medium",
  high: "High",
  xhigh: "Extra high",
  max: "Max",
};

const API_NAME: Record<string, string> = { deepseek: "DeepSeek API", jev: "Jev", "claude-code": "Claude Code" };
export const apiName = (api: string) => API_NAME[api] ?? api;

/** Small amounts matter here: "$0.0021", "$0.13", "$4.20". */
export function formatSpend(usd: number | null | undefined): string {
  if (usd === null || usd === undefined) return "—";
  if (usd === 0) return "$0";
  if (usd < 0.0001) return "<$0.0001";
  if (usd < 0.01) return `$${usd.toFixed(4)}`;
  return `$${usd.toFixed(2)}`;
}

export type SpendLine = {
  key: string;
  label: string; // "DeepSeek API · deepseek-flash", "Run #12 · DeepSeek"
  detail: string; // calls and tokens, or the run's state
  cost: number | null;
  // Billed to an API account, or what a Claude subscription covered (priced at API rates).
  kind: "billed" | "subscription";
  approx: boolean; // an estimate (DeepSeek runs, Jev's pack price)
  runId: number | null;
  pending: boolean; // a run still going: its cost is not in yet
};

export type TurnSpend = { lines: SpendLine[]; billed: number; subscription: number; pending: boolean; unpriced: boolean };

function entryLine(e: UsageEntry, i: number): SpendLine {
  const calls = e.calls === 1 ? "1 call" : `${e.calls} calls`;
  const cached = e.cached_tokens ? ` + ${formatTokens(e.cached_tokens)} cached` : "";
  const out = e.api === "jev" ? "" : ` · ${formatTokens(e.output_tokens)} out`;
  return {
    key: `u${i}`,
    label: e.model ? `${apiName(e.api)} · ${e.model}` : apiName(e.api),
    detail: `${calls} · ${formatTokens(e.input_tokens)} in${cached}${out}`,
    cost: e.cost_usd,
    kind: e.api === "claude-code" ? "subscription" : "billed",
    approx: e.api === "jev",
    runId: null,
    pending: false,
  };
}

/** A turn's bill: its own API calls, then every run it queued. */
export function turnSpend(turn: ChatTurn): TurnSpend {
  const lines = turn.usage.map(entryLine);
  for (const r of turn.runs) {
    if (r.id === turn.run_id) continue; // the session itself: its tokens are the usage lines
    const running = r.status === "queued" || r.status === "running";
    const engine = r.kind === "command" ? "command" : r.engine === "deepseek" ? "DeepSeek" : "Claude";
    lines.push({
      key: `r${r.id}`,
      label: `Run #${r.id} · ${engine}${r.model ? ` ${r.model}` : ""}`,
      detail: `${r.repo_name} · ${r.status}`,
      cost: r.kind === "command" ? 0 : r.cost_usd,
      kind: r.engine === "claude" ? "subscription" : "billed",
      approx: r.engine === "deepseek",
      runId: r.id,
      pending: running,
    });
  }
  let billed = 0;
  let subscription = 0;
  let unpriced = false;
  for (const l of lines) {
    if (l.cost === null) {
      unpriced = unpriced || !l.pending;
      continue;
    }
    if (l.kind === "billed") billed += l.cost;
    else subscription += l.cost;
  }
  return { lines, billed, subscription, pending: lines.some((l) => l.pending), unpriced };
}
