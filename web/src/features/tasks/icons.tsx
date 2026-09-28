// Status, priority and type glyphs. Each renders with an accessible name, so
// a glyph is never the only way to know the value — lists put the label in a
// title/aria-label, and the drawer shows the word next to it.

import clsx from "clsx";
import { Bug, FlaskConical, ServerCog, Sparkles, Wrench } from "lucide-react";
import type { Priority, ProjectStatus, TaskStatus, TaskType } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { priorityLabel, projectStatusLabel, statusLabel, typeLabel } from "@/lib/tasks";

export function StatusIcon({ status, className }: { status: TaskStatus; className?: string }) {
  const cls = clsx("shrink-0", className ?? "size-3.5");
  const label = statusLabel(status);
  switch (status) {
    case "backlog":
      return (
        <svg viewBox="0 0 16 16" className={clsx(cls, "text-fg-3")} role="img" aria-label={label}>
          <circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" strokeWidth="1.6" strokeDasharray="2.2 2.2" />
        </svg>
      );
    case "todo":
      return (
        <svg viewBox="0 0 16 16" className={clsx(cls, "text-fg-2")} role="img" aria-label={label}>
          <circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" strokeWidth="1.6" />
        </svg>
      );
    case "in_progress":
      return (
        <svg viewBox="0 0 16 16" className={clsx(cls, "text-accent")} role="img" aria-label={label}>
          <circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" strokeWidth="1.6" />
          <path d="M8 4a4 4 0 0 1 0 8z" fill="currentColor" />
        </svg>
      );
    case "blocked":
      return (
        <svg viewBox="0 0 16 16" className={clsx(cls, "text-critical")} role="img" aria-label={label}>
          <circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" strokeWidth="1.6" />
          <path d="M4 12 12 4" stroke="currentColor" strokeWidth="1.6" />
        </svg>
      );
    case "done":
      return (
        <svg viewBox="0 0 16 16" className={clsx(cls, "text-good")} role="img" aria-label={label}>
          <circle cx="8" cy="8" r="7" fill="currentColor" />
          <path d="m5 8.2 2 2 4-4.2" fill="none" stroke="var(--surface)" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      );
  }
}

export function PriorityIcon({ priority, className }: { priority: Priority; className?: string }) {
  const cls = clsx("shrink-0", className ?? "size-3.5");
  const label = `${priorityLabel(priority)} priority`;
  if (priority === "urgent") {
    return (
      <svg viewBox="0 0 16 16" className={clsx(cls, "text-critical")} role="img" aria-label={label}>
        <rect x="1.5" y="1.5" width="13" height="13" rx="3" fill="currentColor" />
        <path d="M8 4.5v4.2M8 11.2v.3" stroke="white" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    );
  }
  const bars = priority === "high" ? 3 : priority === "medium" ? 2 : 1;
  return (
    <svg viewBox="0 0 16 16" className={clsx(cls, "text-fg-2")} role="img" aria-label={label}>
      {[0, 1, 2].map((i) => (
        <rect
          key={i}
          x={2 + i * 4.5}
          y={11 - i * 3.5}
          width="3"
          height={3 + i * 3.5}
          rx="1"
          fill="currentColor"
          opacity={i < bars ? 1 : 0.25}
        />
      ))}
    </svg>
  );
}

export function TypeIcon({ type, className }: { type: TaskType; className?: string }) {
  const props = { className: clsx("shrink-0 text-fg-3", className ?? "size-3.5"), "aria-label": typeLabel(type), role: "img" };
  switch (type) {
    case "bug":
      return <Bug {...props} />;
    case "chore":
      return <Wrench {...props} />;
    case "research":
      return <FlaskConical {...props} />;
    case "ops":
      return <ServerCog {...props} />;
    default:
      return <Sparkles {...props} />;
  }
}

export function ProjectStatusBadge({ status }: { status: ProjectStatus }) {
  const tone =
    status === "live" ? "good" : status === "building" ? "accent" : status === "paused" ? "warning" : status === "archived" ? "outline" : "neutral";
  return (
    <Badge tone={tone} dot={status !== "archived"}>
      {projectStatusLabel(status)}
    </Badge>
  );
}
