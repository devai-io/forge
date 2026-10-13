// Settings → Token saving: Jev, TypeSafe's decision model asked through the
// provider (OpenRouter, with its key), answering routine judgements so Claude
// does not spend tokens on them. Everything is off until enabled, and each
// use fails open (no Jev = Forge as before).

import { useJev, useTestJev, useUpdateJev } from "@/api/hooks";
import type { JevSettings } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Switch } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";

const USES: { key: "routing" | "context" | "match"; label: string; hint: string }[] = [
  {
    key: "routing",
    label: "Pick the model for agent runs",
    hint: "A run queued without a model: on DeepSeek, a task Jev is confident is heavy goes to the heavy model (the rest stay on the fast one); on Claude, light or ordinary tasks go to Haiku or Sonnet and anything else keeps the machine's default.",
  },
  {
    key: "context",
    label: "Trim the context Claude sessions start with",
    hint: "With more than 8 open tasks, a session lists only the ones relevant to its repo (focus, in-progress, blocked, urgent and the repo's own tasks always stay).",
  },
  {
    key: "match",
    label: "Match what you ask Claude Code to your tasks",
    hint: "Each request typed into Claude Code (with the Forge hook installed) is checked against the project's open tasks; the likely ones are added to the conversation once per session. Off: shared keywords decide instead.",
  },
];

export function TokenSavingPanel() {
  const jev = useJev();
  const update = useUpdateJev();
  const test = useTestJev();
  const toast = useToast();

  const save = (patch: Partial<JevSettings>) => update.mutate(patch, { onError: (e) => toast.error(e) });

  return (
    <Panel title="Token saving (Jev)" id="settings-jev" bodyClassName="space-y-4 p-4">
      {jev.isPending ? (
        <SkeletonRows rows={3} />
      ) : jev.error ? (
        <p className="text-[13px] text-critical-ink">Could not load: {String(jev.error)}</p>
      ) : (
        <>
          <p className="text-[13px] text-fg-2">
            Jev is TypeSafe's decision model ({jev.data.settings.model} on OpenRouter, paid with the provider key): it answers
            yes/no and pick-one questions quickly for a fraction of the tokens, so routine judgements do not use Claude.
          </p>
          <div className="flex flex-wrap items-center gap-3">
            <Switch
              checked={jev.data.settings.enabled}
              onChange={(v) => save({ enabled: v })}
              label="Use Jev"
              disabled={!jev.data.key_configured && !jev.data.settings.enabled}
            />
            {jev.data.key_configured ? (
              <Badge tone="good">OpenRouter key in the vault</Badge>
            ) : (
              <Badge tone="warning">no OpenRouter key yet (AI provider, above)</Badge>
            )}
            {jev.data.key_configured ? (
              <Button
                size="sm"
                variant="subtle"
                loading={test.isPending}
                onClick={() =>
                  test.mutate(undefined, {
                    onSuccess: (r) => (r.ok ? toast.success(`Jev answered in ${r.ms} ms`) : toast.error(r.error ?? "Jev did not answer")),
                    onError: (e) => toast.error(e),
                  })
                }
              >
                Test
              </Button>
            ) : null}
          </div>

          <ul className="space-y-3">
            {USES.map((u) => (
              <li key={u.key}>
                <Switch
                  checked={jev.data.settings[u.key]}
                  onChange={(v) => save({ [u.key]: v })}
                  label={u.label}
                  disabled={!jev.data.settings.enabled}
                />
                <p className="mt-0.5 pl-11 text-[12px] text-fg-3">{u.hint}</p>
              </li>
            ))}
          </ul>

          <p className="text-[12px] text-fg-3">
            Since the server started: <span className="tabular text-fg-2">{jev.data.stats.calls}</span> calls,{" "}
            <span className="tabular text-fg-2">{jev.data.stats.input_tokens.toLocaleString()}</span> input tokens
            {jev.data.stats.errors ? `, ${jev.data.stats.errors} failed` : ""}
            {jev.data.stats.last_at ? (
              <>
                {" "}
                · last <RelativeTime iso={jev.data.stats.last_at} />
              </>
            ) : null}
            {jev.data.stats.last_error ? <span className="block text-critical-ink">Last error: {jev.data.stats.last_error}</span> : null}
          </p>
        </>
      )}
    </Panel>
  );
}
