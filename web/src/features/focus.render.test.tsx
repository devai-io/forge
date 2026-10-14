import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useLocation } from "react-router-dom";
import type { ProjectFocus } from "@/api/types";
import { FocusPanel } from "@/features/projects/FocusPanel";
import { ShellProvider } from "@/features/shell/context";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

const focus = (over: Partial<ProjectFocus> = {}): ProjectFocus => ({
  focus: {
    headline: "Ship the checkout redesign this week",
    now: [
      { title: "Finish the checkout redesign", why: "In progress for three days.", ref: "SHOP-1", kind: "task" },
      { title: "Fix CI on shop-api", why: "The latest run failed.", ref: null, kind: "repo" },
    ],
    next: [{ title: "Write the restore runbook", why: "Medium priority chore.", ref: "SHOP-5", kind: "task" }],
    roadmap: [
      { title: "Uptime alerts", status: "done", refs: ["SHOP-8"] },
      { title: "Checkout v2", status: "active", refs: ["SHOP-1"] },
      { title: "Mobile 2.1", status: "planned", refs: [] },
    ],
    blockers: [{ title: "Publish mobile app 2.1", why: "Waiting on store review.", ref: "SHOP-4", kind: "task" }],
  },
  source: "rules",
  model: "",
  trigger: "after SHOP-1 updated",
  generated_at: "2026-10-14T10:00:00.000Z",
  dirty: true,
  dirty_reason: "SHOP-2 updated",
  error: "",
  tasks: {
    "SHOP-1": { id: 1, status: "in_progress", title: "Redesign the checkout flow" },
    "SHOP-4": { id: 4, status: "blocked", title: "Publish mobile app 2.1" },
    "SHOP-5": { id: 5, status: "todo", title: "Write the restore-from-backup runbook" },
    "SHOP-8": { id: 8, status: "done", title: "Set up uptime alerts for the API" },
  },
  ...over,
});

describe("Focus panel", () => {
  it("shows now, next, roadmap and blockers, opens tasks and recalculates", async () => {
    let refreshed = 0;
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /projects/SHOP/focus": () => jsonResponse(200, focus()),
        "POST /projects/SHOP/focus/refresh": () => {
          refreshed++;
          return jsonResponse(200, focus({ source: "ai", model: "fake-small", dirty: false, trigger: "recalculated by hand",
            focus: { ...focus().focus, headline: "Fresh take" } }));
        },
      }),
    );
    let search = "";
    function Where() {
      search = useLocation().search;
      return null;
    }
    renderWithProviders(
      <ShellProvider>
        <Where />
        <FocusPanel projectKey="SHOP" />
      </ShellProvider>,
      { route: "/p/SHOP" },
    );
    const user = userEvent.setup();

    expect(await screen.findByText("Ship the checkout redesign this week")).toBeInTheDocument();
    expect(screen.getByText("Finish the checkout redesign")).toBeInTheDocument();
    expect(screen.getByText("Write the restore runbook")).toBeInTheDocument();
    expect(screen.getByText("Checkout v2")).toBeInTheDocument();
    expect(screen.getByText("Publish mobile app 2.1")).toBeInTheDocument();
    expect(screen.getByText(/stale — recalculating soon/)).toBeInTheDocument();
    expect(screen.getByText(/after SHOP-1 updated/)).toBeInTheDocument();

    await user.click(screen.getAllByRole("button", { name: "SHOP-1" })[0]);
    await waitFor(() => expect(search).toBe("?task=1"));

    await user.click(screen.getByRole("button", { name: /Recalculate/ }));
    expect(await screen.findByText("Fresh take")).toBeInTheDocument();
    expect(refreshed).toBe(1);
    expect(screen.getByText(/AI \(fake-small\)/)).toBeInTheDocument();
    expect(screen.queryByText(/stale/)).not.toBeInTheDocument();
  });
});
