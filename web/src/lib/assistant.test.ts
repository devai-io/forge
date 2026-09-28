import { ApiError } from "@/api/client";
import type { Chat, ChatMessage } from "@/api/types";
import { buildChatItems, describeToolCall, formatTokens, isAssistantOff, usageLabel } from "./assistant";
import { lastSeq, mergeMessages, upsertChat } from "./chats";

const msg = (over: Partial<ChatMessage>): ChatMessage => ({
  seq: 1,
  role: "user",
  content: "",
  tool_calls: [],
  tool_call_id: "",
  tool_name: "",
  result: null,
  is_error: false,
  created_at: "2026-09-28T10:00:00Z",
  ...over,
});

const chat = (over: Partial<Chat>): Chat => ({
  id: 1,
  title: "",
  busy: false,
  last_error: "",
  usage: { input_tokens: 0, output_tokens: 0, cached_tokens: 0 },
  created_at: "2026-09-28T10:00:00Z",
  updated_at: "2026-09-28T10:00:00Z",
  ...over,
});

describe("mergeMessages", () => {
  it("appends a page, drops duplicates by seq (newer copy wins) and keeps seq order", () => {
    const prev = [msg({ seq: 1, content: "hi" }), msg({ seq: 2, role: "assistant", content: "old" })];
    const merged = mergeMessages(prev, [msg({ seq: 3, content: "more" }), msg({ seq: 2, role: "assistant", content: "new" })]);
    expect(merged.map((m) => m.seq)).toEqual([1, 2, 3]);
    expect(merged[1].content).toBe("new");
    expect(lastSeq(merged)).toBe(3);
  });

  it("returns the same array for an empty page, and lastSeq of nothing is 0", () => {
    const prev = [msg({ seq: 1 })];
    expect(mergeMessages(prev, [])).toBe(prev);
    expect(lastSeq(undefined)).toBe(0);
    expect(lastSeq([])).toBe(0);
  });
});

describe("upsertChat", () => {
  it("replaces a chat in place or puts a new one first", () => {
    const list = [chat({ id: 2 }), chat({ id: 1 })];
    expect(upsertChat(list, chat({ id: 1, busy: true })).map((c) => [c.id, c.busy])).toEqual([
      [2, false],
      [1, true],
    ]);
    expect(upsertChat(list, chat({ id: 3 })).map((c) => c.id)).toEqual([3, 2, 1]);
  });
});

describe("buildChatItems", () => {
  it("pairs tool calls with their answers and skips empty assistant prose", () => {
    const items = buildChatItems([
      msg({ seq: 1, content: "Fix it" }),
      msg({
        seq: 2,
        role: "assistant",
        content: "",
        tool_calls: [
          { id: "a", name: "list_tasks", arguments: {} },
          { id: "b", name: "get_run", arguments: { run_id: 4 } },
        ],
      }),
      msg({ seq: 3, role: "tool", tool_call_id: "a", tool_name: "list_tasks", result: { tasks: [] } }),
      msg({ seq: 4, role: "assistant", content: "Done." }),
    ]);
    expect(items.map((i) => i.kind)).toEqual(["user", "tool", "tool", "assistant"]);
    const [, a, b] = items;
    expect(a.kind === "tool" && a.result?.seq).toBe(3);
    // No answer yet: still running.
    expect(b.kind === "tool" && b.result).toBeNull();
  });

  it("still shows a tool message nothing called", () => {
    const items = buildChatItems([msg({ seq: 5, role: "tool", tool_call_id: "x", tool_name: "list_runs", result: {} })]);
    expect(items).toHaveLength(1);
    expect(items[0].kind === "tool" && items[0].call.name).toBe("list_runs");
  });
});

describe("describeToolCall", () => {
  it("describes a delegation from its result, falling back to the arguments", () => {
    const args = { project: "SHOP", repo: "shop_api", prompt: "Fix the flaky test", permission_mode: "plan" };
    const done = describeToolCall("delegate_to_claude", args, {
      run_id: 42,
      machine: "desk",
      project: "SHOP",
      repo: "shop_api",
      permission_mode: "acceptEdits",
      model: "sonnet",
      model_note: "Jev: ordinary task",
    });
    expect(done).toMatchObject({
      kind: "delegate",
      text: "Delegated to Claude Code on desk · SHOP/shop_api",
      runId: 42,
      prompt: "Fix the flaky test",
      permissionMode: "acceptEdits",
      model: "sonnet",
      modelNote: "Jev: ordinary task",
    });
    expect(describeToolCall("delegate_to_claude", args, undefined)).toMatchObject({
      text: "Delegating to Claude Code · SHOP/shop_api",
      runId: null,
      permissionMode: "plan",
    });
    expect(describeToolCall("delegate_to_claude", args, null, true).text).toBe("Couldn't delegate to Claude Code · SHOP/shop_api");
  });

  it("describes commands and run checks", () => {
    expect(describeToolCall("run_command", { command: "git-status" }, { run_id: 9, machine: "laptop", command: "git-status" })).toMatchObject({
      kind: "command",
      text: "Ran git-status on laptop",
      runId: 9,
    });
    expect(describeToolCall("get_run", { run_id: 9 }, { run: { id: 9, status: "succeeded" } }).text).toBe("Checked run #9 — succeeded");
    expect(describeToolCall("get_run", { run_id: 9 }, undefined).text).toBe("Checking run #9…");
    expect(
      describeToolCall("wait_for_runs", { run_ids: [9, 10] }, { runs: [{ id: 9, status: "failed" }, { id: 10, status: "running" }], timed_out: true }).text,
    ).toBe("Checked runs #9 — failed, #10 — still running · stopped waiting");
    expect(describeToolCall("wait_for_runs", { run_ids: [9] }, { runs: [{ id: 9, status: "succeeded" }], timed_out: false }).text).toBe(
      "Checked run #9 — succeeded",
    );
    expect(describeToolCall("wait_for_runs", { run_ids: [9] }, undefined).text).toBe("Waiting for run #9…");
  });

  it("links created and updated tasks", () => {
    const created = describeToolCall("create_task", { title: "Pause menu" }, { task: { id: 5, ref: "GAME-3", title: "Pause menu" } });
    expect(created).toMatchObject({ kind: "task", text: "Created GAME-3 · Pause menu", task: { id: 5, ref: "GAME-3" } });
    const updated = describeToolCall("update_task", { ref: "GAME-3", status: "in_progress" }, { task: { id: 5, ref: "GAME-3", title: "Pause menu" } });
    expect(updated.text).toBe("Updated GAME-3 · Pause menu → in progress");
    expect(describeToolCall("create_task", { title: "Pause menu" }, null, true)).toMatchObject({ text: "Couldn't create task “Pause menu”", task: null });
  });

  it("gives every other tool a plain line, even unknown ones and odd shapes", () => {
    expect(describeToolCall("list_projects", {}, {}).text).toBe("Looked at projects");
    expect(describeToolCall("list_tasks", { project: "SHOP" }, {}).text).toBe("Listed tasks in SHOP");
    expect(describeToolCall("comment_on_task", { ref: "SHOP-12" }, {}, true).text).toBe("Couldn't comment on SHOP-12");
    expect(describeToolCall("get_overview", {}, "not an object").text).toBe("Looked at the overview");
    expect(describeToolCall("read_the_stars", {}, {})).toMatchObject({ kind: "generic", text: "Used read the stars" });
  });
});

describe("numbers and errors", () => {
  it("formats token counts", () => {
    expect(formatTokens(950)).toBe("950");
    expect(formatTokens(1000)).toBe("1k");
    expect(formatTokens(12_345)).toBe("12.3k");
    expect(formatTokens(1_100)).toBe("1.1k");
    expect(formatTokens(2_400_000)).toBe("2.4M");
    expect(usageLabel({ input_tokens: 12_345, output_tokens: 1_100, cached_tokens: 9_000 })).toBe("12.3k in / 1.1k out tokens");
  });

  it("recognises the assistant_off error", () => {
    expect(isAssistantOff(new ApiError(503, "assistant_off", "off"))).toBe(true);
    expect(isAssistantOff(new ApiError(503, "internal", "boom"))).toBe(false);
    expect(isAssistantOff(new Error("assistant_off"))).toBe(false);
  });
});
