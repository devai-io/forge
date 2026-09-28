// Infrastructure display helpers.

import type { EndpointCheck, Environment } from "@/api/types";

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
