import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import type { Dashboard, Endpoint, Monitoring, ProjectSessions, Run, Server } from "@/api/types";
import { ProjectSessionsWidget, ServicesWidget, WorkingOnWidget } from "@/features/dashboard/TopWidgets";
import { ShellProvider } from "@/features/shell/context";
import { investigationProject, investigationPrompt, serviceIssues } from "@/lib/monitoring";
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

const server = (o: Partial<Server>): Server =>
  ({ id: 1, name: "web1", role: "", provider: "", arch: "", public_address: "", tailscale_ip: "", environment: "production", critical: false, tags: [], notes: "", ssh_target: "", projects: [], created_at: "", updated_at: "", ...o }) as Server;

describe("investigating service issues", () => {
  const infra = { key: "OPS", name: "Ops", color: "#888888", role: "" };
  const shop = { key: "SHOP", name: "Shop", color: "#3987e5", role: "" };
  const servers = [
    server({ id: 1, name: "web1", tailscale_ip: "198.51.100.7", projects: [shop, infra] }),
    server({ id: 2, name: "db1", projects: [infra] }),
  ];

  it("runs in the project the issues belong to", () => {
    const [down] = serviceIssues({ down: [endpoint()], warning: [] }, undefined);
    expect(investigationProject([down!], servers)).toBe("SHOP");
    // Host issues vote for the server's projects; a tie goes to the one on more servers.
    const hostIssues = serviceIssues(
      { down: [], warning: [] },
      monitoring({
        hosts: [{ name: "web1", server_id: 1, up: false, last_seen: null, uptime_seconds: null, cpu_pct: null, mem_pct: null, disk_pct: null, load1: null }],
        nomad: [{ host: "db1", reachable: false, error: "timeout", jobs: [] }],
      }),
    );
    expect(investigationProject(hostIssues, servers)).toBe("OPS");
    // An alert names the host by address.
    const alert = serviceIssues(
      { down: [], warning: [] },
      monitoring({ grafana: { url: "", connected: true, error: "", dashboards: [], alerts: [{ name: "HighLoad", state: "firing", severity: "warning", summary: "load above 8", labels: { instance: "198.51.100.7:9100" }, since: null }] } }),
    );
    expect(alert[0]?.host).toBe("198.51.100.7:9100");
    expect(investigationProject(alert, [servers[0]!])).toBe("SHOP");
    expect(investigationProject([], servers)).toBeUndefined();
  });

  it("asks for causes and a plan, not production changes", () => {
    const issues = serviceIssues({ down: [endpoint()], warning: [endpoint({ id: 2, name: "shop api", last_code: null, last_error: "timeout" })] }, undefined);
    const prompt = investigationPrompt(issues, new Date("2026-11-01T09:30:00Z"));
    expect(prompt).toContain("these 2 service issues (as of 2026-11-01 09:30 UTC)");
    expect(prompt).toContain("- [failing] shop web is down (SHOP · HTTP 502)");
    expect(prompt).toContain("- [warning] shop api is failing (SHOP · timeout · rechecking)");
    expect(prompt).toContain("no production change");
    expect(prompt).toContain("forge_create_task");
    expect(investigationPrompt(issues.slice(0, 1))).toContain("this service issue");
  });
});

describe("dashboard widgets", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("services: all clear, or what is failing", async () => {
    vi.stubGlobal("fetch", routeFetch({ "GET /monitoring": () => jsonResponse(200, monitoring()) }));
    const { unmount } = renderWithProviders(
      <ShellProvider>
        <ServicesWidget d={dash()} />
      </ShellProvider>,
    );
    expect(await screen.findByText("All services OK")).toBeInTheDocument();
    unmount();

    renderWithProviders(
      <ShellProvider>
        <ServicesWidget d={dash({ endpoints_down: [endpoint()] })} />
      </ShellProvider>,
    );
    expect(screen.getByText("1 failing")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /shop web is down/ })).toHaveAttribute("href", "/p/SHOP");
  });

  it("services: one click opens an agent run that looks into the issues", async () => {
    vi.stubGlobal("fetch", routeFetch({ "GET /monitoring": () => jsonResponse(200, monitoring()), "GET /servers": () => jsonResponse(200, []) }));
    renderWithProviders(
      <ShellProvider>
        <ServicesWidget d={dash({ endpoints_down: [endpoint()], endpoints_warning: [endpoint({ id: 2, name: "shop api", last_status: "warning" })] })} />
      </ShellProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Investigate all 2" }));
    const prompt = (await screen.findByLabelText(/Prompt/i)) as HTMLTextAreaElement;
    expect(prompt.value).toContain("these 2 service issues");
    expect(prompt.value).toContain("shop web is down");
    expect(prompt.value).toContain("shop api is failing");
  });

  it("services: or just one issue", async () => {
    vi.stubGlobal("fetch", routeFetch({ "GET /monitoring": () => jsonResponse(200, monitoring()), "GET /servers": () => jsonResponse(200, []) }));
    renderWithProviders(
      <ShellProvider>
        <ServicesWidget d={dash({ endpoints_down: [endpoint()], endpoints_warning: [endpoint({ id: 2, name: "shop api", last_status: "warning" })] })} />
      </ShellProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Investigate shop api is failing" }));
    const prompt = (await screen.findByLabelText(/Prompt/i)) as HTMLTextAreaElement;
    expect(prompt.value).toContain("this service issue");
    expect(prompt.value).toContain("shop api is failing");
    expect(prompt.value).not.toContain("shop web is down");
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
