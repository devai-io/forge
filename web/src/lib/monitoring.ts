// Monitoring display helpers.

import type { Alert, GrafanaDashboard } from "@/api/types";

export type Level = "ok" | "warn" | "fail";

/** Resource usage thresholds: warn at 80 %, fail at 90 % (same as the check-up). */
export function usageLevel(pct: number | null | undefined): Level {
  if (pct === null || pct === undefined) return "ok";
  if (pct >= 90) return "fail";
  if (pct >= 80) return "warn";
  return "ok";
}

/** TLS certificates: fail under a week, warn under three. */
export function sslLevel(days: number | null | undefined): Level {
  if (days === null || days === undefined) return "ok";
  if (days < 7) return "fail";
  if (days < 21) return "warn";
  return "ok";
}

/** 93784 → "1d 2h", 3700 → "1h 1m", 59 → "59s". */
export function formatUptime(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined) return "—";
  const s = Math.max(0, Math.floor(seconds));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${s}s`;
}

const ALERT_RANK: Record<Alert["state"], number> = { firing: 0, pending: 1, normal: 2 };

/** Firing first, then pending; newest first within a state. */
export function sortAlerts(alerts: Alert[]): Alert[] {
  return [...alerts].sort(
    (a, b) => ALERT_RANK[a.state] - ALERT_RANK[b.state] || (b.since ?? "").localeCompare(a.since ?? ""),
  );
}

/**
 * Best-effort: dashboards whose title or folder mentions the project's name or
 * key as a word ("Shop launch funnel", folder "Ops"). Two-letter keys
 * only match whole words so "KART" does not pick up "sources".
 */
export function dashboardsForProject(
  dashboards: GrafanaDashboard[],
  project: { key: string; name: string },
): GrafanaDashboard[] {
  const terms = [project.name, project.key]
    .map((t) => t.trim().toLowerCase())
    .filter((t, i, all) => t.length >= 2 && all.indexOf(t) === i);
  const escape = (t: string) => t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const res = terms.map((t) => new RegExp(`(^|[^a-z0-9])${escape(t)}([^a-z0-9]|$)`, "i"));
  return dashboards.filter((d) => res.some((re) => re.test(d.title) || re.test(d.folder)));
}
