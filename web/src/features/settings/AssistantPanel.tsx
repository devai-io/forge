// Settings → Assistant: on/off, the engine a new chat starts on (Claude Code
// on the master machine by default, or DeepSeek through the provider), and
// the DeepSeek engine's model. The key is the provider's (Settings → AI
// provider).

import { useRef, useState } from "react";
import { useAssistant, useUpdateAssistant } from "@/api/hooks";
import type { AssistantSettings, AssistantStatus, ChatEngine } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Field, Input, Select, Switch } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { Segmented } from "@/components/ui/Tabs";
import { useToast } from "@/components/ui/Toast";

export function AssistantPanel() {
  const assistant = useAssistant();
  return (
    <Panel title="Assistant" id="settings-assistant" bodyClassName="space-y-4 p-4">
      {assistant.isPending ? (
        <SkeletonRows rows={3} />
      ) : assistant.error ? (
        <p className="text-[13px] text-critical-ink">Could not load: {String(assistant.error)}</p>
      ) : (
        <AssistantForm status={assistant.data} />
      )}
    </Panel>
  );
}

function AssistantForm({ status }: { status: AssistantStatus }) {
  const update = useUpdateAssistant();
  const toast = useToast();
  const [model, setModel] = useState(status.settings.model);
  // Blur and Enter can both fire for one edit; send each value once.
  const sent = useRef<string | undefined>(undefined);

  const save = (patch: Partial<AssistantSettings>) => update.mutate(patch, { onError: (e) => toast.error(e) });
  const saveModel = (value: string) => {
    const v = value.trim();
    if (!v || v === status.settings.model || sent.current === v) return;
    sent.current = v;
    update.mutate(
      { model: v },
      {
        onSuccess: () => toast.success("Model saved"),
        onError: (e) => {
          sent.current = undefined;
          toast.error(e);
        },
      },
    );
  };

  // The provider's DeepSeek models, plus the saved model if the list does not carry it.
  const listed = status.models.filter((m) => m.startsWith("deepseek/"));
  const models = listed.length && !listed.includes(status.settings.model) && status.settings.model
    ? [status.settings.model, ...listed]
    : listed;
  const claude = status.engines.claude;

  return (
    <>
      <p className="text-[13px] text-fg-2">
        A chat that can read and update Forge and delegate work to Claude Code on your machines. On Claude it is a Claude
        Code session on the master machine, with its login (your subscription); on DeepSeek the server runs it through
        OpenRouter with the provider key.
      </p>
      <div className="flex flex-wrap items-center gap-3">
        <Switch checked={status.settings.enabled} onChange={(v) => save({ enabled: v })} label="Use the assistant" />
        <Segmented
          label="New chats start on"
          value={status.settings.engine}
          onChange={(v: ChatEngine) => save({ engine: v })}
          items={[
            { value: "claude", label: "Claude" },
            { value: "openrouter", label: "DeepSeek (OpenRouter)" },
          ]}
        />
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {claude.available ? (
          <Badge tone="good">Claude Code on {claude.machine}</Badge>
        ) : (
          <Badge tone="warning">Claude: {claude.reason || "no machine"}</Badge>
        )}
        {status.key_configured ? (
          <Badge tone="good">OpenRouter key in the vault</Badge>
        ) : (
          <Badge tone="warning">no OpenRouter key: DeepSeek is off</Badge>
        )}
      </div>

      <Field
        label="DeepSeek model"
        hint={listed.length ? "As OpenRouter lists them for this key." : "The provider did not list its models; type the id."}
      >
        {(id, desc) =>
          models.length ? (
            <Select
              id={id}
              aria-describedby={desc}
              className="sm:w-80"
              value={status.settings.model}
              onChange={(e) => save({ model: e.target.value })}
            >
              {models.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </Select>
          ) : (
            <Input
              id={id}
              aria-describedby={desc}
              className="sm:w-80"
              spellCheck={false}
              value={model}
              onChange={(e) => setModel(e.target.value)}
              onBlur={() => saveModel(model)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  saveModel(model);
                }
              }}
              placeholder="deepseek/deepseek-v4-flash"
            />
          )
        }
      </Field>
    </>
  );
}
