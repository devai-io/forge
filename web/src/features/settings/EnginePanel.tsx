// Settings → Agent engine: which backend an agent run's Claude Code talks to
// when the run does not pick one. Claude is Claude Code with the machine's
// own login (the subscription); DeepSeek is the same claude binary pointed
// at the provider's Anthropic-compatible endpoint (OpenRouter), paid with
// the provider key. Also how many runs each machine works on at once.

import { useState } from "react";
import { useEngine, useFocusUsage, useUpdateEngine } from "@/api/hooks";
import type { Engine, EngineSettings } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { Segmented } from "@/components/ui/Tabs";
import { useToast } from "@/components/ui/Toast";
import { formatSpend } from "@/lib/assistant";

export function EnginePanel() {
  const engine = useEngine();
  const focusUsage = useFocusUsage();
  const update = useUpdateEngine();
  const toast = useToast();
  const [models, setModels] = useState<Pick<EngineSettings, "model" | "heavy_model"> | null>(null);
  const [maxRuns, setMaxRuns] = useState<string | null>(null);
  const maxRunsValue = Number(maxRuns);
  const maxRunsValid = maxRuns !== null && Number.isInteger(maxRunsValue) && maxRunsValue >= 1 && maxRunsValue <= 16;

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
            Agent runs start Claude Code or Codex on your machines using their local login.
            Codex supports your eligible ChatGPT plan or an API key. DeepSeek uses Claude Code through OpenRouter,
            paid with the provider key. Each run can choose its engine.
          </p>
          <p className="text-[13px] text-fg-3">To connect Codex on a paired machine, run <code>codex login</code>, then <code>forge agent setup-codex</code> and restart the Forge agent.</p>
          <div className="flex flex-wrap items-center gap-3">
            <Segmented
              label="Default engine"
              value={engine.data.settings.default}
              onChange={(v: Engine) => save({ default: v })}
              items={[
                { value: "claude", label: "Claude" },
                { value: "codex", label: "Codex" },
                { value: "deepseek", label: "DeepSeek (OpenRouter)" },
              ]}
            />
            {engine.data.provider_key ? (
              <Badge tone="good">OpenRouter key in the vault</Badge>
            ) : (
              <Badge tone="warning">no OpenRouter key: DeepSeek runs fall back to Claude</Badge>
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
                  list="deepseek-models"
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
                  list="deepseek-models"
                  value={models?.heavy_model ?? engine.data.settings.heavy_model}
                  onChange={(e) => setModels({ ...(models ?? engine.data.settings), heavy_model: e.target.value })}
                />
              )}
            </Field>
            {/* Suggestions: the DeepSeek models the provider lists, and the known ones. */}
            <datalist id="deepseek-models">
              {(engine.data.models?.deepseek ?? []).map((m) => (
                <option key={m} value={m} />
              ))}
            </datalist>
            <Button type="submit" variant="subtle" className="mb-6" loading={update.isPending} disabled={!models}>
              Save models
            </Button>
          </form>

          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (maxRunsValid) save({ max_concurrent: maxRunsValue }, () => setMaxRuns(null));
            }}
          >
            <Field
              label="Concurrent runs per machine"
              hint="How many runs each machine works on at once; an interactive run holds its place while it waits for you. A machine's agent.json max_concurrent can only lower it."
              error={maxRuns !== null && !maxRunsValid ? "A whole number from 1 to 16" : undefined}
              className="w-72"
            >
              {(id, desc) => (
                <Input
                  id={id}
                  aria-describedby={desc}
                  type="number"
                  min={1}
                  max={16}
                  step={1}
                  value={maxRuns ?? String(engine.data.settings.max_concurrent)}
                  onChange={(e) => setMaxRuns(e.target.value)}
                />
              )}
            </Field>
            <Button type="submit" variant="subtle" className="mb-6" loading={update.isPending} disabled={!maxRunsValid}>
              Save
            </Button>
          </form>

          {focusUsage.data && focusUsage.data.usage.length ? (
            <p className="text-[12.5px] text-fg-3">
              Project focus (the DeepSeek model above writes it) has cost{" "}
              <span className="tabular font-medium text-fg-2">{formatSpend(focusUsage.data.total_usd)}</span> over{" "}
              {focusUsage.data.usage.reduce((n, e) => n + e.calls, 0)} calls
              {focusUsage.data.unpriced ? ", plus calls without a price" : ""}.
            </p>
          ) : null}
        </>
      )}
    </Panel>
  );
}
