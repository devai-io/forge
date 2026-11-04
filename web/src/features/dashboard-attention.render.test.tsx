import { screen, within } from "@testing-library/react";
import type { Dashboard } from "@/api/types";
import { AttentionPanel } from "@/features/dashboard/DashboardPage";
import { ShellProvider } from "@/features/shell/context";
import { makeTask } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";

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
