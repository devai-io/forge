// Activity feed helpers.

import type { Activity } from "@/api/types";

/**
 * Folds runs of look-alike entries ("shop_api: pulled 3 new commit(s)",
 * then 6, then 2) into the newest one with a count. Entries about a task or a
 * run never fold: each of those is its own event.
 */
export function foldRepeats(items: Activity[]): { a: Activity; count: number }[] {
  const out: { a: Activity; count: number }[] = [];
  const shape = (a: Activity) => `${a.kind}|${a.project_key ?? ""}|${a.summary.replace(/\d+/g, "#")}`;
  for (const a of items) {
    const last = out[out.length - 1];
    if (last && !a.task_id && !a.run_id && !last.a.task_id && !last.a.run_id && shape(last.a) === shape(a)) last.count++;
    else out.push({ a, count: 1 });
  }
  return out;
}
