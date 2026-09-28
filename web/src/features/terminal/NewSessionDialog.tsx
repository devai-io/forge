// Start a detached tmux session on a runner — Claude Code (with the Forge
// project context) or a plain shell — in a repo's directory, then attach.

import { Sparkles, SquareTerminal } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { isElevationError } from "@/api/client";
import { useCreateSession, useProject, useProjects, useTerminalHosts } from "@/api/hooks";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Field, Input, Select, Textarea } from "@/components/ui/Input";
import { Segmented } from "@/components/ui/Tabs";
import { useToast } from "@/components/ui/Toast";
import type { NewSessionPrefill } from "@/features/shell/context";
import { attachPath, deriveSessionName, pickHost, readStoredHost, SESSION_NAME_RE } from "@/lib/terminal";
import { UnlockTerminals } from "./bits";

export function NewSessionDialog({ prefill, onClose }: { prefill: NewSessionPrefill; onClose: () => void }) {
  const hosts = useTerminalHosts({ refetchInterval: false });
  const projects = useProjects();
  const create = useCreateSession();
  const toast = useToast();
  const navigate = useNavigate();

  const [runnerId, setRunnerId] = useState<string>(prefill.runner_id ? String(prefill.runner_id) : "");
  const [projectKey, setProjectKey] = useState(prefill.project_key ?? "");
  const [repoId, setRepoId] = useState<string>(prefill.repo_id ? String(prefill.repo_id) : "");
  const [start, setStart] = useState<"claude" | "shell">(prefill.start ?? "claude");
  const [prompt, setPrompt] = useState("");
  const [name, setName] = useState("");

  const project = useProject(projectKey || undefined);
  const hostList = (hosts.data?.hosts ?? []).filter((h) => h.terminal);
  const effectiveRunnerId =
    runnerId ||
    String(
      pickHost(
        hostList.filter((h) => h.online).map((h) => h.runner_id),
        readStoredHost(),
        hosts.data?.default_runner_id ?? null,
      ) ??
        hostList[0]?.runner_id ??
        "",
    );
  const host = hostList.find((h) => String(h.runner_id) === effectiveRunnerId);
  const repos = (project.data?.repos ?? []).filter((r) => r.path);
  const repo = repos.find((r) => String(r.id) === repoId);
  const derived = deriveSessionName(projectKey || null, repo?.name ?? null, host?.sessions.map((s) => s.name) ?? [], start);
  const finalName = name.trim() || derived;
  const nameValid = SESSION_NAME_RE.test(finalName);
  const locked = isElevationError(hosts.error);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!host || !nameValid) return;
    create.mutate(
      {
        runnerId: host.runner_id,
        name: finalName,
        start,
        project_key: projectKey || undefined,
        repo_id: repo ? repo.id : undefined,
        prompt: start === "claude" && prompt.trim() ? prompt.trim() : undefined,
      },
      {
        onSuccess: ({ session }) => {
          onClose();
          navigate(attachPath(host.runner_id, session));
        },
        onError: (err) => toast.error(err),
      },
    );
  };

  return (
    <Dialog
      open
      onClose={onClose}
      size="lg"
      title="New session"
      description="A detached tmux session on one of your machines. It keeps running when you detach."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="new-session-form" loading={create.isPending} disabled={!host || !nameValid}>
            Start and attach
          </Button>
        </>
      }
    >
      {locked ? (
        <UnlockTerminals />
      ) : (
        <form id="new-session-form" onSubmit={submit} className="space-y-4">
          <Segmented
            label="Start"
            value={start}
            onChange={setStart}
            items={[
              {
                value: "claude",
                label: (
                  <>
                    <Sparkles className="size-3.5" aria-hidden /> Claude
                  </>
                ),
              },
              {
                value: "shell",
                label: (
                  <>
                    <SquareTerminal className="size-3.5" aria-hidden /> Shell
                  </>
                ),
              },
            ]}
          />
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <Field label="Host">
              {(id) => (
                <Select id={id} value={effectiveRunnerId} onChange={(e) => setRunnerId(e.target.value)}>
                  {hostList.length === 0 ? <option value="">No runner allows terminals</option> : null}
                  {hostList.map((h) => (
                    <option key={h.runner_id} value={h.runner_id} disabled={!h.online}>
                      {h.runner_name}
                      {h.online ? "" : " · offline"}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <Field label="Project">
              {(id) => (
                <Select
                  id={id}
                  value={projectKey}
                  onChange={(e) => {
                    setProjectKey(e.target.value);
                    setRepoId("");
                  }}
                >
                  <option value="">None</option>
                  {projects.data?.map((p) => (
                    <option key={p.key} value={p.key}>
                      {p.key} · {p.name}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <Field label="Repository">
              {(id) => (
                <Select id={id} value={repoId} onChange={(e) => setRepoId(e.target.value)} disabled={!projectKey}>
                  <option value="">None — home directory</option>
                  {repos.map((r) => (
                    <option key={r.id} value={r.id}>
                      {r.name}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          </div>
          {start === "claude" ? (
            <Field label="First prompt" hint="Optional — typed into Claude once it starts. It already knows the project, open tasks and today's check-up.">
              {(id, desc) => (
                <Textarea
                  id={id}
                  aria-describedby={desc}
                  rows={4}
                  value={prompt}
                  onChange={(e) => setPrompt(e.target.value)}
                  placeholder="Pick up the top task and propose a plan"
                  className="font-mono text-[13px]"
                />
              )}
            </Field>
          ) : null}
          <Field
            label="Session name"
            error={!nameValid ? "Letters, digits, . _ - only; up to 40 characters" : undefined}
            hint={`Default: ${derived}`}
          >
            {(id, desc) => (
              <Input
                id={id}
                aria-describedby={desc}
                aria-invalid={!nameValid}
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={derived}
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
              />
            )}
          </Field>
          {host && host.sessions.some((s) => s.name === finalName) ? (
            <p className="text-[12px] text-fg-3">A session with this name is already running on {host.runner_name} — you'll be attached to it.</p>
          ) : null}
        </form>
      )}
    </Dialog>
  );
}
