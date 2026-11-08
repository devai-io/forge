// The "act on it from the first screen" round: the task list leads with what is
// moving, run pages lead with the result, titles say what a run was about, and
// task refs in markdown open the task.

import { QueryClient } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { Route, Routes, useLocation } from "react-router-dom";
import type { Run, RunEvent } from "@/api/types";
import { Markdown } from "@/components/ui/Markdown";
import { AgentsPage } from "@/features/agents/AgentsPage";
import { RunPage } from "@/features/agents/RunPage";
import { ShellProvider } from "@/features/shell/context";
import { TaskList } from "@/features/tasks/TaskList";
import { groupRunsByDay, runTag, runTitle } from "@/lib/agents";
import { mapRunEvents, withoutEchoedResult } from "@/lib/runlog";
import { defaultListFilters, type ListFilters } from "@/lib/taskFilters";
import { ev, makeTask } from "@/test/fixtures";
import { jsonResponse, renderWithProviders, routeFetch, testClient } from "@/test/render";

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});
beforeEach(() => localStorage.clear());
afterEach(() => vi.unstubAllGlobals());

const AT = "2026-10-02T10:00:00Z";

const run = (over: Partial<Run> = {}): Run => ({
  id: 7,
  runner_id: 1,
  runner_name: "desk",
  project_id: 1,
  project_key: "SHOP",
  project_color: "#3987e5",
  repo_id: 3,
  repo_name: "shop_api",
  task_id: null,
  task_ref: null,
  task_title: null,
  kind: "agent",
  prompt: "Look at the flaky checkout test",
  command: "",
  permission_mode: "plan",
  model: "",
  model_note: "",
  engine: "claude",
  effort: "",
  interactive: false,
  awaiting: "",
  worktree: false,
  resume_run_id: null,
  status: "succeeded",
  cancel_requested: false,
  session_id: "sess-7",
  result: "",
  error: "",
  exit_code: 0,
  cost_usd: 0.25,
  num_turns: 3,
  duration_ms: 60000,
  created_at: AT,
  started_at: AT,
  finished_at: AT,
  inbox_after: 0,
  resumes: 0,
  finish_requested: false,
  one_turn: false,
  handoff: "",
  resume_hint: "",
  ...over,
});

describe("task list", () => {
  const tasks = [
    makeTask({ id: 1, ref: "SHOP-1", title: "Old idea", status: "backlog" }),
    makeTask({ id: 2, ref: "SHOP-2", title: "Write docs", status: "todo" }),
    makeTask({ id: 3, ref: "SHOP-3", title: "Ship checkout", status: "in_progress", due_date: "2026-09-30" }),
    makeTask({ id: 4, ref: "SHOP-4", title: "Store review", status: "blocked" }),
  ];

  function List({ initial = defaultListFilters }: { initial?: ListFilters }) {
    const [filters, setFilters] = useState(initial);
    return <TaskList tasks={tasks} today="2026-09-27" onOpen={() => {}} filters={filters} onFiltersChange={setFilters} />;
  }

  it("leads with in progress and blocked, folds the backlog and remembers it", async () => {
    renderWithProviders(<List />);
    const user = userEvent.setup();
    const groupHeaders = screen.getAllByRole("button").filter((b) => b.hasAttribute("aria-expanded") && /\d$/.test(b.textContent ?? ""));
    expect(groupHeaders.map((b) => b.textContent?.replace(/\d+$/, ""))).toEqual(["In progress", "Blocked", "To do", "Backlog"]);

    expect(screen.getByText("Ship checkout")).toBeInTheDocument();
    expect(screen.queryByText("Old idea")).not.toBeInTheDocument();
    await user.click(groupHeaders[3]);
    expect(screen.getByText("Old idea")).toBeInTheDocument();
    expect(JSON.parse(localStorage.getItem("forge.tasks.collapsed")!)).toEqual([]);
    await user.click(groupHeaders[0]);
    expect(screen.queryByText("Ship checkout")).not.toBeInTheDocument();
    expect(JSON.parse(localStorage.getItem("forge.tasks.collapsed")!)).toEqual(["in_progress"]);
  });

  it("shows folded matches while searching, and counts the phone's filters", async () => {
    renderWithProviders(<List initial={{ ...defaultListFilters, q: "old", priority: "high" }} />);
    expect(screen.getByText("Old idea")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Filters, 1 set" }));
    const dialog = screen.getByRole("dialog", { name: "Filters" });
    expect(within(dialog).getByLabelText("Priority")).toHaveValue("high");
    await user.click(within(dialog).getByRole("button", { name: "Reset" }));
    expect(screen.getByRole("button", { name: "Filters" })).toBeInTheDocument();
  });
});

describe("run transcript and titles", () => {
  it("leaves out the system bookkeeping and the echoed result", () => {
    const events: RunEvent[] = [
      ev("claude", { type: "system", subtype: "thinking_tokens" }),
      ev("claude", { type: "system", subtype: "hook_started" }),
      ev("claude", { type: "system", subtype: "hook_response" }),
      ev("claude", { type: "system", subtype: "permission_check_status" }),
      ev("claude", { type: "system", subtype: "compact_boundary" }),
      ev("claude", { type: "assistant", message: { content: [{ type: "text", text: "All green." }] } }),
      ev("claude", { type: "result", subtype: "success", result: "All green.", is_error: false }),
    ];
    const items = mapRunEvents(events);
    expect(items.filter((i) => i.kind === "note").map((i) => (i as { text: string }).text)).toEqual(["system: compact_boundary"]);
    const trimmed = withoutEchoedResult(items, "All green.\n");
    expect(trimmed.some((i) => i.kind === "text")).toBe(false);
    expect(trimmed.some((i) => i.kind === "result")).toBe(true);
    expect(withoutEchoedResult(items, "Something else")).toHaveLength(items.length);
  });

  it("titles runs by task, result heading or prompt, with status checks as a tag", () => {
    expect(runTitle(run({ task_ref: "SHOP-4", task_title: "Fix rounding", prompt: "SHOP-4: Fix rounding\n\nlong" }))).toBe("SHOP-4 Fix rounding");
    expect(runTitle(run({ result: "## Checkout **audit**\n\nAll fine." }))).toBe("Checkout audit");
    expect(runTitle(run({ result: "## shop_api _payout_ check\n\nAll fine." }))).toBe("shop_api payout check");
    expect(runTitle(run({ status: "running", result: "## Not yet" }))).toBe("Look at the flaky checkout test");
    const status = run({ prompt: "Forge status check: carry on with this run's work from where you left off.\nIf it is finished…", result: "Done: tests pass." });
    expect(runTag(status)).toBe("status check");
    expect(runTitle(status)).toBe("Done: tests pass.");
    expect(runTitle(run({ prompt: "continue", status: "queued" }))).toBe("Continue");
    const long = runTitle(run({ prompt: `READ-ONLY. ${"I need to know where the payouts go ".repeat(5)}` }));
    expect(long.length).toBeLessThanOrEqual(90);
    expect(long.endsWith("…")).toBe(true);
    expect(long).not.toMatch(/\s…$/);
  });

  it("groups history by day with the day's cost", () => {
    const now = new Date("2026-10-02T12:00:00Z");
    const days = groupRunsByDay(
      [
        run({ id: 3, finished_at: "2026-10-02T09:00:00Z", cost_usd: 0.5 }),
        run({ id: 2, finished_at: "2026-10-01T09:00:00Z", cost_usd: null }),
        run({ id: 1, finished_at: "2026-09-20T09:00:00Z", cost_usd: 1 }),
        run({ id: 0, finished_at: "2026-09-19T09:00:00Z", cost_usd: 0.25 }),
      ],
      "UTC",
      now,
    );
    expect(days.map((d) => [d.label, d.runs.map((r) => r.id), d.costUsd])).toEqual([
      ["Today", [3], 0.5],
      ["Yesterday", [2], null],
      ["Earlier", [1, 0], 1.25],
    ]);
  });
});

describe("finished run page", () => {
  it("leads with the result and Continue, the prompt under the title, the transcript folded", async () => {
    const events: RunEvent[] = [
      ev("claude", { type: "assistant", message: { content: [{ type: "tool_use", id: "t1", name: "Bash", input: { command: "go test ./..." } }] } }),
      ev("claude", { type: "user", message: { content: [{ type: "tool_result", tool_use_id: "t1", content: "ok" }] } }),
      ev("claude", { type: "assistant", message: { content: [{ type: "text", text: "## Flaky test\n\nThe clock is not mocked." }] } }),
    ];
    const r = run({ result: "## Flaky test\n\nThe clock is not mocked." });
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /runs/7": () => jsonResponse(200, r),
        "GET /runs/7/events": () => jsonResponse(200, { run: r, events, prompts: [] }),
      }),
    );
    renderWithProviders(
      <ShellProvider>
        <Routes>
          <Route path="/agents/runs/:id" element={<RunPage />} />
        </Routes>
      </ShellProvider>,
      { route: "/agents/runs/7" },
    );
    const user = userEvent.setup();
    expect(await screen.findByRole("heading", { level: 1, name: "Flaky test" })).toBeInTheDocument();
    expect(screen.getByText("Look at the flaky checkout test")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Continue/ })).toBeInTheDocument();
    const toggle = await screen.findByRole("button", { name: /Transcript · 3 events · 1 tool call$/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("button", { name: /Bash/ })).not.toBeInTheDocument();
    await user.click(toggle);
    expect(screen.getByRole("button", { name: /Bash/ })).toBeInTheDocument();
    // The result shows once: in the Result panel, not again as the transcript's last message.
    expect(screen.getAllByText("The clock is not mocked.")).toHaveLength(1);
  });
});

describe("markdown task refs", () => {
  function Where({ onSearch }: { onSearch: (s: string) => void }) {
    onSearch(useLocation().search);
    return null;
  }

  it("opens the task from prose, never from code or links, and skips unknown keys", async () => {
    const qc: QueryClient = testClient();
    qc.setQueryData(["projects", { includeArchived: false }], [{ key: "SHOP" }]);
    qc.setQueryData(["tasks", { project: "SHOP" }], [makeTask({ id: 12, ref: "SHOP-12" })]);
    let search = "";
    renderWithProviders(
      <>
        <Where onSearch={(s) => (search = s)} />
        <Markdown>{"Blocked by SHOP-12, see `SHOP-13` and [SHOP-14](https://example.com). UTF-8 and GAME-2 stay text."}</Markdown>
      </>,
      { qc },
    );
    const user = userEvent.setup();
    expect(screen.getAllByRole("button")).toHaveLength(1);
    await user.click(screen.getByRole("button", { name: "SHOP-12" }));
    await waitFor(() => expect(search).toBe("?task=12"));
    expect(screen.getByText("SHOP-13").tagName).toBe("CODE");
    expect(screen.getByRole("link", { name: "SHOP-14" })).toHaveAttribute("href", "https://example.com");
  });

  it("falls back to the task list, searched, when no task has the ref", async () => {
    vi.stubGlobal("fetch", routeFetch({ "GET /tasks": () => jsonResponse(200, { tasks: [] }) }));
    let path = "";
    function Here() {
      const l = useLocation();
      path = l.pathname + l.search;
      return null;
    }
    renderWithProviders(
      <>
        <Here />
        <Markdown>{"See SHOP-99."}</Markdown>
      </>,
    );
    await userEvent.setup().click(screen.getByRole("button", { name: "SHOP-99" }));
    await waitFor(() => expect(path).toBe("/tasks?q=SHOP-99"));
  });
});

describe("agents page", () => {
  it("says nothing is open in one line and groups the history by day", async () => {
    const today = new Date().toISOString();
    const history = [run({ id: 9, finished_at: today, cost_usd: 0.5, task_ref: "SHOP-4", task_title: "Fix rounding" }), run({ id: 8, finished_at: "2026-01-02T10:00:00Z" })];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) =>
        url.startsWith("/api/projects")
          ? jsonResponse(200, { projects: [] })
          : jsonResponse(200, { runs: url.includes("status=") ? [] : history }),
      ),
    );
    renderWithProviders(
      <ShellProvider>
        <AgentsPage />
      </ShellProvider>,
    );
    const line = await screen.findByText(/Nothing open/);
    expect(within(line).getByRole("button", { name: "New run" })).toBeInTheDocument();
    const todayGroup = await screen.findByRole("region", { name: "Today" });
    expect(within(todayGroup).getByText("SHOP-4 Fix rounding")).toBeInTheDocument();
    expect(within(todayGroup).getByText(/1 run · \$0\.50/)).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Earlier" })).toBeInTheDocument();
  });
});
