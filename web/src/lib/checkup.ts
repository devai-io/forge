// Daily check-up helpers: the fixed category order and the action-list grouping.

import type { CheckCategory, CheckItem, CheckSeverity } from "@/api/types";
import { daysBetween } from "@/lib/format";

export const CATEGORIES: { value: CheckCategory; label: string }[] = [
  { value: "endpoints", label: "Endpoints" },
  { value: "tls", label: "TLS certificates" },
  { value: "hosts", label: "Hosts" },
  { value: "nomad", label: "Nomad jobs" },
  { value: "alerts", label: "Alerts" },
  { value: "backups", label: "Backups" },
  { value: "runners", label: "Runners" },
  { value: "repos", label: "Repositories" },
  { value: "ci", label: "CI" },
  { value: "vault", label: "Vault" },
  { value: "tasks", label: "Tasks" },
  { value: "security", label: "Security" },
];

export const categoryLabel = (c: string) => CATEGORIES.find((x) => x.value === c)?.label ?? c;

export const severityLabel = (s: CheckSeverity) => (s === "fail" ? "Failing" : s === "warn" ? "Warning" : "OK");

const SEVERITY_RANK: Record<CheckSeverity, number> = { fail: 0, warn: 1, ok: 2 };

/** Ticked off, or verified gone by a re-check. */
export const isSettled = (i: CheckItem) => i.done || i.resolved_at !== null;

/** First consecutive check-up with this key was this one (unknown on old rows counts as new). */
export const isNew = (i: { first_seen: string }, date: string) => !i.first_seen || i.first_seen === date;

/** Whole days this key has been showing up as of `date`; 0 when new. */
export const ageDays = (i: { first_seen: string }, date: string) => (isNew(i, date) ? 0 : Math.max(0, daysBetween(i.first_seen, date)));

/**
 * The action list: non-ok, non-snoozed items grouped by category (categories
 * in the fixed order; inside each: open before settled, new before chronic,
 * fails before warns), the ok items and the snoozed items on their own.
 * Unknown categories sort last rather than disappearing.
 */
export function groupCheckItems(
  items: CheckItem[],
  date: string,
): {
  groups: { category: string; label: string; items: CheckItem[] }[];
  ok: CheckItem[];
  snoozed: CheckItem[];
} {
  const actionable = items.filter((i) => i.severity !== "ok" && !i.snoozed);
  const order = (c: string) => {
    const i = CATEGORIES.findIndex((x) => x.value === c);
    return i === -1 ? CATEGORIES.length : i;
  };
  const byCategory = new Map<string, CheckItem[]>();
  for (const item of actionable) {
    const list = byCategory.get(item.category) ?? [];
    list.push(item);
    byCategory.set(item.category, list);
  }
  const groups = Array.from(byCategory.entries())
    .sort(([a], [b]) => order(a) - order(b) || a.localeCompare(b))
    .map(([category, list]) => ({
      category,
      label: categoryLabel(category),
      items: [...list].sort(
        (x, y) =>
          Number(isSettled(x)) - Number(isSettled(y)) ||
          Number(!isNew(x, date)) - Number(!isNew(y, date)) ||
          SEVERITY_RANK[x.severity] - SEVERITY_RANK[y.severity] ||
          x.title.localeCompare(y.title),
      ),
    }));
  return {
    groups,
    ok: items.filter((i) => i.severity === "ok"),
    snoozed: items.filter((i) => i.severity !== "ok" && i.snoozed),
  };
}

/** In-app routes navigate; anything else opens in a new tab. */
export const isInternalLink = (link: string) => link.startsWith("/") && !link.startsWith("//");
