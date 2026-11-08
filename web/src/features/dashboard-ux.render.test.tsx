import { fireEvent, screen } from "@testing-library/react";
import type { Activity, Project } from "@/api/types";
import { ActivityFeed } from "@/features/dashboard/ActivityFeed";
import { ProjectCard } from "@/features/projects/ProjectCard";
import { ShellProvider } from "@/features/shell/context";
import { foldRepeats } from "@/lib/activity";
import { prodFirst, type ServiceIssue } from "@/lib/monitoring";
import { shortHome } from "@/lib/paths";
import { renderWithProviders } from "@/test/render";

const act = (o: Partial<Activity>): Activity => ({
  id: 1,
  project_id: 1,
  project_key: "SHOP",
  project_color: "#3987e5",
  task_id: null,
  task_ref: null,
  run_id: null,
  kind: "project.updated",
  summary: "",
  created_at: "2026-11-01T10:00:00Z",
  ...o,
});

describe("activity feed", () => {
  it("folds look-alike entries in a row, never task or run events", () => {
    const rows = foldRepeats([
      act({ id: 1, summary: "shop_api: pulled 3 new commit(s) on box" }),
      act({ id: 2, summary: "shop_api: pulled 6 new commit(s) on box" }),
      act({ id: 3, summary: "shop_api: pulled 2 new commit(s) on box" }),
      act({ id: 4, kind: "run.finished", run_id: 7, summary: "Run #7 on shop_api succeeded" }),
      act({ id: 5, kind: "run.finished", run_id: 8, summary: "Run #8 on shop_api succeeded" }),
      act({ id: 6, summary: "shop_api: pulled 1 new commit(s) on box" }),
    ]);
    expect(rows.map((r) => [r.a.id, r.count])).toEqual([
      [1, 3],
      [4, 1],
      [5, 1],
      [6, 1],
    ]);
  });

  it("links the run number and task ref where the summary names them, once", () => {
    renderWithProviders(
      <ShellProvider>
        <ActivityFeed
          items={[
            act({ id: 1, kind: "run.finished", run_id: 91, summary: "Run #91 on shop_api succeeded ($0.38)" }),
            act({ id: 2, kind: "task.completed", task_id: 4, task_ref: "SHOP-4", summary: "Completed SHOP-4 Ship v2" }),
            act({ id: 3, kind: "task.commented", task_id: 5, task_ref: "SHOP-5", summary: "Commented: looks good" }),
          ]}
        />
      </ShellProvider>,
    );
    expect(screen.getAllByText("#91")).toHaveLength(1);
    expect(screen.getByRole("link", { name: "#91" })).toHaveAttribute("href", "/agents/runs/91");
    expect(screen.getAllByText("SHOP-4")).toHaveLength(1);
    // A summary that doesn't name its task gets the ref in front.
    expect(screen.getByRole("button", { name: "SHOP-5" })).toBeInTheDocument();
  });

  it("shows the first rows and the rest on demand", () => {
    const items = Array.from({ length: 20 }, (_, i) => act({ id: i + 1, kind: "task.created", task_id: i + 1, task_ref: `SHOP-${i + 1}`, summary: `Created task ${i + 1}` }));
    renderWithProviders(
      <ShellProvider>
        <ActivityFeed items={items} limit={5} />
      </ShellProvider>,
    );
    expect(screen.getAllByRole("listitem")).toHaveLength(5);
    fireEvent.click(screen.getByRole("button", { name: "Show 15 more" }));
    expect(screen.getAllByRole("listitem")).toHaveLength(20);
  });
});

describe("services", () => {
  it("puts failing production hosts first and marks them", () => {
    const issues: ServiceIssue[] = [
      { key: "a", level: "warn", title: "web1 disk 85% full", detail: "host", to: "/monitoring", host: "web1" },
      { key: "b", level: "fail", title: "build1 is not reporting", detail: "host", to: "/monitoring", host: "build1" },
      { key: "c", level: "fail", title: "web1 is not reporting", detail: "host", to: "/monitoring", host: "203.0.113.10" },
      { key: "d", level: "fail", title: "shop is down", detail: "SHOP", to: "/p/SHOP", project_key: "SHOP" },
    ];
    const servers = [
      { name: "web1", tailscale_ip: "", public_address: "203.0.113.10", environment: "production" as const, critical: true },
      { name: "build1", tailscale_ip: "", public_address: "", environment: "infra" as const, critical: false },
    ];
    const out = prodFirst(issues, servers);
    expect(out.map((i) => i.key)).toEqual(["c", "b", "d", "a"]);
    expect(out.filter((i) => i.prod).map((i) => i.key)).toEqual(["c", "a"]);
  });

  it("shortens home directories in session paths", () => {
    expect(shortHome("/home/someone/dev/shop")).toBe("~/dev/shop");
    expect(shortHome("/Users/someone")).toBe("~");
    expect(shortHome("/srv/shop")).toBe("/srv/shop");
  });
});

describe("project card counts", () => {
  const project = (stats: Partial<Project["stats"]>): Project =>
    ({
      id: 1, key: "SHOP", name: "Shop", category: "work", status: "live", priority: 1, color: "#3987e5", summary: "A shop", description: "", infra_notes: "",
      target_date: null, links: [], hide_on_dashboard: false, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
      stats: {
        total: 10, backlog: 2, todo: 3, in_progress: 1, blocked: 2, done: 2, overdue: 1, done_7d: 0, created_7d: 0, commits_7d: 0, dirty_repos: 0, ci_failing: 0,
        endpoints_total: 0, endpoints_up: 0, endpoints_down: 0, endpoints_warning: 0, active_runs: 0, last_activity_at: null, progress: 0.2, ...stats,
      },
    }) as Project;

  it("counts each open task once and flags blocked, overdue and failing CI", () => {
    renderWithProviders(<ProjectCard project={project({ ci_failing: 1 })} today="2026-11-01" />);
    const line = screen.getByText("to do").parentElement!;
    expect(line).toHaveTextContent("5 to do");
    expect(line).toHaveTextContent("1 in progress");
    expect(line).toHaveTextContent("2 blocked");
    expect(line).toHaveTextContent("1 overdue");
    expect(screen.getByText("CI failing")).toBeInTheDocument();
  });

  it("leaves out counts that are zero", () => {
    renderWithProviders(<ProjectCard project={project({ in_progress: 0, blocked: 0, overdue: 0 })} today="2026-11-01" />);
    expect(screen.queryByText(/in progress/)).not.toBeInTheDocument();
    expect(screen.queryByText(/blocked/)).not.toBeInTheDocument();
    expect(screen.queryByText("CI failing")).not.toBeInTheDocument();
  });
});
