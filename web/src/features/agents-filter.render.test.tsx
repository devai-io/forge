import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes, useLocation } from "react-router-dom";
import type { ProjectSession, ProjectSessions, Run } from "@/api/types";
import { AgentsPage } from "@/features/agents/AgentsPage";
import { ShellProvider } from "@/features/shell/context";
import { jsonResponse, renderWithProviders } from "@/test/render";

const session = (over: Partial<ProjectSession>): ProjectSession => ({
  project_key: "SHOP", project_name: "Shop", project_color: "#3987e5", project_status: "live", name: "F-Shop", dir: "/home/demo/dev/shop",
  state: "missing", url: null, attached: 0, activity: null, extra: 0, run_id: null, run_status: "", awaiting: "", run_prompt: "", ...over,
});
const view: ProjectSessions = {
  settings: { skip_permissions: true },
  host: { runner_id: 3, runner_name: "desk", online: true, skip_permissions: true, tmux_agents: true },
  sessions: [session({}), session({ project_key: "BLOG", project_name: "Blog", name: "F-Blog", project_status: "building" })],
};
const run = (o: Partial<Run>) =>
  ({ id: 1, kind: "agent", prompt: "x", command: "", status: "running", task_id: null, project_key: "SHOP", project_color: "#3987e5",
    runner_name: "desk", cancel_requested: false, awaiting: "", created_at: "2026-11-15T10:00:00.000Z", ...o }) as Run;

function Where() {
  const loc = useLocation();
  return <output aria-label="location">{loc.search}</output>;
}

afterEach(() => vi.unstubAllGlobals());

describe("agents page", () => {
  it("a project button filters the page to that project, and New run starts with it picked", async () => {
    const runQueries: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const [p, q = ""] = url.replace(/^\/api/, "").split("?");
        if (p === "/project-sessions") return jsonResponse(200, view);
        if (p === "/runs") {
          runQueries.push(q);
          const all = [run({ id: 1, prompt: "Shop work" }), run({ id: 2, prompt: "Blog work", project_key: "BLOG" })];
          const project = new URLSearchParams(q).get("project");
          const status = new URLSearchParams(q).get("status");
          return jsonResponse(200, { runs: all.filter((r) => (!project || r.project_key === project) && (!status || r.status === status)) });
        }
        if (p === "/engine") return jsonResponse(200, { settings: { default: "claude" }, provider_key: false, models: {}, efforts: [] });
        if (p === "/runners") return jsonResponse(200, { runners: [] });
        if (p === "/projects") return jsonResponse(200, { projects: [{ key: "SHOP", name: "Shop" }, { key: "BLOG", name: "Blog" }] });
        return jsonResponse(200, {});
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(
      <ShellProvider>
        <Routes>
          <Route path="/agents" element={<><AgentsPage /><Where /></>} />
        </Routes>
      </ShellProvider>,
      { route: "/agents" },
    );
    await waitFor(() => expect(screen.getByText("Blog work")).toBeInTheDocument());
    await user.click(await screen.findByRole("button", { name: "Shop: not open" }));
    expect(screen.getByLabelText("location")).toHaveTextContent("?project=SHOP");
    await waitFor(() => expect(screen.queryByText("Blog work")).toBeNull());
    expect(screen.getByText("Shop work")).toBeInTheDocument();
    expect(runQueries.some((q) => q.includes("project=SHOP"))).toBe(true);
    // New run starts with the project picked.
    await user.click(screen.getByRole("button", { name: "New run · SHOP" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() => expect((within(dialog).getByLabelText("Project") as HTMLSelectElement).value).toBe("SHOP"));
    await user.keyboard("{Escape}");
    // Clicked again: every project.
    await user.click(screen.getByRole("button", { name: "Shop: not open" }));
    expect(screen.getByLabelText("location")).toHaveTextContent("");
    await waitFor(() => expect(screen.getByText("Blog work")).toBeInTheDocument());
  });
});
