// A member's Agent page: the one agent the owner gave this account — Claude
// Code on one machine, with a set model and effort. The member asks, reads
// what it does, answers its questions and permission prompts here, and starts
// a new conversation when it likes. It chooses nothing else: where and how
// the agent runs is the owner's decision (`forge member-agent`).

import { Bot, Plus, WifiOff } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Navigate } from "react-router-dom";
import { isActiveRun, useAskMemberAgent, useMemberAgent, useRunEvents } from "@/api/hooks";
import type { MemberAgentView, Run } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { ErrorState } from "@/components/ui/EmptyState";
import { Field, Select, Textarea } from "@/components/ui/Input";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { Skeleton, SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { agentLine } from "@/lib/agents";
import { mapRunEvents } from "@/lib/runlog";
import { RunRow, RunStatusBadge } from "./RunBits";
import { RunInteraction } from "./RunInteraction";
import { RunLog } from "./RunLog";

export function MemberAgentPage() {
  const q = useMemberAgent(true, 4000);
  const [starting, setStarting] = useState(false);
  const v = q.data;

  if (q.isPending) {
    return (
      <div className="mx-auto max-w-4xl space-y-4">
        <Skeleton className="h-10 w-2/3" />
        <SkeletonRows rows={6} />
      </div>
    );
  }
  if (q.error && !v) return <ErrorState error={q.error} onRetry={() => q.refetch()} />;
  // No agent of its own: the runs page is the place.
  if (!v?.agent) return <Navigate to="/agents" replace />;

  const run = v.run;
  return (
    <div className="mx-auto max-w-4xl">
      <PageHeader
        title="Agent"
        subtitle={agentLine(v.agent)}
        actions={
          run && !starting ? (
            <Button size="sm" variant="subtle" onClick={() => setStarting(true)} title="Finish this conversation and start a new one">
              <Plus className="size-3.5" aria-hidden /> New conversation
            </Button>
          ) : null
        }
      />
      {!v.agent.online ? (
        <p className="mb-4 flex items-start gap-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-[13px]" role="status">
          <WifiOff className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden />
          {v.agent.runner_name} is offline. What you ask waits until it is back.
        </p>
      ) : null}

      {run && !starting ? (
        <Conversation run={run} machine={v.agent.runner_name} />
      ) : (
        <StartForm view={v} fresh={!!run} onDone={() => setStarting(false)} onCancel={run ? () => setStarting(false) : undefined} />
      )}

      {v.recent.length ? (
        <Panel title="Earlier conversations" icon={<Bot />} id="agent-recent" className="mt-6">
          <div>
            {v.recent.map((r) => (
              <RunRow key={r.id} run={r} showProject={v.projects.length > 1} />
            ))}
          </div>
        </Panel>
      ) : null}
    </div>
  );
}

/** What to ask, and — with more than one project — where. */
function StartForm({ view, fresh, onDone, onCancel }: { view: MemberAgentView; fresh: boolean; onDone: () => void; onCancel?: () => void }) {
  const ask = useAskMemberAgent();
  const toast = useToast();
  const [prompt, setPrompt] = useState("");
  const [project, setProject] = useState(view.projects[0]?.key ?? "");
  const text = prompt.trim();
  const nowhere = view.projects.length === 0;

  const submit = (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!text || ask.isPending || nowhere) return;
    ask.mutate(
      { prompt: text, project_key: project || undefined, fresh: fresh || undefined },
      {
        onSuccess: () => {
          setPrompt("");
          onDone();
        },
        onError: (err) => toast.error(err),
      },
    );
  };

  return (
    <form onSubmit={submit} className="space-y-3 rounded-xl border border-line bg-surface p-4" aria-label={fresh ? "New conversation" : "Ask the agent"}>
      {nowhere ? (
        <p className="text-[13px] text-fg-2">None of your projects has a repository the agent can work in yet. Ask the owner to add one.</p>
      ) : null}
      {view.projects.length > 1 ? (
        <Field label="Project">
          {(id) => (
            <Select id={id} value={project} onChange={(e) => setProject(e.target.value)} className="sm:w-72">
              {view.projects.map((p) => (
                <option key={p.key} value={p.key}>
                  {p.key} · {p.name}
                </option>
              ))}
            </Select>
          )}
        </Field>
      ) : null}
      <Field
        label={fresh ? "What should it do next?" : "What should the agent do?"}
        hint={
          fresh
            ? "The conversation that is open is finished first; it stays under Earlier conversations. ⌘/Ctrl+Enter to start."
            : `It works in ${view.projects.length === 1 ? view.projects[0].name : "the project you pick"}, and asks you before it runs commands. ⌘/Ctrl+Enter to start.`
        }
      >
        {(id, desc) => (
          <Textarea
            id={id}
            aria-describedby={desc}
            data-autofocus
            autoFocus
            rows={5}
            value={prompt}
            disabled={nowhere}
            onChange={(e) => setPrompt(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submit();
            }}
            placeholder="Describe the task: what to change, where, and how you will know it is done."
          />
        )}
      </Field>
      <div className="flex items-center justify-end gap-2">
        {onCancel ? (
          <Button variant="ghost" onClick={onCancel}>
            Back to the conversation
          </Button>
        ) : null}
        <Button variant="primary" type="submit" loading={ask.isPending} disabled={!text || nowhere}>
          {fresh ? "Start a new conversation" : "Start"}
        </Button>
      </div>
    </form>
  );
}

/** The open conversation: what was asked, what the agent did, and the place to answer it. */
function Conversation({ run: listed, machine }: { run: Run; machine: string }) {
  const events = useRunEvents(listed.id);
  const run = events.data?.run ?? listed;
  const prompts = events.data?.prompts ?? [];
  const items = useMemo(() => mapRunEvents(events.data?.events ?? []), [events.data?.events]);
  const active = isActiveRun(run);
  const waiting = run.status === "waiting";
  const bottom = useRef<HTMLDivElement>(null);
  const pending = prompts.filter((p) => p.status === "pending").length;
  // Follow the tail as the agent works.
  useEffect(() => {
    if (active && items.length) bottom.current?.scrollIntoView?.({ block: "end" });
  }, [items.length, pending, active]);

  return (
    <section aria-label="Conversation">
      <div className="mb-3 flex flex-wrap items-start gap-2 rounded-xl border border-line bg-surface px-3.5 py-3">
        <p className="min-w-0 flex-1 text-[13.5px] break-words whitespace-pre-wrap">{run.prompt}</p>
        <RunStatusBadge run={run} />
      </div>
      {run.error ? (
        <div role="alert" className="mb-3 rounded-lg border border-critical/40 bg-critical/8 px-3 py-2 text-[13px]">
          {run.error}
        </div>
      ) : null}
      {events.isPending ? (
        <SkeletonRows rows={4} />
      ) : items.length ? (
        <RunLog items={items} />
      ) : (
        <p className="text-[13px] text-fg-3">{run.status === "queued" ? `Waiting for ${machine} to pick this up…` : "Nothing yet."}</p>
      )}
      {active ? (
        <p className="mt-3 flex items-center gap-2 text-[12px] text-fg-3" role="status">
          <span className="size-1.5 animate-pulse-soft rounded-full bg-accent" aria-hidden />
          {run.awaiting === "answer"
            ? "Waiting for your answer"
            : run.awaiting === "reply"
              ? "Your turn — reply, or start a new conversation when this one is done"
              : run.status === "queued"
                ? run.resumes > 0
                  ? `Picking the conversation back up on ${machine}…`
                  : "Starting…"
                : "Working"}
        </p>
      ) : waiting ? (
        <p className="mt-3 flex items-center gap-2 text-[12px] text-fg-3" role="status">
          <span className="size-1.5 rounded-full bg-warning" aria-hidden />
          Waiting for you — reply and the agent picks this conversation back up.
        </p>
      ) : null}
      {(active && run.interactive && run.status === "running") || waiting ? <RunInteraction run={run} prompts={prompts} /> : null}
      <div ref={bottom} />
    </section>
  );
}
