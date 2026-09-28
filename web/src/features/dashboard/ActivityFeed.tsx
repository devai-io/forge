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
import { Link } from "react-router-dom";
import type { Activity } from "@/api/types";
import { ColorDot } from "@/components/ui/Badge";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { useShell } from "@/features/shell/context";

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

export function ActivityFeed({
  items,
  showProject = true,
  empty = "No activity yet.",
}: {
  items: Activity[];
  showProject?: boolean;
  empty?: string;
}) {
  const { openTask } = useShell();
  if (!items.length) return <p className="px-2 py-4 text-[13px] text-fg-3">{empty}</p>;
  return (
    <ol className="space-y-0.5">
      {items.map((a) => (
        <li key={a.id} className="flex items-start gap-2.5 rounded-md px-2 py-1.5 hover:bg-surface-2/60">
          <span className="mt-0.5">
            <ActivityIcon kind={a.kind} />
          </span>
          <div className="min-w-0 flex-1 text-[13px] leading-snug">
            <p className="text-fg">
              {a.task_id && a.task_ref ? (
                <button
                  type="button"
                  onClick={() => openTask(a.task_id!)}
                  className="mr-1 font-mono text-[12px] text-fg-2 hover:text-accent hover:underline"
                >
                  {a.task_ref}
                </button>
              ) : null}
              {a.run_id ? (
                <Link to={`/agents/runs/${a.run_id}`} className="mr-1 font-mono text-[12px] text-fg-2 hover:text-accent hover:underline">
                  #{a.run_id}
                </Link>
              ) : null}
              {a.summary}
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
  );
}
