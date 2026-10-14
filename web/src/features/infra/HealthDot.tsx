// A project's endpoint health as one dot: amber while something is failing
// but still being rechecked, red with a soft glow once it is confirmed down.
// Nothing when all is well.

import clsx from "clsx";
import type { ProjectStats } from "@/api/types";
import { endpointHealth } from "@/lib/infra";

export function HealthDot({ stats, className }: { stats: Pick<ProjectStats, "endpoints_down" | "endpoints_warning">; className?: string }) {
  const health = endpointHealth(stats);
  if (!health) return null;
  const label =
    health === "down"
      ? `${stats.endpoints_down} endpoint${stats.endpoints_down === 1 ? "" : "s"} down`
      : `${stats.endpoints_warning} endpoint${stats.endpoints_warning === 1 ? "" : "s"} failing, being rechecked`;
  return (
    <span
      role="img"
      aria-label={label}
      title={label}
      data-health={health}
      className={clsx("size-1.5 shrink-0 rounded-full", health === "down" ? "glow-critical bg-critical" : "bg-warning", className)}
    />
  );
}
