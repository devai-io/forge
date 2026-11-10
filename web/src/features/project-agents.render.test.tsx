import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useLocation } from "react-router-dom";
import type { EngineStatus, ProjectSession, ProjectSessions } from "@/api/types";
import { ProjectAgents } from "@/features/agents/ProjectAgents";
import { groupSessions, groupStatus, isActiveGroup, sessionStatus } from "@/lib/projectAgents";
import { jsonResponse, makeUser, renderWithProviders, routeFetch, testClient } from "@/test/render";

const session = (over: Partial<ProjectSession> = {}): ProjectSession => ({
  project_key: "SHOP", project_name: "Shop", project_color: "#3987e5", project_status: "live", name: "F-Shop", dir: "/home/demo/dev/shop",
  state: "missing", url: null, attached: 0, activity: null, extra: 0, run_id: null, run_status: "", awaiting: "", run_prompt: "", ...over,
});
const view = (sessions: ProjectSession[]): ProjectSessions => ({
  settings: { skip_permissions: true },
  host: { runner_id: 3, runner_name: "desk", online: true, skip_permissions: true, tmux_agents: true },
  sessions,
});
const engine = { settings: {}, provider_key: false, models: { claude: ["opus", "sonnet", "haiku"] }, efforts: ["low", "medium", "high"] } as unknown as EngineStatus;

function Where() {
  const loc = useLocation();
  return <output aria-label="location">{loc.pathname}</output>;
}

beforeEach(() => window.localStorage.clear());
afterEach(() => vi.unstubAllGlobals());

describe("project agents", () => {
  it("says what each session is doing, and a project shows its most pressing one", () => {
    expect(sessionStatus(session()).label).toBe("Start");
    expect(sessionStatus(session({ state: "running" })).label).toBe("Open");
    expect(sessionStatus(session({ state: "running", run_id: 4, run_status: "running" })).label).toBe("Working");
    expect(sessionStatus(session({ state: "running", run_id: 4, run_status: "running", awaiting: "reply" })).label).toBe("Your turn");
    expect(sessionStatus(session({ state: "stopped", run_id: 4, run_status: "waiting" })).tone).toBe("closed");
    const groups = groupSessions([
      session({ state: "running", run_id: 4, run_status: "running" }),
      session({ name: "F-Shop-2", extra: 2, state: "running", run_id: 5, run_status: "running", awaiting: "answer" }),
      session({ project_key: "LAB", project_name: "Lab", name: "F-Lab", project_status: "radar" }),
    ]);
    expect(groups.map((g) => [g.key, g.extras.length])).toEqual([["SHOP", 1], ["LAB", 0]]);
    expect(groupStatus(groups[0]).label).toBe("Needs you");
    expect(groups.map(isActiveGroup)).toEqual([true, false]);
  });

  it("a button per active project; the rest one click away; none for a member", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /project-sessions": () =>
          jsonResponse(200, view([
            session({ state: "running", run_id: 4, run_status: "running", awaiting: "reply", run_prompt: "Fix the cart" }),
            session({ project_key: "BLOG", project_name: "Blog", name: "F-Blog", project_status: "building" }),
            session({ project_key: "LAB", project_name: "Lab", name: "F-Lab", project_status: "paused" }),
          ])),
      }),
    );
    const first = renderWithProviders(<ProjectAgents />);
    const list = await screen.findByRole("list", { name: "Project agents" });
    expect(within(list).getByRole("button", { name: "Shop: Your turn" })).toBeInTheDocument();
    expect(within(list).getByRole("button", { name: "Blog: not open — start its agent" })).toBeInTheDocument();
    expect(within(list).queryByRole("button", { name: /Lab/ })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "All projects (3)" }));
    expect(within(list).getByRole("button", { name: /Lab/ })).toBeInTheDocument();
    first.unmount();

    renderWithProviders(<ProjectAgents />, { qc: testClient({ user: makeUser({ role: "member" }), elevated_until: null }) });
    expect(screen.queryByRole("list", { name: "Project agents" })).toBeNull();
  });

  it("starts a project's agent with a model, an effort and a first request", async () => {
    const posted: { path: string; body: Record<string, unknown> }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit = {}) => {
        const path = url.split("?")[0].replace(/^\/api/, "");
        if (path === "/project-sessions" && (init.method ?? "GET") === "GET") return jsonResponse(200, view([session()]));
        if (path === "/engine") return jsonResponse(200, engine);
        if (init.method === "POST" && path.startsWith("/project-sessions/")) {
          posted.push({ path, body: JSON.parse(String(init.body)) });
          return jsonResponse(200, { session: "F-Shop", runner_id: 3, run: { id: 12 }, started: true });
        }
        return jsonResponse(404, { error: { code: "not_found", message: path } });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <ProjectAgents />
        <Where />
      </>,
    );
    await user.click(await screen.findByRole("button", { name: "Shop: not open — start its agent" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("button", { name: "Start" })).toBeInTheDocument();
    await waitFor(() => expect(within(dialog).getByRole("option", { name: "opus" })).toBeInTheDocument());
    await user.selectOptions(within(dialog).getByLabelText("Model"), "opus");
    await user.selectOptions(within(dialog).getByLabelText("Effort"), "high");
    await user.type(within(dialog).getByLabelText(/First request/), "Fix the cart");
    await user.click(within(dialog).getByRole("button", { name: "Start with this request" }));
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0]).toEqual({ path: "/project-sessions/SHOP", body: { prompt: "Fix the cart", model: "opus", effort: "high", skip_permissions: true } });
    await waitFor(() => expect(screen.getByLabelText("location")).toHaveTextContent("/agents/runs/12"));
    // What it was started with is where the next one starts.
    expect(window.localStorage.getItem("forge.agent.model")).toBe("opus");
  });

  it("an open agent: attach, send it a request, or start an extra session", async () => {
    const posted: Record<string, unknown>[] = [];
    let closed = "";
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit = {}) => {
        const path = url.split("?")[0].replace(/^\/api/, "");
        if (path === "/project-sessions" && (init.method ?? "GET") === "GET")
          return jsonResponse(200, view([
            session({ state: "running", url: "https://claude.ai/code/session_abc", run_id: 4, run_status: "running", run_prompt: "Fix the cart" }),
            session({ name: "F-Shop-2", extra: 2, state: "running" }),
          ]));
        if (path === "/engine") return jsonResponse(200, engine);
        if (init.method === "POST" && path === "/project-sessions/SHOP") {
          posted.push(JSON.parse(String(init.body)));
          return jsonResponse(200, { session: posted.length === 1 ? "F-Shop" : "F-Shop-3", runner_id: 3, run: posted.length === 1 ? { id: 4 } : null, started: posted.length > 1 });
        }
        if (init.method === "DELETE") {
          closed = path;
          return jsonResponse(204);
        }
        return jsonResponse(404, { error: { code: "not_found", message: path } });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <ProjectAgents />
        <Where />
      </>,
    );
    await user.click(await screen.findByRole("button", { name: "Shop: Working, 2 sessions" }));
    let dialog = await screen.findByRole("dialog");
    const rows = within(dialog).getByRole("list", { name: "Sessions" });
    expect(within(rows).getByRole("link", { name: "#4 · Fix the cart" })).toHaveAttribute("href", "/agents/runs/4");
    expect(within(rows).getByRole("link", { name: "Open F-Shop in Claude" })).toHaveAttribute("href", "https://claude.ai/code/session_abc");
    expect(within(rows).getByText("No conversation yet")).toBeInTheDocument();
    // It keeps what it started with; no request: the button attaches.
    expect(within(dialog).getByLabelText("Model")).toBeDisabled();
    expect(within(dialog).getByRole("button", { name: "Attach" })).toBeInTheDocument();
    // Closing a session asks first.
    await user.click(within(rows).getByRole("button", { name: "Close F-Shop-2" }));
    await user.click(within(rows).getByRole("button", { name: "Close it" }));
    await waitFor(() => expect(closed).toBe("/project-sessions/SHOP/F-Shop-2"));
    // A request goes to the open one.
    await user.type(within(dialog).getByLabelText(/Request for F-Shop/), "Also the tests");
    await user.click(within(dialog).getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0]).toEqual({ prompt: "Also the tests" });
    await waitFor(() => expect(screen.getByLabelText("location")).toHaveTextContent("/agents/runs/4"));

    // An extra session starts with its own model, and opens in the terminal.
    await user.click(await screen.findByRole("button", { name: "Shop: Working, 2 sessions" }));
    dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("switch", { name: "Start an extra session for Shop" }));
    expect(within(dialog).getByLabelText("Model")).toBeEnabled();
    await user.click(within(dialog).getByRole("button", { name: "Start an extra session" }));
    await waitFor(() => expect(posted).toHaveLength(2));
    expect(posted[1]).toEqual({ extra: true, skip_permissions: true });
    await waitFor(() => expect(screen.getByLabelText("location")).toHaveTextContent("/terminal/3/F-Shop-3"));
  });
});
