import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes, useParams } from "react-router-dom";
import type { Monitoring, Server } from "@/api/types";
import { ServerPage } from "@/features/infra/ServerPage";
import { MonitoringPage } from "@/features/monitoring/MonitoringPage";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

const server = (over: Partial<Server>): Server => ({
  id: 1,
  name: "web-1",
  role: "Nomad apps",
  provider: "eu-west",
  arch: "aarch64",
  public_address: "203.0.113.10",
  tailscale_ip: "192.0.2.10",
  environment: "production",
  critical: true,
  tags: ["nomad"],
  notes: "Runs **the shop**.",
  ssh_target: "root@web-1.example.com",
  projects: [{ key: "SHOP", name: "Shop", color: "blue", role: "apps" }],
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-01T00:00:00Z",
  ...over,
});

const SERVERS = [server({}), server({ id: 2, name: "db-1", critical: false, ssh_target: "", projects: [{ key: "BLOG", name: "Blog", color: "red", role: "db" }] })];

const MON: Monitoring = {
  fetched_at: "2026-10-14T10:00:00Z",
  hosts: [
    { name: "web-1", server_id: 1, up: true, last_seen: "2026-10-14T10:00:00Z", uptime_seconds: 93784, cpu_pct: 12, mem_pct: 85, disk_pct: 40, load1: 0.5 },
    { name: "db-1", server_id: 2, up: true, last_seen: "2026-10-14T10:00:00Z", uptime_seconds: 100, cpu_pct: 1, mem_pct: 2, disk_pct: 3, load1: 0.1 },
  ],
  probes: [
    { url: "https://shop.example.com/health", up: true, ssl_days_left: 40 },
    { url: "https://blog.example.com/", up: false, ssl_days_left: 5 },
  ],
  nomad: [
    { host: "web-1", reachable: true, error: "", jobs: [{ id: "shop-api", type: "service", status: "running", running: 2, failed: 0, queued: 0 }] },
    { host: "db-1", reachable: true, error: "", jobs: [{ id: "blog-db", type: "service", status: "running", running: 1, failed: 0, queued: 0 }] },
  ],
  grafana: {
    url: "https://grafana.example.com",
    connected: true,
    error: "",
    alerts: [
      { name: "HighMemory", state: "firing", severity: "warning", summary: "Memory above 80%", labels: { instance: "web-1.example.com:9100" }, since: null },
      { name: "DiskFull", state: "pending", severity: "critical", summary: "", labels: { host: "db-1" }, since: null },
    ],
    dashboards: [
      { uid: "a", title: "Shop funnel", folder: "Apps", url: "https://grafana.example.com/d/a" },
      { uid: "b", title: "web-1 node", folder: "Hosts", url: "https://grafana.example.com/d/b" },
      { uid: "c", title: "Blog traffic", folder: "Apps", url: "https://grafana.example.com/d/c" },
    ],
  },
};

const ENDPOINTS = [
  { id: 1, project_key: "SHOP", project_name: "Shop", url: "https://shop.example.com/health", name: "Shop health" },
  { id: 2, project_key: "BLOG", project_name: "Blog", url: "https://blog.example.com/", name: "Blog" },
].map((e) => ({
  project_id: 1,
  project_color: "blue",
  kind: "health",
  expect_status: 200,
  enabled: true,
  last_status: "up",
  last_code: 200,
  last_latency_ms: 20,
  last_error: "",
  last_checked_at: null,
  last_change_at: null,
  uptime_24h: 1,
  ...e,
}));

function Attached() {
  const { runnerId, session } = useParams();
  return <p>attached {runnerId}/{session}</p>;
}

describe("server page", () => {
  let shellBody: unknown = null;
  beforeEach(() => {
    localStorage.clear();
    shellBody = null;
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /servers": () => jsonResponse(200, { servers: SERVERS }),
        "GET /monitoring": () => jsonResponse(200, MON),
        "GET /endpoints": () => jsonResponse(200, { endpoints: ENDPOINTS }),
        "GET /projects": () => jsonResponse(200, { projects: [{ key: "SHOP", name: "Shop" }, { key: "BLOG", name: "Blog" }] }),
        "GET /terminal/hosts": () =>
          jsonResponse(200, {
            hosts: [{ runner_id: 7, runner_name: "desk", hostname: "desk", online: true, terminal: true, sessions: [], updated_at: null, role: "master" }],
            default_runner_id: 7,
          }),
        "POST /servers/1/shell": (init) => {
          shellBody = JSON.parse(String(init.body));
          return jsonResponse(201, { runner_id: 7, session: "ssh-web-1" });
        },
      }),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it("shows the host's details, metrics, jobs, alerts and dashboards, and opens a shell", async () => {
    renderWithProviders(
      <Routes>
        <Route path="/infra/servers/:id" element={<ServerPage />} />
        <Route path="/terminal/:runnerId/:session" element={<Attached />} />
      </Routes>,
      { route: "/infra/servers/1" },
    );
    expect(await screen.findByText("root@web-1.example.com")).toBeInTheDocument();
    expect(screen.getByText("Production-critical: confirm before changing anything on this server.", { exact: false })).toBeInTheDocument();
    expect(await screen.findByRole("meter", { name: "Memory usage" })).toHaveAttribute("aria-valuenow", "85");
    expect(screen.getByText("shop-api")).toBeInTheDocument();
    expect(screen.queryByText("blog-db")).not.toBeInTheDocument();
    expect(screen.getByText("HighMemory")).toBeInTheDocument();
    expect(screen.queryByText("DiskFull")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /web-1 node/ })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Shop/ })).toHaveAttribute("href", "/p/SHOP");
    expect(await screen.findByText("Shop health")).toBeInTheDocument();
    expect(screen.queryByText("Blog")).not.toBeInTheDocument();

    const open = screen.getByRole("button", { name: /Open shell/ });
    await screen.findByTitle("ssh root@web-1.example.com from desk");
    await userEvent.click(open);
    expect(await screen.findByText("attached 7/ssh-web-1")).toBeInTheDocument();
    expect(shellBody).toEqual({ runner_id: 7 });
  });

  it("says so for an unknown server", async () => {
    renderWithProviders(
      <Routes>
        <Route path="/infra/servers/:id" element={<ServerPage />} />
      </Routes>,
      { route: "/infra/servers/99" },
    );
    expect(await screen.findByText("No such server")).toBeInTheDocument();
  });
});

describe("monitoring project filter", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /servers": () => jsonResponse(200, { servers: SERVERS }),
        "GET /monitoring": () => jsonResponse(200, MON),
        "GET /endpoints": () => jsonResponse(200, { endpoints: ENDPOINTS }),
        "GET /projects": () => jsonResponse(200, { projects: [{ key: "SHOP", name: "Shop" }, { key: "BLOG", name: "Blog" }] }),
      }),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it("narrows hosts, jobs, alerts, probes and dashboards to the project", async () => {
    renderWithProviders(<MonitoringPage />, { route: "/monitoring?project=SHOP" });
    expect(await screen.findByText("Showing what belongs to", { exact: false })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "web-1" })).toHaveAttribute("href", "/infra/servers/1");
    expect(screen.queryByText("db-1")).not.toBeInTheDocument();
    expect(screen.getByText("shop-api")).toBeInTheDocument();
    expect(screen.queryByText("blog-db")).not.toBeInTheDocument();
    expect(screen.getByText("HighMemory")).toBeInTheDocument();
    expect(screen.queryByText("DiskFull")).not.toBeInTheDocument();
    expect(screen.getByText("shop.example.com/health")).toBeInTheDocument();
    expect(screen.queryByText("blog.example.com/")).not.toBeInTheDocument();
    expect(screen.getByText("Shop funnel")).toBeInTheDocument();
    expect(screen.queryByText("Blog traffic")).not.toBeInTheDocument();
    expect(screen.getAllByText(/1 of 2 hosts/)).toHaveLength(2); // Hosts and Nomad

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Project" }), "");
    expect(await screen.findByText("blog-db")).toBeInTheDocument();
    expect(screen.queryByText("Showing what belongs to", { exact: false })).not.toBeInTheDocument();
  });
});
