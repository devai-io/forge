// The Assistant: a chat with an LLM that reads and updates Forge and hands
// work to Claude Code on your machines. The chat list sits beside the
// conversation from md up; on a phone they are separate screens (the list at
// /assistant, a chat at /assistant/:chatId, a new one at /assistant/new) and
// the list has its own one-line "Ask…" so a question is one tap away.

import clsx from "clsx";
import { ArrowUp, MessagesSquare, Plus, Search, Sparkles, Trash2 } from "lucide-react";
import { useState } from "react";
import { Link, NavLink, useNavigate, useParams } from "react-router-dom";
import { useAssistant, useChats, useCreateChat, useDeleteChat } from "@/api/hooks";
import type { Chat } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Input } from "@/components/ui/Input";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { ASSISTANT_SETTINGS_PATH, isAssistantOff } from "@/lib/assistant";
import { BotEntry, BotList } from "./Bots";
import { Conversation, NewChat } from "./Conversation";
import { newChatSettings } from "./engines";

export function AssistantPage() {
  const { chatId, botKey } = useParams();
  const status = useAssistant();
  const chats = useChats();

  const off =
    // On without a provider key still works when Claude Code can answer.
    (status.data &&
      (!status.data.settings.enabled || (!status.data.key_configured && !status.data.engines?.claude.available && !status.data.engines?.codex?.available))) ||
    isAssistantOff(chats.error) ||
    isAssistantOff(status.error);
  if (off) return <AssistantOff keyConfigured={!!status.data?.key_configured} />;

  const id = chatId && chatId !== "new" ? Number(chatId) : null;
  const listOnly = chatId === undefined && botKey === undefined; // phones: /assistant is the list

  return (
    <div className="mx-auto flex max-w-6xl gap-6">
      <aside
        aria-label="Chats"
        className={clsx(
          // md:flex-none: on /assistant the list is flex-1 for phones, which would beat md:w-64 and make it jump when a chat opens.
          "min-w-0 md:sticky md:top-17 md:block md:max-h-[calc(100dvh-5.5rem)] md:w-64 md:flex-none md:shrink-0 md:self-start md:overflow-y-auto",
          listOnly ? "flex-1" : "hidden",
        )}
      >
        <ChatList />
      </aside>
      <div className={clsx("min-w-0 flex-1", listOnly && "hidden md:block")}>
        {botKey ? (
          <BotEntry projectKey={botKey} />
        ) : id === null ? (
          <NewChat />
        ) : Number.isInteger(id) && id > 0 ? (
          <Conversation key={id} id={id} />
        ) : (
          <EmptyState title="Chat not found" className="mt-6" />
        )}
      </div>
    </div>
  );
}

/** Past this many chats the list gets a filter box. */
const FILTER_FROM = 10;

function ChatList() {
  const chats = useChats();
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const all = chats.data ?? [];
  const needle = q.trim().toLowerCase();
  const shown = needle ? all.filter((c) => (c.title || "New chat").toLowerCase().includes(needle)) : all;
  return (
    <>
      <div className="mb-3 flex items-center justify-between gap-2">
        <h1 className="text-xl font-semibold tracking-tight">Assistant</h1>
        <Button size="sm" variant="primary" onClick={() => navigate("/assistant/new")}>
          <Plus className="size-3.5" aria-hidden /> New chat
        </Button>
      </div>
      {/* Phones land on this list: ask right here instead of tapping through to a new chat. Desktop has the composer beside it. */}
      <QuickAsk className="mb-4 md:hidden" />
      <BotList />
      <h2 className="mb-1 flex items-center gap-1.5 px-2.5 text-[11px] font-semibold tracking-wide text-fg-3 uppercase">
        <MessagesSquare className="size-3.5" aria-hidden /> Chats
      </h2>
      {all.length > FILTER_FROM ? (
        <div className="relative mb-1.5">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-fg-3" aria-hidden />
          <Input compact aria-label="Filter chats" placeholder="Filter chats…" value={q} onChange={(e) => setQ(e.target.value)} className="pl-8" />
        </div>
      ) : null}
      {chats.isPending ? (
        <div className="space-y-1.5">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-11" />
          ))}
        </div>
      ) : chats.error ? (
        <ErrorState error={chats.error} onRetry={() => chats.refetch()} />
      ) : all.length === 0 ? (
        <EmptyState icon={<MessagesSquare />} title="No chats yet" compact>
          Start one to ask about your projects or hand work to Claude Code.
        </EmptyState>
      ) : shown.length === 0 ? (
        <p className="px-2.5 py-2 text-[12.5px] text-fg-3">No chat matches “{q.trim()}”.</p>
      ) : (
        <ul className="space-y-0.5">
          {shown.map((c) => (
            <li key={c.id} className="group relative">
              <NavLink
                to={`/assistant/${c.id}`}
                className={({ isActive }) =>
                  clsx(
                    "flex min-w-0 items-center gap-2 rounded-md py-2 pr-9 pl-2.5 transition-colors",
                    isActive ? "bg-surface-3 text-fg" : "text-fg-2 hover:bg-surface-2 hover:text-fg",
                  )
                }
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13px] font-medium">{c.title || "New chat"}</span>
                  <RelativeTime iso={c.updated_at} className="block text-[11.5px] text-fg-3" />
                </span>
                {c.busy ? (
                  <span className="size-1.5 shrink-0 animate-pulse-soft rounded-full bg-accent" title="Working">
                    <span className="sr-only">working</span>
                  </span>
                ) : null}
              </NavLink>
              <DeleteChatButton chat={c} />
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

/** The row's delete: shown on hover/focus with a mouse, always on touch screens (no hover there). */
function DeleteChatButton({ chat }: { chat: Chat }) {
  const del = useDeleteChat();
  const toast = useToast();
  const navigate = useNavigate();
  const { chatId } = useParams();
  const [confirming, setConfirming] = useState(false);
  const title = chat.title || "New chat";
  return (
    <>
      <button
        type="button"
        aria-label={`Delete chat ${title}`}
        title="Delete chat"
        onClick={() => setConfirming(true)}
        className="absolute top-1/2 right-1.5 grid size-7 -translate-y-1/2 place-items-center rounded-md text-fg-3 hover:bg-surface-3 hover:text-critical-ink pointer-fine:opacity-0 pointer-fine:group-hover:opacity-100 pointer-fine:focus-visible:opacity-100"
      >
        <Trash2 className="size-3.5" aria-hidden />
      </button>
      <ConfirmDialog
        open={confirming}
        onClose={() => setConfirming(false)}
        title={`Delete “${title}”?`}
        body="The conversation is removed. Runs and tasks it created stay where they are."
        loading={del.isPending}
        onConfirm={() =>
          del.mutate(chat.id, {
            onSuccess: () => {
              setConfirming(false);
              if (chatId === String(chat.id)) navigate("/assistant", { replace: true });
            },
            onError: (e) => toast.error(e),
          })
        }
      />
    </>
  );
}

/** One line that starts a new chat with what was typed — the same create call as the full composer, with its defaults. */
function QuickAsk({ className }: { className?: string }) {
  const status = useAssistant();
  const create = useCreateChat();
  const navigate = useNavigate();
  const toast = useToast();
  const [text, setText] = useState("");
  const submit = () => {
    const content = text.trim();
    if (!content || create.isPending) return;
    create.mutate(
      { content, settings: newChatSettings(status.data) },
      {
        onSuccess: (thread) => {
          setText("");
          navigate(`/assistant/${thread.chat.id}`);
        },
        onError: (err) => toast.error(err),
      },
    );
  };
  return (
    <form
      className={clsx("flex items-center gap-1.5 rounded-xl border border-line-strong bg-surface p-1 focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/25", className)}
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <input
        aria-label="Ask the assistant"
        placeholder="Ask…"
        value={text}
        disabled={create.isPending}
        onChange={(e) => setText(e.target.value)}
        enterKeyHint="send"
        className="h-8 min-w-0 flex-1 bg-transparent px-2 text-sm text-fg placeholder:text-fg-3 focus:outline-none"
      />
      <Button type="submit" variant="primary" size="icon-sm" aria-label="Start a chat" disabled={!text.trim()} loading={create.isPending}>
        {create.isPending ? null : <ArrowUp className="size-4" />}
      </Button>
    </form>
  );
}

function AssistantOff({ keyConfigured }: { keyConfigured: boolean }) {
  return (
    <div className="mx-auto max-w-md pt-6">
      <EmptyState
        icon={<Sparkles />}
        title="The assistant is off"
        action={
          <Link to={ASSISTANT_SETTINGS_PATH} className="text-[13px] font-medium text-accent hover:underline">
            Settings → Assistant
          </Link>
        }
      >
        A chat that reads and updates Forge and delegates work to Claude Code on your machines.{" "}
        {keyConfigured ? "Switch it on" : "Add your provider's API key and switch it on"} to start.
      </EmptyState>
    </div>
  );
}
