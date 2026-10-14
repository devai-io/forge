import { screen, within } from "@testing-library/react";
import type { Endpoint, Project, ProjectStats } from "@/api/types";
import { EndpointsTable } from "@/features/infra/endpoints";
import { AppShell } from "@/features/shell/AppShell";
import { endpointHealth, isFailing, retryLabel } from "@/lib/infra";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

const stats = (o: Partial<ProjectStats> = {}): ProjectStats => ({
  total: 0,
  backlog: 0,
  todo: 0,
  in_progress: 0,
  blocked: 0,
  done: 0,
  overdue: 0,
  done_7d: 0,
  created_7d: 0,
  commits_7d: 0,
  dirty_repos: 0,
  endpoints_total: 2,
  endpoints_up: 2,
  endpoints_down: 0,
  endpoints_warning: 0,
  active_runs: 0,
  last_activity_at: null,
  progress: 0,
  ...o,
});

const project = (key: string, name: string, s: ProjectStats): Project => ({
  id: key.length,
  key,
  name,
  category: "personal",
  status: "live",
  priority: 2,
  color: "#3987e5",
  summary: "",
  description: "",
  infra_notes: "",
  target_date: null,
  links: [],
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-01T00:00:00Z",
  stats: s,
});

const endpoint = (o: Partial<Endpoint> = {}): Endpoint => ({
  id: 1,
  project_id: 1,
  project_key: "SHOP",
  project_name: "Shop",
  project_color: "#3987e5",
  name: "home",
  url: "https://example.com/",
  kind: "web",
  expect_status: 200,
  enabled: true,
  last_status: "up",
  fail_streak: 0,
  retry_limit: 2,
  next_check_at: null,
  last_code: 200,
  last_latency_ms: 80,
  last_error: "",
  last_checked_at: "2026-10-14T10:00:00Z",
  last_change_at: null,
  uptime_24h: 1,
  ...o,
});

describe("endpoint health helpers", () => {
  it("ranks down over warning, and nothing when all is well", () => {
    expect(endpointHealth(stats())).toBeNull();
    expect(endpointHealth(stats({ endpoints_warning: 1 }))).toBe("warning");
    expect(endpointHealth(stats({ endpoints_warning: 1, endpoints_down: 1 }))).toBe("down");
    expect(isFailing({ last_status: "warning" })).toBe(true);
    expect(isFailing({ last_status: "unknown" })).toBe(false);
    expect(retryLabel({ fail_streak: 1, retry_limit: 2 })).toBe("retry 1/2");
    expect(retryLabel({ fail_streak: 5, retry_limit: 2 })).toBe("retry 2/2");
  });
});

describe("sidebar project health dots", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows amber for failing-being-rechecked and a glowing red for down", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /projects": () =>
          jsonResponse(200, {
            projects: [
              project("SHOP", "Shop", stats({ endpoints_warning: 1, endpoints_up: 1 })),
              project("BLOG", "Blog", stats({ endpoints_down: 1, endpoints_warning: 1, endpoints_up: 0 })),
              project("WIKI", "Wiki", stats()),
            ],
          }),
        "GET /runs": () => jsonResponse(200, []),
      }),
    );
    renderWithProviders(<AppShell />);
    const nav = await screen.findByRole("navigation", { name: "Projects" });
    const shop = await within(nav).findByRole("link", { name: /Shop/ });
    const warn = within(shop).getByRole("img", { name: "1 endpoint failing, being rechecked" });
    expect(warn).toHaveClass("bg-warning");
    expect(warn).not.toHaveClass("glow-critical");

    const blog = within(nav).getByRole("link", { name: /Blog/ });
    const down = within(blog).getByRole("img", { name: "1 endpoint down" });
    expect(down).toHaveClass("bg-critical", "glow-critical");

    const wiki = within(nav).getByRole("link", { name: /Wiki/ });
    expect(within(wiki).queryByRole("img")).not.toBeInTheDocument();
  });
});

describe("endpoints table", () => {
  it("tells a warning (with its retry) apart from an error", () => {
    renderWithProviders(
      <EndpointsTable
        endpoints={[
          endpoint({ id: 1, name: "api", last_status: "warning", fail_streak: 1, last_code: 502, last_error: "expected HTTP 200, got 502", next_check_at: "2099-01-01T00:00:00Z" }),
          endpoint({ id: 2, name: "web", last_status: "down", fail_streak: 3, last_code: null, last_error: "timed out" }),
        ]}
      />,
    );
    expect(screen.getByText("Warning: failing, retry 1/2")).toBeInTheDocument();
    expect(screen.getByText("Warning", { selector: "span" })).toBeInTheDocument();
    expect(screen.getByText(/rechecking/)).toBeInTheDocument();
    expect(screen.getByText("Error: down")).toBeInTheDocument();
    expect(screen.getByText("timed out", { exact: false })).toBeInTheDocument();
  });
});
