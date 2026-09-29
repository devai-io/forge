// What answers the chat, above the composer: the engine (the DeepSeek API
// loop on the server, or a Claude Code session on the master machine), its
// model and effort, and — for Claude Code — whether it may edit files.
// Changes apply from the next message.

import { useAssistant } from "@/api/hooks";
import type { ChatEngine, ChatSettings } from "@/api/types";
import { Select, Switch } from "@/components/ui/Input";
import { EFFORT_LABEL, ENGINE_NAME } from "@/lib/assistant";

export function ChatSettingsBar({
  value,
  onChange,
  disabled,
}: {
  value: ChatSettings;
  onChange: (patch: Partial<ChatSettings>) => void;
  disabled?: boolean;
}) {
  const status = useAssistant();
  const engines = status.data?.engines;
  const deepseek = engines?.deepseek;
  const claude = engines?.claude;
  const current = value.engine === "claude" ? claude : deepseek;

  const models = [...(current?.models ?? [])];
  if (value.model && !models.includes(value.model)) models.unshift(value.model);
  const defaultModel = value.engine === "deepseek" && status.data?.settings.model ? ` (${status.data.settings.model})` : "";
  const efforts = current?.efforts ?? [];

  return (
    <div className="mb-1.5 flex flex-wrap items-center gap-1.5" aria-label="Chat settings" role="group">
      <Select
        compact
        aria-label="Engine"
        className="w-auto"
        value={value.engine}
        disabled={disabled}
        // A new engine starts from its own defaults.
        onChange={(e) => onChange({ engine: e.target.value as ChatEngine, model: "", effort: "" })}
      >
        <option value="deepseek" disabled={deepseek ? !deepseek.available : false}>
          {ENGINE_NAME.deepseek}
          {deepseek && !deepseek.available ? " (no key)" : ""}
        </option>
        <option value="claude" disabled={claude ? !claude.available : false}>
          {ENGINE_NAME.claude}
          {claude?.machine ? ` · ${claude.machine}` : ""}
          {claude && !claude.available ? ` (${claude.reason})` : claude?.online === false ? " (offline)" : ""}
        </option>
      </Select>
      <Select compact aria-label="Model" className="w-auto" value={value.model} disabled={disabled} onChange={(e) => onChange({ model: e.target.value })}>
        <option value="">Default model{defaultModel}</option>
        {models.map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
      </Select>
      <Select compact aria-label="Effort" className="w-auto" value={value.effort} disabled={disabled} onChange={(e) => onChange({ effort: e.target.value })}>
        <option value="">Default effort</option>
        {efforts.map((e) => (
          <option key={e} value={e}>
            {EFFORT_LABEL[e] ?? e}
          </option>
        ))}
      </Select>
      {value.engine === "claude" ? (
        <Switch checked={value.edits} onChange={(v) => onChange({ edits: v })} label="Can edit files" disabled={disabled} />
      ) : null}
    </div>
  );
}
