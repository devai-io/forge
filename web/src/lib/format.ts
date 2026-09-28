// Date, time and number formatting.
//
// "Today" is the user's today, not the browser's: the API computes streaks and
// overdue counts in the account timezone (User.timezone), and a laptop in a
// different zone must agree with it or a task shows "due today" on one screen
// and "overdue" on the next. Calendar dates ("YYYY-MM-DD") are compared as
// dates, never through Date objects in local time.

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** "YYYY-MM-DD" for `now` as seen in `timeZone` (falls back to the browser zone). */
export function todayInTz(timeZone?: string, now: Date = new Date()): string {
  try {
    return new Intl.DateTimeFormat("en-CA", {
      timeZone: timeZone || undefined,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
    }).format(now);
  } catch {
    return new Intl.DateTimeFormat("en-CA", { year: "numeric", month: "2-digit", day: "2-digit" }).format(now);
  }
}

/** Hour of day (0-23) in `timeZone`. */
export function hourInTz(timeZone?: string, now: Date = new Date()): number {
  try {
    const h = new Intl.DateTimeFormat("en-GB", { timeZone: timeZone || undefined, hour: "2-digit", hour12: false }).format(
      now,
    );
    return Number(h) % 24;
  } catch {
    return now.getHours();
  }
}

function dateToUtcDays(date: string): number {
  const [y, m, d] = date.slice(0, 10).split("-").map(Number);
  return Date.UTC(y, m - 1, d) / DAY;
}

/** Whole days from `from` to `to` (both "YYYY-MM-DD"); positive when `to` is later. */
export function daysBetween(from: string, to: string): number {
  return Math.round(dateToUtcDays(to) - dateToUtcDays(from));
}

/** Add `n` days to a calendar date. */
export function addDays(date: string, n: number): string {
  const ms = (dateToUtcDays(date) + n) * DAY;
  return new Date(ms).toISOString().slice(0, 10);
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

/** "27 Sep", or "27 Sep 2025" when not in `today`'s year. */
export function formatCalendarDate(date: string, today?: string): string {
  const [y, m, d] = date.slice(0, 10).split("-").map(Number);
  const base = `${d} ${MONTHS[m - 1]}`;
  if (today && Number(today.slice(0, 4)) === y) return base;
  return `${base} ${y}`;
}

export function weekdayOf(date: string): string {
  const [y, m, d] = date.slice(0, 10).split("-").map(Number);
  return WEEKDAYS[new Date(Date.UTC(y, m - 1, d)).getUTCDay()];
}

export type DueState = "overdue" | "today" | "soon" | "later";

/** Label and urgency for a due date relative to today. */
export function dueInfo(due: string, today: string): { label: string; state: DueState; days: number } {
  const days = daysBetween(today, due);
  if (days < 0) return { label: days === -1 ? "Yesterday" : `${-days}d overdue`, state: "overdue", days };
  if (days === 0) return { label: "Today", state: "today", days };
  if (days === 1) return { label: "Tomorrow", state: "soon", days };
  if (days <= 6) return { label: `${weekdayOf(due)} · ${days}d`, state: days <= 3 ? "soon" : "later", days };
  return { label: formatCalendarDate(due, today), state: "later", days };
}

/** "in 9 days" / "today" / "3 days ago" for a target date. */
export function countdown(target: string, today: string): string {
  const days = daysBetween(today, target);
  if (days === 0) return "today";
  if (days === 1) return "tomorrow";
  if (days === -1) return "yesterday";
  if (days > 0) return `in ${days} days`;
  return `${-days} days ago`;
}

/** "just now", "5m ago", "3h ago", "2d ago", "in 4h" … */
export function relativeTime(iso: string | null | undefined, now: Date = new Date()): string {
  if (!iso) return "never";
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "—";
  const diff = now.getTime() - then;
  const future = diff < 0;
  const abs = Math.abs(diff);
  let text: string;
  if (abs < 45_000) return future ? "in a moment" : "just now";
  if (abs < HOUR) text = `${Math.round(abs / MINUTE)}m`;
  else if (abs < DAY) text = `${Math.round(abs / HOUR)}h`;
  else if (abs < 14 * DAY) text = `${Math.round(abs / DAY)}d`;
  else if (abs < 60 * DAY) text = `${Math.round(abs / (7 * DAY))}w`;
  else if (abs < 365 * DAY) text = `${Math.round(abs / (30 * DAY))}mo`;
  else text = `${Math.round(abs / (365 * DAY))}y`;
  return future ? `in ${text}` : `${text} ago`;
}

/** Absolute timestamp for hover titles, in the given zone. */
export function formatDateTime(iso: string | null | undefined, timeZone?: string): string {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  try {
    return new Intl.DateTimeFormat("en-GB", {
      timeZone: timeZone || undefined,
      dateStyle: "medium",
      timeStyle: "short",
    }).format(date);
  } catch {
    return date.toLocaleString();
  }
}

/** Short clock time "14:05" in the given zone. */
export function formatTime(iso: string, timeZone?: string): string {
  try {
    return new Intl.DateTimeFormat("en-GB", {
      timeZone: timeZone || undefined,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    }).format(new Date(iso));
  } catch {
    return iso.slice(11, 19);
  }
}

/** "450ms", "12s", "1m 23s", "2h 5m". */
export function formatDuration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || Number.isNaN(ms)) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

export function formatCost(usd: number | null | undefined): string {
  if (usd === null || usd === undefined) return "—";
  if (usd === 0) return "$0";
  if (usd < 0.01) return "<$0.01";
  return `$${usd.toFixed(2)}`;
}

/** 1,284 / 12.9K / 4.2M */
export function compactNumber(n: number): string {
  if (Math.abs(n) < 10_000) return n.toLocaleString("en-US");
  return new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 }).format(n);
}

export function percent(fraction: number | null | undefined, digits = 0): string {
  if (fraction === null || fraction === undefined) return "—";
  return `${(fraction * 100).toFixed(digits)}%`;
}

export function greeting(hour: number): string {
  if (hour < 5) return "Late night";
  if (hour < 12) return "Good morning";
  if (hour < 18) return "Good afternoon";
  return "Good evening";
}

/** Long form "Sunday, 27 September" for the dashboard header. */
export function longDate(date: string): string {
  const [y, m, d] = date.split("-").map(Number);
  return new Intl.DateTimeFormat("en-GB", { weekday: "long", day: "numeric", month: "long", timeZone: "UTC" }).format(
    new Date(Date.UTC(y, m - 1, d)),
  );
}
