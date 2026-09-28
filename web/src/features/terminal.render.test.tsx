import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { SessionCard } from "@/features/terminal/bits";
import { makeSession, makeWindow } from "@/test/fixtures";

describe("SessionCard", () => {
  it("lists windows with their project, repo and Claude marker, and attaches per window", async () => {
    const onAttach = vi.fn();
    const onPeek = vi.fn();
    const onKill = vi.fn();
    const session = makeSession({
      name: "Work",
      windows: 2,
      attached: 2,
      claude: true,
      window_list: [
        makeWindow({ index: 0, name: "infra", active: true, project_key: "SRV", repo_name: "infra", path: "/home/ada/dev/work/infra" }),
        makeWindow({ index: 1, name: "forge", active: false, claude: true, command: "claude", project_key: "WORK", repo_name: "forge_api" }),
      ],
    });
    render(
      <MemoryRouter>
        <SessionCard session={session} onAttach={onAttach} onPeek={onPeek} onKill={onKill} />
      </MemoryRouter>,
    );

    const card = screen.getByRole("article", { name: "Session Work" });
    expect(within(card).getAllByText("Claude")).toHaveLength(2); // session badge + window 1
    expect(within(card).getByText("2 attached")).toBeInTheDocument();
    expect(within(card).getByText("0:infra")).toBeInTheDocument();
    expect(within(card).getByText("forge_api")).toBeInTheDocument();
    expect(within(card).getByLabelText("current window")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(within(card).getByRole("button", { name: "Attach to window 1 (forge)" }));
    expect(onAttach).toHaveBeenCalledWith(expect.objectContaining({ index: 1, name: "forge" }));
    await user.click(within(card).getByRole("button", { name: "Peek at window 0 (infra)" }));
    expect(onPeek).toHaveBeenCalledWith(expect.objectContaining({ index: 0 }));
    await user.click(within(card).getByRole("button", { name: /^Attach$/ }));
    expect(onAttach).toHaveBeenLastCalledWith();
    await user.click(within(card).getByRole("button", { name: "Kill Work" }));
    expect(onKill).toHaveBeenCalled();
  });

  it("falls back to the session's own path and project when the runner reports no windows", () => {
    render(
      <MemoryRouter>
        <SessionCard
          session={makeSession({ name: "old", project_key: "GAME", repo_name: "game", path: "/home/ada/dev/work/game/game" })}
          onAttach={() => {}}
          onPeek={() => {}}
        />
      </MemoryRouter>,
    );
    expect(screen.getByText("GAME")).toBeInTheDocument();
    expect(screen.getByText(/game ·/)).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: /Windows of/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Kill/ })).not.toBeInTheDocument();
  });
});
