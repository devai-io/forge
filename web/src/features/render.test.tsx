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
    // The tool calls between what Claude says fold into one quiet line.
    const group = screen.getByRole("button", { name: /Bash · Read/ });
    expect(group).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("go test ./...")).not.toBeInTheDocument();
    expect(screen.getByText("Finished")).toBeInTheDocument();
    expect(screen.getByText("$0.12")).toBeInTheDocument();
    expect(screen.getByText("1m 5s")).toBeInTheDocument();
    expect(screen.getByText("warning: something")).toBeInTheDocument();

    // Opened: a line per call; a call's output only when that call is opened.
    await userEvent.click(group);
    expect(screen.getByText("go test ./...")).toBeInTheDocument();
    expect(screen.queryByText(/ok\s+shop\/pkg\/api/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /^Bash go test/ }));
    expect(screen.getByText(/ok\s+shop\/pkg\/api/)).toBeInTheDocument();
    expect(screen.getByText("$")).toBeInTheDocument(); // the command, terminal style
  });
});

describe("RunLog activity details", () => {
  it("keeps terminal colours in output and shows edits as diffs", async () => {
    const items = mapRunEvents([
      { seq: 1, at: "2026-11-12T10:00:00Z", kind: "claude", data: { type: "assistant", message: { content: [
        { type: "tool_use", id: "b1", name: "Bash", input: { command: "npm test" } },
        { type: "tool_use", id: "e1", name: "Edit", input: { file_path: "/src/a.ts", old_string: "const a = 1;", new_string: "const a = 2;" } },
      ] } } },
      { seq: 2, at: "2026-11-12T10:00:01Z", kind: "claude", data: { type: "user", message: { content: [
        { type: "tool_result", tool_use_id: "b1", content: "\x1b[32m✓ 12 passed\x1b[0m" },
        { type: "tool_result", tool_use_id: "e1", content: "ok" },
      ] } } },
    ] as never);
    render(
      <MemoryRouter>
        <RunLog items={items} />
      </MemoryRouter>,
    );
    await userEvent.click(screen.getByRole("button", { name: /Bash · Edit/ }));
    await userEvent.click(screen.getByRole("button", { name: /^Bash npm test/ }));
    const passed = screen.getByText("✓ 12 passed");
    expect(passed.tagName).toBe("SPAN");
    expect(passed).toHaveStyle({ color: "#9ece6a" });
    await userEvent.click(screen.getByRole("button", { name: /^Edit/ }));
    expect(screen.getAllByText("/src/a.ts")).toHaveLength(2); // the call's line and the diff's header
    expect(screen.getByText(/const a = 1;/).closest("div")).toHaveClass("bg-critical/10");
    expect(screen.getByText(/const a = 2;/).closest("div")).toHaveClass("bg-good/10");
  });
});

