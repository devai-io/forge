// What answers the chat, above the composer: the engine (the DeepSeek API
// loop on the server, or a Claude Code session on the master machine), its
// model and effort, and — for Claude Code — whether it may make changes
// (edit files, update tasks; otherwise it only reads) and, where the
// machine allows bypassPermissions, whether it skips permission prompts.
// Skipping prompts wins over "can make changes" on the server, so it is only
// offered once changes are on (and turning changes off turns it off too).
// Changes apply from the next message.

import clsx from "clsx";
import { WifiOff } from "lucide-react";
import { Link } from "react-router-dom";
import { useAssistant } from "@/api/hooks";
import type { ChatEngine, ChatSettings } from "@/api/types";
import { Input, Select, Switch } from "@/components/ui/Input";
import { EFFORT_LABEL, ENGINE_NAME } from "@/lib/assistant";
import { engineUp } from "./engines";

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
  const codex = engines?.codex;
  const current = engines?.[value.engine];
  const local = value.engine === "codex" ? codex : claude;

  const models = [...(current?.models ?? [])];
  if (value.model && !models.includes(value.model)) models.unshift(value.model);
  const defaultModel = value.engine === "deepseek" && status.data?.settings.model ? ` (${status.data.settings.model})` : "";
  const efforts = current?.efforts ?? [];
  // The engine's machine is down: say so above the composer, and whether another engine can answer instead.
  const offlineMachine = value.engine !== "deepseek" && local?.online === false ? local.machine || "The master" : null;
  const alternative = (["claude", "codex", "deepseek"] as const).some((e) => e !== value.engine && engineUp(engines, e));
  const skipping = !!value.skip_permissions;

  return (
    <>
      {offlineMachine ? (
        <p role="status" className="mb-1.5 flex items-center gap-2 rounded-lg border border-warning/40 bg-warning/10 px-2.5 py-1.5 text-[12.5px]">
          <WifiOff className="size-3.5 shrink-0 text-warning" aria-hidden />
          <span className="min-w-0 flex-1">
            {offlineMachine} is offline{alternative ? " — pick another engine" : ""}
          </span>
          <Link to="/machines" className="shrink-0 font-medium text-accent hover:underline">
            Machines →
          </Link>
        </p>
      ) : null}
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
          <option value="claude" disabled={claude ? !claude.available : false}>
            {ENGINE_NAME.claude}
            {claude?.machine ? ` · ${claude.machine}` : ""}
            {claude && !claude.available ? ` (${claude.reason})` : claude?.online === false ? " (offline)" : ""}
          </option>
          <option value="codex" disabled={!codex?.available}>
            Codex{codex?.machine ? ` · ${codex.machine}` : ""}{codex && !codex.available ? ` (${codex.reason})` : ""}
          </option>
          <option value="deepseek" disabled={deepseek ? !deepseek.available : false}>
            {ENGINE_NAME.deepseek}
            {deepseek && !deepseek.available ? " (no key)" : ""}
          </option>
        </Select>
        {value.engine === "codex" ? <Input aria-label="Model" className="w-44" placeholder="Default Codex model" value={value.model} disabled={disabled} onChange={(e) => onChange({ model: e.target.value })} /> : <Select compact aria-label="Model" className="w-auto" value={value.model} disabled={disabled} onChange={(e) => onChange({ model: e.target.value })}>
          <option value="">Default model{defaultModel}</option>
          {models.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </Select>}
        <Select compact aria-label="Effort" className="w-auto" value={value.effort} disabled={disabled} onChange={(e) => onChange({ effort: e.target.value })}>
          <option value="">Default effort</option>
          {efforts.map((e) => (
            <option key={e} value={e}>
              {EFFORT_LABEL[e] ?? e}
            </option>
          ))}
        </Select>
        {value.engine !== "deepseek" ? (
          <Switch
            checked={value.edits}
            // Skipping prompts would still let it change things: it goes off with changes.
            onChange={(v) => onChange(!v && skipping ? { edits: v, skip_permissions: false } : { edits: v })}
            label="Can make changes"
            disabled={disabled}
          />
        ) : null}
        {/* Offered only with changes on; still shown while on (an older chat may have it without changes) so it can be turned off. */}
        {value.engine !== "deepseek" && ((value.edits && local?.skip_permissions) || skipping) ? (
          <span className={clsx("rounded-md", skipping && "border border-warning/50 bg-warning/12 px-1.5 py-0.5")}>
            <Switch
              checked={skipping}
              onChange={(v) => onChange({ skip_permissions: v })}
              label={
                <span title="Every tool runs without asking; requires this machine’s explicit permission" className={clsx(skipping && "font-medium")}>
                  Skip permissions{skipping ? " — no prompts at all" : ""}
                </span>
              }
              disabled={disabled}
            />
          </span>
        ) : null}
      </div>
    </>
  );
}
