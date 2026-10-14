// One chat: the messages, the assistant's tool activity between them, and the
// composer. The server runs the agent; this view posts a message, shows it at
// once, and polls the chat (useChat) until the turn is done. Like a run's
// transcript it follows the bottom unless the reader has scrolled up.

import { ArrowLeft, ArrowUp, Bot as BotIcon, ChevronDown, Pencil, Radar, Sparkles, Square, Trash2 } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from "react";
import { Link, useNavigate } from "react-router-dom";
import { ApiError } from "@/api/client";
import { useAssistant, useChat, useChatSettings, useCreateChat, useDeleteChat, useRenameChat, useSendBotMessage, useSendMessage, useStopChat } from "@/api/hooks";
import type { Chat, ChatMessage, ChatSettings, ChatTurn } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Input } from "@/components/ui/Input";
import { Markdown } from "@/components/ui/Markdown";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { Spinner } from "@/components/ui/Spinner";
import { useToast } from "@/components/ui/Toast";
import { buildChatItems, EXAMPLE_PROMPTS, usageLabel } from "@/lib/assistant";
import { BotSettingsButton } from "./BotSettings";
import { ChatSettingsBar } from "./ChatSettingsBar";
import { ToolActivity } from "./ToolActivity";
import { TurnSpend } from "./TurnSpend";

// Fills the page below the top bar, so the composer sits at the bottom even
// when the chat is short (the main area's own padding differs on phones).
// The composer is sticky, so text scrolls under it; "the bottom" is the end
// marker after it (ChatEnd), never a point above it — else the composer would
// cover the last lines.
const FRAME = "flex min-h-[calc(100dvh-10.25rem)] flex-col md:min-h-[calc(100dvh-6.75rem)]";

/**
 * A brand-new chat: the intro, examples, and a composer whose first send
 * creates the chat. With `bot`, the first message goes to that project's bot
 * and creates its chat.
 */
export function NewChat({ bot }: { bot?: { key: string; name: string } } = {}) {
  const createChat = useCreateChat();
  const botSend = useSendBotMessage(bot?.key ?? "");
  const create = bot ? botSend : createChat;
  const navigate = useNavigate();
  const toast = useToast();
  const [draft, setDraft] = useState("");
  const status = useAssistant();
  const [picked, setSettings] = useState<ChatSettings | null>(null);
  // The configured engine (Claude by default; always Claude for a bot), unless it cannot answer and the other can.
  const configured = bot ? "claude" : (status.data?.settings.engine ?? "claude");
  const other = configured === "claude" ? "deepseek" : "claude";
  const settings: ChatSettings = picked ?? {
    engine: status.data && !status.data.engines?.[configured].available && status.data.engines?.[other].available ? other : configured,
    model: "",
    effort: "",
    edits: false,
    skip_permissions: !!status.data?.engines?.claude.skip_permissions,
  };
  const input = useRef<HTMLTextAreaElement>(null);
  const end = useRef<HTMLDivElement>(null);

  const send = (text: string) => {
    setDraft("");
    create.mutate({ content: text, settings }, {
      onSuccess: (thread) => navigate(`/assistant/${thread.chat.id}`, { replace: true }),
      onError: (err) => {
        setDraft(text);
        toast.error(err);
      },
    });
  };

  return (
    <div className={FRAME}>
      <ChatHeader
        title={
          bot ? (
            <h2 className="flex min-w-0 items-center gap-2 truncate text-[15px] font-semibold">
              {bot.name} <BotBadge projectKey={bot.key} />
              <BotSettingsButton projectKey={bot.key} />
            </h2>
          ) : (
            <h2 className="truncate text-[15px] font-semibold">New chat</h2>
          )
        }
      />
      <div className="flex-1">
        {create.isPending ? (
          <Messages messages={[]} turns={[]} pendingText={create.variables?.content} working endRef={end} />
        ) : bot ? (
          <BotIntro name={bot.name} projectKey={bot.key} />
        ) : (
          <Intro
            onPick={(prompt) => {
              setDraft(prompt);
              input.current?.focus();
            }}
          />
        )}
      </div>
      <Composer
        value={draft}
        onChange={setDraft}
        onSend={send}
        disabled={create.isPending}
        textareaRef={input}
        autoFocus
        settings={<ChatSettingsBar value={settings} onChange={(p) => setSettings({ ...settings, ...p })} disabled={create.isPending} />}
      />
      <ChatEnd ref={end} />
    </div>
  );
}

export function Conversation({ id }: { id: number }) {
  const thread = useChat(id);
  if (thread.error instanceof ApiError && thread.error.status === 404) {
    return <EmptyState title="Chat not found" className="mt-6" action={<Link to="/assistant" className="text-[13px] text-accent hover:underline">All chats</Link>} />;
  }
  if (thread.error && !thread.data) return <ErrorState error={thread.error} onRetry={() => thread.refetch()} />;
  if (!thread.data) {
    return (
      <div className={FRAME}>
        <SkeletonRows rows={6} />
      </div>
    );
  }
  return <ChatView chat={thread.data.chat} messages={thread.data.messages} turns={thread.data.turns} />;
}

function ChatView({ chat, messages, turns }: { chat: Chat; messages: ChatMessage[]; turns: ChatTurn[] }) {
  const send = useSendMessage(chat.id);
  const stop = useStopChat(chat.id);
  const settings = useChatSettings(chat.id);
  const toast = useToast();
  const [draft, setDraft] = useState("");
  const end = useRef<HTMLDivElement>(null);
  const pendingText = send.isPending ? send.variables : undefined;
  const busy = chat.busy || send.isPending;

  return (
    <div className={FRAME}>
      <ChatHeader title={<ChatTitle chat={chat} />} chat={chat} />
      {chat.summary ? <FoldedSummary chat={chat} /> : null}
      <div className="flex-1">
        <Messages
          messages={messages}
          turns={turns}
          pendingText={pendingText}
          working={busy}
          onStop={chat.busy ? () => stop.mutate(undefined, { onError: (e) => toast.error(e) }) : undefined}
          stopping={stop.isPending}
          lastError={busy ? "" : chat.last_error}
          endRef={end}
        />
      </div>
      <Composer
        value={draft}
        onChange={setDraft}
        disabled={busy}
        settings={
          <ChatSettingsBar
            value={settings.isPending ? { ...chat, ...settings.variables } : chat}
            onChange={(patch) => settings.mutate(patch, { onError: (e) => toast.error(e) })}
          />
        }
        onSend={(text) => {
          setDraft("");
          send.mutate(text, {
            onError: (err) => {
              setDraft(text);
              toast.error(err);
            },
          });
        }}
      />
      <ChatEnd ref={end} />
    </div>
  );
}

/**
 * The end of the chat, after the composer. On phones the tab bar covers the
 * last 3.5rem of the screen, so "at the end" stops that much short.
 */
function ChatEnd({ ref }: { ref: RefObject<HTMLDivElement | null> }) {
  return <div ref={ref} aria-hidden className="h-px scroll-mb-[calc(3.5rem+env(safe-area-inset-bottom))] md:scroll-mb-0" />;
}

// ── Header ─────────────────────────────────────────────────────────────────

function ChatHeader({ title, chat }: { title: ReactNode; chat?: Chat }) {
  return (
    <header className="mb-4 flex min-w-0 items-center gap-2 border-b border-line pb-3">
      <Link
        to="/assistant"
        aria-label="All chats"
        className="-ml-1 grid size-7 shrink-0 place-items-center rounded-md text-fg-3 hover:bg-surface-2 hover:text-fg md:hidden"
      >
        <ArrowLeft className="size-4" />
      </Link>
      <div className="min-w-0 flex-1">{title}</div>
      {chat?.project_key ? (
        <>
          <BotBadge projectKey={chat.project_key} />
          <BotSettingsButton projectKey={chat.project_key} />
        </>
      ) : null}
      {chat ? (
        <>
          <span className="hidden shrink-0 text-[11.5px] text-fg-3 tabular sm:inline" title={`${chat.usage.cached_tokens.toLocaleString()} input tokens from cache`}>
            {usageLabel(chat.usage)}
          </span>
          <DeleteChat chat={chat} />
        </>
      ) : null}
    </header>
  );
}

function ChatTitle({ chat }: { chat: Chat }) {
  const rename = useRenameChat(chat.id);
  const toast = useToast();
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(chat.title);
  const title = chat.title || "New chat";

  const save = () => {
    setEditing(false);
    const next = value.trim();
    if (!next || next === chat.title) return;
    rename.mutate(next, { onError: (e) => toast.error(e) });
  };

  if (editing) {
    return (
      <Input
        compact
        autoFocus
        aria-label="Chat title"
        value={value}
        maxLength={200}
        onChange={(e) => setValue(e.target.value)}
        onBlur={save}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            save();
          } else if (e.key === "Escape") {
            e.preventDefault();
            setEditing(false);
          }
        }}
      />
    );
  }
  return (
    <h2 className="min-w-0 text-[15px] font-semibold">
      <button
        type="button"
        onClick={() => {
          setValue(chat.title);
          setEditing(true);
        }}
        className="group flex max-w-full min-w-0 items-center gap-1.5 rounded-md text-left"
        title="Rename"
      >
        <span className="truncate">{rename.isPending ? rename.variables : title}</span>
        <Pencil className="size-3.5 shrink-0 text-fg-3 opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100" aria-hidden />
        <span className="sr-only">(rename)</span>
      </button>
    </h2>
  );
}

function DeleteChat({ chat }: { chat: Chat }) {
  const del = useDeleteChat();
  const toast = useToast();
  const navigate = useNavigate();
  const [confirming, setConfirming] = useState(false);
  const bot = chat.project_key;
  const label = bot ? `Reset the ${bot} bot` : "Delete chat";
  return (
    <>
      <Button size="icon-sm" variant="ghost" aria-label={label} title={label} onClick={() => setConfirming(true)}>
        <Trash2 className="size-3.5" />
      </Button>
      <ConfirmDialog
        open={confirming}
        onClose={() => setConfirming(false)}
        title={bot ? `Reset the ${bot} bot?` : "Delete this chat?"}
        body={
          bot
            ? "Its conversation is removed and the next message starts a new one. The project's memory, runs and tasks stay."
            : "The conversation is removed. Runs and tasks it created stay where they are."
        }
        loading={del.isPending}
        onConfirm={() =>
          del.mutate(chat.id, {
            onSuccess: () => {
              setConfirming(false);
              navigate("/assistant", { replace: true });
            },
            onError: (e) => toast.error(e),
          })
        }
      />
    </>
  );
}

// ── Project bots ───────────────────────────────────────────────────────────

export function BotBadge({ projectKey }: { projectKey: string }) {
  return (
    <Badge tone="outline" className="shrink-0" title="This project's bot: one long conversation that keeps going">
      <BotIcon className="size-3" aria-hidden /> {projectKey} bot
    </Badge>
  );
}

function BotIntro({ name, projectKey }: { name: string; projectKey: string }) {
  return (
    <div className="mx-auto max-w-lg py-6 text-center">
      <BotIcon className="mx-auto size-6 text-fg-3" aria-hidden />
      <p className="mt-2 text-sm font-medium text-fg-2">{name}'s bot</p>
      <p className="mt-1 text-[13px] text-fg-3">
        One conversation that goes on for as long as {projectKey} does. It knows the project's repos, servers, tasks and memory, works through
        Claude Code on your machines, and keeps the project's memory up to date. Older messages are folded into a summary as it grows.
      </p>
    </div>
  );
}

/** The older part of a long chat, folded into a summary the model reads instead. */
function FoldedSummary({ chat }: { chat: Chat }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="mb-4 rounded-lg border border-line bg-surface-2/60 px-3 py-2 text-[12.5px]">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center gap-1.5 text-left text-fg-2 hover:text-fg"
      >
        <ChevronDown className={`size-3.5 shrink-0 transition-transform ${open ? "rotate-180" : ""}`} aria-hidden />
        Earlier messages are folded into a summary — the model reads it instead of them.
      </button>
      {open ? <Markdown className="mt-2 text-[12.5px]">{chat.summary}</Markdown> : null}
    </div>
  );
}

// ── Messages ───────────────────────────────────────────────────────────────

function Messages({
  messages,
  turns,
  pendingText,
  working,
  onStop,
  stopping,
  lastError = "",
  endRef,
}: {
  endRef: RefObject<HTMLDivElement | null>;
  messages: ChatMessage[];
  turns: ChatTurn[];
  pendingText?: string;
  working: boolean;
  onStop?: () => void;
  stopping?: boolean;
  lastError?: string;
}) {
  const items = useMemo(() => buildChatItems(messages), [messages]);
  const turnBySeq = useMemo(() => new Map(turns.map((t) => [t.seq, t])), [turns]);
  const [follow, setFollow] = useState(true);

  // Follow the bottom while the reader is there; stop once they scroll up.
  useEffect(() => {
    const onScroll = () => {
      const atBottom = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 120;
      setFollow(atBottom);
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);
  // The last message's seq, whether its tool calls have answers, and the
  // turns' spend lines change as the turn goes on; any of them moves the end.
  const lastTurn = turns[turns.length - 1];
  const tail = `${items.length}:${messages[messages.length - 1]?.seq ?? 0}:${pendingText ?? ""}:${working}:${lastError}:${turns.length}:${lastTurn?.status}`;
  useEffect(() => {
    if (follow) endRef.current?.scrollIntoView?.({ block: "end" });
  }, [tail, follow, endRef]);

  return (
    <>
      <div role="log" aria-live="polite" aria-label="Conversation" className="space-y-3">
        {withTurnSpend(items, turnBySeq)}
        {pendingText ? <UserBubble text={pendingText} /> : null}
      </div>
      {working ? (
        <div role="status" className="mt-3 flex items-center gap-2 px-1 text-[12.5px] text-fg-3">
          <Spinner className="size-3.5" />
          Working…
          {onStop ? (
            <Button size="sm" variant="subtle" className="ml-1" loading={stopping} onClick={onStop}>
              <Square className="size-3" aria-hidden /> Stop
            </Button>
          ) : null}
        </div>
      ) : null}
      {lastError ? (
        <div role="alert" className="mt-3 rounded-lg border border-critical/40 bg-critical/8 px-3 py-2 text-[13px]">
          <p className="text-fg">The last turn failed: {lastError}</p>
          <p className="mt-0.5 text-[12px] text-fg-3">Send a message to try again.</p>
        </div>
      ) : null}
    </>
  );
}

/** The items, with each turn's spend after the last thing it produced. */
function withTurnSpend(items: ReturnType<typeof buildChatItems>, turnBySeq: Map<number, ChatTurn>): ReactNode[] {
  const out: ReactNode[] = [];
  let turn: ChatTurn | undefined;
  const flush = () => {
    if (turn) out.push(<TurnSpend key={`spend${turn.id}`} turn={turn} />);
  };
  for (const item of items) {
    if (item.kind === "user") {
      flush();
      turn = turnBySeq.get(item.message.seq);
      const origin = item.message.origin;
      out.push(
        origin === "event" || origin === "schedule" ? (
          <ForgeEvent key={item.key} text={item.message.content} origin={origin} />
        ) : (
          <UserBubble key={item.key} text={item.message.content} via={origin === "telegram" ? "Telegram" : undefined} />
        ),
      );
    } else if (item.kind === "assistant") {
      out.push(<AssistantMessage key={item.key} text={item.message.content} />);
    } else {
      out.push(<ToolActivity key={item.key} call={item.call} result={item.result} />);
    }
  }
  flush();
  return out;
}

function UserBubble({ text, via }: { text: string; via?: string }) {
  return (
    <div className="flex flex-col items-end">
      <div className="max-w-[85%] rounded-2xl rounded-br-md border border-accent/25 bg-accent/10 px-3.5 py-2 text-sm leading-relaxed break-words whitespace-pre-wrap">
        {text}
      </div>
      {via ? <span className="mt-0.5 pr-1 text-[11px] text-fg-3">via {via}</span> : null}
    </div>
  );
}

/** A message Forge wrote to a proactive bot: the project's events, or the daily review. */
function ForgeEvent({ text, origin }: { text: string; origin: "event" | "schedule" }) {
  const [open, setOpen] = useState(false);
  const body = text.replace(/^Forge:\s*/, "");
  const first = body.split("\n")[0];
  return (
    <div role="note" aria-label={origin === "event" ? "Forge event" : "Forge daily review"} className="mx-auto max-w-[92%] rounded-lg border border-dashed border-line-strong bg-surface-2/60 px-3 py-2 text-[12.5px] text-fg-2">
      <button type="button" onClick={() => setOpen((v) => !v)} aria-expanded={open} className="flex w-full items-start gap-2 text-left">
        <Radar className="mt-0.5 size-3.5 shrink-0 text-fg-3" aria-hidden />
        <span className="min-w-0 flex-1">
          <span className="font-medium text-fg">{origin === "event" ? "Forge · events" : "Forge · daily review"}</span>
          {open ? null : <span className="block truncate text-fg-3">{first}</span>}
        </span>
        <ChevronDown className={`mt-0.5 size-3.5 shrink-0 transition-transform ${open ? "rotate-180" : ""}`} aria-hidden />
      </button>
      {open ? <Markdown className="mt-1.5 pl-5.5 text-[12.5px]">{body}</Markdown> : null}
    </div>
  );
}

function AssistantMessage({ text }: { text: string }) {
  return (
    <div className="flex min-w-0 gap-2.5">
      <Sparkles className="mt-1 size-4 shrink-0 text-fg-3" aria-hidden />
      <div className="min-w-0 flex-1 break-words">
        <Markdown>{text}</Markdown>
      </div>
    </div>
  );
}

function Intro({ onPick }: { onPick: (prompt: string) => void }) {
  return (
    <div className="mx-auto max-w-lg py-6 text-center">
      <Sparkles className="mx-auto size-6 text-fg-3" aria-hidden />
      <p className="mt-2 text-sm font-medium text-fg-2">What should we work on?</p>
      <p className="mt-1 text-[13px] text-fg-3">
        Ask about your projects, or have Claude Code do the work: e.g.{" "}
        <em>Review the open SHOP bugs and fix the easiest one on desk</em>.
      </p>
      <ul className="mt-4 grid gap-2 text-left sm:grid-cols-2" aria-label="Example prompts">
        {EXAMPLE_PROMPTS.map((p) => (
          <li key={p}>
            <button
              type="button"
              onClick={() => onPick(p)}
              className="h-full w-full rounded-lg border border-line bg-surface px-3 py-2 text-left text-[13px] text-fg-2 hover:border-line-strong hover:bg-surface-2 hover:text-fg"
            >
              {p}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

// ── Composer ───────────────────────────────────────────────────────────────

const MAX_HEIGHT = 200; // about eight lines

function Composer({
  value,
  onChange,
  onSend,
  disabled,
  textareaRef,
  autoFocus,
  settings,
}: {
  value: string;
  onChange: (v: string) => void;
  onSend: (text: string) => void;
  disabled: boolean;
  textareaRef?: RefObject<HTMLTextAreaElement | null>;
  autoFocus?: boolean;
  settings?: ReactNode;
}) {
  const own = useRef<HTMLTextAreaElement>(null);
  const ref = textareaRef ?? own;
  const wasDisabled = useRef(disabled);

  // Grow with the text up to MAX_HEIGHT, then scroll.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, MAX_HEIGHT)}px`;
    el.style.overflowY = el.scrollHeight > MAX_HEIGHT ? "auto" : "hidden";
  }, [value, ref]);

  // Disabling the field while the agent works drops focus; give it back after.
  useEffect(() => {
    if (wasDisabled.current && !disabled && document.activeElement === document.body) ref.current?.focus();
    wasDisabled.current = disabled;
  }, [disabled, ref]);

  const submit = () => {
    const text = value.trim();
    if (!text || disabled) return;
    onSend(text);
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
      className="sticky bottom-[calc(3.5rem+env(safe-area-inset-bottom))] z-10 mt-4 bg-bg pt-1 pb-3 md:bottom-0"
    >
      {settings}
      <div className="flex items-end gap-2 rounded-xl border border-line-strong bg-surface p-1.5 transition-colors focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/25">
        <textarea
          ref={ref}
          rows={1}
          aria-label="Message"
          autoFocus={autoFocus}
          value={value}
          disabled={disabled}
          placeholder={disabled ? "Working…" : "Ask, or tell Claude Code what to do…"}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              submit();
            }
          }}
          className="min-h-8 flex-1 resize-none bg-transparent px-2 py-1.5 text-sm leading-relaxed text-fg placeholder:text-fg-3 focus:outline-none disabled:opacity-60"
        />
        <Button type="submit" variant="primary" size="icon" aria-label="Send" disabled={disabled || !value.trim()}>
          <ArrowUp className="size-4" />
        </Button>
      </div>
      <p className="mt-1 hidden px-1 text-[11px] text-fg-3 sm:block">Enter to send · Shift+Enter for a new line</p>
    </form>
  );
}
