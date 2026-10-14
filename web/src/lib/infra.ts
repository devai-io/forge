// Infrastructure display helpers.

import type { Endpoint, EndpointCheck, Environment, ProjectStats } from "@/api/types";

export function environmentTone(env: Environment) {
  return env === "production" ? "serious" : env === "staging" ? "warning" : env === "infra" ? "accent" : "neutral";
}

export type Bucket = { start: number; ok: number; fail: number };

/** Split the window into equal buckets; a bucket with any failure reads as down. */
export function bucketChecks(checks: EndpointCheck[], now: number, hours = 24, buckets = 48): Bucket[] {
  const span = (hours * 3_600_000) / buckets;
  const start = now - hours * 3_600_000;
  const out: Bucket[] = Array.from({ length: buckets }, (_, i) => ({ start: start + i * span, ok: 0, fail: 0 }));
  for (const c of checks) {
    const t = new Date(c.at).getTime();
    const i = Math.floor((t - start) / span);
    if (i < 0 || i >= buckets) continue;
    if (c.ok) out[i].ok++;
    else out[i].fail++;
  }
  return out;
}

/**
 * A project's endpoint health for its sidebar dot: "down" once any endpoint
 * is confirmed down, "warning" while some are failing but still being
 * rechecked, null when nothing is failing.
 */
export function endpointHealth(stats: Pick<ProjectStats, "endpoints_down" | "endpoints_warning">): "down" | "warning" | null {
  if (stats.endpoints_down > 0) return "down";
  if ((stats.endpoints_warning ?? 0) > 0) return "warning";
  return null;
}

/** Failing in any way: confirmed down, or failing and being rechecked. */
export function isFailing(e: Pick<Endpoint, "last_status">): boolean {
  return e.last_status === "down" || e.last_status === "warning";
}

/** "retry 1/2" — which recheck a warning endpoint is waiting for. */
export function retryLabel(e: Pick<Endpoint, "fail_streak" | "retry_limit">): string {
  const limit = e.retry_limit || 2;
  return `retry ${Math.min(Math.max(e.fail_streak, 1), limit)}/${limit}`;
}
