// Chat cache helpers for the assistant hooks: merging polled pages of
// messages and keeping the chat list current. Kept apart from lib/assistant
// (the display side) because hooks.ts is in the main bundle and this is all
// of the assistant it needs.

import type { Chat, ChatMessage } from "@/api/types";

export function lastSeq(messages: ChatMessage[] | undefined): number {
  return messages?.length ? messages[messages.length - 1].seq : 0;
}

/**
 * Append a polled page to what is already held. Pages can overlap (a POST's
 * answer and a poll racing each other), so seq decides: one copy of each, the
 * newer copy wins, and the result stays in seq order.
 */
export function mergeMessages(prev: ChatMessage[], incoming: ChatMessage[]): ChatMessage[] {
  if (!incoming.length) return prev;
  const bySeq = new Map<number, ChatMessage>();
  for (const m of prev) bySeq.set(m.seq, m);
  for (const m of incoming) bySeq.set(m.seq, m);
  return Array.from(bySeq.values()).sort((a, b) => a.seq - b.seq);
}

/** Replace a chat in the list, or put a new one first (the list is newest first). */
export function upsertChat(list: Chat[], chat: Chat): Chat[] {
  return list.some((c) => c.id === chat.id) ? list.map((c) => (c.id === chat.id ? chat : c)) : [chat, ...list];
}
