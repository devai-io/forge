// What the assistant did, between its messages: delegations and commands as
// small cards with the run's live status, everything else as one muted line.
// lib/assistant decides the wording; this file only lays it out.

import clsx from "clsx";
import { ChevronRight, CircleAlert, CircleCheck, ListChecks, SquareCheck } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { useRun } from "@/api/hooks";
import type { ChatMessage, ChatToolCall } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Spinner } from "@/components/ui/Spinner";
import { RunKindIcon, RunStatusBadge } from "@/features/agents/RunBits";
import { useShell } from "@/features/shell/context";
import { describeToolCall, type ToolView } from "@/lib/assistant";

export function ToolActivity({ call, result }: { call: ChatToolCall; result: ChatMessage | null }) {
  const name = call.name || result?.tool_name || "";
  const isError = !!result?.is_error;
  // No tool message yet = still running; a tool message without a result is a finished call.
  const view = describeToolCall(name, call.arguments ?? {}, result ? (result.result ?? null) : undefined, isError);
  const error = isError ? result?.content || "The tool failed." : "";

  if (view.kind === "delegate" || view.kind === "command") {
    return <RunCard view={view} pending={!result} error={error} />;
  }
  return <ToolLine view={view} call={call} result={result} error={error} />;
}

function StateIcon({ pending, error }: { pending: boolean; error: string }) {
  if (pending) return <Spinner className="mt-0.5 size-3.5 shrink-0" />;
  if (error) return <CircleAlert className="mt-0.5 size-3.5 shrink-0 text-critical" aria-label="Failed" role="img" />;
  return null;
}

function RunCard({ view, pending, error }: { view: ToolView; pending: boolean; error: string }) {
  const run = useRun(view.runId ?? 0, { live: true });
  return (
    <div className="rounded-lg border border-line bg-surface px-3 py-2.5 text-[12.5px]">
      <div className="flex min-w-0 items-start gap-2">
        <RunKindIcon kind={view.kind === "delegate" ? "agent" : "command"} className="mt-0.5 size-4" />
        <div className="min-w-0 flex-1">
          <p className="font-medium break-words text-fg">{view.text}</p>
          {view.permissionMode || view.model ? (
            <div className="mt-1 flex flex-wrap gap-1.5">
              {view.permissionMode ? <Badge tone="outline">{view.permissionMode}</Badge> : null}
              {view.model ? (
                <Badge tone="outline" title={view.modelNote || undefined}>
                  {view.model}
                </Badge>
              ) : null}
            </div>
          ) : null}
        </div>
        {view.runId !== null ? (
          run.data ? (
            <RunStatusBadge run={run.data} />
          ) : (
            <Spinner className="size-3.5" />
          )
        ) : (
          <StateIcon pending={pending} error={error} />
        )}
      </div>
      {error ? <p className="mt-1.5 pl-6 break-words text-fg-2">{error}</p> : null}
      {view.prompt ? (
        <details className="mt-1.5 pl-6">
          <summary className="cursor-pointer text-fg-3 hover:text-fg-2">Prompt</summary>
          <pre className="mt-1 max-h-64 overflow-auto rounded bg-surface-2 px-2 py-1.5 font-mono text-[11.5px] whitespace-pre-wrap text-fg-2">
            {view.prompt}
          </pre>
        </details>
      ) : null}
      {view.runId !== null ? (
        <Link to={`/agents/runs/${view.runId}`} className="mt-1.5 ml-6 inline-block text-accent hover:underline">
          Run #{view.runId}
        </Link>
      ) : null}
    </div>
  );
}

function ToolLine({
  view,
  call,
  result,
  error,
}: {
  view: ToolView;
  call: ChatToolCall;
  result: ChatMessage | null;
  error: string;
}) {
  const { openTask } = useShell();
  const [open, setOpen] = useState(false);
  const pending = !result;
  // The task ref inside the line opens the task drawer.
  const task = view.task;
  const refAt = task ? view.text.indexOf(task.ref) : -1;
  const icon = pending || error ? (
    <StateIcon pending={pending} error={error} />
  ) : view.kind === "task" ? (
    <SquareCheck className="mt-0.5 size-3.5 shrink-0 text-fg-3" aria-hidden />
  ) : view.kind === "runs" ? (
    <ListChecks className="mt-0.5 size-3.5 shrink-0 text-fg-3" aria-hidden />
  ) : (
    <CircleCheck className="mt-0.5 size-3.5 shrink-0 text-fg-3" aria-hidden />
  );

  return (
    <div className="flex min-w-0 items-start gap-2 px-1 text-[12.5px] text-fg-3">
      {icon}
      <div className="min-w-0 flex-1">
        {view.kind === "generic" ? (
          <button
            type="button"
            onClick={() => setOpen((o) => !o)}
            aria-expanded={open}
            className="inline-flex max-w-full items-center gap-1 text-left hover:text-fg-2"
          >
            <span className="break-words">{view.text}</span>
            <ChevronRight className={clsx("size-3 shrink-0 transition-transform", open && "rotate-90")} aria-hidden />
          </button>
        ) : task && refAt >= 0 ? (
          <span className="break-words">
            {view.text.slice(0, refAt)}
            <button type="button" className="font-mono text-fg-2 hover:text-accent" onClick={() => openTask(task.id)}>
              {task.ref}
            </button>
            {view.text.slice(refAt + task.ref.length)}
          </span>
        ) : (
          <span className="break-words">{view.text}</span>
        )}
        {error ? <p className="mt-0.5 break-words text-fg-2">{error}</p> : null}
        {open ? (
          <pre className="mt-1 max-h-64 overflow-auto rounded bg-surface-2 px-2 py-1.5 font-mono text-[11.5px] whitespace-pre-wrap text-fg-2">
            {JSON.stringify({ arguments: call.arguments ?? {}, result: result?.result ?? null }, null, 2)}
          </pre>
        ) : null}
      </div>
    </div>
  );
}
