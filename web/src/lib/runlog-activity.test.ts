import type { DisplayItem } from "./runlog";
import { groupActivity, summarizeActivity, toolDiff, toolLabel } from "./runlog";

const tool = (key: string, name: string, result: { text: string; isError: boolean } | null = { text: "", isError: false }): DisplayItem => ({
  kind: "tool", key, id: key, name, summary: key, input: {}, result,
});
const text = (key: string): DisplayItem => ({ kind: "text", key, text: key });

describe("tool activity", () => {
  it("folds each stretch of tool calls into one block", () => {
    const blocks = groupActivity([
      text("a"),
      tool("t1", "Bash"),
      { kind: "thinking", key: "th", text: "hmm" },
      tool("t2", "Bash"),
      tool("t3", "Read"),
      text("b"),
      { kind: "thinking", key: "th2", text: "alone" },
      text("c"),
    ]);
    expect(blocks.map((b) => b.kind)).toEqual(["text", "activity", "text", "thinking", "text"]);
    const act = blocks[1] as Extract<(typeof blocks)[number], { kind: "activity" }>;
    expect(act.items.map((i) => i.key)).toEqual(["t1", "th", "t2", "t3"]);
  });

  it("sums a stretch up: counts in order, errors, what runs now", () => {
    const s = summarizeActivity([
      tool("t1", "Bash"),
      tool("t2", "Read", { text: "nope", isError: true }),
      tool("t3", "Bash", null),
    ] as never);
    expect(s).toMatchObject({ tools: 3, label: "Bash ×2 · Read", errors: 1, pending: true });
    expect(s.last?.key).toBe("t3");
    expect(toolLabel("mcp__forge__forge_tasks")).toBe("forge·forge_tasks");
  });

  it("shows an edit as a diff", () => {
    expect(toolDiff("Edit", { file_path: "/a.go", old_string: "x := 1", new_string: "x := 2\ny := 3" })).toEqual({
      file: "/a.go",
      lines: [
        { sign: "-", text: "x := 1" },
        { sign: "+", text: "x := 2" },
        { sign: "+", text: "y := 3" },
      ],
    });
    expect(toolDiff("Write", { file_path: "/b", content: "hi" })?.lines).toEqual([{ sign: "+", text: "hi" }]);
    expect(toolDiff("Bash", { command: "ls" })).toBeNull();
  });
});
