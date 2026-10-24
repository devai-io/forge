import { screen, waitFor, within } from "@testing-library/react";
import type { CheckItem, Checkup } from "@/api/types";
import { CheckupPage } from "@/features/checkup/CheckupPage";
import { ShellProvider } from "@/features/shell/context";
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
