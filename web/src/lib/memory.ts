import type { Memory, MemoryKind } from "@/api/types";

export const KIND_LABELS: Record<MemoryKind, string> = {
  fact: "Fact",
  decision: "Decision",
  convention: "Convention",
  gotcha: "Gotcha",
  preference: "Preference",
  howto: "How-to",
};

export const KIND_HINTS: Record<MemoryKind, string> = {
  fact: "Something true about the project",
  decision: "What was decided, and why",
  convention: "How things are done here",
  gotcha: "A trap someone fell into",
  preference: "How you like things done",
  howto: "Steps to build, test, deploy…",
};

/** Who saved a memory, for its meta line. */
export function memorySource(m: Pick<Memory, "source" | "origin">): string {
  switch (m.source) {
    case "claude":
      return m.origin || "Claude";
    case "assistant":
      return "the Assistant";
    case "telegram":
      return "you, on Telegram";
    default:
      return "you";
  }
}

/** The message after a save: duplicate, or how many similar ones exist. */
export function savedMessage(duplicate: boolean, similar: number): string {
  if (duplicate) return "Already remembered word for word";
  if (similar === 0) return "Remembered";
  return `Remembered — ${similar} similar ${similar === 1 ? "memory" : "memories"} below, merge if they overlap`;
}
