import { renderHook, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Route, Routes } from "react-router-dom";
import type { MemberAgentView, Run } from "@/api/types";
import { MemberAgentPage } from "@/features/agents/MemberAgentPage";
import { ShellProvider } from "@/features/shell/context";
import { TaskDrawer } from "@/features/tasks/TaskDrawer";
import { makeTask } from "@/test/fixtures";
import { useNav } from "@/features/shell/nav";
import { agentLine, startsFresh } from "@/lib/agents";
import { commentPrompt } from "@/lib/tasks";
import { jsonResponse, makeUser, renderWithProviders, testClient } from "@/test/render";

const AT = "2026-11-10T09:00:00Z";
const run = (over: Partial<Run> = {}): Run => ({
  id: 7, runner_id: 1, runner_name: "desk", project_id: 2, project_key: "SHOP", project_color: "#3987e5", repo_id: 11, repo_name: "shop_api",
  task_id: null, task_ref: null, task_title: null, kind: "agent", prompt: "Fix the cart", command: "", permission_mode: "acceptEdits",
  model: "opus", model_note: "", engine: "claude", effort: "high", interactive: true, awaiting: "reply", worktree: false, resume_run_id: null,
  status: "running", cancel_requested: false, session_id: "0b6e3f4a-1c2d-4e5f-8a9b-0c1d2e3f4a5b", result: "", error: "", exit_code: null,
  cost_usd: null, num_turns: 1, duration_ms: null, created_at: AT, started_at: AT, finished_at: null, inbox_after: 0, resumes: 0,
  finish_requested: false, one_turn: false, handoff: "", resume_hint: "", tmux_session: "", origin: "forge", ...over,
});
const agent = { runner_id: 1, runner_name: "desk", online: true, model: "opus", effort: "high", permission_mode: "acceptEdits" };
const view = (over: Partial<MemberAgentView> = {}): MemberAgentView => ({
  agent, projects: [{ key: "SHOP", name: "Shop", color: "#3987e5" }], run: null, recent: [], ...over,
});
const member = () => testClient({ user: makeUser({ role: "member" }), elevated_until: null });

afterEach(() => vi.unstubAllGlobals());

describe("a member's agent", () => {
  it("says what it is: the model, the effort and the one machine", () => {
    expect(agentLine(agent)).toBe("Claude Code · opus · High effort · on desk");
    expect(agentLine({ ...agent, model: "", effort: "" })).toBe("Claude Code · default model · on desk");
  });

  it("takes the place of Agents in a member's menu — only when the owner gave it one", async () => {
    const wrap = (qc: ReturnType<typeof testClient>) =>
      function Wrapper({ children }: { children: ReactNode }) {
        return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
      };
    const withAgent = member();
    withAgent.setQueryData(["member-agent"], view());
    expect(renderHook(() => useNav(), { wrapper: wrap(withAgent) }).result.current.map((n) => n.to)).toEqual(["/tasks", "/projects", "/agent"]);
    const without = member();
    without.setQueryData(["member-agent"], view({ agent: null, projects: [] }));
    expect(renderHook(() => useNav(), { wrapper: wrap(without) }).result.current.map((n) => n.to)).toEqual(["/tasks", "/projects", "/agents"]);
    // The owner's menu has no such page (and never asks for one).
    const owner = renderHook(() => useNav(), { wrapper: wrap(testClient()) }).result.current.map((n) => n.to);
    expect(owner).toContain("/agents");
    expect(owner).not.toContain("/agent");
  });

  it("starts with a request, then shows the conversation and takes replies; a new conversation finishes the open one", async () => {
    let current = view();
    const asked: Record<string, unknown>[] = [];
    const messages: Record<string, unknown>[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit = {}) => {
        const path = url.split("?")[0].replace(/^\/api/, "");
        const method = init.method ?? "GET";
        if (path === "/agent" && method === "GET") return jsonResponse(200, current);
        if (path === "/agent" && method === "POST") {
          asked.push(JSON.parse(String(init.body)));
          const id = 6 + asked.length;
          current = view({ run: run({ id, prompt: String(asked[asked.length - 1].prompt) }), recent: asked.length > 1 ? [run({ id: 7, status: "succeeded" })] : [] });
          return jsonResponse(201, current.run);
        }
        if (/^\/runs\/\d+\/events$/.test(path))
          return jsonResponse(200, { run: current.run, prompts: [], events: [{ seq: 1, at: AT, kind: "claude", data: { type: "assistant", message: { role: "assistant", content: [{ type: "text", text: "The cart total is fixed." }] } } }] });
        if (/^\/runs\/\d+\/messages$/.test(path)) {
          messages.push(JSON.parse(String(init.body)));
          return jsonResponse(200, current.run);
        }
        return jsonResponse(404, { error: { code: "not_found", message: path } });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<MemberAgentPage />, { qc: member() });
    expect(await screen.findByText("Claude Code · opus · High effort · on desk")).toBeInTheDocument();
    // One project: nothing to pick. No machine, model or effort to choose either.
    expect(screen.queryByLabelText("Project")).toBeNull();
    expect(screen.queryByLabelText("Model")).toBeNull();
    await user.type(screen.getByLabelText("What should the agent do?"), "Fix the cart");
    await user.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect(asked).toEqual([{ prompt: "Fix the cart", project_key: "SHOP" }]));

    const conversation = await screen.findByRole("region", { name: "Conversation" });
    expect(within(conversation).getByText("Fix the cart")).toBeInTheDocument();
    expect(await within(conversation).findByText("The cart total is fixed.")).toBeInTheDocument();
    expect(within(conversation).getByText(/Your turn — reply/)).toBeInTheDocument();
    await user.type(within(conversation).getByRole("textbox"), "Also the tests{Control>}{Enter}{/Control}");
    await waitFor(() => expect(messages).toEqual([{ text: "Also the tests" }]));

    await user.click(screen.getByRole("button", { name: "New conversation" }));
    await user.type(screen.getByLabelText("What should it do next?"), "Review the checkout");
    await user.click(screen.getByRole("button", { name: "Start a new conversation" }));
    await waitFor(() => expect(asked[1]).toEqual({ prompt: "Review the checkout", project_key: "SHOP", fresh: true }));
    expect(await screen.findByRole("link", { name: /Fix the cart/ })).toHaveAttribute("href", "/agents/runs/7");
  });

  it("an account without an agent is sent to the runs page", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse(200, view({ agent: null, projects: [] }))));
    renderWithProviders(
      <Routes>
        <Route path="/agent" element={<MemberAgentPage />} />
        <Route path="/agents" element={<p>runs page</p>} />
      </Routes>,
      { qc: testClient(), route: "/agent" },
    );
    expect(await screen.findByText("runs page")).toBeInTheDocument();
  });

  it("a request about a task joins the conversation on that task, and starts a new one otherwise", () => {
    expect(startsFresh({ run: null }, 5)).toBe(false);
    expect(startsFresh({ run: { task_id: 5 } }, 5)).toBe(false);
    expect(startsFresh({ run: { task_id: 9 } }, 5)).toBe(true);
    expect(startsFresh({ run: { task_id: 9 } }, null)).toBe(false);
    const task = { ref: "SHOP-5", title: "Fix the cart", description: "Totals are wrong" };
    expect(commentPrompt(task, " Also the tax ", true)).toBe("New comment on SHOP-5:\n\nAlso the tax");
    expect(commentPrompt(task, "Also the tax", false)).toBe("SHOP-5: Fix the cart\n\nTotals are wrong\n\nComment:\n\nAlso the tax");
  });

  it("Run agent on a task, and Comment and run agent, go to the member's own agent", async () => {
    const task = { ...makeTask({ id: 5, ref: "SHOP-5", title: "Fix the cart", description: "Totals are wrong" }), comments: [], runs: [] };
    let current = view();
    const asked: Record<string, unknown>[] = [];
    const comments: Record<string, unknown>[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit = {}) => {
        const path = url.split("?")[0].replace(/^\/api/, "");
        const method = init.method ?? "GET";
        if (path === "/tasks/5" && method === "GET") return jsonResponse(200, task);
        if (path === "/projects/SHOP") return jsonResponse(200, { id: 1, key: "SHOP", name: "Shop", color: "#3987e5", repos: [], servers: [], endpoints: [] });
        if (path === "/agent" && method === "GET") return jsonResponse(200, current);
        if (path === "/tasks/5/comments" && method === "POST") {
          comments.push(JSON.parse(String(init.body)));
          return jsonResponse(201, { id: comments.length, task_id: 5, body: comments[comments.length - 1].body, created_at: AT });
        }
        if (path === "/agent" && method === "POST") {
          asked.push(JSON.parse(String(init.body)));
          current = view({ run: run({ id: 20 + asked.length, task_id: 5, task_ref: "SHOP-5" }) });
          return jsonResponse(201, current.run);
        }
        return jsonResponse(404, { error: { code: "not_found", message: path } });
      }),
    );
    const user = userEvent.setup();
    const qc = member();
    qc.setQueryData(["member-agent"], current);
    const drawer = (
      <ShellProvider>
        <TaskDrawer />
      </ShellProvider>
    );
    const first = renderWithProviders(drawer, { qc, route: "/tasks?task=5" });
    // Run agent: no machine, model or mode to pick — the task goes to the agent.
    await user.click(await screen.findByRole("button", { name: "Run agent" }));
    const dialog = await screen.findByRole("dialog", { name: "Run your agent on SHOP-5" });
    expect(within(dialog).getByText("Claude Code · opus · High effort · on desk")).toBeInTheDocument();
    expect(within(dialog).queryByLabelText("Runner")).toBeNull();
    expect(within(dialog).queryByLabelText("Permission mode")).toBeNull();
    expect(within(dialog).getByLabelText("What should it do?")).toHaveValue("SHOP-5: Fix the cart\n\nTotals are wrong");
    await user.click(within(dialog).getByRole("button", { name: "Start" }));
    await waitFor(() => expect(asked).toEqual([{ prompt: "SHOP-5: Fix the cart\n\nTotals are wrong", task_id: 5 }]));

    // (It then shows the Agent page.) Back on the task: a comment handed to the agent
    // is posted, then sent to the conversation open on this task.
    await waitFor(() => expect(qc.getQueryData<MemberAgentView>(["member-agent"])?.run?.id).toBe(21));
    first.unmount();
    renderWithProviders(drawer, { qc, route: "/tasks?task=5" });
    await user.type(await screen.findByLabelText("New comment"), "Also round the tax");
    await user.click(screen.getByRole("button", { name: "Comment and run agent" }));
    await waitFor(() => expect(comments).toEqual([{ body: "Also round the tax" }]));
    await waitFor(() => expect(asked[1]).toEqual({ prompt: "New comment on SHOP-5:\n\nAlso round the tax", task_id: 5 }));
  });
});
