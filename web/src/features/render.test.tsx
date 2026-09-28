import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { RunLog } from "@/features/agents/RunLog";
import { FocusRow, TaskCard } from "@/features/tasks/TaskCard";
import { mapRunEvents } from "@/lib/runlog";
import { claudeTranscript, makeTask } from "@/test/fixtures";

describe("TaskCard", () => {
  it("shows ref, title, labels and an overdue due date, and opens on click", async () => {
    const onOpen = vi.fn();
    render(<TaskCard task={makeTask({ due_date: "2026-09-25" })} today="2026-09-27" onOpen={onOpen} />);
    const card = screen.getByRole("button", { name: "SHOP-12: Ship the payout dashboard" });
    expect(within(card).getByText("SHOP-12")).toBeInTheDocument();
    expect(within(card).getByText("launch")).toBeInTheDocument();
    expect(within(card).getByText("2d overdue")).toBeInTheDocument();
    await userEvent.click(card);
    expect(onOpen).toHaveBeenCalledWith(1);
  });
});

describe("FocusRow", () => {
  it("completes without opening the task", async () => {
    const onOpen = vi.fn();
    const onComplete = vi.fn();
    render(<FocusRow task={makeTask()} today="2026-09-27" onOpen={onOpen} onComplete={onComplete} />);
    await userEvent.click(screen.getByRole("button", { name: "Mark SHOP-12 done" }));
    expect(onComplete).toHaveBeenCalledTimes(1);
    expect(onOpen).not.toHaveBeenCalled();
  });
});

describe("RunLog", () => {
  it("renders a Claude transcript readably", async () => {
    render(
      <MemoryRouter>
        <RunLog items={mapRunEvents(claudeTranscript())} />
      </MemoryRouter>,
    );
    expect(screen.getByText(/Session started/)).toBeInTheDocument();
    expect(screen.getByText("tests")).toBeInTheDocument(); // markdown bold rendered
    expect(screen.getByText("go test ./...")).toBeInTheDocument();
    expect(screen.getByText("Finished")).toBeInTheDocument();
    expect(screen.getByText("$0.12")).toBeInTheDocument();
    expect(screen.getByText("1m 5s")).toBeInTheDocument();
    expect(screen.getByText("warning: something")).toBeInTheDocument();

    // Tool output stays collapsed until asked for.
    expect(screen.queryByText(/ok\s+shop\/pkg\/api/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Bash/ }));
    expect(screen.getByText(/ok\s+shop\/pkg\/api/)).toBeInTheDocument();
  });
});
