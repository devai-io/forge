// Settings → Token saving: Jev, TypeSafe's small decision model, answering
// routine judgements so Claude does not spend tokens on them. Everything is
// off until enabled, and each use fails open (no Jev = Forge as before).

import { useState } from "react";
import { useJev, useSetJevKey, useTestJev, useUpdateJev } from "@/api/hooks";
import type { JevSettings } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Field, Input, Switch } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";

const USES: { key: keyof Omit<JevSettings, "enabled">; label: string; hint: string }[] = [
  {
    key: "routing",
    label: "Pick the model for agent runs",
    hint: "A run queued without a model goes to Haiku or Sonnet when Jev is confident the task is light or ordinary; anything else keeps the machine's default.",
  },
  {
    key: "context",
    label: "Trim the context Claude sessions start with",
    hint: "With more than 8 open tasks, a session lists only the ones relevant to its repo (focus, in-progress, blocked, urgent and the repo's own tasks always stay).",
  },
  {
    key: "compaction",
    label: "Jev compaction in Claude Code on every machine",
    hint: "Each machine installs the fast-jev-compaction plugin (pinned by Forge) and gets the key, so long sessions drop tool output that no longer matters instead of summarising.",
  },
];

export function TokenSavingPanel() {
  const jev = useJev();
  const update = useUpdateJev();
  const setKey = useSetJevKey();
  const test = useTestJev();
  const toast = useToast();
  const [key, setKeyText] = useState("");

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
            Jev is TypeSafe's small decision model: it answers yes/no and pick-one questions in a fraction of a second for a
            fraction of the tokens, so routine judgements do not use Claude.
          </p>
          <div className="flex flex-wrap items-center gap-3">
            <Switch
              checked={jev.data.settings.enabled}
              onChange={(v) => save({ enabled: v })}
              label="Use Jev"
              disabled={!jev.data.key_configured && !jev.data.settings.enabled}
            />
            {jev.data.key_configured ? (
              <Badge tone="good">key in the vault</Badge>
            ) : (
              <Badge tone="warning">no key yet</Badge>
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

          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              setKey.mutate(key.trim(), {
                onSuccess: () => {
                  setKeyText("");
                  toast.success("Key stored in the vault");
                },
                onError: (err) => toast.error(err),
              });
            }}
          >
            <Field
              label={jev.data.key_configured ? "Replace the TypeSafe API key" : "TypeSafe API key"}
              hint="Sealed in the vault (tag integration:jev); never shown again. Machines get it only when compaction is on."
              className="min-w-64 flex-1"
            >
              {(id, desc) => (
                <Input
                  id={id}
                  aria-describedby={desc}
                  type="password"
                  autoComplete="off"
                  spellCheck={false}
                  value={key}
                  onChange={(e) => setKeyText(e.target.value)}
                  placeholder="apikey_…"
                />
              )}
            </Field>
            <Button type="submit" variant="primary" className="mb-6" loading={setKey.isPending} disabled={key.trim().length < 16}>
              Save key
            </Button>
          </form>

          <p className="text-[12px] text-fg-3">
            Since the server started: <span className="tabular text-fg-2">{jev.data.stats.calls}</span> calls,{" "}
            <span className="tabular text-fg-2">{jev.data.stats.input_tokens.toLocaleString()}</span> Jev input tokens
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
