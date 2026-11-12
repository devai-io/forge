// Run transcript → display model.
//
// The runner forwards `claude -p --output-format stream-json` output one JSON
// line per event (kind "claude"), plus plain stdout/stderr/system lines. This
// module turns that stream into a flat list of things worth drawing: assistant
// text, tool calls (with their results attached), the init banner and the final
// result card. It is pure and tested; the RunLog component only renders.

/* eslint-disable @typescript-eslint/no-explicit-any */

import type { RunEvent } from "@/api/types";

export type LogStream = "stdout" | "stderr" | "system";

export type DisplayItem =
  | { kind: "init"; key: string; model: string; cwd: string; sessionId: string; permissionMode: string; tools: number }
  | { kind: "text"; key: string; text: string }
  // What the user said in an interactive session (Claude Code replays it).
  | { kind: "user"; key: string; text: string }
  | { kind: "thinking"; key: string; text: string }
  | {
      kind: "tool";
      key: string;
      id: string;
      name: string;
      summary: string;
      input: unknown;
      result: { text: string; isError: boolean } | null;
    }
  | {
      kind: "result";
      key: string;
      isError: boolean;
      subtype: string;
      text: string;
      costUsd: number | null;
      turns: number | null;
      durationMs: number | null;
    }
  | { kind: "note"; key: string; text: string }
  | { kind: "log"; key: string; lines: { stream: LogStream; text: string; seq: number }[] };

export const RESULT_PREVIEW_CHARS = 600;

// System events Claude Code reports that say nothing to a reader of the run
// (token counts, hook bookkeeping, permission polling): left out of the transcript.
const QUIET_SYSTEM = new Set(["thinking_tokens", "hook_started", "hook_response", "permission_check_status"]);

const str = (v: unknown): string => (typeof v === "string" ? v : "");

function truncateMiddle(text: string, max: number): string {
  if (text.length <= max) return text;
  return `${text.slice(0, max - 1)}…`;
}

/** The one argument that tells you what a tool call is doing. */
export function toolSummary(name: string, input: unknown): string {
  const i = (input && typeof input === "object" ? input : {}) as Record<string, unknown>;
  const pick = (...fields: string[]) => {
    for (const f of fields) if (typeof i[f] === "string" && i[f]) return i[f] as string;
    return "";
  };
  let s: string;
  switch (name) {
    case "Bash":
      s = pick("command", "description");
      break;
    case "Read":
    case "Write":
    case "Edit":
    case "MultiEdit":
    case "NotebookEdit":
      s = pick("file_path", "notebook_path", "path");
      break;
    case "Grep":
    case "Glob":
      s = [pick("pattern"), pick("path")].filter(Boolean).join(" in ");
      break;
    case "WebFetch":
      s = pick("url");
      break;
    case "WebSearch":
      s = pick("query");
      break;
    case "Task":
    case "Agent":
      s = pick("description", "prompt");
      break;
    case "TodoWrite": {
      const todos = Array.isArray(i.todos) ? i.todos.length : 0;
      s = `${todos} todo${todos === 1 ? "" : "s"}`;
      break;
    }
    default:
      s = Object.values(i).find((v): v is string => typeof v === "string" && v.length > 0) ?? "";
  }
  return truncateMiddle(s.replace(/\s+/g, " ").trim(), 140);
}

/** tool_result content is a string or a list of {type:"text"} blocks. */
export function toolResultText(content: unknown): string {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((b) => {
        if (b && typeof b === "object") {
          if ((b as any).type === "text") return str((b as any).text);
          if ((b as any).type === "image") return "[image]";
        }
        return typeof b === "string" ? b : "";
      })
      .filter(Boolean)
      .join("\n");
  }
  if (content === null || content === undefined) return "";
  return JSON.stringify(content);
}

/**
 * Map raw events to display items. Consecutive plain log lines are merged into
 * one block; tool results are attached to the tool call they answer, so the
 * transcript reads "Bash · npm test → output" instead of two disconnected rows.
 */
export function mapRunEvents(events: RunEvent[]): DisplayItem[] {
  const items: DisplayItem[] = [];
  const tools = new Map<string, Extract<DisplayItem, { kind: "tool" }>>();
  const codexTexts = new Map<string, Extract<DisplayItem, { kind: "text" }>>();

  const pushLog = (stream: LogStream, text: string, seq: number) => {
    const last = items[items.length - 1];
    if (last && last.kind === "log") last.lines.push({ stream, text, seq });
    else items.push({ kind: "log", key: `log-${seq}`, lines: [{ stream, text, seq }] });
  };

  for (const ev of events) {
    if (ev.kind === "codex" && ev.data?.method) {
      const { method, params: p = {} } = ev.data;
      if (method === "item/agentMessage/delta") {
        const id = str(p.itemId);
        let item = codexTexts.get(id);
        if (!item) { item = { kind: "text", key: `codex-${ev.seq}`, text: "" }; codexTexts.set(id, item); items.push(item); }
        item.text += str(p.delta);
      } else if (method === "item/started" || method === "item/completed") {
        const i = p.item ?? {};
        const id = str(i.id);
        if (i.type === "agentMessage" && method === "item/completed") {
          const existing = codexTexts.get(id);
          if (existing) existing.text = str(i.text);
          else { const item: Extract<DisplayItem, { kind: "text" }> = { kind: "text", key: `codex-${ev.seq}`, text: str(i.text) }; codexTexts.set(id, item); items.push(item); }
        } else if (["commandExecution", "fileChange", "mcpToolCall", "webSearch"].includes(i.type)) {
          let tool = tools.get(id);
          if (!tool) {
            const name = i.type === "commandExecution" ? "Bash" : i.type === "mcpToolCall" ? `${i.server} · ${i.tool}` : i.type;
            const input = i.type === "commandExecution" ? { command: i.command } : i.arguments ?? i.changes ?? i;
            tool = { kind: "tool", key: `codex-tool-${ev.seq}`, id, name, input, summary: toolSummary(name, input), result: null };
            tools.set(id, tool); items.push(tool);
          }
          if (method === "item/completed") tool.result = { text: toolResultText(i.aggregatedOutput ?? i.result ?? i.changes ?? i.status), isError: i.status === "failed" || i.status === "declined" || !!i.error };
        }
      } else if (method === "error") {
        items.push({ kind: "note", key: `codex-error-${ev.seq}`, text: str(p.error?.message) });
      }
      continue;
    }
    if (ev.kind !== "claude" && ev.kind !== "codex") {
      const text = ev.data && typeof ev.data === "object" ? str(ev.data.text) : str(ev.data);
      pushLog(ev.kind, text, ev.seq);
      continue;
    }
    const d = ev.data;
    if (!d || typeof d !== "object") continue;
    const type = str(d.type);

    if (type === "system") {
      if (d.subtype === "init") {
        items.push({
          kind: "init",
          key: `init-${ev.seq}`,
          model: str(d.model),
          cwd: str(d.cwd),
          sessionId: str(d.session_id),
          permissionMode: str(d.permissionMode ?? d.permission_mode),
          tools: Array.isArray(d.tools) ? d.tools.length : 0,
        });
      } else if (d.subtype && !QUIET_SYSTEM.has(str(d.subtype))) {
        items.push({ kind: "note", key: `note-${ev.seq}`, text: `system: ${str(d.subtype)}` });
      }
      continue;
    }

    if (type === "user" && d.isReplay) {
      const content = d.message?.content;
      const text =
        typeof content === "string"
          ? content
          : Array.isArray(content)
            ? content.map((b: any) => (b?.type === "text" ? str(b.text) : "")).filter(Boolean).join("\n")
            : "";
      if (text.trim()) items.push({ kind: "user", key: `user-${ev.seq}`, text });
      continue;
    }

    if (type === "assistant" || type === "user") {
      const content = d.message?.content;
      if (typeof content === "string") {
        if (type === "assistant" && content.trim()) items.push({ kind: "text", key: `text-${ev.seq}`, text: content });
        continue;
      }
      if (!Array.isArray(content)) continue;
      content.forEach((block: any, i: number) => {
        const key = `${ev.seq}-${i}`;
        if (!block || typeof block !== "object") return;
        if (block.type === "text" && type === "assistant") {
          if (str(block.text).trim()) items.push({ kind: "text", key: `text-${key}`, text: str(block.text) });
        } else if (block.type === "thinking") {
          if (str(block.thinking).trim()) items.push({ kind: "thinking", key: `think-${key}`, text: str(block.thinking) });
        } else if (block.type === "tool_use") {
          const item: Extract<DisplayItem, { kind: "tool" }> = {
            kind: "tool",
            key: `tool-${key}`,
            id: str(block.id),
            name: str(block.name) || "tool",
            summary: toolSummary(str(block.name), block.input),
            input: block.input,
            result: null,
          };
          if (item.id) tools.set(item.id, item);
          items.push(item);
        } else if (block.type === "tool_result") {
          const text = toolResultText(block.content);
          const owner = tools.get(str(block.tool_use_id));
          if (owner) owner.result = { text, isError: !!block.is_error };
          else
            items.push({
              kind: "tool",
              key: `orphan-${key}`,
              id: str(block.tool_use_id),
              name: "result",
              summary: truncateMiddle(text.split("\n")[0] ?? "", 140),
              input: null,
              result: { text, isError: !!block.is_error },
            });
        }
      });
      continue;
    }

    if (type === "result") {
      items.push({
        kind: "result",
        key: `result-${ev.seq}`,
        isError: !!d.is_error,
        subtype: str(d.subtype),
        text: str(d.result),
        costUsd: typeof d.total_cost_usd === "number" ? d.total_cost_usd : typeof d.cost_usd === "number" ? d.cost_usd : null,
        turns: typeof d.num_turns === "number" ? d.num_turns : null,
        durationMs: typeof d.duration_ms === "number" ? d.duration_ms : null,
      });
      continue;
    }
    // stream_event partials and anything newer are ignored rather than dumped:
    // the complete assistant message that follows carries the same content.
  }
  return items;
}

export function previewText(text: string, max = RESULT_PREVIEW_CHARS): { text: string; truncated: boolean } {
  if (text.length <= max) return { text, truncated: false };
  return { text: text.slice(0, max), truncated: true };
}

/**
 * The transcript without its last assistant message when that message is the
 * run's result: the run page shows the result on its own above.
 */
export function withoutEchoedResult(items: DisplayItem[], result: string): DisplayItem[] {
  const r = result.trim();
  if (!r) return items;
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (it.kind === "text") return it.text.trim() === r ? [...items.slice(0, i), ...items.slice(i + 1)] : items;
    if (it.kind !== "result" && it.kind !== "note" && it.kind !== "log") return items;
  }
  return items;
}

export const toolCallCount = (items: DisplayItem[]) => items.filter((i) => i.kind === "tool").length;

// ── Activity: the tool calls between what Claude says ─────────────────────

export type ActivityItem = Extract<DisplayItem, { kind: "tool" | "thinking" | "note" }>;
export type LogBlock = Exclude<DisplayItem, ActivityItem> | ActivityItem | { kind: "activity"; key: string; items: ActivityItem[] };

const isActivity = (i: DisplayItem): i is ActivityItem => i.kind === "tool" || i.kind === "thinking" || i.kind === "note";

/**
 * Folds each stretch of tool calls (with the thinking and notes among them)
 * into one block, so a transcript reads as what Claude said, with "Bash ×4 ·
 * Read ×2" between. A stretch without a tool call stays as it is.
 */
export function groupActivity(items: DisplayItem[]): LogBlock[] {
  const out: LogBlock[] = [];
  let run: ActivityItem[] = [];
  const flush = () => {
    if (!run.length) return;
    if (run.some((i) => i.kind === "tool")) out.push({ kind: "activity", key: `act-${run[0].key}`, items: run });
    else out.push(...run);
    run = [];
  };
  for (const item of items) {
    if (isActivity(item)) run.push(item);
    else {
      flush();
      out.push(item);
    }
  }
  flush();
  return out;
}

export type ActivitySummary = {
  tools: number;
  /** "Bash ×4 · Read ×2 · Edit", in the order they were first used. */
  label: string;
  errors: number;
  /** A call without its result yet: Claude is working. */
  pending: boolean;
  /** The latest call, for "what is it doing now". */
  last: Extract<DisplayItem, { kind: "tool" }> | null;
};

export function summarizeActivity(items: ActivityItem[]): ActivitySummary {
  const counts = new Map<string, number>();
  let errors = 0;
  let pending = false;
  let last: Extract<DisplayItem, { kind: "tool" }> | null = null;
  for (const i of items) {
    if (i.kind !== "tool") continue;
    const name = toolLabel(i.name);
    counts.set(name, (counts.get(name) ?? 0) + 1);
    if (i.result?.isError) errors++;
    if (!i.result) pending = true;
    last = i;
  }
  const label = [...counts].map(([n, c]) => (c > 1 ? `${n} ×${c}` : n)).join(" · ");
  const tools = [...counts.values()].reduce((a, b) => a + b, 0);
  return { tools, label, errors, pending, last };
}

/** A tool's name as a reader wants it: Forge's own MCP tools without the prefix. */
export function toolLabel(name: string): string {
  const m = /^mcp__([^_]+(?:_[^_]+)*)__(.+)$/.exec(name);
  return m ? `${m[1]}·${m[2]}` : name;
}

/** An Edit / MultiEdit / Write call as diff lines (removed, added). */
export function toolDiff(name: string, input: unknown): { file: string; lines: { sign: "-" | "+" | " "; text: string }[] } | null {
  const i = (input && typeof input === "object" ? input : {}) as Record<string, any>;
  const file = str(i.file_path ?? i.path ?? i.notebook_path);
  const edits: { old: string; new: string }[] =
    name === "Edit"
      ? [{ old: str(i.old_string), new: str(i.new_string) }]
      : name === "MultiEdit" && Array.isArray(i.edits)
        ? i.edits.map((e: any) => ({ old: str(e?.old_string), new: str(e?.new_string) }))
        : name === "Write"
          ? [{ old: "", new: str(i.content) }]
          : [];
  if (!edits.length) return null;
  const lines: { sign: "-" | "+" | " "; text: string }[] = [];
  edits.forEach((e, n) => {
    if (n > 0) lines.push({ sign: " ", text: "⋯" });
    if (e.old) for (const l of e.old.split("\n")) lines.push({ sign: "-", text: l });
    if (e.new) for (const l of e.new.split("\n")) lines.push({ sign: "+", text: l });
  });
  return { file, lines };
}
