import { screen, within } from "@testing-library/react";
import type { Runner } from "@/api/types";
import { MachinesPage } from "@/features/agents/MachinesPage";
import { newerVersion, versionLabel } from "@/features/agents/versions";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

const runner = (o: Partial<Runner>): Runner => ({
  id: 1,
  name: "desk",
  hostname: "desk.example.com",
  os: "linux/amd64",
  version: "v0.1.42",
  online: true,
  last_seen_at: "2026-10-14T10:00:00Z",
  capabilities: { claude: true, permission_modes: [], commands: [], max_concurrent: 1, command_details: [], ci: false, terminal: true, code: false },
  running: 0,
  created_at: "2026-09-01T00:00:00Z",
  role: "master",
  pair_expires_at: null, members_allowed: false,
  ...o,
});

describe("agent versions", () => {
  it("labels without doubling the v and tells when the server is ahead", () => {
    expect(versionLabel("v0.1.42")).toBe("v0.1.42");
    expect(versionLabel("0.1.42")).toBe("v0.1.42");
    expect(versionLabel("dev")).toBe("dev");
    expect(newerVersion("v0.1.42", "v0.1.40")).toBe(true);
    expect(newerVersion("v0.1.42", "0.1.42")).toBe(false);
    expect(newerVersion("v0.1.10", "v0.1.9")).toBe(true);
    expect(newerVersion("v0.1.9", "v0.1.10")).toBe(false);
    expect(newerVersion("dev", "v0.1.40")).toBe(false);
  });
});

describe("MachinesPage", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("marks an offline master red, prints the version once and flags agents behind the server", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /runners": () =>
          jsonResponse(200, {
            runners: [runner({ online: false }), runner({ id: 2, name: "laptop", role: "worker", online: false, version: "v0.1.40" })],
          }),
        "GET /system": () => jsonResponse(200, { version: "v0.1.42" }),
      }),
    );
    renderWithProviders(<MachinesPage />, { route: "/machines" });
    const desk = (await screen.findByText("desk", { selector: "span" })).closest("li")!;
    expect(desk.querySelector(".rounded-full")).toHaveClass("bg-critical");
    expect(within(desk).getByText(/· v0\.1\.42 ·/)).toBeInTheDocument();
    expect(within(desk).queryByText(/vv/)).not.toBeInTheDocument();
    expect(within(desk).queryByText(/update available/)).not.toBeInTheDocument();

    const laptop = screen.getByText("laptop", { selector: "span" }).closest("li")!;
    expect(laptop.querySelector(".rounded-full")).toHaveClass("bg-fg-3"); // asleep, nothing wrong
    const update = await within(laptop).findByText("update available v0.1.40 → v0.1.42");
    expect(update.closest("[title]")).toHaveAttribute("title", "Run `forge update` on it");
  });
});
