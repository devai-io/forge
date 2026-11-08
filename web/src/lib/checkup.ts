// Daily check-up helpers: the categories, the action-list grouping and ordering,
// and which server a finding is about.

import type { CheckCategory, CheckItem, CheckSeverity, Server } from "@/api/types";
import { daysBetween } from "@/lib/format";
import { hostAliases, serverAliases } from "@/lib/monitoring";

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
  { value: "tasks", label: "Tasks" }, // check-ups before v0.1.42 only; tasks live on the dashboard now
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

/** One line of a category: an item, or several open items that share a finding and its action. */
export type CheckEntry = { kind: "item"; item: CheckItem } | { kind: "group"; group: CheckGroup };

/** Three or more open items in one category with the same action: "10 hosts not reporting metrics". */
export type CheckGroup = {
  key: string;
  category: string;
  /** "10 hosts not reporting metrics". */
  title: string;
  action: string;
  severity: CheckSeverity;
  /** What each item is about — the part of its title that differs ("web-1"), or the whole title. */
  subjects: string[];
  items: CheckItem[];
};

/** Items sharing a finding collapse into one row from this many. */
export const GROUP_MIN = 3;

const NOUNS: Record<string, [string, string]> = {
  hosts: ["host", "hosts"],
  nomad: ["job", "jobs"],
  endpoints: ["endpoint", "endpoints"],
  tls: ["certificate", "certificates"],
  repos: ["repository", "repositories"],
  ci: ["workflow", "workflows"],
  runners: ["machine", "machines"],
  backups: ["backup", "backups"],
  alerts: ["alert", "alerts"],
  vault: ["secret", "secrets"],
};

/**
 * Name a set of items by what their titles share: "web-1 is not reporting
 * metrics", "db-1 is not reporting metrics" → "2 hosts not reporting
 * metrics" with subjects ["web-1", "db-1"]. Without a shared ending of a
 * word or more, the subjects are the whole titles.
 */
export function nameGroup(category: string, titles: string[]): { title: string; subjects: string[] } {
  const [one, many] = NOUNS[category] ?? ["item", "items"];
  const noun = titles.length === 1 ? one : many;
  const words = titles.map((t) => t.trim().split(/\s+/));
  const longest = Math.min(...words.map((w) => w.length)) - 1; // keep at least one word of each title
  const wordFromEnd = (w: string[], n: number) => w[w.length - 1 - n];
  let shared = 0;
  while (shared < longest && words.every((w) => wordFromEnd(w, shared) === wordFromEnd(words[0], shared))) shared++;
  if (!shared) return { title: `${titles.length} ${noun} with the same finding`, subjects: titles };
  const tail = words[0].slice(words[0].length - shared);
  if (tail[0] === "is" || tail[0] === "are" || tail[0] === "has" || tail[0] === "have") {
    if (tail[0] === "is" || tail[0] === "are") tail.shift();
    else tail[0] = titles.length === 1 ? "has" : "have";
  }
  return { title: `${titles.length} ${noun} ${tail.join(" ")}`, subjects: words.map((w) => w.slice(0, w.length - shared).join(" ")) };
}

/**
 * The action list: non-ok, non-snoozed items grouped by category, the ok
 * items and the snoozed items on their own. Categories come worst first:
 * by their worst open item, then by how many are open (the fixed order
 * only breaks ties; unknown categories sort last rather than disappearing).
 * Inside a category: open before settled, then fails before warns, then
 * production-critical before the rest (`isProd`), then new before chronic.
 * Three or more open items with the same action collapse into one group
 * entry (`entries`); `items` keeps every item, in order.
 */
export function groupCheckItems(
  items: CheckItem[],
  date: string,
  isProd: (i: CheckItem) => boolean = () => false,
): {
  groups: { category: string; label: string; items: CheckItem[]; entries: CheckEntry[] }[];
  ok: CheckItem[];
  snoozed: CheckItem[];
} {
  const actionable = items.filter((i) => i.severity !== "ok" && !i.snoozed);
  const order = (c: string) => {
    const i = CATEGORIES.findIndex((x) => x.value === c);
    return i === -1 ? CATEGORIES.length : i;
  };
  const prod = new Map(actionable.map((i) => [i.key, isProd(i)]));
  const cmp = (x: CheckItem, y: CheckItem) =>
    Number(isSettled(x)) - Number(isSettled(y)) ||
    SEVERITY_RANK[x.severity] - SEVERITY_RANK[y.severity] ||
    Number(!prod.get(x.key)) - Number(!prod.get(y.key)) ||
    Number(!isNew(x, date)) - Number(!isNew(y, date)) ||
    x.title.localeCompare(y.title);
  const byCategory = new Map<string, CheckItem[]>();
  for (const item of actionable) {
    const list = byCategory.get(item.category) ?? [];
    list.push(item);
    byCategory.set(item.category, list);
  }
  const rank = (list: CheckItem[]) => {
    const open = list.filter((i) => !isSettled(i));
    return { worst: open.length ? Math.min(...open.map((i) => SEVERITY_RANK[i.severity])) : 3, open: open.length };
  };
  const groups = Array.from(byCategory.entries())
    .map(([category, list]) => ({ category, list: [...list].sort(cmp), rank: rank(list) }))
    .sort((a, b) => a.rank.worst - b.rank.worst || b.rank.open - a.rank.open || order(a.category) - order(b.category) || a.category.localeCompare(b.category))
    .map(({ category, list }) => ({ category, label: categoryLabel(category), items: list, entries: entriesOf(category, list) }));
  return {
    groups,
    ok: items.filter((i) => i.severity === "ok"),
    snoozed: items.filter((i) => i.severity !== "ok" && i.snoozed),
  };
}

/** A sorted category's lines: open items sharing an action fold into a group at the place of its first item. */
function entriesOf(category: string, sorted: CheckItem[]): CheckEntry[] {
  const byAction = new Map<string, CheckItem[]>();
  for (const i of sorted) {
    if (isSettled(i) || !i.action.trim()) continue;
    byAction.set(i.action, [...(byAction.get(i.action) ?? []), i]);
  }
  const grouped = new Map<string, CheckItem[]>();
  for (const [, list] of byAction) if (list.length >= GROUP_MIN) for (const i of list) grouped.set(i.key, list);
  const out: CheckEntry[] = [];
  const placed = new Set<CheckItem[]>();
  for (const item of sorted) {
    const list = grouped.get(item.key);
    if (!list) {
      out.push({ kind: "item", item });
      continue;
    }
    if (placed.has(list)) continue;
    placed.add(list);
    const named = nameGroup(category, list.map((i) => i.title));
    out.push({
      kind: "group",
      group: {
        key: `group:${category}:${list[0].action}`,
        category,
        title: named.title,
        action: list[0].action,
        severity: list.some((i) => i.severity === "fail") ? "fail" : "warn",
        subjects: named.subjects,
        items: list,
      },
    });
  }
  return out;
}

/**
 * The host a finding is about, from its key ("host:web-1", "disk:web-1",
 * "nomad:web-1", "job:web-1:shop-api", "alert:HighCPU:web-1:9100"); "" when
 * it is not about one host.
 */
export function itemHost(i: Pick<CheckItem, "key" | "category">): string {
  const [kind, ...rest] = i.key.split(":");
  if (!rest.length) return "";
  if (kind === "host" || kind === "disk" || kind === "mem" || kind === "cpu" || kind === "nomad") return rest.join(":");
  if (kind === "job") return rest[0] ?? "";
  if (kind === "alert") return rest.slice(1).join(":");
  return "";
}

/**
 * The server a finding is about: by its `/infra/servers/:id` link, the host
 * in its key (any name the server goes by), or the server's name as a word
 * of the title.
 */
export function itemServer<S extends Pick<Server, "id" | "name" | "public_address" | "tailscale_ip"> & { ssh_target?: string }>(
  i: Pick<CheckItem, "key" | "category" | "title" | "link">,
  servers: S[],
): S | undefined {
  const linked = i.link?.match(/^\/infra\/servers\/(\d+)/);
  if (linked) {
    const s = servers.find((x) => x.id === Number(linked[1]));
    if (s) return s;
  }
  const host = itemHost(i);
  if (host) {
    const names = hostAliases(host);
    const s = servers.find((x) => {
      const aliases = serverAliases(x);
      return names.some((n) => aliases.has(n));
    });
    if (s) return s;
  }
  const words = new Set(i.title.toLowerCase().split(/[^a-z0-9._-]+/).filter(Boolean));
  return servers.find((x) => words.has(x.name.toLowerCase()));
}

/** Production-critical: marked critical, or in the production environment. */
export const isProdServer = (s: Pick<Server, "critical" | "environment">) => s.critical || s.environment === "production";

/** In-app routes navigate; anything else opens in a new tab. */
export const isInternalLink = (link: string) => link.startsWith("/") && !link.startsWith("//");
