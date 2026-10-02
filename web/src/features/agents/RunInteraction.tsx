// An interactive run's side of the conversation: what the session is asking
// (a question, a tool permission, a plan to approve) as cards to answer, and
// a composer for follow-ups — the browser stand-in for Claude Code's
// terminal. The machine decides what an answer may allow (see the agent's
// "approvals" setting); this only collects the user's choice.

import clsx from "clsx";
import { ArrowUp, CircleHelp, ClipboardCheck, ShieldQuestion, Square } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { useAnswerPrompt, useEndRunSession, useSendRunMessage } from "@/api/hooks";
import type { PromptAnswer, Run, RunPrompt } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Markdown } from "@/components/ui/Markdown";
import { useToast } from "@/components/ui/Toast";
import { toolSummary } from "@/lib/runlog";

const MAX_HEIGHT = 200;

export function RunInteraction({ run, prompts }: { run: Run; prompts: RunPrompt[] }) {
  const answer = useAnswerPrompt(run.id);
  const toast = useToast();
  // Hide a card as soon as it is answered; the next poll confirms it.
  const [answered, setAnswered] = useState<Set<number>>(() => new Set());
  const pending = prompts.filter((p) => p.status === "pending" && !answered.has(p.id));

  const submit = (p: RunPrompt, a: PromptAnswer) =>
    answer.mutate(
      { promptId: p.id, answer: a },
      {
        onSuccess: () => setAnswered((s) => new Set(s).add(p.id)),
        onError: (e) => toast.error(e),
      },
    );

  return (
    <div className="sticky bottom-[calc(3.5rem+env(safe-area-inset-bottom))] z-10 mt-4 space-y-2 bg-bg pt-1 pb-3 md:bottom-0">
      {pending.map((p) => (
        <PromptCard key={p.id} prompt={p} busy={answer.isPending} onAnswer={(a) => submit(p, a)} />
      ))}
      <RunComposer run={run} />
    </div>
  );
}

function PromptCard({ prompt, busy, onAnswer }: { prompt: RunPrompt; busy: boolean; onAnswer: (a: PromptAnswer) => void }) {
  const Icon = prompt.kind === "question" ? CircleHelp : prompt.kind === "plan" ? ClipboardCheck : ShieldQuestion;
  const title =
    prompt.kind === "question"
      ? "Claude has a question"
      : prompt.kind === "plan"
        ? "Claude has a plan ready"
        : `Claude wants to use ${prompt.tool_name}`;
  return (
    <section
      aria-label={title}
      className="rounded-xl border border-warning/50 bg-surface p-3 shadow-pop"
      data-prompt-kind={prompt.kind}
    >
      <h3 className="mb-2 flex items-center gap-2 text-[13px] font-semibold">
        <Icon className="size-4 text-warning" aria-hidden /> {title}
      </h3>
      {prompt.kind === "question" ? (
        <QuestionForm prompt={prompt} busy={busy} onAnswer={onAnswer} />
      ) : prompt.kind === "plan" ? (
        <PlanForm prompt={prompt} busy={busy} onAnswer={onAnswer} />
      ) : (
        <PermissionForm prompt={prompt} busy={busy} onAnswer={onAnswer} />
      )}
    </section>
  );
}

type Question = {
  question: string;
  header?: string;
  multiSelect?: boolean;
  options?: { label: string; description?: string }[];
};

function QuestionForm({ prompt, busy, onAnswer }: { prompt: RunPrompt; busy: boolean; onAnswer: (a: PromptAnswer) => void }) {
  const questions: Question[] = Array.isArray(prompt.input?.questions) ? prompt.input.questions : [];
  const [picked, setPicked] = useState<Record<string, string[]>>({});
  const [other, setOther] = useState<Record<string, string>>({});

  const answerFor = (q: Question) => {
    const own = (other[q.question] ?? "").trim();
    return own || (picked[q.question] ?? []).join(", ");
  };
  const complete = questions.length > 0 && questions.every((q) => answerFor(q));

  const toggle = (q: Question, label: string) =>
    setPicked((s) => {
      const cur = s[q.question] ?? [];
      const next = q.multiSelect ? (cur.includes(label) ? cur.filter((l) => l !== label) : [...cur, label]) : [label];
      return { ...s, [q.question]: next };
    });

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (!complete) return;
        onAnswer({ decision: "answer", answers: Object.fromEntries(questions.map((q) => [q.question, answerFor(q)])) });
      }}
    >
      {questions.map((q) => (
        <fieldset key={q.question} className="space-y-1.5">
          <legend className="mb-1 text-[13.5px]">
            {q.header ? <span className="mr-1.5 rounded bg-surface-2 px-1.5 py-0.5 text-[11px] text-fg-2">{q.header}</span> : null}
            {q.question}
            {q.multiSelect ? <span className="ml-1 text-[11.5px] text-fg-3">(pick any)</span> : null}
          </legend>
          <div className="flex flex-wrap gap-1.5">
            {(q.options ?? []).map((o) => {
              const on = (picked[q.question] ?? []).includes(o.label);
              return (
                <button
                  key={o.label}
                  type="button"
                  aria-pressed={on}
                  title={o.description}
                  onClick={() => toggle(q, o.label)}
                  className={clsx(
                    "max-w-full rounded-lg border px-2.5 py-1.5 text-left text-[13px] transition-colors",
                    on ? "border-accent bg-accent/12" : "border-line hover:bg-surface-2",
                  )}
                >
                  <span className="font-medium">{o.label}</span>
                  {o.description ? <span className="block text-[11.5px] text-fg-3">{o.description}</span> : null}
                </button>
              );
            })}
          </div>
          <input
            aria-label={`Other answer: ${q.question}`}
            value={other[q.question] ?? ""}
            onChange={(e) => setOther((s) => ({ ...s, [q.question]: e.target.value }))}
            placeholder="Or type your own answer…"
            className="w-full rounded-md border border-line bg-surface px-2.5 py-1.5 text-[13px] placeholder:text-fg-3 focus:border-accent focus:outline-none"
          />
        </fieldset>
      ))}
      <div className="flex flex-wrap justify-end gap-2">
        <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => onAnswer({ decision: "deny" })}>
          Skip
        </Button>
        <Button type="submit" size="sm" variant="primary" disabled={!complete} loading={busy}>
          Answer
        </Button>
      </div>
    </form>
  );
}

function PermissionForm({ prompt, busy, onAnswer }: { prompt: RunPrompt; busy: boolean; onAnswer: (a: PromptAnswer) => void }) {
  const [message, setMessage] = useState("");
  const summary = toolSummary(prompt.tool_name, prompt.input);
  return (
    <div className="space-y-2">
      {prompt.description && prompt.description !== summary ? (
        <p className="text-[13px] text-fg-2">{prompt.description}</p>
      ) : null}
      {summary ? (
        <pre className="overflow-x-auto rounded-md bg-surface-2 px-2.5 py-1.5 font-mono text-[12px] whitespace-pre-wrap">{summary}</pre>
      ) : null}
      <details className="text-[12px] text-fg-3">
        <summary className="cursor-pointer">Details</summary>
        <pre className="mt-1 max-h-48 overflow-auto rounded bg-surface-2 px-2 py-1.5 font-mono text-[11.5px] whitespace-pre-wrap">
          {JSON.stringify(prompt.input, null, 2)}
        </pre>
      </details>
      <input
        aria-label="Note for Claude"
        value={message}
        onChange={(e) => setMessage(e.target.value)}
        placeholder="If you deny: tell Claude why, or what to do instead (optional)"
        className="w-full rounded-md border border-line bg-surface px-2.5 py-1.5 text-[13px] placeholder:text-fg-3 focus:border-accent focus:outline-none"
      />
      <div className="flex flex-wrap justify-end gap-2">
        <Button size="sm" variant="ghost" disabled={busy} onClick={() => onAnswer({ decision: "deny", message })}>
          Deny
        </Button>
        {prompt.suggestions?.length ? (
          <Button size="sm" variant="subtle" disabled={busy} onClick={() => onAnswer({ decision: "allow_always" })}>
            Allow for this session
          </Button>
        ) : null}
        <Button size="sm" variant="primary" loading={busy} onClick={() => onAnswer({ decision: "allow" })}>
          Allow
        </Button>
      </div>
    </div>
  );
}

function PlanForm({ prompt, busy, onAnswer }: { prompt: RunPrompt; busy: boolean; onAnswer: (a: PromptAnswer) => void }) {
  const [message, setMessage] = useState("");
  const plan = typeof prompt.input?.plan === "string" ? prompt.input.plan : "";
  return (
    <div className="space-y-2">
      {plan ? (
        <div className="max-h-[45vh] overflow-auto rounded-md border border-line px-3 py-2">
          <Markdown>{plan}</Markdown>
        </div>
      ) : null}
      <input
        aria-label="What to change"
        value={message}
        onChange={(e) => setMessage(e.target.value)}
        placeholder="To keep planning: what should change? (optional)"
        className="w-full rounded-md border border-line bg-surface px-2.5 py-1.5 text-[13px] placeholder:text-fg-3 focus:border-accent focus:outline-none"
      />
      <div className="flex flex-wrap justify-end gap-2">
        <Button size="sm" variant="ghost" disabled={busy} onClick={() => onAnswer({ decision: "deny", message })}>
          Keep planning
        </Button>
        <Button size="sm" variant="subtle" disabled={busy} onClick={() => onAnswer({ decision: "approve" })}>
          Approve, ask before edits
        </Button>
        <Button size="sm" variant="primary" loading={busy} onClick={() => onAnswer({ decision: "approve_edits" })}>
          Approve, auto-accept edits
        </Button>
      </div>
    </div>
  );
}

function RunComposer({ run }: { run: Run }) {
  const send = useSendRunMessage(run.id);
  const end = useEndRunSession(run.id);
  const toast = useToast();
  const [text, setText] = useState("");
  const ref = useRef<HTMLTextAreaElement>(null);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, MAX_HEIGHT)}px`;
    el.style.overflowY = el.scrollHeight > MAX_HEIGHT ? "auto" : "hidden";
  }, [text]);

  const submit = () => {
    const t = text.trim();
    if (!t || send.isPending) return;
    send.mutate(t, { onSuccess: () => setText(""), onError: (e) => toast.error(e) });
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <div className="flex items-end gap-2 rounded-xl border border-line-strong bg-surface p-1.5 transition-colors focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/25">
        <textarea
          ref={ref}
          rows={1}
          aria-label="Message"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={run.awaiting === "reply" ? "Reply to Claude…" : "Send a message — Claude reads it when it can…"}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              submit();
            }
          }}
          className="min-h-8 flex-1 resize-none bg-transparent px-2 py-1.5 text-sm leading-relaxed text-fg placeholder:text-fg-3 focus:outline-none"
        />
        <Button type="submit" variant="primary" size="icon" aria-label="Send" disabled={!text.trim()} loading={send.isPending}>
          <ArrowUp className="size-4" />
        </Button>
      </div>
      <div className="mt-1 flex items-center justify-between gap-2 px-1 text-[11px] text-fg-3">
        <span className="hidden sm:inline">Enter to send · Shift+Enter for a new line</span>
        <button
          type="button"
          disabled={end.isPending}
          onClick={() => end.mutate(undefined, { onError: (e) => toast.error(e) })}
          className="ml-auto inline-flex items-center gap-1 hover:text-fg-2 disabled:opacity-50"
        >
          <Square className="size-3" aria-hidden /> End session
        </button>
      </div>
    </form>
  );
}
