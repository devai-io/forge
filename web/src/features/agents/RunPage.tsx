// One run, live. Polls the transcript every 1.5 s while the run is queued or
// running (every 5 s while it waits for you), follows the bottom unless the
// reader has scrolled up, and puts the latest answer at the top.
//
// An agent run you started stays open after it answers — "Waiting for you" —
// until you Finish it: reply in the composer and the same run picks its
// session back up, or open the session in Claude Code on its machine.

import { ArrowDown, ArrowLeft, Ban, CircleCheck, CornerDownRight, Copy, SquareTerminal } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ApiError } from "@/api/client";
import { isActiveRun, isOpenRun, useCancelRun, useFinishRun, useHandoffRun, useRun, useRunEvents } from "@/api/hooks";
import type { Run, RunEvent, RunPrompt } from "@/api/types";
import { ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Markdown } from "@/components/ui/Markdown";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { useNow } from "@/lib/now";
import { Skeleton, SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { useShell } from "@/features/shell/context";
import { formatCost, formatDuration } from "@/lib/format";
import { mapRunEvents } from "@/lib/runlog";
import { ENGINE_LABEL, runTitle } from "@/lib/agents";
import { RunKindIcon, RunStatusBadge } from "./RunBits";
import { RunInteraction } from "./RunInteraction";
import { RunLog } from "./RunLog";

export function RunPage() {
  const id = Number(useParams().id);
  const run = useRun(id);
  const events = useRunEvents(id);
  const current = events.data?.run ?? run.data;

  if (!Number.isInteger(id) || id <= 0) return <EmptyState title="Run not found" />;
  if (run.error instanceof ApiError && run.error.status === 404) return <EmptyState title="Run not found" />;
  if (run.error && !current) return <ErrorState error={run.error} onRetry={() => run.refetch()} />;
  if (!current) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-10 w-2/3" />
        <SkeletonRows rows={8} />
      </div>
    );
  }
  return (
    <RunView run={current} events={events.data?.events} prompts={events.data?.prompts ?? []} loadingEvents={events.isPending} />
  );
}

function RunView({
  run,
  events,
  prompts,
  loadingEvents,
}: {
  run: Run;
  events: RunEvent[] | undefined;
  prompts: RunPrompt[];
  loadingEvents: boolean;
}) {
  const cancel = useCancelRun();
  const finish = useFinishRun();
  const handoff = useHandoffRun();
  const navigate = useNavigate();
  const toast = useToast();
  const { newRun, openTask } = useShell();
  const open = isOpenRun(run);
  const waiting = run.status === "waiting";
  // The run can be taken into Claude Code once it has a session and is not mid-work.
  const canHandoff = run.kind === "agent" && !!run.session_id && !run.handoff && run.status !== "queued" &&
    !(run.status === "running" && !run.interactive);
  const openInClaude = () =>
    handoff.mutate(run.id, {
      onSuccess: ({ runner_id, session }) => {
        toast.success(`Opened in Claude Code on ${run.runner_name} (tmux ${session})`);
        navigate(`/terminal/${runner_id}/${session}`);
      },
      onError: (e) => toast.error(e),
    });
  const items = useMemo(() => mapRunEvents(events ?? []), [events]);
  const bottom = useRef<HTMLDivElement>(null);
  const [follow, setFollow] = useState(true);
  const active = isActiveRun(run);
  const now = useNow();

  // Follow the tail while the reader is at the bottom; stop once they scroll up.
  useEffect(() => {
    const onScroll = () => {
      const atBottom = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 80;
      setFollow(atBottom);
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);
  const pendingPrompts = prompts.filter((p) => p.status === "pending").length;
  useEffect(() => {
    if (follow && active) bottom.current?.scrollIntoView({ block: "end" });
  }, [items.length, pendingPrompts, follow, active]);

  const duration =
    run.duration_ms ??
    (run.started_at ? (run.finished_at ? new Date(run.finished_at).getTime() : now) - new Date(run.started_at).getTime() : null);

  return (
    <div className="mx-auto max-w-4xl">
      <Link to="/agents" className="mb-3 inline-flex items-center gap-1 text-[12.5px] text-fg-3 hover:text-fg">
        <ArrowLeft className="size-3.5" aria-hidden /> Agents
      </Link>

      <header className="mb-4 space-y-3">
        <div className="flex flex-wrap items-start gap-3">
          <RunKindIcon kind={run.kind} className="mt-1 size-5" />
          <div className="min-w-0 flex-1">
            <h1 className="text-lg leading-snug font-semibold break-words">{runTitle(run)}</h1>
            <p className="mt-0.5 flex flex-wrap items-center gap-x-2 text-[12.5px] text-fg-3">
              <span className="font-mono">#{run.id}</span>
              {run.project_key ? (
                <>
                  <Link to={`/p/${run.project_key}`} className="inline-flex items-center gap-1 hover:text-fg-2">
                    <ColorDot color={run.project_color} className="size-2" /> {run.project_key}
                  </Link>
                  <span>· {run.repo_name}</span>
                </>
              ) : (
                <Link to="/assistant" className="hover:text-fg-2">
                  Assistant session
                </Link>
              )}
              <span>· {run.runner_name}</span>
              {run.task_id && run.task_ref ? (
                <button type="button" className="font-mono hover:text-accent" onClick={() => openTask(run.task_id!)}>
                  · {run.task_ref}
                </button>
              ) : null}
              {run.resume_run_id ? (
                <Link to={`/agents/runs/${run.resume_run_id}`} className="inline-flex items-center gap-0.5 hover:text-fg-2">
                  <CornerDownRight className="size-3" aria-hidden /> continues #{run.resume_run_id}
                </Link>
              ) : null}
            </p>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            <RunStatusBadge run={run} />
            {canHandoff ? (
              <Button
                size="sm"
                variant="subtle"
                loading={handoff.isPending}
                onClick={openInClaude}
                title={`Open this session in ${run.engine === "codex" ? "Codex" : "Claude Code"} in a terminal on ${run.runner_name}; Forge hands the run over`}
              >
                <SquareTerminal className="size-3.5" aria-hidden /> Open in {run.engine === "codex" ? "Codex" : "Claude Code"}
              </Button>
            ) : null}
            {open && run.kind === "agent" ? (
              <Button
                size="sm"
                variant={waiting ? "primary" : "subtle"}
                disabled={run.finish_requested && run.status === "running"}
                loading={finish.isPending}
                onClick={() => finish.mutate(run.id, { onError: (e) => toast.error(e) })}
                title="The work is done: close the session and the run"
              >
                <CircleCheck className="size-3.5" aria-hidden /> {run.finish_requested && run.status === "running" ? "Finishing…" : "Finish"}
              </Button>
            ) : null}
            {open ? (
              <Button
                size="sm"
                variant="ghost"
                disabled={run.cancel_requested}
                loading={cancel.isPending}
                onClick={() => cancel.mutate(run.id, { onError: (e) => toast.error(e) })}
              >
                <Ban className="size-3.5" aria-hidden /> {run.cancel_requested ? "Cancelling…" : "Cancel"}
              </Button>
            ) : run.kind === "agent" && run.session_id && !run.handoff ? (
              <Button size="sm" variant="primary" onClick={() => newRun({ resume: run })}>
                <CornerDownRight className="size-3.5" aria-hidden /> Continue
              </Button>
            ) : null}
          </div>
        </div>

        <dl className="grid grid-cols-2 gap-2 rounded-lg border border-line bg-surface p-3 text-[12px] sm:grid-cols-4 lg:grid-cols-6">
          {[
            ["Mode", run.kind === "agent" ? run.permission_mode || "—" : "command"],
            [
              "Model",
              [
                run.engine ? ENGINE_LABEL[run.engine] : "",
                run.model || "default",
                run.effort ? `${run.effort} effort` : "",
                run.model_note,
              ].filter(Boolean).join(" · "),
            ],
            ["Worktree", run.worktree ? "yes" : "no"],
            // DeepSeek runs are priced by the machine from token counts: an estimate.
            ["Cost", run.engine === "deepseek" && run.cost_usd != null ? `≈ ${formatCost(run.cost_usd)}` : formatCost(run.cost_usd)],
            ["Turns", run.num_turns ?? "—"],
            ["Duration", formatDuration(duration)],
          ].map(([k, v]) => (
            <div key={k as string}>
              <dt className="text-fg-3">{k}</dt>
              <dd className="tabular font-medium">{v}</dd>
            </div>
          ))}
          <div className="col-span-2 sm:col-span-4 lg:col-span-6">
            <dt className="text-fg-3">Queued</dt>
            <dd>
              <RelativeTime iso={run.created_at} />
              {run.started_at ? (
                <>
                  {" "}
                  · started <RelativeTime iso={run.started_at} />
                </>
              ) : null}
              {run.finished_at ? (
                <>
                  {" "}
                  · finished <RelativeTime iso={run.finished_at} />
                </>
              ) : null}
              {run.exit_code !== null ? <> · exit {run.exit_code}</> : null}
            </dd>
          </div>
          {run.session_id ? (
            <div className="col-span-2 flex items-center gap-2 sm:col-span-4 lg:col-span-6">
              <dt className="text-fg-3">Session</dt>
              <dd className="min-w-0 truncate font-mono">{run.session_id}</dd>
              <button
                type="button"
                aria-label="Copy session id"
                className="rounded p-0.5 text-fg-3 hover:text-fg"
                onClick={() => void navigator.clipboard?.writeText(run.session_id).then(() => toast.success("Session id copied"))}
              >
                <Copy className="size-3.5" />
              </button>
            </div>
          ) : null}
          {run.resume_hint ? (
            <div className="col-span-2 sm:col-span-4 lg:col-span-6">
              <dt className="text-fg-3">
                In a terminal on {run.runner_name}{" "}
                <span className="text-fg-3">(Claude Code's /resume does not list Forge runs; this, or </span>
                <code className="font-mono text-fg-2">forge resume {run.id}</code>
                <span className="text-fg-3">, opens it)</span>
              </dt>
              <dd className="mt-0.5 flex items-center gap-2">
                <code className="min-w-0 flex-1 truncate rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11.5px]">{run.resume_hint}</code>
                <button
                  type="button"
                  aria-label="Copy the resume command"
                  className="rounded p-0.5 text-fg-3 hover:text-fg"
                  onClick={() => void navigator.clipboard?.writeText(run.resume_hint).then(() => toast.success("Command copied"))}
                >
                  <Copy className="size-3.5" />
                </button>
              </dd>
            </div>
          ) : null}
        </dl>
      </header>

      {run.handoff ? (
        <div role="status" className="mb-4 flex flex-wrap items-center gap-2 rounded-lg border border-accent/40 bg-accent/8 px-3 py-2 text-[13px]">
          <SquareTerminal className="size-4 text-fg-3" aria-hidden />
          Continued in {run.engine === "codex" ? "Codex" : "Claude Code"} outside Forge: {run.handoff}.
          {run.handoff.includes("tmux ") ? (
            <Link
              to={`/terminal/${run.runner_id}/${run.handoff.split("tmux ")[1]}`}
              className="text-accent hover:underline"
            >
              Open the terminal
            </Link>
          ) : null}
        </div>
      ) : null}

      {run.error ? (
        <div role="alert" className="mb-4 rounded-lg border border-critical/40 bg-critical/8 px-3 py-2 text-[13px]">
          {run.error}
        </div>
      ) : null}

      {run.result.trim() ? (
        <section aria-labelledby="run-result" className="mb-5 rounded-xl border border-line-strong bg-surface p-4">
          <h2 id="run-result" className="mb-2 text-[13px] font-semibold text-fg-2">
            Result
          </h2>
          <Markdown>{run.result}</Markdown>
        </section>
      ) : null}

      {run.kind === "agent" && run.prompt ? (
        <details className="mb-4 rounded-lg border border-line bg-surface px-3 py-2">
          <summary className="cursor-pointer text-[13px] font-medium text-fg-2">Prompt</summary>
          <pre className="mt-2 font-mono text-[12.5px] whitespace-pre-wrap">{run.prompt}</pre>
        </details>
      ) : null}

      <section aria-labelledby="run-transcript" aria-live="off">
        <h2 id="run-transcript" className="mb-2 text-[13px] font-semibold text-fg-2">
          Transcript {events ? <span className="font-normal text-fg-3">· {events.length} events</span> : null}
        </h2>
        {loadingEvents ? (
          <SkeletonRows rows={5} />
        ) : items.length ? (
          <RunLog items={items} />
        ) : (
          <p className="text-[13px] text-fg-3">
            {run.status === "queued" ? `Waiting for ${run.runner_name} to pick this up…` : "No output."}
          </p>
        )}
        {active ? (
          <p className="mt-3 flex items-center gap-2 text-[12px] text-fg-3" role="status">
            <span className="size-1.5 animate-pulse-soft rounded-full bg-accent" aria-hidden />
            {run.awaiting === "answer"
              ? "Waiting for your answer"
              : run.awaiting === "reply"
                ? "Your turn — reply, or Finish when the work is done"
                : run.status === "queued" && run.resumes > 0
                  ? `Picking the session back up on ${run.runner_name}…`
                  : "Live"}
          </p>
        ) : waiting ? (
          <p className="mt-3 flex items-center gap-2 text-[12px] text-fg-3" role="status">
            <span className="size-1.5 rounded-full bg-warning" aria-hidden />
            Waiting for you — reply and {run.runner_name} picks the session back up in this run, or Finish when the work is done.
          </p>
        ) : null}
        {(active && run.interactive && run.status === "running") || waiting ? <RunInteraction run={run} prompts={prompts} /> : null}
        <div ref={bottom} />
      </section>

      {!follow && active ? (
        <button
          type="button"
          onClick={() => {
            setFollow(true);
            bottom.current?.scrollIntoView({ behavior: "smooth", block: "end" });
          }}
          className="fixed right-4 bottom-20 z-20 inline-flex items-center gap-1.5 rounded-full border border-line-strong bg-surface px-3 py-1.5 text-[12.5px] shadow-pop md:bottom-6"
        >
          <ArrowDown className="size-3.5" aria-hidden /> Follow
        </button>
      ) : null}
    </div>
  );
}
