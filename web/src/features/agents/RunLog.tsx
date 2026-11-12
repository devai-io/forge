// Renders a run transcript (lib/runlog's display model): what Claude says as
// markdown, and between it each stretch of tool calls folded into one quiet
// line ("● Bash ×4 · Read ×2"). Open it for a line per call; open a call for
// its command and output (terminal colours kept), or its edit as a diff.
// Plain output is a terminal block; the result is a summary card.

import clsx from "clsx";
import { Brain, ChevronRight, CircleAlert, CircleCheck, Cpu } from "lucide-react";
import { useMemo, useState } from "react";
import { hasAnsi, parseAnsi } from "@/lib/ansi";
import type { ActivityItem, DisplayItem, LogBlock } from "@/lib/runlog";
import { groupActivity, previewText, summarizeActivity, toolDiff, toolLabel } from "@/lib/runlog";
import { Markdown } from "@/components/ui/Markdown";
import { formatCost, formatDuration } from "@/lib/format";

export function RunLog({ items }: { items: DisplayItem[] }) {
  const blocks = useMemo(() => groupActivity(items), [items]);
  return (
    <div className="space-y-2.5">
      {blocks.map((block) => (
        <LogItem key={block.key} item={block} />
      ))}
    </div>
  );
}

function LogItem({ item }: { item: LogBlock }) {
  switch (item.kind) {
    case "activity":
      return <Activity items={item.items} />;
    case "init":
      return (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md border border-line bg-surface-2/50 px-3 py-2 text-[12px] text-fg-2">
          <Cpu className="size-3.5 text-fg-3" aria-hidden />
          <span>
            Session started{item.model ? <> · <span className="font-mono">{item.model}</span></> : null}
          </span>
          {item.permissionMode ? <span>mode {item.permissionMode}</span> : null}
          {item.tools ? <span>{item.tools} tools</span> : null}
          {item.cwd ? <span className="min-w-0 truncate font-mono text-fg-3">{item.cwd}</span> : null}
        </div>
      );
    case "text":
      return (
        <div className="rounded-md px-1">
          <Markdown>{item.text}</Markdown>
        </div>
      );
    case "user":
      return (
        <div className="ml-auto w-fit max-w-[85%] rounded-xl rounded-br-sm bg-accent/12 px-3 py-2 text-[13.5px] whitespace-pre-wrap">
          <span className="sr-only">You: </span>
          {item.text}
        </div>
      );
    case "thinking":
      return <Thinking text={item.text} />;
    case "tool":
      return <ToolCall item={item} />;
    case "result":
      return (
        <div
          className={clsx(
            "rounded-lg border px-3 py-2.5 text-[12.5px]",
            item.isError ? "border-critical/40 bg-critical/8" : "border-good/40 bg-good/8",
          )}
        >
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span className="inline-flex items-center gap-1.5 font-medium">
              {item.isError ? (
                <CircleAlert className="size-4 text-critical" aria-hidden />
              ) : (
                <CircleCheck className="size-4 text-good" aria-hidden />
              )}
              {item.isError ? "Finished with an error" : "Finished"}
              {item.subtype && item.subtype !== "success" ? <span className="font-mono text-fg-3">({item.subtype})</span> : null}
            </span>
            <span className="tabular text-fg-2">{formatCost(item.costUsd)}</span>
            {item.turns !== null ? <span className="tabular text-fg-2">{item.turns} turns</span> : null}
            {item.durationMs !== null ? <span className="tabular text-fg-2">{formatDuration(item.durationMs)}</span> : null}
          </div>
        </div>
      );
    case "note":
      return <p className="px-1 font-mono text-[11.5px] text-fg-3">{item.text}</p>;
    case "log":
      return (
        <pre className="overflow-x-auto rounded-md border border-line bg-[#0b0b0c] px-3 py-2 font-mono text-[12px] leading-relaxed text-[#e6e6e3]">
          {item.lines.map((l) => (
            <div
              key={l.seq}
              className={clsx(l.stream === "stderr" && "text-[#f08a8a]", l.stream === "system" && "text-[#8fb4e8] italic")}
            >
              {l.text || " "}
            </div>
          ))}
        </pre>
      );
  }
}

function Thinking({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="inline-flex items-center gap-1.5 px-1 text-[12px] text-fg-3 italic hover:text-fg-2"
      >
        <Brain className="size-3" aria-hidden /> Thinking
        <ChevronRight className={clsx("size-3 transition-transform", open && "rotate-90")} aria-hidden />
      </button>
      {open ? <p className="mt-1 ml-1.5 border-l-2 border-line pl-3 text-[12.5px] whitespace-pre-wrap text-fg-2 italic">{text}</p> : null}
    </div>
  );
}

/** Claude Code's ⏺: grey while it runs, green when done, red on an error. */
function Dot({ state }: { state: "pending" | "error" | "done" }) {
  return (
    <span
      aria-hidden
      className={clsx(
        "inline-block size-1.5 shrink-0 rounded-full",
        state === "pending" ? "animate-pulse-soft bg-fg-3" : state === "error" ? "bg-critical" : "bg-good",
      )}
    />
  );
}

const toolState = (t: Extract<DisplayItem, { kind: "tool" }>) => (!t.result ? "pending" : t.result.isError ? "error" : "done");

/** A stretch of tool calls: one quiet line until opened. */
function Activity({ items }: { items: ActivityItem[] }) {
  const [open, setOpen] = useState(false);
  const sum = summarizeActivity(items);
  // A lone call needs no group around it: its own line opens its details.
  if (items.length === 1 && items[0].kind === "tool") return <ToolCall item={items[0]} />;
  const state = sum.pending ? "pending" : sum.errors ? "error" : "done";
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full min-w-0 items-center gap-2 rounded-md px-1 py-0.5 text-left text-[12px] text-fg-3 hover:bg-surface-2/60 hover:text-fg-2"
      >
        <Dot state={state} />
        <span className="shrink-0">{sum.label}</span>
        {sum.pending && sum.last ? (
          <span className="min-w-0 truncate font-mono text-[11.5px]">
            · {toolLabel(sum.last.name)} {sum.last.summary}
          </span>
        ) : null}
        {sum.errors ? (
          <span className="shrink-0 text-critical-ink">
            · {sum.errors} error{sum.errors === 1 ? "" : "s"}
          </span>
        ) : null}
        <ChevronRight className={clsx("size-3 shrink-0 transition-transform", open && "rotate-90")} aria-hidden />
      </button>
      {open ? (
        <div className="mt-1 ml-[7px] space-y-0.5 border-l border-line pl-3">
          {items.map((i) =>
            i.kind === "tool" ? (
              <ToolCall key={i.key} item={i} />
            ) : i.kind === "thinking" ? (
              <Thinking key={i.key} text={i.text} />
            ) : (
              <p key={i.key} className="px-1 font-mono text-[11px] text-fg-3">
                {i.text}
              </p>
            ),
          )}
        </div>
      ) : null}
    </div>
  );
}

/** One call: a quiet line ("● Bash  go test ./..."); opened, what it did. */
function ToolCall({ item }: { item: Extract<DisplayItem, { kind: "tool" }> }) {
  const [open, setOpen] = useState(false);
  const state = toolState(item);
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full min-w-0 items-center gap-2 rounded-md px-1 py-0.5 text-left text-[12px] text-fg-3 hover:bg-surface-2/60 hover:text-fg-2"
      >
        <Dot state={state} />
        <span className="shrink-0 font-medium text-fg-2">{toolLabel(item.name)}</span>
        {item.summary ? <span className="min-w-0 truncate font-mono text-[11.5px]">{item.summary}</span> : null}
        {state === "error" ? (
          <span className="ml-auto inline-flex shrink-0 items-center gap-1 text-[11px] text-critical-ink">
            <CircleAlert className="size-3" aria-hidden /> error
          </span>
        ) : null}
      </button>
      {open ? <ToolDetail item={item} /> : null}
    </div>
  );
}

const TERMINAL = "overflow-auto rounded-md bg-[#0b0b0c] px-3 py-2 font-mono text-[11.5px] leading-relaxed whitespace-pre-wrap text-[#e6e6e3]";

function ToolDetail({ item }: { item: Extract<DisplayItem, { kind: "tool" }> }) {
  const input = (item.input && typeof item.input === "object" ? item.input : {}) as Record<string, unknown>;
  const diff = toolDiff(item.name, item.input);
  const result = item.result;
  return (
    <div className="mt-1 mb-2 ml-3.5 space-y-1.5">
      {item.name === "Bash" && typeof input.command === "string" ? (
        <pre className={clsx(TERMINAL, "max-h-48")}>
          <span className="text-[#9ece6a] select-none">$ </span>
          {input.command}
        </pre>
      ) : diff ? (
        <DiffView file={diff.file} lines={diff.lines} />
      ) : item.name === "TodoWrite" && Array.isArray(input.todos) ? (
        <ul className="space-y-0.5 px-1 text-[12.5px]">
          {(input.todos as { content?: string; status?: string }[]).map((t, n) => (
            <li key={n} className={clsx("flex gap-2", t.status === "completed" ? "text-fg-3 line-through" : "text-fg-2")}>
              <span aria-hidden>{t.status === "completed" ? "☑" : t.status === "in_progress" ? "◐" : "☐"}</span>
              {t.content}
            </li>
          ))}
        </ul>
      ) : item.input !== null && item.input !== undefined ? (
        <pre className="max-h-48 overflow-auto rounded-md bg-surface-2 px-2.5 py-1.5 font-mono text-[11.5px] whitespace-pre-wrap text-fg-2">
          {JSON.stringify(item.input, null, 2)}
        </pre>
      ) : null}
      {result && (!diff || result.isError) ? <ToolOutput text={result.text} isError={result.isError} /> : null}
    </div>
  );
}

function ToolOutput({ text, isError }: { text: string; isError: boolean }) {
  const [full, setFull] = useState(false);
  const preview = previewText(text);
  if (!preview.text.trim()) return <p className="px-1 text-[11.5px] text-fg-3 italic">(no output)</p>;
  return (
    <div>
      <pre className={clsx(TERMINAL, "max-h-96", isError && "ring-1 ring-critical/50")}>
        <AnsiText text={full ? text : preview.text} />
        {preview.truncated && !full ? "…" : ""}
      </pre>
      {preview.truncated ? (
        <button type="button" onClick={() => setFull((f) => !f)} className="mt-1 text-[11.5px] text-accent hover:underline">
          {full ? "Show less" : `Show all (${text.length.toLocaleString()} chars)`}
        </button>
      ) : null}
    </div>
  );
}

function DiffView({ file, lines }: { file: string; lines: { sign: "-" | "+" | " "; text: string }[] }) {
  const [full, setFull] = useState(false);
  const MAX = 40;
  const shown = full ? lines : lines.slice(0, MAX);
  return (
    <div className="overflow-hidden rounded-md border border-line">
      {file ? <div className="truncate border-b border-line bg-surface-2 px-2.5 py-1 font-mono text-[11px] text-fg-3">{file}</div> : null}
      <pre className="max-h-96 overflow-auto font-mono text-[11.5px] leading-relaxed">
        {shown.map((l, n) => (
          <div
            key={n}
            className={clsx(
              "px-2.5 whitespace-pre-wrap",
              l.sign === "-" && "bg-critical/10 text-critical-ink",
              l.sign === "+" && "bg-good/10 text-good-ink",
              l.sign === " " && "text-fg-3",
            )}
          >
            <span className="select-none">{l.sign === " " ? " " : l.sign} </span>
            {l.text || " "}
          </div>
        ))}
      </pre>
      {lines.length > MAX ? (
        <button type="button" onClick={() => setFull((f) => !f)} className="w-full border-t border-line px-2.5 py-1 text-left text-[11.5px] text-accent hover:underline">
          {full ? "Show less" : `Show all ${lines.length} lines`}
        </button>
      ) : null}
    </div>
  );
}

/** Text with terminal colours (ANSI SGR) as styled spans. */
export function AnsiText({ text }: { text: string }) {
  if (!hasAnsi(text)) return <>{text}</>;
  return (
    <>
      {parseAnsi(text).map((seg, n) => (
        <span
          key={n}
          style={{
            color: seg.style.fg,
            backgroundColor: seg.style.bg,
            fontWeight: seg.style.bold ? 600 : undefined,
            opacity: seg.style.dim ? 0.65 : undefined,
            fontStyle: seg.style.italic ? "italic" : undefined,
            textDecoration: seg.style.underline ? "underline" : undefined,
          }}
        >
          {seg.text}
        </span>
      ))}
    </>
  );
}
