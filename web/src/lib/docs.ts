// Documentation-page helpers: tier ordering/colours and the live-interval
// overlay from GET /api/system. Pure, tested.

import { useEffect } from "react";
import { useLocation } from "react-router-dom";
import type { SystemFacts } from "@/api/types";
import type { Tier } from "@/features/docs/content";

export const TIER_ORDER: Tier[] = ["automated", "live", "on-demand", "stored"];

export const TIER_TONE: Record<Tier, "accent" | "good" | "warning" | "neutral"> = {
  automated: "accent",
  live: "good",
  "on-demand": "warning",
  stored: "neutral",
};

/** Go durations as people say them: "2m0s" → "2m", "1h30m0s" → "1h 30m", "90s" → "90s". */
export function prettyDuration(raw: string): string {
  const m = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+(?:\.\d+)?)s)?$/.exec(raw.trim());
  if (!m || raw.trim() === "") return raw.trim();
  const [, h, min, s] = m;
  const parts: string[] = [];
  if (h && Number(h)) parts.push(`${Number(h)}h`);
  if (min && Number(min)) parts.push(`${Number(min)}m`);
  if (s && Number(s)) parts.push(`${Number(s)}s`);
  return parts.length ? parts.join(" ") : "0s";
}

/**
 * The live schedule for a source, from the API's own settings — so the page
 * says what the server does, not what the text assumed when it was written.
 */
export function formatInterval(key: string, intervals: Partial<SystemFacts["intervals"]> | undefined): string | null {
  const value = intervals?.[key as keyof SystemFacts["intervals"]];
  if (!value) return null;
  if (key === "checkup_time") return `daily at ${value}${intervals?.timezone ? ` ${intervals.timezone}` : ""}`;
  return `every ${prettyDuration(value)}`;
}

/** Sources grouped by tier, in the fixed tier order. */
export function groupByTier<T extends { tier: Tier }>(items: T[]): { tier: Tier; items: T[] }[] {
  return TIER_ORDER.map((tier) => ({ tier, items: items.filter((i) => i.tier === tier) })).filter((g) => g.items.length);
}

/** Checks grouped by category, keeping the order of first appearance. */
export function groupByCategory<T extends { category: string }>(items: T[]): { category: string; items: T[] }[] {
  const out: { category: string; items: T[] }[] = [];
  for (const item of items) {
    const g = out.find((x) => x.category === item.category);
    if (g) g.items.push(item);
    else out.push({ category: item.category, items: [item] });
  }
  return out;
}

/** "fail / warn" → ["fail", "warn"]. */
export const severities = (s: string) => s.split("/").map((x) => x.trim()).filter(Boolean);

/** A stable anchor id from a heading: "The daily check-up" → "the-daily-check-up". */
export const slug = (text: string) =>
  text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");

/** Scroll to `#anchor` after the page renders (and when the hash changes). */
export function useHashScroll(ready = true) {
  const { hash } = useLocation();
  useEffect(() => {
    if (!ready || !hash) return;
    const el = document.getElementById(decodeURIComponent(hash.slice(1)));
    if (!el) return;
    const t = window.setTimeout(() => el.scrollIntoView({ block: "start", behavior: "smooth" }), 30);
    return () => window.clearTimeout(t);
  }, [hash, ready]);
}
