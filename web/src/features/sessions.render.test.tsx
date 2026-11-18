import { screen } from "@testing-library/react";
import type { CLISession } from "@/api/types";
import { SessionBadges } from "@/features/agents/SessionBadges";
import { sessionsCheckedAt } from "@/lib/agents";
import { renderWithProviders } from "@/test/render";

const at = (minutesAgo: number) => new Date(Date.now() - minutesAgo * 60_000).toISOString();

describe("SessionBadges", () => {
  it("shows each CLI's state and version, and when they were checked", () => {
    const sessions: CLISession[] = [
      { cli: "claude", state: "logged_in", version: "2.1.0", detail: "claude.ai, pro", checked_at: at(3) },
      { cli: "codex", state: "missing", version: "", detail: "", checked_at: at(3) },
      { cli: "cursor", state: "unknown", version: "2026.01.28-abc1234", detail: "status check timed out", checked_at: at(2) },
    ];
    renderWithProviders(<SessionBadges sessions={sessions} />);
    const group = screen.getByLabelText("CLI sessions");
    expect(group).toHaveTextContent("Claude Code: logged in v2.1.0");
    expect(group).toHaveTextContent("Codex: not installed");
    expect(group).toHaveTextContent("Cursor: unknown v2026.01.28-abc1234");
    expect(screen.getByTitle("Cursor: unknown · v2026.01.28-abc1234 · status check timed out")).toBeInTheDocument();
    expect(screen.getByTitle("Claude Code: logged in · v2.1.0 · claude.ai, pro")).toBeInTheDocument();
    expect(group).toHaveTextContent(/checked 2m ago/);
  });

  it("marks a logged-out CLI", () => {
    renderWithProviders(
      <SessionBadges sessions={[{ cli: "codex", state: "logged_out", version: "0.50.0", detail: "", checked_at: at(1) }]} />,
    );
    expect(screen.getByText(/Codex: logged out/)).toBeInTheDocument();
  });

  it("says so when an older agent reports nothing", () => {
    renderWithProviders(<SessionBadges sessions={[]} />);
    expect(screen.getByText(/not reported/)).toBeInTheDocument();
    renderWithProviders(<SessionBadges sessions={undefined} />);
    expect(screen.getAllByText(/not reported/)).toHaveLength(2);
  });
});

describe("sessionsCheckedAt", () => {
  it("takes the latest check", () => {
    expect(sessionsCheckedAt([])).toBeNull();
    expect(
      sessionsCheckedAt([
        { cli: "claude", state: "logged_in", version: "", detail: "", checked_at: "2026-10-14T21:00:00Z" },
        { cli: "cursor", state: "unknown", version: "", detail: "", checked_at: "2026-10-14T21:05:00Z" },
        { cli: "codex", state: "missing", version: "", detail: "", checked_at: null },
      ]),
    ).toBe("2026-10-14T21:05:00Z");
  });
});
