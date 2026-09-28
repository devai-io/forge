import { claudeTranscript, ev } from "@/test/fixtures";
import { mapRunEvents, previewText, toolResultText, toolSummary } from "./runlog";

describe("mapRunEvents", () => {
  const items = mapRunEvents(claudeTranscript());

  it("keeps order and kinds", () => {
    expect(items.map((i) => i.kind)).toEqual(["log", "init", "text", "tool", "tool", "result", "log"]);
  });

  it("reads the init banner", () => {
    expect(items[1]).toMatchObject({ kind: "init", model: "claude-opus-5-5", permissionMode: "plan", tools: 3, sessionId: "sess-1" });
  });

  it("attaches tool results to the call they answer", () => {
    expect(items[3]).toMatchObject({ kind: "tool", name: "Bash", summary: "go test ./...", result: { text: "ok  shop/pkg/api", isError: false } });
    expect(items[4]).toMatchObject({ kind: "tool", name: "Read", summary: "/x/main.go", result: { text: "boom", isError: true } });
  });

  it("summarises the result", () => {
    expect(items[5]).toMatchObject({ kind: "result", text: "All tests pass.", costUsd: 0.1234, turns: 4, durationMs: 65000, isError: false });
  });

  it("merges consecutive plain log lines into one block", () => {
    const last = items[6];
    expect(last.kind).toBe("log");
    if (last.kind === "log") expect(last.lines.map((l) => [l.stream, l.text])).toEqual([["stderr", "warning: something"], ["stdout", "done"]]);
  });

  it("shows a result with no matching call on its own and ignores partial stream events", () => {
    const out = mapRunEvents([
      ev("claude", { type: "stream_event", event: {} }),
      ev("claude", { type: "user", message: { content: [{ type: "tool_result", tool_use_id: "nope", content: "orphan" }] } }),
    ]);
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ kind: "tool", name: "result", result: { text: "orphan" } });
  });
});

describe("tool helpers", () => {
  it("picks the telling argument per tool", () => {
    expect(toolSummary("Grep", { pattern: "TODO", path: "pkg" })).toBe("TODO in pkg");
    expect(toolSummary("TodoWrite", { todos: [1, 2] })).toBe("2 todos");
    expect(toolSummary("Mystery", { a: 1, b: "value" })).toBe("value");
    expect(toolSummary("Bash", { command: "a\n  b" })).toBe("a b");
  });
  it("flattens tool_result content", () => {
    expect(toolResultText([{ type: "text", text: "a" }, { type: "image" }, { type: "text", text: "b" }])).toBe("a\n[image]\nb");
    expect(toolResultText(null)).toBe("");
  });
  it("truncates long previews", () => {
    expect(previewText("x".repeat(700), 600)).toEqual({ text: "x".repeat(600), truncated: true });
  });
});
