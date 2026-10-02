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

  const pushLog = (stream: LogStream, text: string, seq: number) => {
    const last = items[items.length - 1];
    if (last && last.kind === "log") last.lines.push({ stream, text, seq });
    else items.push({ kind: "log", key: `log-${seq}`, lines: [{ stream, text, seq }] });
  };

  for (const ev of events) {
    if (ev.kind !== "claude") {
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
      } else if (d.subtype) {
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
