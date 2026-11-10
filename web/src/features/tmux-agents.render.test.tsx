import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Project, ProjectDetail, ProjectSession, ProjectSessions, Run, Runner } from "@/api/types";
import { NewRunDialog } from "@/features/agents/NewRunDialog";
import { RunRow, RunStatusBadge } from "@/features/agents/RunBits";
import { SessionCard } from "@/features/terminal/bits";
import { makeSession, makeTask, makeWindow } from "@/test/fixtures";
import { jsonResponse, renderWithProviders, routeFetch, testClient } from "@/test/render";

const AT = "2026-11-10T09:00:00Z";
const run = (over: Partial<Run> = {}): Run => ({
  id: 7, runner_id: 1, runner_name: "desk", project_id: 2, project_key: "SHOP", project_color: "#3987e5", repo_id: 11, repo_name: "shop_api",
  task_id: 5, task_ref: "SHOP-5", task_title: "Fix the cart", kind: "agent", prompt: "Fix the cart", command: "", permission_mode: "plan",
  model: "", model_note: "", engine: "claude", effort: "", interactive: true, awaiting: "", worktree: false, resume_run_id: null,
  status: "running", cancel_requested: false, session_id: "0b6e3f4a-1c2d-4e5f-8a9b-0c1d2e3f4a5b", result: "", error: "", exit_code: null,
  cost_usd: null, num_turns: 1, duration_ms: null, created_at: AT, started_at: AT, finished_at: null, inbox_after: 0, resumes: 0,
  finish_requested: false, one_turn: false, handoff: "", resume_hint: "", tmux_session: "F-Shop", origin: "forge", ...over,
});

afterEach(() => vi.unstubAllGlobals());

describe("agents in tmux", () => {
  it("shows where a run lives, and that a closed session is not waiting on anyone", () => {
    renderWithProviders(<RunRow run={run()} />);
    expect(screen.getByTitle("Lives in this tmux session on its machine")).toHaveTextContent("F-Shop");
    renderWithProviders(<RunStatusBadge run={run({ status: "waiting" })} />);
    expect(screen.getByText("Closed")).toBeInTheDocument();
    renderWithProviders(<RunStatusBadge run={run({ awaiting: "reply" })} />);
    expect(screen.getByText("Your turn")).toBeInTheDocument();
  });

  it("links a tmux session to the agent run living in it", () => {
    renderWithProviders(
      <SessionCard
        session={makeSession({ name: "F-Shop", run_id: 7, claude: true, window_list: [makeWindow({ index: 0, claude: true, command: "claude" })] })}
        onAttach={() => {}}
        onPeek={() => {}}
      />,
    );
    expect(screen.getByRole("link", { name: /run #7/ })).toHaveAttribute("href", "/agents/runs/7");
  });

  const runner = {
    id: 1, name: "desk", hostname: "desk", os: "linux", version: "0.1.44", online: true, last_seen_at: AT, running: 0, created_at: AT,
    role: "master", pair_expires_at: null,
    capabilities: { claude: true, permission_modes: ["plan"], commands: [], command_details: [], max_concurrent: 2, ci: false, terminal: true,
      code: false, interactive: true, tmux_agents: true },
  } as unknown as Runner;
  const project = { id: 2, key: "SHOP", name: "Shop", color: "#3987e5" } as Project;
  const detail = { ...project, repos: [{ id: 11, project_id: 2, name: "shop_api", path: "/srv/shop/shop_api", git: null }], servers: [], endpoints: [] } as unknown as ProjectDetail;
  const setup = () => {
    const qc = testClient();
    qc.setQueryData(["runners"], [runner]);
    qc.setQueryData(["projects", { includeArchived: false }], [project]);
    qc.setQueryData(["project", "SHOP"], detail);
    qc.setQueryData(["tasks", { project: "SHOP", open: true }], [makeTask({ id: 5, ref: "SHOP-5", title: "Fix the cart" })]);
    qc.setQueryData(["task-agent", 5], run({ status: "waiting" }));
    return qc;
  };

  const sessions = (over: Partial<ProjectSession> = {}): ProjectSessions => ({
    settings: { skip_permissions: false },
    host: { runner_id: 1, runner_name: "desk", online: true, skip_permissions: false, tmux_agents: true },
    sessions: [
      { project_key: "SHOP", project_name: "Shop", project_color: "#3987e5", project_status: "live", name: "F-Shop", dir: "/srv/shop",
        state: "missing", url: null, attached: 0, activity: null, extra: 0, run_id: null, run_status: "", awaiting: "", run_prompt: "", ...over },
    ],
  });

  it("sends a task's next request to its agent, or starts a fresh one when asked", async () => {
    const posted: Record<string, unknown>[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /runs": () => jsonResponse(200, { runs: [run({ status: "waiting" })] }),
        "GET /project-sessions": () => jsonResponse(200, sessions()),
        "POST /runs": (init) => {
          posted.push(JSON.parse(String(init.body)));
          return posted.length === 1 ? jsonResponse(200, run({ continued: true })) : jsonResponse(201, run({ id: 8, tmux_session: "F-Shop-2" }));
        },
      }),
    );
    const user = userEvent.setup();
    const first = renderWithProviders(<NewRunDialog prefill={{ project_key: "SHOP", task_id: 5 }} onClose={() => {}} />, { qc: setup() });
    expect(await screen.findByText(/SHOP-5 already has an agent/)).toBeInTheDocument();
    expect(screen.getByText(/picks that conversation up again/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Prompt"), "Also the tests");
    await user.click(screen.getByRole("button", { name: "Queue run" }));
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0].fresh).toBeUndefined();
    expect(await screen.findByText(/Sent to F-Shop \(run #7\)/)).toBeInTheDocument();
    first.unmount();

    renderWithProviders(<NewRunDialog prefill={{ project_key: "SHOP", task_id: 5 }} onClose={() => {}} />, { qc: setup() });
    await user.click(await screen.findByRole("switch", { name: "Start a fresh agent instead" }));
    expect(screen.getByText(/A new, separate agent starts instead/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Prompt"), "Start over");
    await user.click(screen.getByRole("button", { name: "Queue run" }));
    await waitFor(() => expect(posted).toHaveLength(2));
    expect(posted[1]).toMatchObject({ fresh: true, task_id: 5 });
  });

  it("one project, one agent: a new run goes to the project's open conversation, unless an extra agent is asked for", async () => {
    const posted: Record<string, unknown>[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /runs": () => jsonResponse(200, { runs: [] }),
        "GET /project-sessions": () => jsonResponse(200, sessions({ state: "running", run_id: 7, run_status: "running", run_prompt: "Fix the cart" })),
        "POST /runs": (init) => {
          posted.push(JSON.parse(String(init.body)));
          return jsonResponse(201, run({ id: 8, tmux_session: "F-Shop-2" }));
        },
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<NewRunDialog prefill={{ project_key: "SHOP" }} onClose={() => {}} />, { qc: setup() });
    expect(await screen.findByText(/SHOP's agent has a conversation open/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "run #7" })).toHaveAttribute("href", "/agents/runs/7");
    expect(screen.getByText(/one project, one agent/)).toBeInTheDocument();
    await user.click(screen.getByRole("switch", { name: "Start an extra agent instead" }));
    expect(screen.getByText(/An extra agent starts in a session of its own/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Prompt"), "Review the checkout");
    await user.click(screen.getByRole("button", { name: "Queue run" }));
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0]).toMatchObject({ fresh: true, prompt: "Review the checkout" });
  });
});
