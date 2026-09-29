// Queue an agent (Claude Code) or a named command on a runner.
//
// An agent run uses the server's default engine (normally DeepSeek) unless
// Claude is picked here explicitly; a continued run keeps its engine.
//
// The runner decides what it will accept: permission modes and commands come
// from its advertised capabilities, so this form can only offer what the
// machine on the other end has agreed to run. Continuing a run (resume) copies
// runner and repo from the original on the server; only a new prompt is asked.

import { ShieldAlert, TriangleAlert } from "lucide-react";
import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useCreateRun, useEngine, useProject, useProjects, useRunners, useTasks } from "@/api/hooks";
import type { Engine, RunKind } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Field, Input, Select, Switch, Textarea } from "@/components/ui/Input";
import { Segmented } from "@/components/ui/Tabs";
import { useToast } from "@/components/ui/Toast";
import type { NewRunPrefill } from "@/features/shell/context";
import { commandOptions, defaultRunner, ENGINE_LABEL, PERMISSION_MODE_HELP, runnerForCommand } from "@/lib/agents";

export function NewRunDialog({ prefill, onClose }: { prefill: NewRunPrefill; onClose: () => void }) {
  const toast = useToast();
  const navigate = useNavigate();
  const create = useCreateRun();
  const runners = useRunners(false);
  const projects = useProjects();
  const engineStatus = useEngine();
  const resume = prefill.resume;

  const [runnerId, setRunnerId] = useState<string>(resume ? String(resume.runner_id) : "");
  const [projectKey, setProjectKey] = useState(resume?.project_key ?? prefill.project_key ?? "");
  const [repoId, setRepoId] = useState<string>(
    resume ? String(resume.repo_id) : prefill.repo_id ? String(prefill.repo_id) : "",
  );
  const [kind, setKind] = useState<RunKind>("agent");
  const [prompt, setPrompt] = useState(prefill.prompt ?? "");
  const [command, setCommand] = useState("");
  const [mode, setMode] = useState(resume?.permission_mode || "plan");
  const [worktree, setWorktree] = useState(resume?.worktree ?? false);
  const [model, setModel] = useState(resume?.model ?? "");
  // "" = the server's default engine.
  const [engine, setEngine] = useState<Engine | "">("");
  const defaultEngine = engineStatus.data?.settings.default ?? "deepseek";
  const [confirmText, setConfirmText] = useState("");
  const [taskId, setTaskId] = useState<string>(
    resume?.task_id ? String(resume.task_id) : prefill.task_id ? String(prefill.task_id) : "",
  );

  const runnerList = useMemo(() => runners.data ?? [], [runners.data]);
  // Default to the master (the primary machine), else the first online runner.
  const effectiveRunnerId = runnerId || String(defaultRunner(runnerList)?.id ?? "");
  const runner = runnerList.find((r) => String(r.id) === effectiveRunnerId);
  const effectiveProjectKey = projectKey || projects.data?.[0]?.key || "";
  const project = useProject(effectiveProjectKey || undefined);
  const repos = (project.data?.repos ?? []).filter((r) => r.path);
  const effectiveRepoId = repoId || String(repos[0]?.id ?? "");
  const tasks = useTasks({ project: effectiveProjectKey, open: true }, { enabled: !!effectiveProjectKey && !resume });

  const modes = runner?.capabilities.permission_modes.length ? runner.capabilities.permission_modes : ["plan"];
  const repoName = repos.find((r) => String(r.id) === effectiveRepoId)?.name;
  // Commands any runner offers for this repo (unscoped, or scoped to it) —
  // picking one that lives elsewhere (an iOS upload on the Mac) moves the run there.
  const commands = commandOptions(runnerList, repoName, runner?.id);
  const effectiveMode = modes.includes(mode) ? mode : modes[0];
  const selectedCommand = commands.find((c) => c.name === command) ?? commands[0];
  const effectiveCommand = selectedCommand?.name ?? "";
  // A command the selected runner doesn't have runs where it lives.
  const runRunner =
    kind === "command" && !resume && selectedCommand && runner && !selectedCommand.runners.includes(runner.name)
      ? (runnerForCommand(runnerList, selectedCommand.name, runner.id) ?? runner)
      : runner;
  // A confirm-command (store upload, deploy) is typed back by name before it can be queued.
  const needsConfirm = kind === "command" && !resume && !!selectedCommand?.confirm;
  const confirmed = needsConfirm && confirmText.trim() === effectiveCommand;

  const canSubmit =
    !!effectiveRunnerId &&
    !!effectiveRepoId &&
    (kind === "agent" ? prompt.trim().length > 0 : !!effectiveCommand && (!needsConfirm || confirmed)) &&
    !create.isPending;

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    create.mutate(
      {
        runner_id: runRunner ? runRunner.id : Number(effectiveRunnerId),
        repo_id: Number(effectiveRepoId),
        kind: resume ? "agent" : kind,
        prompt: kind === "agent" || resume ? prompt.trim() : undefined,
        command: kind === "command" && !resume ? effectiveCommand : undefined,
        confirmed: needsConfirm ? confirmed : undefined,
        permission_mode: kind === "agent" || resume ? effectiveMode : undefined,
        model: model.trim() || undefined,
        engine: kind === "agent" && !resume && engine ? engine : undefined,
        worktree: kind === "agent" ? worktree : false,
        task_id: taskId ? Number(taskId) : null,
        resume_run_id: resume?.id ?? null,
      },
      {
        onSuccess: (run) => {
          toast.success(`Run #${run.id} queued on ${run.runner_name}`);
          onClose();
          navigate(`/agents/runs/${run.id}`);
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
      title={resume ? `Continue run #${resume.id}` : "New agent run"}
      description={
        resume
          ? `Resumes the Claude session on ${resume.runner_name} in ${resume.repo_name}.`
          : "Runs on one of your machines. The runner only accepts what its local config allows."
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="new-run-form" loading={create.isPending} disabled={!canSubmit}>
            {resume ? "Continue" : "Queue run"}
          </Button>
        </>
      }
    >
      <form id="new-run-form" onSubmit={submit} className="space-y-4">
        {!resume ? (
          <>
            {runners.isSuccess && runnerList.length === 0 ? (
              <p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-[13px]">
                No machines yet. Add one on the Agents page and pair it — one command on that machine.
              </p>
            ) : null}
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
              <Field label="Runner">
                {(id) => (
                  <Select id={id} value={effectiveRunnerId} onChange={(e) => setRunnerId(e.target.value)}>
                    {runnerList.map((r) => (
                      <option key={r.id} value={r.id}>
                        {r.name} {r.online ? "· online" : "· offline"}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
              <Field label="Project">
                {(id) => (
                  <Select
                    id={id}
                    value={effectiveProjectKey}
                    onChange={(e) => {
                      setProjectKey(e.target.value);
                      setRepoId("");
                      setTaskId("");
                    }}
                  >
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
                  <Select id={id} value={effectiveRepoId} onChange={(e) => setRepoId(e.target.value)}>
                    {repos.length === 0 ? <option value="">No repo with a local path</option> : null}
                    {repos.map((r) => (
                      <option key={r.id} value={r.id}>
                        {r.name}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
            </div>
            {runner && !runner.online ? (
              <p className="flex items-start gap-2 text-[13px] text-fg-2">
                <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden />
                {runner.name} is offline. The run will wait in the queue until it reconnects.
              </p>
            ) : null}
            <Segmented
              label="Run kind"
              value={kind}
              onChange={setKind}
              items={[
                { value: "agent", label: "Agent" },
                { value: "command", label: "Command" },
              ]}
            />
          </>
        ) : null}

        {kind === "agent" || resume ? (
          <>
            <Field label={resume ? "Follow-up prompt" : "Prompt"} hint="Plain text or markdown. ⌘/Ctrl+Enter to queue.">
              {(id, desc) => (
                <Textarea
                  id={id}
                  aria-describedby={desc}
                  data-autofocus
                  value={prompt}
                  onChange={(e) => setPrompt(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submit(e);
                  }}
                  rows={8}
                  className="font-mono text-[13px]"
                  placeholder="Review the Maths game for bugs and propose fixes…"
                />
              )}
            </Field>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
              <Field
                label="Engine"
                hint={
                  resume
                    ? "A continued run keeps its engine."
                    : (engine || defaultEngine) === "claude"
                      ? "Anthropic — costs more."
                      : "DeepSeek — the cheap default."
                }
              >
                {(id, desc) => (
                  <Select
                    id={id}
                    aria-describedby={desc}
                    value={resume ? resume.engine || "claude" : engine}
                    disabled={!!resume}
                    onChange={(e) => {
                      setEngine(e.target.value as Engine | "");
                      setModel("");
                    }}
                  >
                    {resume ? (
                      <option value={resume.engine || "claude"}>{ENGINE_LABEL[resume.engine || "claude"]}</option>
                    ) : (
                      <>
                        <option value="">Default ({ENGINE_LABEL[defaultEngine]})</option>
                        <option value="deepseek">DeepSeek</option>
                        <option value="claude">Claude (Anthropic)</option>
                      </>
                    )}
                  </Select>
                )}
              </Field>
              <Field label="Permission mode" hint={PERMISSION_MODE_HELP[effectiveMode] ?? ""}>
                {(id, desc) => (
                  <Select id={id} aria-describedby={desc} value={effectiveMode} onChange={(e) => setMode(e.target.value)}>
                    {modes.map((m) => (
                      <option key={m} value={m}>
                        {m}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
              <Field label="Model" hint="Empty = Forge picks (Jev) or the default">
                {(id, desc) => (
                  <Input id={id} aria-describedby={desc} value={model} onChange={(e) => setModel(e.target.value)} placeholder="default" />
                )}
              </Field>
            </div>
            {!resume ? (
              <Switch
                checked={worktree}
                onChange={setWorktree}
                label="Run in a fresh git worktree (keeps your working copy untouched)"
              />
            ) : null}
          </>
        ) : (
          <div className="space-y-3">
            <Field
              label="Command"
              hint={
                <>
                  {selectedCommand?.description || "Named commands come from the runners' local config."}
                  {runRunner && runRunner !== runner ? <> Runs on {runRunner.name}.</> : null}
                </>
              }
            >
              {(id, desc) =>
                commands.length ? (
                  <Select
                    id={id}
                    aria-describedby={desc}
                    value={effectiveCommand}
                    onChange={(e) => {
                      const name = e.target.value;
                      setCommand(name);
                      setConfirmText("");
                      const target = runnerForCommand(runnerList, name, runner?.id);
                      if (target && target.id !== runner?.id) setRunnerId(String(target.id));
                    }}
                  >
                    {commands.map((c) => (
                      <option key={c.name} value={c.name}>
                        {c.name}
                        {c.confirm ? " ⚠" : ""}
                        {runner && !c.runners.includes(runner.name) ? ` · on ${c.runners.join(", ")}` : ""}
                      </option>
                    ))}
                  </Select>
                ) : (
                  <p id={id} className="text-[13px] text-fg-3">
                    {runner?.capabilities.commands.length
                      ? `None of ${runner.name}'s commands apply to ${repoName ?? "this repo"}.`
                      : "This runner advertises no commands."}
                  </p>
                )
              }
            </Field>
            {needsConfirm ? (
              <div className="space-y-2 rounded-lg border border-warning/50 bg-warning/10 p-3">
                <p className="flex items-start gap-2 text-[13px]">
                  <ShieldAlert className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden />
                  <span>
                    <strong className="font-semibold">{effectiveCommand}</strong> is destructive or reaches outside this
                    machine (a deploy, a store upload). It runs in <span className="font-mono">{repoName}</span> on{" "}
                    {runRunner?.name}.
                  </span>
                </p>
                <Field label={`Type ${effectiveCommand} to confirm`}>
                  {(id) => (
                    <Input
                      id={id}
                      value={confirmText}
                      onChange={(e) => setConfirmText(e.target.value)}
                      autoComplete="off"
                      spellCheck={false}
                      className="font-mono"
                      aria-invalid={confirmText.length > 0 && !confirmed}
                    />
                  )}
                </Field>
              </div>
            ) : null}
          </div>
        )}

        {!resume ? (
          <Field label="Task" hint="Optional — links the run to a task.">
            {(id, desc) => (
              <Select id={id} aria-describedby={desc} value={taskId} onChange={(e) => setTaskId(e.target.value)}>
                <option value="">None</option>
                {prefill.task_id && !tasks.data?.some((t) => t.id === prefill.task_id) ? (
                  <option value={prefill.task_id}>{prefill.task_ref ?? `#${prefill.task_id}`}</option>
                ) : null}
                {tasks.data?.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.ref} · {t.title}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        ) : null}
      </form>
    </Dialog>
  );
}
