import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { AdminUser, AdminView } from "@/api/types";
import { AdminPage } from "@/features/admin/AdminPage";
import { ImpersonationBar } from "@/features/shell/AppShell";
import { jsonResponse, makeUser, renderWithProviders, testClient } from "@/test/render";

const AT = "2026-11-10T09:00:00Z";
const account = (over: Partial<AdminUser> = {}): AdminUser => ({
  id: 2, username: "gus", email: "gus@example.com", display_name: "Gus", role: "member", totp_enabled: false, password_generated: true,
  created_at: AT, last_seen_at: null, sessions: 0, projects: ["SHOP"], agent: null, ...over,
});
const owner = account({ id: 1, username: "ada", email: "ada@example.com", display_name: "Ada", role: "owner", password_generated: false, projects: [], last_seen_at: AT });
const base = (): AdminView => ({
  users: [owner, account()],
  machines: [{ id: 1, name: "desk", role: "master", online: true, members_allowed: false, permission_modes: ["plan", "acceptEdits"] }],
  projects: [
    { key: "SHOP", name: "Shop", color: "#3987e5" },
    { key: "GAME", name: "Game", color: "#e5a039" },
  ],
});

let assigned: string[] = [];
beforeEach(() => {
  assigned = [];
  vi.stubGlobal("location", { ...window.location, assign: (url: string) => assigned.push(url) });
});
afterEach(() => vi.unstubAllGlobals());

describe("admin", () => {
  it("lists the accounts with what each may do, and creates one — its password shown once", async () => {
    let view = base();
    const posted: Record<string, unknown>[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit = {}) => {
        const path = url.split("?")[0].replace(/^\/api/, "");
        const method = init.method ?? "GET";
        if (path === "/admin/users" && method === "GET") return jsonResponse(200, view);
        if (path === "/admin/users" && method === "POST") {
          posted.push(JSON.parse(String(init.body)));
          const user = account({ id: 3, username: "zed", email: "zed@example.com", display_name: "Zed", projects: ["GAME"] });
          view = { ...view, users: [...view.users, user] };
          return jsonResponse(201, { user, password: "aB3-made-up-once" });
        }
        return jsonResponse(404, { error: { code: "not_found", message: path } });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<AdminPage />);
    const list = await screen.findByRole("list", { name: "Accounts" });
    const me = within(list).getByRole("listitem", { name: "ada" });
    expect(within(me).getByText("Owner")).toBeInTheDocument();
    expect(within(me).getByText("You")).toBeInTheDocument();
    // Nothing to do to yourself from here but edit your name.
    expect(within(me).queryByRole("button", { name: /Act as/ })).toBeNull();
    const gus = within(list).getByRole("listitem", { name: "gus" });
    expect(within(gus).getByText("Member")).toBeInTheDocument();
    expect(within(gus).getByText("SHOP")).toBeInTheDocument();
    expect(within(gus).getByText("No agent of its own")).toBeInTheDocument();
    expect(within(gus).getByText("Password not changed")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "New account" }));
    const dialog = await screen.findByRole("dialog", { name: "New account" });
    await user.type(within(dialog).getByLabelText("Username"), "zed");
    await user.type(within(dialog).getByLabelText("E-mail"), "zed@example.com");
    await user.click(within(dialog).getByRole("checkbox", { name: /GAME/ }));
    await user.click(within(dialog).getByRole("button", { name: "Create account" }));
    await waitFor(() => expect(posted).toEqual([{ username: "zed", email: "zed@example.com", role: "member", projects: ["GAME"] }]));
    const shown = await screen.findByRole("dialog", { name: "Account zed created" });
    expect(within(shown).getByLabelText("Password")).toHaveTextContent("aB3-made-up-once");
  });

  it("sets a member's projects and its own agent, and acts as it", async () => {
    let view = base();
    const patched: Record<string, unknown>[] = [];
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit = {}) => {
        const path = url.split("?")[0].replace(/^\/api/, "");
        const method = init.method ?? "GET";
        calls.push(`${method} ${path}`);
        if (path === "/admin/users" && method === "GET") return jsonResponse(200, view);
        if (path === "/admin/users/2" && method === "PATCH") {
          patched.push(JSON.parse(String(init.body)));
          const updated = account({ projects: ["SHOP", "GAME"], agent: { runner_id: 1, runner_name: "desk", online: true, model: "opus", effort: "high", permission_mode: "acceptEdits" } });
          view = { ...view, users: [owner, updated] };
          return jsonResponse(200, updated);
        }
        if (path === "/admin/impersonate" && method === "POST") return jsonResponse(200, { user: makeUser({ id: 2, username: "gus" }) });
        return jsonResponse(404, { error: { code: "not_found", message: path } });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<AdminPage />);
    const gus = within(await screen.findByRole("list", { name: "Accounts" })).getByRole("listitem", { name: "gus" });
    await user.click(within(gus).getByRole("button", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog", { name: "gus" });
    await user.click(within(dialog).getByRole("checkbox", { name: /GAME/ }));
    await user.click(within(dialog).getByRole("switch", { name: "Give this member one agent" }));
    // The master, opus and high are what it starts with; skipping permissions is not on offer.
    expect(within(dialog).getByLabelText("Machine")).toHaveValue("desk");
    expect(within(dialog).getByLabelText("Model")).toHaveValue("opus");
    expect(within(dialog).getByLabelText("Effort")).toHaveValue("high");
    expect(within(dialog).queryByRole("option", { name: "bypassPermissions" })).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(patched).toEqual([{ projects: ["SHOP", "GAME"], agent: { machine: "desk", model: "opus", effort: "high", permission_mode: "acceptEdits" } }]),
    );
    expect(await within(gus).findByText(/Agent: Claude Code · opus · High effort · on desk · acceptEdits/)).toBeInTheDocument();
    // The machines panel says whose agent lives where.
    expect(within(screen.getByRole("list", { name: "Machines" })).getByText(/Agent of gus/)).toBeInTheDocument();

    await user.click(within(gus).getByRole("button", { name: /Act as/ }));
    await waitFor(() => expect(calls).toContain("POST /admin/impersonate"));
    await waitFor(() => expect(assigned).toEqual(["/"]));
  });

  it("while acting as an account, says so everywhere and offers the way back", async () => {
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit = {}) => {
        calls.push(`${init.method ?? "GET"} ${url.split("?")[0].replace(/^\/api/, "")}`);
        return jsonResponse(204);
      }),
    );
    const plain = renderWithProviders(<ImpersonationBar />);
    expect(screen.queryByRole("status")).toBeNull();
    plain.unmount();
    const qc = testClient({
      user: makeUser({ id: 2, username: "gus", display_name: "Gus", role: "member" }),
      elevated_until: null,
      impersonator: { id: 1, username: "ada", display_name: "Ada" },
    });
    renderWithProviders(<ImpersonationBar />, { qc });
    const bar = screen.getByRole("status");
    expect(bar).toHaveTextContent("Acting as Gus · member");
    await userEvent.click(within(bar).getByRole("button", { name: "Stop" }));
    await waitFor(() => expect(calls).toEqual(["DELETE /admin/impersonate"]));
    await waitFor(() => expect(assigned).toEqual(["/admin"]));
  });
});
