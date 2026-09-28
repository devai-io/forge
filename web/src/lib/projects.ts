// Project display helpers.

import type { CIStatus, Project } from "@/api/types";

// Distinct, readable on both surfaces; the first eight follow the dataviz
// categorical order so the default choices stay CVD-separable side by side.
export const PROJECT_COLORS = ["#3987e5", "#eb6834", "#1baf7a", "#eda100", "#e87ba4", "#008300", "#9085e9", "#e34948"];

const STALE_DAYS = 7;

/** Live/building projects with no activity for a week need a nudge. */
export function isStale(project: Project, now: Date = new Date()): boolean {
  if (project.status !== "live" && project.status !== "building") return false;
  if (!project.stats.last_activity_at) return true;
  return now.getTime() - new Date(project.stats.last_activity_at).getTime() > STALE_DAYS * 86_400_000;
}


export type CIState = "success" | "failure" | "running" | "cancelled" | "neutral";

/** Collapse GitHub's status/conclusion pair into what the badge shows. */
export function ciState(ci: Pick<CIStatus, "status" | "conclusion">): CIState {
  if (ci.status !== "completed") return "running";
  switch (ci.conclusion) {
    case "success":
      return "success";
    case "failure":
    case "timed_out":
    case "startup_failure":
      return "failure";
    case "cancelled":
      return "cancelled";
    default:
      return "neutral"; // skipped, neutral, action_required, stale
  }
}
