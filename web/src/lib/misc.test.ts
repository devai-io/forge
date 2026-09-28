import { makeTask } from "@/test/fixtures";
import { niceMax } from "./chart";
import { bucketChecks } from "./infra";
import { passwordStrength } from "./password";
import { isStale, syncLabel } from "./projects";
import { defaultListFilters, filterTasks } from "./taskFilters";
import { safeNext } from "./auth";
import type { Project } from "@/api/types";

describe("niceMax", () => {
  it("rounds an axis top to a clean number with at most four steps", () => {
    expect(niceMax(0)).toEqual({ top: 4, step: 2 });
    expect(niceMax(3)).toEqual({ top: 3, step: 1 });
    expect(niceMax(7)).toEqual({ top: 8, step: 2 });
    expect(niceMax(37)).toEqual({ top: 40, step: 10 });
  });
});

describe("bucketChecks", () => {
  it("puts checks into half-hour windows and flags any failure", () => {
    const now = Date.parse("2026-09-27T12:00:00Z");
    const b = bucketChecks(
      [
        { at: "2026-09-27T11:50:00Z", ok: true, code: 200, latency_ms: 80, error: "" },
        { at: "2026-09-27T11:52:00Z", ok: false, code: 502, latency_ms: 90, error: "" },
        { at: "2026-09-26T08:00:00Z", ok: true, code: 200, latency_ms: 80, error: "" }, // outside the window
      ],
      now,
    );
    expect(b).toHaveLength(48);
    expect(b[47]).toMatchObject({ ok: 1, fail: 1 });
    expect(b.reduce((n, x) => n + x.ok + x.fail, 0)).toBe(2);
  });
});

describe("passwordStrength", () => {
  it("scores length and variety, and punishes repetition", () => {
    expect(passwordStrength("short").score).toBe(0);
    expect(passwordStrength("abcdefghijkl").score).toBe(1);
    expect(passwordStrength("correct-Horse-battery-9").score).toBe(4);
    expect(passwordStrength("aaaaaaaaaaaaaaaaaaaaaa").score).toBeLessThanOrEqual(1);
  });
});

describe("isStale", () => {
  const base = { status: "live", stats: { last_activity_at: "2026-09-10T00:00:00Z" } } as unknown as Project;
  const now = new Date("2026-09-27T00:00:00Z");
  it("flags live/building projects idle for over a week only", () => {
    expect(isStale(base, now)).toBe(true);
    expect(isStale({ ...base, status: "radar" } as Project, now)).toBe(false);
    expect(isStale({ ...base, stats: { ...base.stats, last_activity_at: "2026-09-25T00:00:00Z" } } as Project, now)).toBe(false);
  });
});

describe("filterTasks", () => {
  const tasks = [
    makeTask({ id: 1, title: "Fix login", labels: ["auth"], status: "todo" }),
    makeTask({ id: 2, ref: "SHOP-7", title: "Payouts", status: "done" }),
    makeTask({ id: 3, title: "Jigsaw review", priority: "urgent", status: "in_progress" }),
  ];
  it("applies status, search and priority filters", () => {
    expect(filterTasks(tasks, defaultListFilters).map((t) => t.id)).toEqual([1, 3]);
    expect(filterTasks(tasks, { ...defaultListFilters, statuses: [], q: "shop-7" }).map((t) => t.id)).toEqual([2]);
    expect(filterTasks(tasks, { ...defaultListFilters, q: "AUTH" }).map((t) => t.id)).toEqual([1]);
    expect(filterTasks(tasks, { ...defaultListFilters, priority: "urgent" }).map((t) => t.id)).toEqual([3]);
  });
});

describe("safeNext", () => {
  it("only follows same-app paths", () => {
    expect(safeNext("/p/SHOP?tab=board")).toBe("/p/SHOP?tab=board");
    expect(safeNext("https://evil.example")).toBe("/");
    expect(safeNext("//evil.example")).toBe("/");
    expect(safeNext("/\\evil.example")).toBe("/");
    expect(safeNext(null)).toBe("/");
  });
});

describe("syncLabel", () => {
  it("says what the last automatic pull did", () => {
    const at = "2026-09-28T10:00:00Z";
    expect(syncLabel({ at, result: "pulled", detail: "fast-forwarded 3 commits", pulled: 3 }).text).toBe("pulled 3");
    expect(syncLabel({ at, result: "up_to_date", detail: "", pulled: 0 })).toMatchObject({ text: "synced", tone: "muted" });
    expect(syncLabel({ at, result: "skipped", detail: "2 local changes; 1 commit to pull", pulled: 0 })).toMatchObject({
      text: "pull skipped",
      tone: "warning",
      title: "2 local changes; 1 commit to pull",
    });
    expect(syncLabel({ at, result: "error", detail: "fetch: Permission denied", pulled: 0 }).tone).toBe("critical");
  });
});
