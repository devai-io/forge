import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import type { CheckItem, CheckupSummary, Dashboard, Project } from "@/api/types";
import { CheckupPanel } from "@/features/checkup/CheckupPage";
import { AttentionPanel, DashboardProjectCard } from "@/features/dashboard/DashboardPage";
import { ShellProvider } from "@/features/shell/context";
import { makeTask } from "@/test/fixtures";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

const dash = (o: Partial<Dashboard> = {}): Dashboard =>
  ({ today: "2026-10-29", overdue: [], due_soon: [], blocked: [], in_progress: [], active_runs: [], ...o }) as Dashboard;

describe("AttentionPanel", () => {
  it("renders nothing when no task needs a decision", () => {
    renderWithProviders(
      <ShellProvider>
        <AttentionPanel d={dash()} />
      </ShellProvider>,
    );
    expect(screen.queryByText(/Needs attention/)).not.toBeInTheDocument();
  });

  it("groups overdue, due-soon and blocked tasks, each section linking to the list", () => {
    renderWithProviders(
      <ShellProvider>
        <AttentionPanel
          d={dash({
            overdue: [makeTask({ id: 1, ref: "SHOP-1", title: "Late invoice", due_date: "2026-10-01" })],
            due_soon: [
              makeTask({ id: 2, ref: "SHOP-2", title: "Renew domain", due_date: "2026-10-29" }),
              makeTask({ id: 3, ref: "SHOP-3", title: "Ship v2", due_date: "2026-10-31" }),
            ],
            blocked: [makeTask({ id: 4, ref: "GAME-4", title: "Store review", status: "blocked" })],
          })}
        />
      </ShellProvider>,
    );
    expect(screen.getByText("· 4")).toBeInTheDocument();
    const overdue = screen.getByRole("region", { name: /Overdue/ });
    expect(within(overdue).getByText("Late invoice")).toBeInTheDocument();
    expect(within(overdue).getByRole("link", { name: "All →" })).toHaveAttribute("href", "/tasks?overdue=1");
    const soon = screen.getByRole("region", { name: /Due soon/ });
    expect(within(soon).getByText("Renew domain")).toBeInTheDocument();
    expect(within(soon).getByText("Ship v2")).toBeInTheDocument();
    const blocked = screen.getByRole("region", { name: /Blocked/ });
    expect(within(blocked).getByText("Store review")).toBeInTheDocument();
    expect(within(blocked).getByRole("link", { name: "All →" })).toHaveAttribute("href", "/tasks?status=blocked");
    // Old dashboards without the new lists still render.
    renderWithProviders(
      <ShellProvider>
        <AttentionPanel d={{ ...dash({ overdue: [makeTask({ id: 9, title: "Only overdue" })] }), due_soon: undefined, blocked: undefined } as unknown as Dashboard} />
      </ShellProvider>,
    );
    expect(screen.getByText("Only overdue")).toBeInTheDocument();
  });
});

const checkItem = (o: Partial<CheckItem>): CheckItem => ({
  key: "k",
  category: "backups",
  severity: "warn",
  title: "",
  detail: "",
  action: "",
  link: null,
  project_key: null,
  done: false,
  task_id: null,
  task_ref: null,
  task_status: "",
  first_seen: "2026-11-02",
  checked_at: null,
  resolved_at: null,
  snoozed: false,
  snoozed_until: null,
  fixes: [],
  ...o,
});

describe("CheckupPanel on the dashboard", () => {
  it("lists the first open items and how many more there are", () => {
    const summary: CheckupSummary = {
      id: 3,
      date: "2026-11-04",
      trigger: "schedule",
      started_at: "2026-11-04T06:30:00Z",
      finished_at: "2026-11-04T06:30:09Z",
      status: "fail",
      counts: { ok: 8, warn: 2, fail: 1 },
      actions_total: 4,
      actions_done: 1,
      actions_new: 1,
      actions_snoozed: 0,
      fixed_total: 0,
      emailed: false,
    };
    const open = [
      checkItem({ key: "backup:forge", severity: "fail", title: "Forge's last database snapshot is 30 h old", action: "Check the server log", first_seen: "2026-11-04" }),
      checkItem({ key: "runner-version:laptop", category: "runners", title: "laptop runs Forge v0.1.40, the server v0.1.42" }),
    ];
    renderWithProviders(<CheckupPanel summary={summary} open={open} />);
    const list = screen.getByRole("list", { name: "Open check-up items" });
    expect(within(list).getByRole("link", { name: /30 h old/ })).toHaveAttribute("href", "/checkup#checkup-backups");
    expect(within(list).getByRole("link", { name: /v0\.1\.40/ })).toBeInTheDocument();
    expect(within(list).getAllByText("new")).toHaveLength(1);
    expect(within(list).getByRole("link", { name: "+1 more →" })).toHaveAttribute("href", "/checkup");
  });
});

const project = {
  id: 1,
  key: "SHOP",
  name: "Shop",
  category: "work",
  status: "live",
  priority: 1,
  color: "#3987e5",
  summary: "",
  description: "",
  infra_notes: "",
  target_date: null,
  links: [],
  hide_on_dashboard: false,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  stats: {
    total: 0, backlog: 0, todo: 0, in_progress: 0, blocked: 0, done: 0, overdue: 0, done_7d: 0, created_7d: 0, commits_7d: 0,
    dirty_repos: 0, endpoints_total: 0, endpoints_up: 0, endpoints_down: 0, endpoints_warning: 0, active_runs: 0, last_activity_at: null, progress: 0,
  },
} as Project;

describe("DashboardProjectCard", () => {
  it("hides the project from the dashboard in one click, with undo", async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "PATCH /projects/SHOP": (init) => {
          bodies.push(JSON.parse(String(init.body)));
          return jsonResponse(200, { ...project, hide_on_dashboard: true, repos: [], servers: [], endpoints: [] });
        },
      }),
    );
    renderWithProviders(
      <ShellProvider>
        <DashboardProjectCard project={project} today="2026-11-04" />
      </ShellProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Hide Shop from the dashboard" }));
    const undo = await screen.findByRole("button", { name: "Undo" });
    expect(bodies[0]).toEqual({ hide_on_dashboard: true });
    fireEvent.click(undo);
    await waitFor(() => expect(bodies[1]).toEqual({ hide_on_dashboard: false }));
    vi.unstubAllGlobals();
  });
});
