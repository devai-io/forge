// Settings → Agent engine: which backend an agent run's Claude Code talks to
// when the run does not pick one. DeepSeek speaks Anthropic's API, so the
// same claude binary runs against it for a fraction of the price; Claude is
// then only used for the runs someone explicitly queues on Claude.

import { useState } from "react";
import { useEngine, useSetDeepseekKey, useUpdateEngine } from "@/api/hooks";
import type { Engine, EngineSettings } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { Segmented } from "@/components/ui/Tabs";
import { useToast } from "@/components/ui/Toast";

export function EnginePanel() {
  const engine = useEngine();
  const update = useUpdateEngine();
  const setKey = useSetDeepseekKey();
  const toast = useToast();
  const [key, setKeyText] = useState("");
  const [models, setModels] = useState<Pick<EngineSettings, "model" | "heavy_model"> | null>(null);

  const save = (patch: Partial<EngineSettings>, done?: () => void) =>
    update.mutate(patch, { onSuccess: () => done?.(), onError: (e) => toast.error(e) });

  return (
    <Panel title="Agent engine" id="settings-engine" bodyClassName="space-y-4 p-4">
      {engine.isPending ? (
        <SkeletonRows rows={3} />
      ) : engine.error ? (
        <p className="text-[13px] text-critical-ink">Could not load: {String(engine.error)}</p>
      ) : (
        <>
          <p className="text-[13px] text-fg-2">
            Agent runs start Claude Code on your machines. On DeepSeek it talks to DeepSeek's Anthropic-compatible API
            instead of Anthropic, at a fraction of the price. A run can still pick Claude when you queue it.
          </p>
          <div className="flex flex-wrap items-center gap-3">
            <Segmented
              label="Default engine"
              value={engine.data.settings.default}
              onChange={(v: Engine) => save({ default: v })}
              items={[
                { value: "deepseek", label: "DeepSeek" },
                { value: "claude", label: "Claude" },
              ]}
            />
            {engine.data.deepseek_key ? (
              <Badge tone="good">DeepSeek key in the vault</Badge>
            ) : (
              <Badge tone="warning">no DeepSeek key: runs fall back to Claude</Badge>
            )}
          </div>

          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (models) save(models, () => setModels(null));
            }}
          >
            <Field label="DeepSeek model" hint="Ordinary runs." className="min-w-48 flex-1">
              {(id, desc) => (
                <Input
                  id={id}
                  aria-describedby={desc}
                  spellCheck={false}
                  value={models?.model ?? engine.data.settings.model}
                  onChange={(e) => setModels({ ...(models ?? engine.data.settings), model: e.target.value })}
                />
              )}
            </Field>
            <Field label="Heavy model" hint="When Jev judges a task heavy." className="min-w-48 flex-1">
              {(id, desc) => (
                <Input
                  id={id}
                  aria-describedby={desc}
                  spellCheck={false}
                  value={models?.heavy_model ?? engine.data.settings.heavy_model}
                  onChange={(e) => setModels({ ...(models ?? engine.data.settings), heavy_model: e.target.value })}
                />
              )}
            </Field>
            <Button type="submit" variant="subtle" className="mb-6" loading={update.isPending} disabled={!models}>
              Save models
            </Button>
          </form>

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
              label={engine.data.deepseek_key ? "Replace the DeepSeek API key" : "DeepSeek API key"}
              hint="Sealed in the vault (tag integration:deepseek). Without it, the Assistant's key is used when the Assistant talks to DeepSeek. Machines receive it with each DeepSeek run."
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
                  placeholder="sk-…"
                />
              )}
            </Field>
            <Button type="submit" variant="primary" className="mb-6" loading={setKey.isPending} disabled={key.trim().length < 16}>
              Save key
            </Button>
          </form>
        </>
      )}
    </Panel>
  );
}
