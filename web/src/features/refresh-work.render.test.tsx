import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import type { Dashboard, Run } from "@/api/types";
import { WorkingOnWidget } from "@/features/dashboard/TopWidgets";
import { ShellProvider } from "@/features/shell/context";
import { continueInput, planRefresh, refreshText, summarize } from "@/lib/working";
import { makeTask } from "@/test/fixtures";
import { jsonResponse, renderWithProviders } from "@/test/render";

const run = (o: Partial<Run>): Run =>
  ({
    id: 1,
    runner_id: 3,
    runner_name: "desk",
    project_key: "SHOP",
    project_color: "#3987e5",
    repo_id: 7,
    repo_name: "shop_api",
    task_id: null,
    kind: "agent",
    prompt: "Work on it",
    command: "",
    permission_mode: "acceptEdits",
    model: "",
    engine: "claude",
    effort: "",
    interactive: true,
    awaiting: "",
    worktree: false,
    status: "running",
    cancel_requested: false,
    finish_requested: false,
    session_id: "sess-1",
    handoff: "",
    ...o,
  }) as Run;

const tA = makeTask({ id: 1, ref: "SHOP-1", title: "Payouts", status: "in_progress" });
const tB = makeTask({ id: 2, ref: "SHOP-2", title: "Login rate limit", status: "in_progress" });
const tC = makeTask({ id: 3, ref: "SHOP-3", title: "Receipts", status: "in_progress" });
const tD = makeTask({ id: 4, ref: "SHOP-4", title: "Docs", status: "in_progress" });
const tE = makeTask({ id: 5, ref: "SHOP-5", title: "Question pending", status: "in_progress" });

describe("planRefresh", () => {
  it("asks running sessions, resumes parked ones, continues ended ones", () => {
    const open = [
      run({ id: 10, task_id: 1, status: "running" }),
      run({ id: 11, task_id: 2, status: "waiting" }),
      run({ id: 14, task_id: 5, status: "running", awaiting: "answer" }),
    ];
    const latest = new Map<number, Run | undefined>([
      [3, run({ id: 12, task_id: 3, status: "succeeded" })],
      [4, undefined],
    ]);
    const plan = planRefresh([tA, tB, tC, tD, tE], open, latest);
    expect(plan.map((s) => [s.task.ref, s.action, s.run?.id ?? null])).toEqual([
      ["SHOP-1", "ask", 10],
      ["SHOP-2", "resume", 11],
      ["SHOP-3", "continue", 12],
      ["SHOP-4", "skip", null],
      ["SHOP-5", "skip", 14],
    ]);
    expect(plan[4]!.why).toBe("waiting for your answer");
    expect(summarize(plan)).toBe("1 to ask for status · 1 to resume · 1 to continue · 2 left alone");
  });

  it("leaves queued, ending, handed-off and session-less runs alone", () => {
    const plan = planRefresh(
      [tA, tB, tC, tD],
      [run({ id: 10, task_id: 1, status: "queued" }), run({ id: 11, task_id: 2, finish_requested: true })],
      new Map([
        [3, run({ id: 12, task_id: 3, status: "succeeded", handoff: "desk · tmux run-12" })],
        [4, run({ id: 13, task_id: 4, status: "failed", session_id: "" })],
      ]),
    );
    expect(plan.map((s) => s.action)).toEqual(["skip", "skip", "skip", "skip"]);
  });

  it("continues a session with the same machine, repo and settings", () => {
    const body = continueInput(run({ id: 12, permission_mode: "plan", effort: "high", model: "opus" }), tC);
    expect(body).toMatchObject({ runner_id: 3, repo_id: 7, kind: "agent", permission_mode: "plan", effort: "high", model: "opus", interactive: true, task_id: 3, resume_run_id: 12 });
    expect(body.prompt).toBe(refreshText(tC));
    expect(refreshText(tC)).toContain("forge_update_task");
  });
});

describe("Working on → Refresh", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows the plan, then messages and continues the sessions", async () => {
    const posts: [string, unknown][] = [];
    const fetch = vi.fn(async (url: string, init: RequestInit = {}) => {
      const u = new URL(url, "http://forge.test");
      const path = u.pathname.replace(/^\/api/, "");
      const method = init.method ?? "GET";
      if (method === "GET" && path === "/runs") {
        const task = Number(u.searchParams.get("task"));
        return jsonResponse(200, { runs: task === 3 ? [run({ id: 12, task_id: 3, status: "succeeded" })] : [] });
      }
      if (method === "POST") {
        posts.push([path, JSON.parse(String(init.body ?? "{}"))]);
        return jsonResponse(path === "/runs" ? 201 : 200, run({ id: 99 }));
      }
      return jsonResponse(404, { error: { code: "not_found", message: path } });
    });
    vi.stubGlobal("fetch", fetch);
    const d = {
      today: "2026-11-02",
      endpoints_down: [],
      endpoints_warning: [],
      in_progress: [tA, tB, tC, tD],
      active_runs: [run({ id: 10, task_id: 1, status: "running" }), run({ id: 11, task_id: 2, status: "waiting" })],
    } as unknown as Dashboard;
    renderWithProviders(
      <ShellProvider>
        <WorkingOnWidget d={d} />
      </ShellProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));
    const plan = await screen.findByRole("list", { name: "Refresh plan" });
    expect(within(plan).getByText(/run #12 ended: continue its session/)).toBeInTheDocument();
    expect(within(plan).getByText("no agent session")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Refresh 3" }));
    await waitFor(() => expect(posts).toHaveLength(3));
    expect(posts.map(([p]) => p)).toEqual(["/runs/10/messages", "/runs/11/messages", "/runs"]);
    expect((posts[0]![1] as { text: string }).text).toContain('SHOP-1 "Payouts"');
    expect(posts[2]![1]).toMatchObject({ resume_run_id: 12, task_id: 3, runner_id: 3, repo_id: 7 });
    await waitFor(() => expect(screen.queryByRole("list", { name: "Refresh plan" })).not.toBeInTheDocument());
  });

  it("continues a parked session whose machine cannot resume it in place", async () => {
    const posts: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit = {}) => {
        const path = new URL(url, "http://forge.test").pathname.replace(/^\/api/, "");
        if ((init.method ?? "GET") === "POST") {
          posts.push(path);
          if (path === "/runs/11/messages") return jsonResponse(422, { error: { code: "validation", message: "this machine's agent cannot resume in place" } });
          return jsonResponse(201, run({ id: 99 }));
        }
        return jsonResponse(200, { runs: [] });
      }),
    );
    const d = { in_progress: [tB], active_runs: [run({ id: 11, task_id: 2, status: "waiting" })], endpoints_down: [] } as unknown as Dashboard;
    renderWithProviders(
      <ShellProvider>
        <WorkingOnWidget d={d} />
      </ShellProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Refresh 1" }));
    await waitFor(() => expect(posts).toEqual(["/runs/11/messages", "/runs"]));
  });
});
