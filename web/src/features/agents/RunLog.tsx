// Renders a run transcript (lib/runlog's display model): assistant prose as
// markdown, tool calls as one-line collapsibles with their results tucked
// inside, plain output as a terminal block, and the result as a summary card.

import clsx from "clsx";
import { Brain, ChevronRight, CircleAlert, CircleCheck, Cpu, TerminalSquare, Wrench } from "lucide-react";
import { useState } from "react";
import type { DisplayItem } from "@/lib/runlog";
import { previewText } from "@/lib/runlog";
import { Markdown } from "@/components/ui/Markdown";
import { formatCost, formatDuration } from "@/lib/format";

export function RunLog({ items }: { items: DisplayItem[] }) {
  return (
    <div className="space-y-2.5">
      {items.map((item) => (
        <LogItem key={item.key} item={item} />
      ))}
    </div>
  );
}

function LogItem({ item }: { item: DisplayItem }) {
  switch (item.kind) {
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
    <div className="px-1">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="inline-flex items-center gap-1 text-[12px] text-fg-3 hover:text-fg-2"
      >
        <Brain className="size-3.5" aria-hidden /> Thinking
        <ChevronRight className={clsx("size-3 transition-transform", open && "rotate-90")} aria-hidden />
      </button>
      {open ? <p className="mt-1 border-l-2 border-line pl-3 text-[12.5px] whitespace-pre-wrap text-fg-2">{text}</p> : null}
    </div>
  );
}

function ToolCall({ item }: { item: Extract<DisplayItem, { kind: "tool" }> }) {
  const [open, setOpen] = useState(false);
  const [full, setFull] = useState(false);
  const result = item.result;
  const preview = result ? previewText(result.text) : null;
  return (
    <div className={clsx("rounded-md border", result?.isError ? "border-critical/35" : "border-line")}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full min-w-0 items-center gap-2 px-2.5 py-1.5 text-left text-[12.5px] hover:bg-surface-2/60"
      >
        <ChevronRight className={clsx("size-3 shrink-0 text-fg-3 transition-transform", open && "rotate-90")} aria-hidden />
        {item.name === "Bash" ? (
          <TerminalSquare className="size-3.5 shrink-0 text-fg-3" aria-hidden />
        ) : (
          <Wrench className="size-3.5 shrink-0 text-fg-3" aria-hidden />
        )}
        <span className="shrink-0 font-medium">{item.name}</span>
        {item.summary ? <span className="min-w-0 truncate font-mono text-[11.5px] text-fg-2">{item.summary}</span> : null}
        <span className="ml-auto shrink-0 text-[11px]">
          {result ? (
            result.isError ? (
              <span className="inline-flex items-center gap-1 text-critical-ink">
                <CircleAlert className="size-3" aria-hidden /> error
              </span>
            ) : (
              <span className="text-fg-3">done</span>
            )
          ) : (
            <span className="animate-pulse-soft text-fg-3">…</span>
          )}
        </span>
      </button>
      {open ? (
        <div className="space-y-2 border-t border-line px-2.5 py-2">
          {item.input !== null && item.input !== undefined ? (
            <pre className="max-h-64 overflow-auto rounded bg-surface-2 px-2 py-1.5 font-mono text-[11.5px] whitespace-pre-wrap text-fg-2">
              {JSON.stringify(item.input, null, 2)}
            </pre>
          ) : null}
          {result && preview ? (
            <div>
              <pre className="max-h-96 overflow-auto rounded bg-surface-2 px-2 py-1.5 font-mono text-[11.5px] whitespace-pre-wrap">
                {full ? result.text : preview.text}
                {preview.truncated && !full ? "…" : ""}
              </pre>
              {preview.truncated ? (
                <button type="button" onClick={() => setFull((f) => !f)} className="mt-1 text-[11.5px] text-accent hover:underline">
                  {full ? "Show less" : `Show all (${result.text.length.toLocaleString()} chars)`}
                </button>
              ) : null}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
