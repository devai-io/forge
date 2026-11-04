import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import type { Dashboard, Run } from "@/api/types";
import { WorkingOnWidget } from "@/features/dashboard/TopWidgets";
import { ShellProvider } from "@/features/shell/context";
import { DEFAULT_BUMP, continueInput, messageInput, modelLabel, planRefresh, refreshText, startInput, summarize, type StartTarget } from "@/lib/working";
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
    model: "claude-sonnet-5-5",
    engine: "claude",
    effort: "high",
    interactive: true,
    awaiting: "",
    worktree: false,
    status: "running",
    cancel_requested: false,
    finish_requested: false,
  one_turn: false,
    session_id: "sess-1",
    handoff: "",
    ...o,
  }) as Run;

const tA = makeTask({ id: 1, ref: "SHOP-1", title: "Payouts", status: "in_progress" });
const tB = makeTask({ id: 2, ref: "SHOP-2", title: "Login rate limit", status: "in_progress" });
const tC = makeTask({ id: 3, ref: "SHOP-3", title: "Receipts", status: "in_progress" });
const tD = makeTask({ id: 4, ref: "SHOP-4", title: "Docs", status: "in_progress" });
const tE = makeTask({ id: 5, ref: "SHOP-5", title: "Question pending", status: "in_progress" });
const tF = makeTask({ id: 6, ref: "SHOP-6", title: "Parked on Opus already", status: "in_progress" });
const target: StartTarget = { runner_id: 3, repo_id: 7, permission_mode: "bypassPermissions", interactive: true };

describe("planRefresh", () => {
  it("bumps every task: asks, resumes, continues, starts", () => {
    const open = [
      run({ id: 10, task_id: 1, status: "running" }),
      run({ id: 11, task_id: 2, status: "waiting" }), // parked on Sonnet/high: continue on Opus/medium
      run({ id: 14, task_id: 5, status: "running", awaiting: "answer" }),
      run({ id: 15, task_id: 6, status: "waiting", model: "claude-opus-5-5", effort: "medium" }), // already on the settings: resume in place
      run({ id: 16, task_id: null, status: "waiting" }), // parked, on no task
      run({ id: 17, task_id: null, status: "running" }), // working already: not listed
    ];
    const latest = new Map<number, Run | undefined>([
      [3, run({ id: 12, task_id: 3, status: "succeeded" })],
      [4, undefined],
    ]);
    const plan = planRefresh([tA, tB, tC, tD, tE, tF], open, latest, DEFAULT_BUMP, new Map([[4, target]]));
    expect(plan.map((s) => [s.key, s.action, s.run?.id ?? null])).toEqual([
      ["t1", "ask", 10],
      ["t2", "continue", 11],
      ["t3", "continue", 12],
      ["t4", "start", null],
      ["t5", "skip", 14],
      ["t6", "resume", 15],
      ["r16", "continue", 16],
    ]);
    expect(plan[1]!.parked).toBe(true);
    expect(plan[3]!.why).toContain("Opus 5.5 · medium");
    expect(plan[4]!.why).toBe("waiting for your answer");
    expect(summarize(plan)).toBe("1 to ask for status · 1 to resume · 3 to continue · 1 to start · 1 left alone");
  });

  it("leaves alone what is queued, ending, handed off, or has nowhere to run", () => {
    const plan = planRefresh(
      [tA, tB, tC, tD],
      [run({ id: 10, task_id: 1, status: "queued" }), run({ id: 11, task_id: 2, finish_requested: true })],
      new Map([
        [3, run({ id: 12, task_id: 3, status: "succeeded", handoff: "desk · tmux run-12" })],
        [4, undefined],
      ]),
      DEFAULT_BUMP,
      new Map(), // no machine or repo for SHOP-4
    );
    expect(plan.map((s) => s.action)).toEqual(["skip", "skip", "skip", "skip"]);
    expect(plan[3]!.why).toContain("no machine or repo");
  });

  it("a Codex session keeps its engine: resumed, or continued on its own model", () => {
    const codex = run({ id: 20, task_id: 1, status: "waiting", engine: "codex", model: "gpt-x", effort: "high" });
    const [step] = planRefresh([tA], [codex], new Map(), DEFAULT_BUMP);
    expect(step!.action).toBe("resume");
    const body = continueInput(codex, tA, DEFAULT_BUMP);
    expect(body).toMatchObject({ model: "gpt-x", effort: "high", resume_run_id: 20 });
  });

  it("builds follow-up and fresh runs on the chosen model", () => {
    expect(continueInput(run({ id: 12 }), tC, DEFAULT_BUMP)).toMatchObject({
      runner_id: 3,
      repo_id: 7,
      kind: "agent",
      model: "claude-opus-5-5",
      effort: "medium",
      task_id: 3,
      resume_run_id: 12,
      one_turn: true,
      prompt: refreshText(tC),
    });
    expect(startInput(tD, target, DEFAULT_BUMP)).toMatchObject({
      runner_id: 3,
      repo_id: 7,
      engine: "claude",
      model: "claude-opus-5-5",
      effort: "medium",
      permission_mode: "bypassPermissions",
      interactive: true,
      one_turn: true,
      task_id: 4,
    });
    // A parked session resumes for one turn, then finishes; a live one keeps its lifecycle.
    expect(messageInput(run({ status: "waiting" }), tC)).toEqual({ text: refreshText(tC), one_turn: true });
    expect(messageInput(run({ status: "running" }), null)).toEqual({ text: refreshText(null), one_turn: false });
    expect(modelLabel("claude-opus-5-5")).toBe("Opus 5.5");
    expect(refreshText(null)).toContain("carry on");
  });
});

describe("Working on → Refresh", () => {
  afterEach(() => vi.unstubAllGlobals());

  const fetchFor = (posts: [string, unknown][], opts: { failResume?: boolean } = {}) =>
    vi.fn(async (url: string, init: RequestInit = {}) => {
      const u = new URL(url, "http://forge.test");
      const path = u.pathname.replace(/^\/api/, "");
      const method = init.method ?? "GET";
      if (method === "GET" && path === "/runs") {
        const task = Number(u.searchParams.get("task"));
        return jsonResponse(200, { runs: task === 3 ? [run({ id: 12, task_id: 3, status: "succeeded" })] : [] });
      }
      if (method === "GET" && path === "/runners")
        return jsonResponse(200, {
          runners: [
            { id: 3, name: "desk", online: true, role: "master", capabilities: { claude: true, interactive: true, permission_modes: ["plan", "acceptEdits", "bypassPermissions"] } },
          ],
        });
      if (method === "GET" && path === "/engine")
        return jsonResponse(200, { settings: { default: "claude" }, models: { claude: ["claude-fable-5-1", "claude-opus-5-5"] }, efforts: ["low", "medium", "high"] });
      if (method === "GET" && path === "/projects/SHOP") return jsonResponse(200, { key: "SHOP", repos: [{ id: 7, name: "shop_api", path: "/home/demo/shop_api" }] });
      if (method === "POST") {
        posts.push([path, JSON.parse(String(init.body ?? "{}"))]);
        if (opts.failResume && path.endsWith("/messages")) return jsonResponse(422, { error: { code: "validation", message: "cannot resume in place" } });
        return jsonResponse(path === "/runs" ? 201 : 200, run({ id: 99 }));
      }
      return jsonResponse(404, { error: { code: "not_found", message: path } });
    });

  it("shows the plan with Opus 5.5 · medium, then asks, continues and starts", async () => {
    const posts: [string, unknown][] = [];
    vi.stubGlobal("fetch", fetchFor(posts));
    const d = {
      in_progress: [tA, tB, tC, tD],
      active_runs: [run({ id: 10, task_id: 1, status: "running" }), run({ id: 11, task_id: 2, status: "waiting" })],
      endpoints_down: [],
    } as unknown as Dashboard;
    renderWithProviders(
      <ShellProvider>
        <WorkingOnWidget d={d} />
      </ShellProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));
    const plan = await screen.findByRole("list", { name: "Refresh plan" });
    expect((screen.getByLabelText("Model") as HTMLSelectElement).value).toBe("claude-opus-5-5");
    expect((screen.getByLabelText("Effort") as HTMLSelectElement).value).toBe("medium");
    expect(within(plan).getByText(/no session yet: start one on Opus 5.5 · medium/)).toBeInTheDocument();
    expect(within(plan).getByText(/parked: continue it on Opus 5.5 · medium/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Refresh 4" }));
    await waitFor(() => expect(posts).toHaveLength(5));
    expect(posts.map(([p]) => p)).toEqual(["/runs/10/messages", "/runs", "/runs/11/finish", "/runs", "/runs"]);
    expect(posts[1]![1]).toMatchObject({ resume_run_id: 11, model: "claude-opus-5-5", effort: "medium", task_id: 2 });
    expect(posts[3]![1]).toMatchObject({ resume_run_id: 12, model: "claude-opus-5-5", effort: "medium", task_id: 3 });
    expect(posts[4]![1]).toMatchObject({ engine: "claude", model: "claude-opus-5-5", effort: "medium", task_id: 4, repo_id: 7, runner_id: 3, permission_mode: "bypassPermissions" });
  });

  it("another model can be picked for the bump", async () => {
    const posts: [string, unknown][] = [];
    vi.stubGlobal("fetch", fetchFor(posts));
    const d = { in_progress: [tD], active_runs: [], endpoints_down: [] } as unknown as Dashboard;
    renderWithProviders(
      <ShellProvider>
        <WorkingOnWidget d={d} />
      </ShellProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));
    await screen.findByRole("list", { name: "Refresh plan" });
    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "claude-fable-5-1" } });
    fireEvent.change(screen.getByLabelText("Effort"), { target: { value: "high" } });
    fireEvent.click(screen.getByRole("button", { name: "Refresh 1" }));
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(posts[0]![1]).toMatchObject({ model: "claude-fable-5-1", effort: "high" });
  });

  it("continues a parked session whose machine cannot resume it in place", async () => {
    const posts: [string, unknown][] = [];
    vi.stubGlobal("fetch", fetchFor(posts, { failResume: true }));
    const d = {
      in_progress: [tB],
      active_runs: [run({ id: 11, task_id: 2, status: "waiting", model: "claude-opus-5-5", effort: "medium" })],
      endpoints_down: [],
    } as unknown as Dashboard;
    renderWithProviders(
      <ShellProvider>
        <WorkingOnWidget d={d} />
      </ShellProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Refresh 1" }));
    await waitFor(() => expect(posts.map(([p]) => p)).toEqual(["/runs/11/messages", "/runs", "/runs/11/finish"]));
  });
});
