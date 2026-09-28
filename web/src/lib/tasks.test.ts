import { makeTask } from "@/test/fixtures";
import { buildAgentPrompt, formatRef, groupByStatus, isOverdue, normalizeLabel, parseRef, sortTasks } from "./tasks";

describe("task refs", () => {
  it("formats and parses refs", () => {
    expect(formatRef("shop", 12)).toBe("SHOP-12");
    expect(parseRef(" game-3 ")).toEqual({ key: "GAME", number: 3 });
    expect(parseRef("not a ref")).toBeNull();
    expect(parseRef("1X-2")).toBeNull();
  });
});

describe("isOverdue", () => {
  it("is overdue only when open and due before today", () => {
    expect(isOverdue({ status: "todo", due_date: "2026-09-26" }, "2026-09-27")).toBe(true);
    expect(isOverdue({ status: "todo", due_date: "2026-09-27" }, "2026-09-27")).toBe(false);
    expect(isOverdue({ status: "done", due_date: "2026-09-01" }, "2026-09-27")).toBe(false);
    expect(isOverdue({ status: "todo", due_date: null }, "2026-09-27")).toBe(false);
  });
});

describe("grouping and sorting", () => {
  const tasks = [
    makeTask({ id: 1, status: "todo", sort_order: 2000, priority: "low" }),
    makeTask({ id: 2, status: "done", sort_order: 1000, priority: "urgent" }),
    makeTask({ id: 3, status: "todo", sort_order: 1000, priority: "medium", due_date: "2026-10-01" }),
    makeTask({ id: 4, status: "blocked", sort_order: 1000, priority: "high", due_date: "2026-09-28" }),
  ];
  it("groups by status in board order", () => {
    const g = groupByStatus(tasks);
    expect(g.todo.map((t) => t.id)).toEqual([3, 1]);
    expect(g.done.map((t) => t.id)).toEqual([2]);
    expect(g.backlog).toEqual([]);
  });
  it("sorts by priority, then due date", () => {
    expect(sortTasks(tasks, "priority").map((t) => t.id)).toEqual([2, 4, 3, 1]);
    expect(sortTasks(tasks, "due").map((t) => t.id)).toEqual([4, 3, 2, 1]);
    expect(sortTasks(tasks, "board").map((t) => t.id)).toEqual([3, 1, 4, 2]);
  });
});

describe("labels and prompts", () => {
  it("normalises labels", () => {
    expect(normalizeLabel("  Launch Day ")).toBe("launch-day");
  });
  it("builds an agent prompt from a task", () => {
    expect(buildAgentPrompt({ ref: "GAME-4", title: "Review Maths", description: "Check every level.\n" })).toBe(
      "GAME-4: Review Maths\n\nCheck every level.",
    );
    expect(buildAgentPrompt({ ref: "GAME-5", title: "Jigsaw", description: "" })).toBe("GAME-5: Jigsaw");
  });
});
