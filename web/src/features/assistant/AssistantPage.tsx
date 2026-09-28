// The Assistant: a chat with an LLM that reads and updates Forge and hands
// work to Claude Code on your machines. The chat list sits beside the
// conversation from md up; on a phone they are separate screens (the list at
// /assistant, a chat at /assistant/:chatId, a new one at /assistant/new).

import clsx from "clsx";
import { MessagesSquare, Plus, Sparkles } from "lucide-react";
import { Link, NavLink, useNavigate, useParams } from "react-router-dom";
import { useAssistant, useChats } from "@/api/hooks";
import { Button } from "@/components/ui/Button";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton } from "@/components/ui/Skeleton";
import { ASSISTANT_SETTINGS_PATH, isAssistantOff } from "@/lib/assistant";
import { Conversation, NewChat } from "./Conversation";

export function AssistantPage() {
  const { chatId } = useParams();
  const status = useAssistant();
  const chats = useChats();

  const off =
    (status.data && (!status.data.settings.enabled || !status.data.key_configured)) ||
    isAssistantOff(chats.error) ||
    isAssistantOff(status.error);
  if (off) return <AssistantOff keyConfigured={!!status.data?.key_configured} />;

  const id = chatId && chatId !== "new" ? Number(chatId) : null;
  const listOnly = chatId === undefined; // phones: /assistant is the list

  return (
    <div className="mx-auto flex max-w-6xl gap-6">
      <aside
        aria-label="Chats"
        className={clsx(
          "min-w-0 md:sticky md:top-17 md:block md:max-h-[calc(100dvh-5.5rem)] md:w-64 md:shrink-0 md:self-start md:overflow-y-auto",
          listOnly ? "flex-1" : "hidden",
        )}
      >
        <ChatList />
      </aside>
      <div className={clsx("min-w-0 flex-1", listOnly && "hidden md:block")}>
        {id === null ? (
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

function ChatList() {
  const chats = useChats();
  const navigate = useNavigate();
  return (
    <>
      <div className="mb-3 flex items-center justify-between gap-2">
        <h1 className="text-xl font-semibold tracking-tight">Assistant</h1>
        <Button size="sm" variant="primary" onClick={() => navigate("/assistant/new")}>
          <Plus className="size-3.5" aria-hidden /> New chat
        </Button>
      </div>
      {chats.isPending ? (
        <div className="space-y-1.5">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-11" />
          ))}
        </div>
      ) : chats.error ? (
        <ErrorState error={chats.error} onRetry={() => chats.refetch()} />
      ) : chats.data.length === 0 ? (
        <EmptyState icon={<MessagesSquare />} title="No chats yet" compact>
          Start one to ask about your projects or hand work to Claude Code.
        </EmptyState>
      ) : (
        <ul className="space-y-0.5">
          {chats.data.map((c) => (
            <li key={c.id}>
              <NavLink
                to={`/assistant/${c.id}`}
                className={({ isActive }) =>
                  clsx(
                    "flex min-w-0 items-center gap-2 rounded-md px-2.5 py-2 transition-colors",
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
            </li>
          ))}
        </ul>
      )}
    </>
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
