// Project bots: each project's own long-lived Assistant chat. The list sits
// under the Assistant's chats; /assistant/bot/:key opens a project's bot — its
// chat when it has one, else a composer whose first message creates it.

import clsx from "clsx";
import { Bot as BotIcon, ChevronDown, Radar } from "lucide-react";
import { useState } from "react";
import { Link, Navigate, NavLink } from "react-router-dom";
import { useBots } from "@/api/hooks";
import type { Bot } from "@/api/types";
import { ColorDot } from "@/components/ui/Badge";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { botPath } from "@/lib/chats";
import { NewChat } from "./Conversation";

/** /assistant/bot/:key — the bot's chat, or a first message to start it. */
export function BotEntry({ projectKey }: { projectKey: string }) {
  const bots = useBots();
  const key = projectKey.toUpperCase();
  if (bots.error && !bots.data) return <ErrorState error={bots.error} onRetry={() => bots.refetch()} />;
  if (!bots.data) return <SkeletonRows rows={6} />;
  const bot = bots.data.find((b) => b.project.key === key);
  if (!bot) {
    return (
      <EmptyState
        title="No bot for this project"
        className="mt-6"
        action={
          <Link to="/assistant" className="text-[13px] text-accent hover:underline">
            All chats
          </Link>
        }
      >
        Archived projects have no bot.
      </EmptyState>
    );
  }
  if (bot.chat) return <Navigate to={`/assistant/${bot.chat.id}`} replace />;
  return <NewChat key={key} bot={{ key, name: bot.project.name }} />;
}

/** The bots in the chat list: the ones that have talked, then a fold with the rest. */
export function BotList() {
  const bots = useBots();
  const [more, setMore] = useState(false);
  if (bots.isPending) return <SkeletonRows rows={2} className="mt-5" />;
  if (bots.error || !bots.data.length) return null;
  const active = bots.data.filter((b) => b.chat);
  const idle = bots.data.filter((b) => !b.chat);
  return (
    <section aria-labelledby="bots-heading" className="mt-5">
      <h2 id="bots-heading" className="mb-1 flex items-center gap-1.5 px-2.5 text-[11px] font-semibold tracking-wide text-fg-3 uppercase">
        <BotIcon className="size-3.5" aria-hidden /> Project bots
      </h2>
      <ul className="space-y-0.5">
        {active.map((b) => (
          <li key={b.project.key}>
            <BotRow bot={b} />
          </li>
        ))}
      </ul>
      {idle.length ? (
        <>
          <button
            type="button"
            onClick={() => setMore((v) => !v)}
            aria-expanded={more}
            className="mt-0.5 flex w-full items-center gap-1.5 rounded-md px-2.5 py-1.5 text-left text-[12px] text-fg-3 hover:bg-surface-2 hover:text-fg-2"
          >
            <ChevronDown className={clsx("size-3.5 transition-transform", more && "rotate-180")} aria-hidden />
            {active.length ? `${idle.length} more project${idle.length === 1 ? "" : "s"}` : `Start a bot · ${idle.length} project${idle.length === 1 ? "" : "s"}`}
          </button>
          {more ? (
            <ul className="space-y-0.5">
              {idle.map((b) => (
                <li key={b.project.key}>
                  <BotRow bot={b} />
                </li>
              ))}
            </ul>
          ) : null}
        </>
      ) : null}
    </section>
  );
}

function BotRow({ bot: b }: { bot: Bot }) {
  const to = b.chat ? `/assistant/${b.chat.id}` : botPath(b.project.key);
  return (
    <NavLink
      to={to}
      className={({ isActive }) =>
        clsx(
          "flex min-w-0 items-start gap-2 rounded-md px-2.5 py-2 transition-colors",
          isActive ? "bg-surface-3 text-fg" : "text-fg-2 hover:bg-surface-2 hover:text-fg",
        )
      }
    >
      <ColorDot color={b.project.color} className="mt-1" />
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-baseline gap-1.5">
          <span className="truncate text-[13px] font-medium">{b.project.name}</span>
          <span className="shrink-0 font-mono text-[10.5px] text-fg-3">{b.project.key}</span>
          {!b.settings.enabled ? (
            <span className="shrink-0 text-[10.5px] text-fg-3">off</span>
          ) : b.settings.proactive ? (
            <span className="inline-flex shrink-0 items-center gap-0.5 text-[10.5px] text-accent" title="Proactive: Forge wakes it with the project's events and a daily review">
              <Radar className="size-3" aria-hidden />
              proactive
            </span>
          ) : null}
        </span>
        {b.last ? (
          <span className="block truncate text-[11.5px] text-fg-3">
            {b.last.role === "user" ? "You: " : ""}
            {b.last.content}
          </span>
        ) : null}
        {b.chat ? <RelativeTime iso={b.chat.updated_at} className="block text-[11px] text-fg-3" /> : null}
      </span>
      {b.chat?.busy ? (
        <span className="mt-1.5 size-1.5 shrink-0 animate-pulse-soft rounded-full bg-accent" title="Working">
          <span className="sr-only">working</span>
        </span>
      ) : null}
    </NavLink>
  );
}
