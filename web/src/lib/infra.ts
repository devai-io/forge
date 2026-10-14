// Infrastructure display helpers.

import type { Endpoint, EndpointCheck, Environment, ProjectStats } from "@/api/types";

export function environmentTone(env: Environment) {
  return env === "production" ? "serious" : env === "staging" ? "warning" : env === "infra" ? "accent" : "neutral";
}

/**
 * One window of the check strip. `fail` counts failures that were confirmed
 * (part of a run of more than `retries` failures in a row: the endpoint went
 * down); `warn` the ones that cleared on a recheck or are still being
 * rechecked.
 */
export type Bucket = { start: number; ok: number; warn: number; fail: number };

/**
 * Split the window into equal buckets. A failure counts as confirmed when it
 * belongs to a run of more than `retries` consecutive failures — what turns
 * an endpoint down; shorter runs are warnings.
 */
export function bucketChecks(checks: EndpointCheck[], now: number, hours = 24, buckets = 48, retries = 2): Bucket[] {
  const span = (hours * 3_600_000) / buckets;
  const start = now - hours * 3_600_000;
  const out: Bucket[] = Array.from({ length: buckets }, (_, i) => ({ start: start + i * span, ok: 0, warn: 0, fail: 0 }));
  const sorted = checks.map((c) => ({ ok: c.ok, t: new Date(c.at).getTime() })).sort((a, b) => a.t - b.t);
  // Mark each failure with the length of the run of failures it belongs to.
  const runLength = new Array<number>(sorted.length).fill(0);
  for (let i = 0; i < sorted.length; ) {
    if (sorted[i].ok) {
      i++;
      continue;
    }
    let j = i;
    while (j < sorted.length && !sorted[j].ok) j++;
    for (let k = i; k < j; k++) runLength[k] = j - i;
    i = j;
  }
  sorted.forEach((c, n) => {
    const i = Math.floor((c.t - start) / span);
    if (i < 0 || i >= buckets) return;
    if (c.ok) out[i].ok++;
    else if (runLength[n] > retries) out[i].fail++;
    else out[i].warn++;
  });
  return out;
}

/** The page of one server. */
export const serverPath = (id: number) => `/infra/servers/${id}`;

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

/** "30, 60" → [30, 60]; null unless 1–5 whole numbers from 10 to 900. */
export function parseDelays(text: string): number[] | null {
  const parts = text
    .split(/[\s,]+/)
    .map((p) => p.trim())
    .filter(Boolean);
  if (parts.length < 1 || parts.length > 5) return null;
  const nums = parts.map(Number);
  return nums.every((n) => Number.isInteger(n) && n >= 10 && n <= 900) ? nums : null;
}
