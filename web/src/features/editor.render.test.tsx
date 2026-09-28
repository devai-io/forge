import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import { EditorPage } from "@/features/editor/EditorPage";
import { EditorToolbar } from "@/features/editor/EditorToolbar";
import { storeOpen } from "@/lib/code";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  sessionStorage.clear();
});

describe("EditorToolbar", () => {
  it("labels the editor and wires its buttons", async () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    const onReload = vi.fn();
    const onClose = vi.fn();
    render(<EditorToolbar label="Shop" detail="shop_api, shop_ui" url="/code/?workspace=w" onReload={onReload} onClose={onClose} />);
    expect(screen.getByText("Shop")).toBeInTheDocument();
    expect(screen.getByText("shop_api, shop_ui")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /Open in new tab/ }));
    expect(open).toHaveBeenCalledWith("/code/?workspace=w", "_blank", "noopener");
    await user.click(screen.getByRole("button", { name: /Reload/ }));
    await user.click(screen.getByRole("button", { name: /Close/ }));
    expect(onReload).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("can't open a new tab before there is a URL", () => {
    render(<EditorToolbar label="x" url={null} onReload={() => {}} onClose={() => {}} />);
    expect(screen.getByRole("button", { name: /Open in new tab/ })).toBeDisabled();
  });
});

describe("EditorPage", () => {
  const status = { available: true, runner_name: "desk", reason: "" };
  const routes = (
    <Routes>
      <Route path="/editor" element={<EditorPage />} />
    </Routes>
  );

  it("opens a project with one POST and frames the returned /code/ URL", async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /code/status": () => jsonResponse(200, status),
        "GET /projects": () => jsonResponse(200, { projects: [{ key: "SHOP", name: "Shop" }] }),
        "POST /code/open": (init) => {
          bodies.push(JSON.parse(String(init.body)));
          return jsonResponse(200, { url: "/code/?workspace=shop", workspace: "shop", expires_at: "2099-01-01T00:00:00Z" });
        },
      }),
    );
    renderWithProviders(routes, { route: "/editor?project=SHOP" });
    const frame = await screen.findByTitle(/VS Code — /);
    // The resolved Forge theme rides along so VS Code's first paint matches (jsdom: light).
    expect(frame.getAttribute("src")).toBe("/code/?workspace=shop&forge_theme=light&forge_accent=2a78d6");
    expect(frame.getAttribute("allow")).toBe("clipboard-read; clipboard-write");
    expect(frame.hasAttribute("sandbox")).toBe(false);
    expect(bodies).toEqual([{ project_key: "SHOP" }]);
  });

  it("reuses this tab's open, and offers to reopen when the cookie has expired", async () => {
    storeOpen({ kind: "project", projectKey: "SHOP", repoIds: [] }, { url: "/code/?workspace=old", workspace: "old", expires_at: "2099-01-01T00:00:00Z" }, "Shop");
    let opens = 0;
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /code/status": () => jsonResponse(200, status),
        "GET /projects": () => jsonResponse(200, { projects: [] }),
        "GET /code/": () => jsonResponse(401, { error: { code: "unauthorized", message: "editor session expired" } }),
        "POST /code/open": () => {
          opens++;
          return jsonResponse(200, { url: "/code/?workspace=new", workspace: "new", expires_at: "2099-01-01T00:00:00Z" });
        },
      }),
    );
    renderWithProviders(routes, { route: "/editor?project=SHOP" });
    await screen.findByText("Session expired — reopen");
    expect(opens).toBe(0); // the remembered URL was tried first, no POST
    await userEvent.setup().click(screen.getByRole("button", { name: "Reopen" }));
    const frame = await screen.findByTitle(/VS Code — /);
    await waitFor(() => expect(frame.getAttribute("src")).toBe("/code/?workspace=new&forge_theme=light&forge_accent=2a78d6"));
    expect(opens).toBe(1);
  });

  it("shows the reason instead of opening when VS Code is unavailable", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /code/status": () => jsonResponse(200, { available: false, runner_name: null, reason: "The master (desk) is offline." }),
        "GET /projects": () => jsonResponse(200, { projects: [] }),
      }),
    );
    renderWithProviders(routes, { route: "/editor?folder=/home/ada/dev/forge" });
    expect(await screen.findByText("The master (desk) is offline.")).toBeInTheDocument();
    expect(screen.queryByTitle(/VS Code — /)).not.toBeInTheDocument();
  });
});
