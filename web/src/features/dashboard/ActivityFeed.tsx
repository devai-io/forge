import {
  Bot,
  CircleAlert,
  CircleCheck,
  CirclePlus,
  FolderPlus,
  MessageSquare,
  PencilLine,
  RotateCcw,
  Shuffle,
  Wifi,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import type { Activity } from "@/api/types";
import { ColorDot } from "@/components/ui/Badge";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { useShell } from "@/features/shell/context";
import { foldRepeats } from "@/lib/activity";

function ActivityIcon({ kind }: { kind: string }) {
  const cls = "size-3.5 shrink-0";
  switch (kind) {
    case "task.done":
      return <CircleCheck className={`${cls} text-good`} aria-hidden />;
    case "task.created":
      return <CirclePlus className={`${cls} text-fg-3`} aria-hidden />;
    case "task.status":
      return <Shuffle className={`${cls} text-fg-3`} aria-hidden />;
    case "task.reopened":
      return <RotateCcw className={`${cls} text-fg-3`} aria-hidden />;
    case "task.comment":
      return <MessageSquare className={`${cls} text-fg-3`} aria-hidden />;
    case "project.created":
      return <FolderPlus className={`${cls} text-fg-3`} aria-hidden />;
    case "project.updated":
      return <PencilLine className={`${cls} text-fg-3`} aria-hidden />;
    case "run.queued":
    case "run.finished":
      return <Bot className={`${cls} text-fg-3`} aria-hidden />;
    case "endpoint.down":
      return <CircleAlert className={`${cls} text-critical`} aria-hidden />;
    case "endpoint.up":
      return <Wifi className={`${cls} text-good`} aria-hidden />;
    default:
      return <PencilLine className={`${cls} text-fg-3`} aria-hidden />;
  }
}

/** The summary with its task ref or run number linked in place, or put in front when the text doesn't mention it. */
function Summary({ a }: { a: Activity }) {
  const { openTask } = useShell();
  const cls = "font-mono text-[12px] text-fg-2 hover:text-accent hover:underline";
  let token = "";
  let link: ReactNode = null;
  if (a.task_id && a.task_ref) {
    token = a.task_ref;
    link = (
      <button type="button" onClick={() => openTask(a.task_id!)} className={cls}>
        {a.task_ref}
      </button>
    );
  } else if (a.run_id) {
    token = `#${a.run_id}`;
    link = (
      <Link to={`/agents/runs/${a.run_id}`} className={cls}>
        #{a.run_id}
      </Link>
    );
  }
  if (!link) return <>{a.summary}</>;
  const at = a.summary.indexOf(token);
  if (at < 0)
    return (
      <>
        <span className="mr-1">{link}</span>
        {a.summary}
      </>
    );
  return (
    <>
      {a.summary.slice(0, at)}
      {link}
      {a.summary.slice(at + token.length)}
    </>
  );
}

export function ActivityFeed({
  items,
  showProject = true,
  empty = "No activity yet.",
  limit = 15,
}: {
  items: Activity[];
  showProject?: boolean;
  empty?: string;
  /** Rows shown before "Show more". */
  limit?: number;
}) {
  const [all, setAll] = useState(false);
  if (!items.length) return <p className="px-2 py-4 text-[13px] text-fg-3">{empty}</p>;
  const rows = foldRepeats(items);
  const shown = all ? rows : rows.slice(0, limit);
  return (
    <>
      <ol className="space-y-0.5">
        {shown.map(({ a, count }) => (
          <li key={a.id} className="flex items-start gap-2.5 rounded-md px-2 py-1.5 hover:bg-surface-2/60">
            <span className="mt-0.5">
              <ActivityIcon kind={a.kind} />
            </span>
            <div className="min-w-0 flex-1 text-[13px] leading-snug">
              <p className="text-fg">
                <Summary a={a} />
                {count > 1 ? <span className="ml-1.5 rounded bg-surface-2 px-1 text-[11px] text-fg-3 tabular">×{count}</span> : null}
              </p>
              <p className="mt-0.5 flex items-center gap-1.5 text-[11.5px] text-fg-3">
                {showProject && a.project_key ? (
                  <Link to={`/p/${a.project_key}`} className="inline-flex items-center gap-1 hover:text-fg-2">
                    <ColorDot color={a.project_color} className="size-2" />
                    {a.project_key}
                  </Link>
                ) : null}
                <RelativeTime iso={a.created_at} />
              </p>
            </div>
          </li>
        ))}
      </ol>
      {rows.length > shown.length ? (
        <button type="button" onClick={() => setAll(true)} className="mt-1 px-2 py-1 text-[12px] text-fg-3 hover:text-fg">
          Show {rows.length - shown.length} more
        </button>
      ) : null}
    </>
  );
}
