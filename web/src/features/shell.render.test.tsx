import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AppShell } from "@/features/shell/AppShell";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

describe("AppShell sidebar", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /projects": () => jsonResponse(200, []),
        "GET /runs": () => jsonResponse(200, []),
      }),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it("opens the account menu upwards with the user, settings, theme and sign out", async () => {
    renderWithProviders(<AppShell />);
    const sidebar = screen.getByRole("complementary");
    await userEvent.click(within(sidebar).getByRole("button", { name: "Account" }));
    const menu = screen.getByRole("menu", { name: "Account" });
    expect(menu).toHaveClass("bottom-full");
    expect(within(menu).getByText("ada@example.com")).toBeInTheDocument();
    expect(within(menu).getByRole("menuitem", { name: "Settings" })).toBeInTheDocument();
    expect(within(menu).getByRole("menuitemcheckbox", { name: "Match system" })).toHaveAttribute("aria-checked", "true");
    expect(within(menu).getByRole("menuitem", { name: "Sign out" })).toBeInTheDocument();
  });

  it("collapses to an icon rail with the button or [, and remembers it", async () => {
    const { unmount } = renderWithProviders(<AppShell />);
    const sidebar = screen.getByRole("complementary");
    expect(sidebar).toHaveClass("w-60");
    await userEvent.click(within(sidebar).getByRole("button", { name: "Collapse sidebar" }));
    expect(sidebar).toHaveClass("w-14");
    expect(within(sidebar).queryByText("Projects")).not.toBeInTheDocument();
    expect(localStorage.getItem("forge.sidebar")).toBe("collapsed");
    unmount();

    renderWithProviders(<AppShell />);
    const again = screen.getByRole("complementary");
    expect(again).toHaveClass("w-14");
    await userEvent.keyboard("[[");
    expect(again).toHaveClass("w-60");
  });
});
