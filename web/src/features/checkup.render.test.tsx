import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CheckItem, Checkup, Server } from "@/api/types";
import { CheckupPage } from "@/features/checkup/CheckupPage";
import { ShellProvider } from "@/features/shell/context";
import { groupCheckItems, itemHost, itemServer, nameGroup } from "@/lib/checkup";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

const item = (o: Partial<CheckItem> = {}): CheckItem => ({
  key: "task:GAME-1",
  category: "tasks",
  severity: "warn",
  title: "GAME-1 overdue since 2026-10-01: Ship it",
  detail: "",
  action: "Do it today, move the date, or drop it",
  link: "/p/GAME?task=1",
  project_key: "GAME",
  done: false,
  task_id: null,
  task_ref: null,
  task_status: "",
  first_seen: "2026-10-01",
  checked_at: null,
  resolved_at: null,
  snoozed: false,
  snoozed_until: null,
  fixes: [],
  ...o,
});

const checkup: Checkup = {
  id: 7,
  date: "2026-10-22",
  trigger: "schedule",
  started_at: "2026-10-22T05:30:00Z",
  finished_at: "2026-10-22T05:30:12Z",
  status: "fail",
  counts: { ok: 1, warn: 2, fail: 1 },
  actions_total: 3,
  actions_done: 1,
  actions_new: 1,
  actions_snoozed: 1,
  fixed_total: 1,
  emailed: true,
  items: [
    item(),
    item({ key: "vault:5", category: "vault", severity: "fail", title: "api key expired 2026-06-11", first_seen: "2026-10-22", task_id: 9, task_ref: "SRV-9", task_status: "done" }),
    item({
      key: "repo:3:dirty",
      category: "repos",
      title: "shop: 2 uncommitted change(s) on main",
      first_seen: "2026-10-20",
      checked_at: "2026-10-22T09:00:00Z",
      resolved_at: "2026-10-22T09:00:00Z",
    }),
    item({ key: "task:GAME-2", title: "GAME-2 overdue since 2026-09-01: Old thing", first_seen: "2026-09-02", snoozed: true, snoozed_until: null }),
    item({ key: "ok:hosts", category: "hosts", severity: "ok", title: "All 3 hosts reporting", action: "", link: null, project_key: null }),
  ],
  resolved: [
    { key: "endpoint:2", category: "endpoints", severity: "fail", title: "shop is down", project_key: "GAME", task_ref: null, first_seen: "2026-10-19", since_at: "2026-10-21T05:30:00Z" },
  ],
};

function mount() {
  const fetch = routeFetch({
    "GET /checkups/latest": () => jsonResponse(200, checkup),
    "GET /checkups": () => jsonResponse(200, { checkups: [checkup] }),
    "GET /system": () => jsonResponse(404),
    "POST /checkups/7/items/task%3AGAME-1/recheck": () =>
      jsonResponse(200, {
        ...checkup,
        items: checkup.items.map((i) => (i.key === "task:GAME-1" ? { ...i, checked_at: "2026-10-22T10:00:00Z", resolved_at: "2026-10-22T10:00:00Z" } : i)),
      }),
  });
  vi.stubGlobal("fetch", fetch);
  renderWithProviders(
    <ShellProvider>
      <CheckupPage />
    </ShellProvider>,
    { route: "/checkup" },
  );
  return fetch;
}

describe("CheckupPage", () => {
  it("tells chronic from new, shows task state, re-check, fixed and snoozed", async () => {
    const fetch = mount();
    await screen.findByText("GAME-1 overdue since 2026-10-01: Ship it");

    // Age: the overdue task has been there since 1 Oct; the vault item is new.
    expect(screen.getByText("since 21 days")).toBeInTheDocument();
    expect(screen.getAllByText("new today").length).toBeGreaterThan(0);
    expect(screen.getByTitle(/New: first seen/)).toHaveTextContent("1 new · 2 recurring");

    // A done task under a still-failing item is called out, and another task can be made.
    expect(screen.getByText("task done, still here")).toBeInTheDocument();
    expect(screen.getByText("Make another task")).toBeInTheDocument();

    // A resolved item is ticked, disabled and labelled.
    const resolvedBox = screen.getByRole("checkbox", { name: /uncommitted change.*resolved/ });
    expect(resolvedBox).toBeChecked();
    expect(resolvedBox).toBeDisabled();
    expect(screen.getByText(/resolved · verified/)).toBeInTheDocument();

    // The fixed list and the snoozed list are there; snoozed items are not in the action groups.
    expect(screen.getByText("shop is down")).toBeInTheDocument();
    expect(screen.getByText(/was open 3 days/)).toBeInTheDocument();
    const snoozed = screen.getByText(/Snoozed \(1\)/).closest("details")!;
    expect(within(snoozed).getByText("GAME-2 overdue since 2026-09-01: Old thing")).toBeInTheDocument();
    expect(within(snoozed).getByText("until it changes")).toBeInTheDocument();

    // Header: one bulk re-check for the 2 open (not settled, not snoozed) items.
    expect(screen.getByRole("button", { name: /Re-check 2 open/ })).toBeInTheDocument();

    // Re-checking the overdue task resolves it.
    const row = screen.getByText("GAME-1 overdue since 2026-10-01: Ship it").closest("li")!;
    within(row).getByRole("button", { name: /Re-check/ }).click();
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/checkups/7/items/task%3AGAME-1/recheck"), expect.anything()));
    await screen.findByText(/Resolved — "GAME-1 overdue/);
  });

  it("applies a one-click fix and says whether the item is gone", async () => {
    const withFix: Checkup = {
      ...checkup,
      items: checkup.items.map((i) =>
        i.key === "task:GAME-1" ? { ...i, fixes: [{ id: "done", label: "Done" }, { id: "week", label: "Due in a week" }] } : i,
      ),
    };
    const fetch = routeFetch({
      "GET /checkups/latest": () => jsonResponse(200, withFix),
      "GET /checkups": () => jsonResponse(200, { checkups: [withFix] }),
      "GET /system": () => jsonResponse(404),
      "POST /checkups/7/items/task%3AGAME-1/fix": () =>
        jsonResponse(200, {
          note: "",
          checkup: { ...withFix, items: withFix.items.map((i) => (i.key === "task:GAME-1" ? { ...i, resolved_at: "2026-10-22T10:00:00Z", checked_at: "2026-10-22T10:00:00Z" } : i)) },
        }),
    });
    vi.stubGlobal("fetch", fetch);
    renderWithProviders(
      <ShellProvider>
        <CheckupPage />
      </ShellProvider>,
      { route: "/checkup" },
    );
    const row = (await screen.findByText("GAME-1 overdue since 2026-10-01: Ship it")).closest("li")!;
    within(row).getByRole("button", { name: "Due in a week" }).click();
    await screen.findByText("Due in a week — resolved");
    const call = fetch.mock.calls.find((c) => String(c[0]).includes("/fix"));
    const body = JSON.parse(String(call?.[1]?.body));
    expect(body).toEqual({ fix: "week" });
  });
});

describe("check-up grouping, ordering and servers", () => {
  const srv = (o: Partial<Server>): Server => ({
    id: 1,
    name: "web-1",
    role: "",
    provider: "",
    arch: "",
    public_address: "203.0.113.10",
    tailscale_ip: "192.0.2.10",
    environment: "production",
    critical: true,
    tags: [],
    notes: "",
    ssh_target: "",
    projects: [],
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-01T00:00:00Z",
    ...o,
  });
  const SERVERS = [
    srv({}),
    srv({ id: 2, name: "db-1", environment: "staging", critical: false, public_address: "", tailscale_ip: "" }),
    srv({ id: 3, name: "build-1", environment: "infra", critical: false, public_address: "", tailscale_ip: "" }),
    srv({ id: 4, name: "mail-1", environment: "infra", critical: false, public_address: "", tailscale_ip: "" }),
  ];
  const silent = (name: string, o: Partial<CheckItem> = {}) =>
    item({
      key: `host:${name}`,
      category: "hosts",
      severity: "fail",
      title: `${name} is not reporting metrics`,
      action: "SSH in and check the metrics agent / the host itself",
      link: "/monitoring",
      project_key: null,
      first_seen: "2026-10-20",
      ...o,
    });

  it("orders categories worst first, then by count; production servers lead within a severity", () => {
    const { groups } = groupCheckItems(
      [
        item({ key: "vault:1", category: "vault", severity: "warn", title: "a" }),
        item({ key: "vault:2", category: "vault", severity: "warn", title: "b" }),
        item({ key: "repo:1", category: "repos", severity: "warn", title: "c" }),
        item({ key: "disk:db-1", category: "hosts", severity: "warn", title: "db-1 root disk 85% full", first_seen: "2026-10-22" }),
        item({ key: "disk:web-1", category: "hosts", severity: "warn", title: "web-1 root disk 85% full" }),
        item({ key: "ci:x", category: "ci", severity: "fail", title: "d" }),
      ],
      "2026-10-22",
      (i) => itemServer(i, SERVERS)?.name === "web-1",
    );
    // ci (a fail) before every warn-only category; vault (2) before hosts (2, fixed order) before repos (1).
    expect(groups.map((g) => g.category)).toEqual(["ci", "hosts", "vault", "repos"]);
    // The production host's warning outranks the newer one on a staging host.
    expect(groups[1].items.map((i) => i.key)).toEqual(["disk:web-1", "disk:db-1"]);
  });

  it("folds three or more open items with one action into a named group", () => {
    const { groups } = groupCheckItems(
      [silent("web-1"), silent("db-1"), silent("build-1"), silent("old-1", { done: true }), item({ key: "disk:web-1", category: "hosts", title: "web-1 root disk 91% full", action: "Prune" })],
      "2026-10-22",
    );
    const [hosts] = groups;
    expect(hosts.items).toHaveLength(5);
    expect(hosts.entries.map((e) => e.kind)).toEqual(["group", "item", "item"]);
    const g = hosts.entries[0].kind === "group" ? hosts.entries[0].group : null;
    expect(g?.title).toBe("3 hosts not reporting metrics");
    expect(g?.subjects).toEqual(["build-1", "db-1", "web-1"]);
    expect(nameGroup("nomad", ["Job a has no running allocation", "Job b has no running allocation"]).title).toBe("2 jobs have no running allocation");
    expect(nameGroup("repos", ["x", "y"]).title).toBe("2 repositories with the same finding");
  });

  it("finds the server a finding is about", () => {
    expect(itemHost({ key: "job:web-1:shop-api", category: "nomad" })).toBe("web-1");
    expect(itemHost({ key: "alert:HighCPU:web-1.example.com:9100", category: "alerts" })).toBe("web-1.example.com:9100");
    expect(itemServer({ key: "nomad:192.0.2.10", category: "nomad", title: "", link: null }, SERVERS)?.name).toBe("web-1");
    expect(itemServer({ key: "x", category: "backups", title: "", link: "/infra/servers/2" }, SERVERS)?.name).toBe("db-1");
    expect(itemServer({ key: "backup:1", category: "backups", title: "Backup of db-1 is 3 days old", link: null }, SERVERS)?.name).toBe("db-1");
    expect(itemServer({ key: "repo:1", category: "repos", title: "shop: dirty", link: null }, SERVERS)).toBeUndefined();
  });

  it("shows a group row with chips, the pipeline hint and bulk re-check; PROD and a shell on single host rows", async () => {
    const hosts: Checkup = {
      ...checkup,
      items: [
        silent("web-1"),
        silent("db-1"),
        silent("build-1"),
        silent("mail-1"),
        item({ key: "job:web-1:shop-api", category: "nomad", severity: "fail", title: "Job shop-api is dead on web-1", action: "Redeploy it", link: "/monitoring", project_key: null }),
      ],
    };
    const fetch = routeFetch({
      "GET /checkups/latest": () => jsonResponse(200, hosts),
      "GET /checkups": () => jsonResponse(200, { checkups: [hosts] }),
      "GET /system": () => jsonResponse(404),
      "GET /servers": () => jsonResponse(200, { servers: SERVERS }),
      "POST /checkups/7/items/host%3Aweb-1/recheck": () => jsonResponse(200, hosts),
      "POST /checkups/7/items/host%3Adb-1/recheck": () => jsonResponse(200, hosts),
      "POST /checkups/7/items/host%3Abuild-1/recheck": () => jsonResponse(200, hosts),
      "POST /checkups/7/items/host%3Amail-1/recheck": () =>
        jsonResponse(200, { ...hosts, items: hosts.items.map((i) => (i.key === "host:mail-1" ? { ...i, resolved_at: "2026-10-22T10:00:00Z" } : i)) }),
    });
    vi.stubGlobal("fetch", fetch);
    renderWithProviders(
      <ShellProvider>
        <CheckupPage />
      </ShellProvider>,
      { route: "/checkup" },
    );
    const title = await screen.findByText("4 hosts not reporting metrics");
    const row = title.closest("li")!;
    const chips = within(row).getByRole("list", { name: "Affected" });
    expect(await within(chips).findByRole("link", { name: "web-1" })).toHaveAttribute("href", "/infra/servers/1");
    expect(within(chips).getAllByRole("listitem")).toHaveLength(4);
    expect(within(row).getByText(/shared metrics\s+pipeline/)).toBeInTheDocument();
    // One of them is on a production server.
    expect(await within(row).findByText("PROD")).toBeInTheDocument();
    // The single rows are behind the disclosure.
    expect(within(row).queryByRole("checkbox")).not.toBeInTheDocument();
    await userEvent.click(within(row).getByRole("button", { name: "Show the 4 items" }));
    expect(within(row).getAllByRole("checkbox")).toHaveLength(4);

    await userEvent.click(within(row).getByRole("button", { name: /Re-check all/ }));
    await screen.findByText("Re-checked 4: 1 resolved, 3 still open");
    expect(fetch.mock.calls.filter((c) => String(c[0]).includes("/recheck"))).toHaveLength(4);

    // The Nomad item stays a single row, marked PROD, with a shell on its server; Make task/Snooze also sit in a ⋯ menu for phones.
    const job = screen.getByText("Job shop-api is dead on web-1").closest("li")!;
    expect(within(job).getByText("PROD")).toBeInTheDocument();
    expect(within(job).getByRole("button", { name: "Open a shell on web-1" })).toBeInTheDocument();
    await userEvent.click(within(job).getByRole("button", { name: 'More actions for "Job shop-api is dead on web-1"' }));
    expect(within(job).getByRole("menuitem", { name: "Make task" })).toBeInTheDocument();
    expect(within(job).getByRole("menuitem", { name: "Hide for 7 days" })).toBeInTheDocument();

    // The failing Nomad category (1 item) sorts after hosts (4 fails) — worst, then most.
    const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
    expect(headings.findIndex((t) => t?.startsWith("Hosts"))).toBeLessThan(headings.findIndex((t) => t?.startsWith("Nomad jobs")));
  });
});
