// Which chat engines can answer right now — shared by the new-chat default and
// the settings bar's "machine is offline" notice.

import type { AssistantStatus, ChatEngine, ChatSettings } from "@/api/types";

/** The engine can answer now: available, and (Claude, Codex) its machine is not known to be offline. */
export function engineUp(engines: AssistantStatus["engines"] | undefined, engine: ChatEngine): boolean {
  if (engine === "deepseek") return !!engines?.deepseek.available;
  const e = engines?.[engine];
  return !!e?.available && e.online !== false;
}

/**
 * What a new chat starts with: the configured engine (always Claude for a
 * bot) unless it cannot answer and another can — preferring one whose
 * machine is online, then any available one. Read-only, and so without
 * skipping prompts: that wins over "can make changes" on the server.
 */
export function newChatSettings(status: AssistantStatus | undefined, bot = false): ChatSettings {
  const configured: ChatEngine = bot ? "claude" : (status?.settings.engine ?? "claude");
  const order = [configured, "claude", "codex", "deepseek"] as const;
  const engines = status?.engines;
  const engine = order.find((e) => engineUp(engines, e)) ?? order.find((e) => engines?.[e]?.available) ?? configured;
  return { engine, model: "", effort: "", edits: false, skip_permissions: false };
}
