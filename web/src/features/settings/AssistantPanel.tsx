// Settings → Assistant: the provider (any OpenAI-compatible API, DeepSeek by
// default), the model, and the key. The key goes into the vault like Jev's and
// is never shown again; the switch stays off until one is stored.

import { useRef, useState } from "react";
import { useAssistant, useSetAssistantKey, useUpdateAssistant } from "@/api/hooks";
import type { AssistantSettings, AssistantStatus } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Field, Input, Select, Switch } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";

const DEFAULT_BASE_URL = "https://api.deepseek.com";

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
  const setKey = useSetAssistantKey();
  const toast = useToast();
  const [key, setKeyText] = useState("");
  const [baseUrl, setBaseUrl] = useState(status.settings.base_url || DEFAULT_BASE_URL);
  const [model, setModel] = useState(status.settings.model);
  // Blur and the Save button can both fire for one edit; send each value once.
  const sent = useRef<Partial<AssistantSettings>>({});

  const save = (patch: Partial<AssistantSettings>) => update.mutate(patch, { onError: (e) => toast.error(e) });
  const saveText = (field: "base_url" | "model", value: string) => {
    const v = value.trim();
    if (!v || v === status.settings[field] || sent.current[field] === v) return;
    sent.current[field] = v;
    update.mutate(
      { [field]: v },
      {
        onSuccess: () => toast.success(field === "model" ? "Model saved" : "Provider saved"),
        onError: (e) => {
          sent.current[field] = undefined;
          toast.error(e);
        },
      },
    );
  };

  // The provider's list, plus the saved model if the list does not carry it.
  const models = status.models.length && !status.models.includes(status.settings.model) && status.settings.model
    ? [status.settings.model, ...status.models]
    : status.models;

  return (
    <>
      <p className="text-[13px] text-fg-2">
        A chat that can read and update Forge and delegate work to Claude Code on your machines. Uses an OpenAI-compatible
        API (DeepSeek by default).
      </p>
      <div className="flex flex-wrap items-center gap-3">
        <Switch
          checked={status.settings.enabled}
          onChange={(v) => save({ enabled: v })}
          label="Use the assistant"
          disabled={!status.key_configured && !status.settings.enabled}
        />
        {status.key_configured ? <Badge tone="good">key in the vault</Badge> : <Badge tone="warning">no key yet</Badge>}
      </div>

      <form
        className="flex flex-wrap items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          saveText("base_url", baseUrl);
        }}
      >
        <Field label="Provider API base URL" hint="Any OpenAI-compatible endpoint." className="min-w-64 flex-1">
          {(id, desc) => (
            <Input
              id={id}
              aria-describedby={desc}
              type="url"
              inputMode="url"
              spellCheck={false}
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              onBlur={() => saveText("base_url", baseUrl)}
              placeholder={DEFAULT_BASE_URL}
            />
          )}
        </Field>
        <Button
          type="submit"
          className="mb-6"
          disabled={!baseUrl.trim() || baseUrl.trim() === status.settings.base_url}
          loading={update.isPending && update.variables?.base_url !== undefined}
        >
          Save
        </Button>
      </form>

      <Field
        label="Model"
        hint={status.models.length ? "As listed by the provider for this key." : "The provider did not list its models; type the name."}
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
              onBlur={() => saveText("model", model)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  saveText("model", model);
                }
              }}
              placeholder="deepseek-flash"
            />
          )
        }
      </Field>

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
          label={status.key_configured ? "Replace the provider API key" : "Provider API key"}
          hint="Sealed in the vault (tag integration:assistant); never shown again."
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
              placeholder="Paste the key"
            />
          )}
        </Field>
        <Button type="submit" variant="primary" className="mb-6" loading={setKey.isPending} disabled={key.trim().length < 16}>
          Save key
        </Button>
      </form>
    </>
  );
}
