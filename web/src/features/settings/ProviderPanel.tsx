// Settings → AI provider: one OpenAI-compatible gateway (OpenRouter by
// default) and one key for everything Forge pays for itself — the
// Assistant's DeepSeek engine, agent runs on DeepSeek, and Jev. Claude needs
// no key: it is Claude Code as logged in on your machines.

import { useState } from "react";
import { useProvider, useSetProviderKey, useTestProvider, useUpdateProvider } from "@/api/hooks";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";

export function ProviderPanel() {
  const provider = useProvider();
  const update = useUpdateProvider();
  const setKey = useSetProviderKey();
  const test = useTestProvider();
  const toast = useToast();
  const [key, setKeyText] = useState("");
  const [baseUrl, setBaseUrl] = useState<string | null>(null);

  return (
    <Panel title="AI provider" id="settings-provider" bodyClassName="space-y-4 p-4">
      {provider.isPending ? (
        <SkeletonRows rows={3} />
      ) : provider.error ? (
        <p className="text-[13px] text-critical-ink">Could not load: {String(provider.error)}</p>
      ) : (
        <>
          <p className="text-[13px] text-fg-2">
            One OpenRouter key pays for the DeepSeek engine (Assistant and agent runs) and for Jev. Claude needs no key:
            it is Claude Code with the login on your machines.
          </p>
          <div className="flex flex-wrap items-center gap-3">
            {provider.data.key_configured ? (
              <Badge tone="good">key in the vault</Badge>
            ) : (
              <Badge tone="warning">no key yet: DeepSeek and Jev are off</Badge>
            )}
            <Button
              variant="subtle"
              disabled={!provider.data.key_configured}
              loading={test.isPending}
              onClick={() =>
                test.mutate(undefined, {
                  onSuccess: (r) =>
                    r.ok
                      ? toast.success(`${r.model} answered in ${r.ms} ms`)
                      : toast.error(new Error(r.error ?? "the call failed")),
                  onError: (e) => toast.error(e),
                })
              }
            >
              Test the key
            </Button>
          </div>

          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (baseUrl === null) return;
              update.mutate(
                { base_url: baseUrl.trim() },
                {
                  onSuccess: () => {
                    setBaseUrl(null);
                    toast.success("Provider saved");
                  },
                  onError: (err) => toast.error(err),
                },
              );
            }}
          >
            <Field
              label="API base URL"
              hint={`OpenAI-compatible; Claude Code gets ${provider.data.anthropic_url} for DeepSeek runs.`}
              className="min-w-64 flex-1"
            >
              {(id, desc) => (
                <Input
                  id={id}
                  aria-describedby={desc}
                  type="url"
                  inputMode="url"
                  spellCheck={false}
                  value={baseUrl ?? provider.data.settings.base_url}
                  onChange={(e) => setBaseUrl(e.target.value)}
                  placeholder={provider.data.default_url}
                />
              )}
            </Field>
            <Button
              type="submit"
              variant="subtle"
              className="mb-6"
              loading={update.isPending}
              disabled={baseUrl === null || baseUrl.trim() === provider.data.settings.base_url}
            >
              Save
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
              label={provider.data.key_configured ? "Replace the OpenRouter API key" : "OpenRouter API key"}
              hint="Sealed in the vault (tag integration:openrouter); never shown again. Machines receive it with each DeepSeek run."
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
                  placeholder="sk-or-…"
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
