// "Run agent" for a member who has an agent of its own: no machine, model or
// mode to pick — the request goes to that one agent. A request about a task
// starts a conversation on that task (or joins the one already open on it).

import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useAskMemberAgent } from "@/api/hooks";
import type { MemberAgentView } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Field, Select, Switch, Textarea } from "@/components/ui/Input";
import { useToast } from "@/components/ui/Toast";
import type { NewRunPrefill } from "@/features/shell/context";
import { agentLine, startsFresh } from "@/lib/agents";

export function MemberRunDialog({ prefill, view, onClose }: { prefill: NewRunPrefill; view: MemberAgentView; onClose: () => void }) {
  const ask = useAskMemberAgent();
  const toast = useToast();
  const navigate = useNavigate();
  const agent = view.agent!;
  const open = view.run;
  const taskId = prefill.task_id ?? null;
  const [prompt, setPrompt] = useState(prefill.prompt ?? "");
  const [project, setProject] = useState(
    prefill.project_key && view.projects.some((p) => p.key === prefill.project_key) ? prefill.project_key : (view.projects[0]?.key ?? ""),
  );
  const [fresh, setFresh] = useState(() => startsFresh(view, taskId));
  const text = prompt.trim();
  const joins = !!open && !fresh; // one more message to the open conversation
  const nowhere = view.projects.length === 0;

  const submit = (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!text || ask.isPending || nowhere) return;
    ask.mutate(
      { prompt: text, project_key: taskId ? undefined : project || undefined, task_id: taskId ?? undefined, fresh: open && fresh ? true : undefined },
      {
        onSuccess: () => {
          onClose();
          toast.success(joins ? "Sent to your agent" : "Your agent is on it");
          navigate("/agent");
        },
        onError: (err) => toast.error(err),
      },
    );
  };

  return (
    <Dialog
      open
      onClose={onClose}
      size="md"
      title={prefill.task_ref ? `Run your agent on ${prefill.task_ref}` : "Run your agent"}
      description={agentLine(agent)}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="member-run-form" loading={ask.isPending} disabled={!text || nowhere}>
            {joins ? "Send" : "Start"}
          </Button>
        </>
      }
    >
      <form id="member-run-form" onSubmit={submit} className="space-y-4">
        {nowhere ? (
          <p className="text-[13px] text-fg-2">None of your projects has a repository the agent can work in yet. Ask the owner to add one.</p>
        ) : null}
        {!taskId && view.projects.length > 1 && !joins ? (
          <Field label="Project">
            {(id) => (
              <Select id={id} value={project} onChange={(e) => setProject(e.target.value)}>
                {view.projects.map((p) => (
                  <option key={p.key} value={p.key}>
                    {p.key} · {p.name}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        ) : null}
        <Field label="What should it do?" hint="Plain text or markdown. ⌘/Ctrl+Enter to send.">
          {(id, desc) => (
            <Textarea
              id={id}
              aria-describedby={desc}
              data-autofocus
              rows={7}
              value={prompt}
              disabled={nowhere}
              onChange={(e) => setPrompt(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submit();
              }}
            />
          )}
        </Field>
        {open ? (
          <div className="space-y-2 rounded-md border border-line bg-surface-2/60 px-3 py-2 text-[12.5px]" role="note">
            <p className="text-fg-2">
              Your agent has a conversation open{open.task_ref ? ` on ${open.task_ref}` : ""}.{" "}
              {fresh ? "It is finished first; this request starts a new one." : "This request goes to it as one more message."}
            </p>
            <Switch checked={fresh} onChange={setFresh} label="Start a new conversation" />
          </div>
        ) : null}
      </form>
    </Dialog>
  );
}
