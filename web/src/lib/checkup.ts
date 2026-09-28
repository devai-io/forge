// Daily check-up helpers: the fixed category order and the action-list grouping.

import type { CheckCategory, CheckItem, CheckSeverity } from "@/api/types";

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
];

export const categoryLabel = (c: string) => CATEGORIES.find((x) => x.value === c)?.label ?? c;

export const severityLabel = (s: CheckSeverity) => (s === "fail" ? "Failing" : s === "warn" ? "Warning" : "OK");

const SEVERITY_RANK: Record<CheckSeverity, number> = { fail: 0, warn: 1, ok: 2 };

/**
 * The action list: non-ok items grouped by category (categories in the fixed
 * order, fails before warns inside each, open before done), plus the ok items
 * on their own. Unknown categories sort last rather than disappearing.
 */
export function groupCheckItems(items: CheckItem[]): {
  groups: { category: string; label: string; items: CheckItem[] }[];
  ok: CheckItem[];
} {
  const actionable = items.filter((i) => i.severity !== "ok");
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
        (x, y) => Number(x.done) - Number(y.done) || SEVERITY_RANK[x.severity] - SEVERITY_RANK[y.severity] || x.title.localeCompare(y.title),
      ),
    }));
  return { groups, ok: items.filter((i) => i.severity === "ok") };
}

/** In-app routes navigate; anything else opens in a new tab. */
export const isInternalLink = (link: string) => link.startsWith("/") && !link.startsWith("//");
