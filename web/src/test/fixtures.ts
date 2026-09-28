import type { RunEvent, Task, TmuxSession, TmuxWindow } from "@/api/types";

export function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 1,
    project_id: 1,
    project_key: "SHOP",
    project_name: "Shop",
    project_color: "#3987e5",
    number: 12,
    ref: "SHOP-12",
    title: "Ship the payout dashboard",
    description: "Make payouts visible.",
    status: "todo",
    priority: "high",
    type: "feature",
    labels: ["launch"],
    due_date: null,
    focus: false,
    repo_id: null,
    repo_name: null,
    estimate: null,
    sort_order: 1000,
    completed_at: null,
    created_at: "2026-09-20T10:00:00Z",
    updated_at: "2026-09-26T10:00:00Z",
    comment_count: 0,
    ...overrides,
  };
}

let seq = 0;
export const ev = (kind: RunEvent["kind"], data: unknown): RunEvent => ({
  seq: ++seq,
  at: "2026-09-27T10:00:00Z",
  kind,
  data,
});

export function claudeTranscript(): RunEvent[] {
  seq = 0;
  return [
    ev("system", { text: "runner: starting claude in /home/ada/dev/shop/shop_api" }),
    ev("claude", {
      type: "system",
      subtype: "init",
      model: "claude-opus-5-5",
      cwd: "/home/ada/dev/shop/shop_api",
      session_id: "sess-1",
      permissionMode: "plan",
      tools: ["Bash", "Read", "Grep"],
    }),
    ev("claude", {
      type: "assistant",
      message: {
        content: [
          { type: "text", text: "Let me look at the **tests**." },
          { type: "tool_use", id: "toolu_1", name: "Bash", input: { command: "go test ./...", description: "Run tests" } },
        ],
      },
    }),
    ev("claude", {
      type: "user",
      message: { content: [{ type: "tool_result", tool_use_id: "toolu_1", content: [{ type: "text", text: "ok  shop/pkg/api" }] }] },
    }),
    ev("claude", {
      type: "assistant",
      message: { content: [{ type: "tool_use", id: "toolu_2", name: "Read", input: { file_path: "/x/main.go" } }] },
    }),
    ev("claude", {
      type: "user",
      message: { content: [{ type: "tool_result", tool_use_id: "toolu_2", content: "boom", is_error: true }] },
    }),
    ev("claude", {
      type: "result",
      subtype: "success",
      is_error: false,
      result: "All tests pass.",
      total_cost_usd: 0.1234,
      num_turns: 4,
      duration_ms: 65000,
      session_id: "sess-1",
    }),
    ev("stderr", { text: "warning: something" }),
    ev("stdout", { text: "done" }),
  ];
}

export function makeWindow(overrides: Partial<TmuxWindow> = {}): TmuxWindow {
  return {
    index: 0,
    name: "zsh",
    active: true,
    command: "zsh",
    path: "/home/ada",
    claude: false,
    project_key: null,
    project_color: null,
    repo_id: null,
    repo_name: null,
    ...overrides,
  };
}

export function makeSession(overrides: Partial<TmuxSession> = {}): TmuxSession {
  return {
    name: "main",
    windows: 1,
    window_list: [],
    attached: 0,
    created: "2026-09-27T08:00:00Z",
    activity: "2026-09-27T10:00:00Z",
    path: "/home/ada",
    command: "zsh",
    claude: false,
    project_key: null,
    project_color: null,
    repo_id: null,
    repo_name: null,
    ...overrides,
  };
}
