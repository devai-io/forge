import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DocsPage } from "@/features/docs/DocsPage";
import { SecurityLogPanel } from "@/features/settings/SecurityLog";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

const facts = {
  version: "0.5.0",
  started_at: "2026-09-28T06:00:00Z",
  public_url: "https://forge.example.com",
  intervals: {
    endpoint_check: "2m0s",
    repo_scan: "5m0s",
    repo_sync: "30m0s",
    ci_status: "15m0s",
    runner_heartbeat: "10s",
    monitoring_cache: "30s",
    checkup_time: "07:30",
    timezone: "Europe/Lisbon",
    session_ttl: "720h0m0s",
    session_max: "2160h0m0s",
    elevation: "10m0s",
    code_session: "12h0m0s",
  },
  features: { smtp: true, vault: true, totp_enabled: false, grafana_connected: false, victoriametrics: true, monitoring_sources: ["vm"] },
  master: { name: "desk", online: true, code: true, terminal: true },
  runners: [{ name: "desk", role: "master", online: true, commands: 4, terminal: true }, { name: "mac", role: "ios", online: false, commands: 1, terminal: true }],
  counts: { projects: 10, repos: 32, repos_scanned: 30, servers: 10, endpoints: 14, tasks_open: 41, tasks_done: 12, vault_items: 55, vault_with_values: 12, checkups: 3, runs: 9, security_events: 40 },
  checkup: { last_at: "2026-09-28T05:30:00Z", last_trigger: "schedule", next_at: "2026-09-29T05:30:00Z", emailed_last: true },
  seeded_at: "2026-09-27T20:00:00Z",
};

describe("DocsPage", () => {
  it("renders every section with an anchor, the tier legend and the live facts", async () => {
    vi.stubGlobal("fetch", routeFetch({ "GET /system": () => jsonResponse(200, facts) }));
    renderWithProviders(<DocsPage />, { route: "/docs" });
    for (const id of ["overview", "now", "setup", "sources", "checkup", "stored", "security", "limits"]) {
      expect(document.getElementById(id)).not.toBeNull();
    }
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("What Forge does — and what it doesn't");
    // Four tiers in the legend, and the chips reused on the source cards.
    expect(screen.getAllByText("Automated").length).toBeGreaterThan(1);
    expect(screen.getAllByText("Stored").length).toBeGreaterThan(1);
    // The facts strip and the live interval overlaid on a source card.
    expect(await screen.findByText("desk")).toBeInTheDocument();
    expect(screen.getByText("12 with values")).toBeInTheDocument();
    expect(screen.getByText("every 2m")).toBeInTheDocument();
    expect(screen.getByText("daily at 07:30 Europe/Lisbon")).toBeInTheDocument();
    expect(screen.getByText("SMTP on")).toBeInTheDocument();
    expect(screen.getByText("Grafana off")).toBeInTheDocument();
    // The check table: a "fail / warn" rule shows both badges.
    const table = screen.getByRole("table");
    expect(within(table).getAllByText("fail").length).toBeGreaterThan(3);
    expect(within(table).getAllByText("warn").length).toBeGreaterThan(3);
  });
});

describe("SecurityLogPanel", () => {
  it("highlights new-device and failed sign-ins, and filters by kind", async () => {
    const seen: string[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /auth/security": () => {
          seen.push("hit");
          return jsonResponse(200, {
            events: [
              { id: 3, kind: "login_new_device", detail: "", ip: "203.0.113.7", user_agent: "Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) Version/19.0 Safari/604.1", session_id: 2, at: "2026-09-28T09:00:00Z" },
              { id: 2, kind: "vault_reveal", detail: "Play upload keystore", ip: "198.51.100.9", user_agent: "Mozilla/5.0 (X11; Linux x86_64) Chrome/140.0 Safari/537.36", session_id: 1, at: "2026-09-28T08:00:00Z" },
              { id: 1, kind: "login_failed", detail: "ada", ip: "203.0.113.9", user_agent: "curl/8.9.1", session_id: null, at: "2026-09-28T07:00:00Z" },
            ],
          });
        },
      }),
    );
    renderWithProviders(<SecurityLogPanel />);
    const rows = await screen.findAllByRole("listitem");
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent("Signed in from a new device");
    expect(rows[0]).toHaveTextContent("Safari · iOS");
    expect(rows[0].className).toMatch(/bg-warning/);
    expect(rows[1]).toHaveTextContent("Vault secret revealed");
    expect(rows[1]).toHaveTextContent("Play upload keystore");
    expect(rows[1].className).not.toMatch(/bg-warning/);
    expect(rows[2]).toHaveTextContent("Failed sign-in");
    expect(rows[2]).toHaveTextContent("curl");
    expect(rows[2].className).toMatch(/bg-warning/);

    await userEvent.setup().selectOptions(screen.getByLabelText("Filter by kind"), "vault_reveal");
    expect(seen.length).toBeGreaterThanOrEqual(2);
  });
});
