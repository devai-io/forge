import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import type { Dashboard, Endpoint, Monitoring, ProjectSessions, Run } from "@/api/types";
import { ProjectSessionsWidget, ServicesWidget, WorkingOnWidget } from "@/features/dashboard/TopWidgets";
import { ShellProvider } from "@/features/shell/context";
import { serviceIssues } from "@/lib/monitoring";
import { makeTask } from "@/test/fixtures";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

const endpoint = (o: Partial<Endpoint> = {}): Endpoint => ({
  id: 1,
  project_id: 1,
  project_key: "SHOP",
  project_name: "Shop",
  project_color: "#3987e5",
  name: "shop web",
  url: "https://shop.example.com",
  kind: "web",
  expect_status: 200,
  enabled: true,
  last_status: "down",
  fail_streak: 3,
  retry_limit: 2,
  next_check_at: null,
  last_code: 502,
  last_latency_ms: null,
  last_error: "",
  last_checked_at: null,
  last_change_at: null,
  uptime_24h: null,
  ...o,
});

const monitoring = (o: Partial<Monitoring> = {}): Monitoring => ({
  fetched_at: "2026-10-29T10:00:00Z",
  hosts: [],
  probes: [],
  nomad: [],
  grafana: { url: "", connected: false, error: "", alerts: [], dashboards: [] },
  ...o,
});

const dash = (o: Partial<Dashboard> = {}): Dashboard =>
  ({
    today: "2026-10-29",
    endpoints_down: [],
    endpoints_warning: [],
    in_progress: [],
    active_runs: [],
    ...o,
  }) as Dashboard;

describe("serviceIssues", () => {
  it("lists failing before warning, with the check-up's rules", () => {
    const issues = serviceIssues(
      { down: [], warning: [endpoint({ id: 2, name: "shop api", last_status: "warning" })] },
      monitoring({
        hosts: [
          { name: "web1", server_id: null, up: false, last_seen: null, uptime_seconds: null, cpu_pct: null, mem_pct: null, disk_pct: null, load1: null },
          { name: "web2", server_id: null, up: true, last_seen: null, uptime_seconds: null, cpu_pct: 10, mem_pct: 10, disk_pct: 85, load1: null },
        ],
        nomad: [
          {
            host: "web1",
            reachable: true,
            error: "",
            jobs: [
              { id: "shop", type: "service", status: "dead", running: 0, failed: 1, queued: 0 },
              { id: "backup", type: "batch", status: "dead", running: 0, failed: 0, queued: 0 },
            ],
          },
        ],
      }),
    );
    expect(issues.map((i) => [i.level, i.title])).toEqual([
      ["fail", "web1 is not reporting"],
      ["fail", "shop is dead"],
      ["warn", "shop api is failing"],
      ["warn", "web2 disk 85% full"],
    ]);
  });
});

describe("dashboard widgets", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("services: all clear, or what is failing", async () => {
    vi.stubGlobal("fetch", routeFetch({ "GET /monitoring": () => jsonResponse(200, monitoring()) }));
    const { unmount } = renderWithProviders(<ServicesWidget d={dash()} />);
    expect(await screen.findByText("All services OK")).toBeInTheDocument();
    unmount();

    renderWithProviders(<ServicesWidget d={dash({ endpoints_down: [endpoint()] })} />);
    expect(screen.getByText("1 failing")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /shop web is down/ })).toHaveAttribute("href", "/p/SHOP");
  });

  it("working on: in-progress tasks with their agent run, and runs on no task", () => {
    const run = (o: Partial<Run>) => ({ id: 1, kind: "agent", prompt: "Fix the build", command: "", status: "running", task_id: null, project_key: "SHOP", project_color: "#3987e5", runner_name: "desk", cancel_requested: false, awaiting: null, ...o }) as Run;
    renderWithProviders(
      <ShellProvider>
        <WorkingOnWidget
          d={dash({
            in_progress: [makeTask({ id: 5, ref: "SHOP-5", title: "Payouts", status: "in_progress" })],
            active_runs: [run({ id: 9, task_id: 5 }), run({ id: 10, prompt: "Tidy the logs" })],
          })}
        />
      </ShellProvider>,
    );
    const list = screen.getByRole("list", { name: "In progress" });
    const task = within(list).getByRole("button", { name: /Payouts/ });
    expect(within(task).getByText("Running")).toBeInTheDocument();
    expect(within(list).getByRole("link", { name: /Tidy the logs/ })).toHaveAttribute("href", "/agents/runs/10");
    expect(screen.getByText("· 2")).toBeInTheDocument();
  });

  it("project sessions: turn on, then open in Claude or attach", async () => {
    let view: ProjectSessions = {
      settings: { enabled: false, skip_permissions: false },
      host: { runner_id: 3, runner_name: "desk", online: true, skip_permissions: true },
      sessions: [],
    };
    const fetch = routeFetch({
      "GET /project-sessions": () => jsonResponse(200, view),
      "PATCH /project-sessions": () => {
        view = {
          ...view,
          settings: { enabled: true, skip_permissions: true },
          sessions: [
            { project_key: "SHOP", project_name: "Shop", project_color: "#3987e5", name: "F-Shop", dir: "/home/demo/dev/shop", state: "running", url: "https://claude.ai/code/session_abc", attached: 0, activity: null },
            { project_key: "GAME", project_name: "Game", project_color: "#e5a039", name: "F-Game", dir: "/home/demo/dev/game", state: "starting", url: null, attached: 0, activity: null },
          ],
        };
        return jsonResponse(200, view);
      },
    });
    vi.stubGlobal("fetch", fetch);
    renderWithProviders(<ProjectSessionsWidget />);
    fireEvent.click(await screen.findByRole("button", { name: /Turn on/ }));
    await waitFor(() => expect(screen.getByText("F-Shop")).toBeInTheDocument());
    expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/project-sessions"), expect.objectContaining({ method: "PATCH" }));
    expect(screen.getByRole("link", { name: "Open F-Shop in Claude" })).toHaveAttribute("href", "https://claude.ai/code/session_abc");
    expect(screen.queryByRole("link", { name: "Open F-Game in Claude" })).toBeNull();
    expect(screen.getByText("Starting")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Attach to F-Game" })).toBeEnabled();
  });
});
