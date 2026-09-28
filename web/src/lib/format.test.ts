import {
  addDays,
  countdown,
  daysBetween,
  dueInfo,
  formatCost,
  formatDuration,
  relativeTime,
  todayInTz,
} from "./format";

describe("todayInTz", () => {
  // 23:30 UTC on 27 Sep is already 28 Sep in Lisbon (UTC+2) and still 27 Sep in New York.
  const late = new Date("2026-09-27T23:30:00Z");
  it("uses the account timezone, not the browser's", () => {
    expect(todayInTz("Europe/Lisbon", late)).toBe("2026-09-28");
    expect(todayInTz("America/New_York", late)).toBe("2026-09-27");
    expect(todayInTz("UTC", late)).toBe("2026-09-27");
  });
  it("falls back instead of throwing on an unknown zone", () => {
    expect(todayInTz("Not/AZone", late)).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });
});

describe("calendar arithmetic", () => {
  it("counts whole days across month ends and DST changes", () => {
    expect(daysBetween("2026-09-27", "2026-10-06")).toBe(9);
    expect(daysBetween("2026-10-24", "2026-10-26")).toBe(2);
    expect(daysBetween("2026-10-06", "2026-09-27")).toBe(-9);
  });
  it("adds days", () => {
    expect(addDays("2026-09-30", 1)).toBe("2026-10-01");
    expect(addDays("2026-01-01", -1)).toBe("2025-12-31");
  });
});

describe("dueInfo", () => {
  const today = "2026-09-27";
  it("labels overdue, today, tomorrow and later", () => {
    expect(dueInfo("2026-09-24", today)).toMatchObject({ state: "overdue", label: "3d overdue" });
    expect(dueInfo("2026-09-26", today)).toMatchObject({ state: "overdue", label: "Yesterday" });
    expect(dueInfo("2026-09-27", today)).toMatchObject({ state: "today", label: "Today" });
    expect(dueInfo("2026-09-28", today)).toMatchObject({ state: "soon", label: "Tomorrow" });
    expect(dueInfo("2026-10-20", today)).toMatchObject({ state: "later", label: "20 Oct" });
  });
  it("counts down to a target", () => {
    expect(countdown("2026-10-06", today)).toBe("in 9 days");
    expect(countdown("2026-09-27", today)).toBe("today");
    expect(countdown("2026-09-20", today)).toBe("7 days ago");
  });
});

describe("relativeTime", () => {
  const now = new Date("2026-09-27T12:00:00Z");
  it("formats past and future spans compactly", () => {
    expect(relativeTime("2026-09-27T11:59:50Z", now)).toBe("just now");
    expect(relativeTime("2026-09-27T11:55:00Z", now)).toBe("5m ago");
    expect(relativeTime("2026-09-27T09:00:00Z", now)).toBe("3h ago");
    expect(relativeTime("2026-09-25T12:00:00Z", now)).toBe("2d ago");
    expect(relativeTime("2026-09-27T16:00:00Z", now)).toBe("in 4h");
    expect(relativeTime(null, now)).toBe("never");
  });
});

describe("durations and money", () => {
  it("formats durations", () => {
    expect(formatDuration(450)).toBe("450ms");
    expect(formatDuration(12_000)).toBe("12s");
    expect(formatDuration(83_000)).toBe("1m 23s");
    expect(formatDuration(7_500_000)).toBe("2h 5m");
    expect(formatDuration(null)).toBe("—");
  });
  it("formats cost", () => {
    expect(formatCost(0.1234)).toBe("$0.12");
    expect(formatCost(0.004)).toBe("<$0.01");
    expect(formatCost(null)).toBe("—");
  });
});
