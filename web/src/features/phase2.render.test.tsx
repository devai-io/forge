import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Project, ProjectDetail, Runner, VaultItem } from "@/api/types";
import { useLocation } from "react-router-dom";
import { NewRunDialog } from "@/features/agents/NewRunDialog";
import { LoginPage } from "@/features/auth/AuthPages";
import { ShellProvider } from "@/features/shell/context";
import { NewTaskDialog } from "@/features/tasks/NewTaskDialog";
import { VaultItemSheet } from "@/features/vault/VaultItemSheet";
import { jsonResponse, makeUser, renderWithProviders, routeFetch, testClient } from "@/test/render";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("LoginPage two-factor step", () => {
  it("reveals a code field on totp_required and resubmits with the code", async () => {
    const bodies: unknown[] = [];
    let attempt = 0;
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /auth/me": () => jsonResponse(401, { error: { code: "unauthorized", message: "no session" } }),
        "POST /auth/login": (init) => {
          bodies.push(JSON.parse(String(init.body)));
          attempt++;
          return attempt === 1
            ? jsonResponse(401, { error: { code: "totp_required", message: "code required" } })
            : jsonResponse(200, { user: makeUser({ totp_enabled: true }), elevated_until: null });
        },
      }),
    );
    renderWithProviders(<LoginPage />, { qc: testClient(null), route: "/login" });
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Username or e-mail"), "ada");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    const code = await screen.findByLabelText("Authenticator code");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument(); // not shown as a failure
    const submit = screen.getByRole("button", { name: "Verify and sign in" });
    expect(submit).toBeDisabled();
    await user.type(code, "12a3456");
    expect(code).toHaveValue("123456");
    await user.click(submit);
    await waitFor(() => expect(bodies).toHaveLength(2));
    expect(bodies[0]).toEqual({ username: "ada", password: "correct horse battery" });
    expect(bodies[1]).toEqual({ username: "ada", password: "correct horse battery", code: "123456" });
  });
});

const vaultItem: VaultItem = {
  id: 7,
  name: "Play upload keystore",
  kind: "keystore",
  project_key: "GAME",
  project_color: "#eda100",
  platform: "android",
  host: "desk",
  identifier: "upload",
  fields: { alias: "upload" },
  secret_keys: ["store_password", "key_password"],
  has_file: true,
  file_name: "upload.jks",
  file_size: 2765,
  location: "~/keys/upload.jks",
  expires_at: null,
  notes: "",
  tags: [],
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-02T00:00:00Z",
  last_revealed_at: null,
};

describe("Vault reveal", () => {
  it("reveals masked, shows on demand, keeps values out of the query cache, and hides after 60 s", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /vault/7": () => jsonResponse(200, vaultItem),
        "POST /vault/7/reveal": () => jsonResponse(200, { secret: { store_password: "s3cret-store", key_password: "s3cret-key" } }),
      }),
    );
    const { qc } = renderWithProviders(<VaultItemSheet id={7} onClose={() => {}} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    await user.click(await screen.findByRole("button", { name: "Reveal" }));
    await screen.findByText(/hides in 60s/);
    expect(screen.queryByText("s3cret-store")).not.toBeInTheDocument(); // masked until asked

    await user.click(screen.getByRole("button", { name: "Show store_password" }));
    expect(screen.getByText("s3cret-store")).toBeInTheDocument();
    expect(screen.queryByText("s3cret-key")).not.toBeInTheDocument();

    const cached = JSON.stringify([
      ...qc.getQueryCache().getAll().map((q) => q.state.data),
      ...qc.getMutationCache().getAll().map((m) => m.state.data),
    ]);
    expect(cached).not.toContain("s3cret");

    await act(async () => {
      vi.advanceTimersByTime(61_000);
    });
    expect(screen.queryByText("s3cret-store")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reveal" })).toBeInTheDocument();
  });
});

describe("NewRunDialog confirm-commands", () => {
  const runner: Runner = {
    id: 1,
    name: "desktop",
    hostname: "infra",
    os: "linux",
    version: "0.2.0",
    online: true,
    last_seen_at: "2026-09-27T10:00:00Z",
    running: 0,
    created_at: "2026-09-01T00:00:00Z",
    role: "master",
    pair_expires_at: null,
    capabilities: {
      claude: true,
      permission_modes: ["plan"],
      commands: ["git-pull", "publish-android", "deploy-ui"],
      command_details: [
        { name: "git-pull", description: "Fast-forward the default branch", repos: [], confirm: false },
        { name: "publish-android", description: "Upload the AAB to Play", repos: ["shop_mobile"], confirm: true },
        { name: "deploy-ui", description: "Push to prod", repos: ["shop_ui"], confirm: true },
      ],
      max_concurrent: 2,
      ci: true,
      terminal: false,
      code: false,
    },
  };
  const project = { id: 2, key: "SHOP", name: "Shop", color: "#3987e5" } as Project;
  const detail = {
    ...project,
    repos: [{ id: 11, project_id: 2, name: "shop_mobile", path: "/home/ada/dev/shop/shop_mobile", git: null }],
    servers: [],
    endpoints: [],
  } as unknown as ProjectDetail;

  it("offers only this repo's commands and needs the name typed back before queueing", async () => {
    const posted: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "POST /runs": (init) => {
          posted.push(JSON.parse(String(init.body)));
          return jsonResponse(201, { id: 99, runner_name: "desktop" });
        },
      }),
    );
    const qc = testClient();
    qc.setQueryData(["runners"], [runner]);
    qc.setQueryData(["projects", { includeArchived: false }], [project]);
    qc.setQueryData(["project", "SHOP"], detail);
    qc.setQueryData(["tasks", { project: "SHOP", open: true }], []);
    renderWithProviders(<NewRunDialog prefill={{ project_key: "SHOP" }} onClose={() => {}} />, { qc });
    const user = userEvent.setup();

    await user.click(screen.getByRole("radio", { name: "Command" }));
    const select = screen.getByLabelText("Command");
    const options = Array.from((select as HTMLSelectElement).options).map((o) => o.value);
    expect(options).toEqual(["git-pull", "publish-android"]); // deploy-ui is scoped to shop_ui

    await user.selectOptions(select, "publish-android");
    expect(screen.getByText("Upload the AAB to Play")).toBeInTheDocument();
    const queue = screen.getByRole("button", { name: "Queue run" });
    expect(queue).toBeDisabled();
    await user.type(screen.getByLabelText("Type publish-android to confirm"), "publish-androi");
    expect(queue).toBeDisabled();
    await user.type(screen.getByLabelText("Type publish-android to confirm"), "d");
    expect(queue).toBeEnabled();
    await user.click(queue);
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0]).toMatchObject({ kind: "command", command: "publish-android", repo_id: 11, runner_id: 1, confirmed: true });
  });

  it("offers each engine's models and the effort levels for an agent run", async () => {
    const posted: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "POST /runs": (init) => {
          posted.push(JSON.parse(String(init.body)));
          return jsonResponse(201, { id: 99, runner_name: "desktop" });
        },
      }),
    );
    const qc = testClient();
    qc.setQueryData(["runners"], [runner]);
    qc.setQueryData(["projects", { includeArchived: false }], [project]);
    qc.setQueryData(["project", "SHOP"], detail);
    qc.setQueryData(["tasks", { project: "SHOP", open: true }], []);
    qc.setQueryData(["engine"], {
      settings: { default: "deepseek", model: "deepseek-flash", heavy_model: "deepseek-v4-pro" },
      deepseek_key: true,
      models: { deepseek: ["deepseek-flash", "deepseek-v4-pro"], claude: ["claude-opus-5-5", "claude-sonnet-5-5"] },
      efforts: ["low", "medium", "high", "xhigh", "max"],
    });
    renderWithProviders(<NewRunDialog prefill={{ project_key: "SHOP" }} onClose={() => {}} />, { qc });
    const user = userEvent.setup();
    const values = (label: string) => Array.from((screen.getByLabelText(label) as HTMLSelectElement).options).map((o) => o.value);

    expect(values("Model")).toEqual(["", "deepseek-flash", "deepseek-v4-pro", "__custom__"]);
    await user.selectOptions(screen.getByLabelText("Engine"), "claude");
    expect(values("Model")).toEqual(["", "claude-opus-5-5", "claude-sonnet-5-5", "__custom__"]);
    expect(values("Effort")).toEqual(["", "low", "medium", "high", "xhigh", "max"]);
    await user.selectOptions(screen.getByLabelText("Model"), "claude-opus-5-5");
    await user.selectOptions(screen.getByLabelText("Effort"), "xhigh");
    await user.type(screen.getByLabelText("Prompt"), "Fix the flaky test");
    await user.click(screen.getByRole("button", { name: "Queue run" }));
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0]).toMatchObject({ kind: "agent", engine: "claude", model: "claude-opus-5-5", effort: "xhigh" });

    // "Other…" takes a typed id.
    await user.selectOptions(screen.getByLabelText("Model"), "__custom__");
    await user.type(screen.getByLabelText("Model id"), "claude-opus-4-1");
    expect(screen.getByLabelText("Model id")).toHaveValue("claude-opus-4-1");
  });
});

describe("NewTaskDialog", () => {
  it("opens the task it created", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch({ "POST /tasks": () => jsonResponse(201, { id: 42, ref: "SHOP-7", title: "Pause menu" }) }),
    );
    const qc = testClient();
    const project = { id: 2, key: "SHOP", name: "Shop", color: "#3987e5" } as Project;
    qc.setQueryData(["projects", { includeArchived: false }], [project]);
    qc.setQueryData(["project", "SHOP"], { ...project, repos: [], servers: [], endpoints: [] });
    let search = "";
    function Where() {
      search = useLocation().search;
      return null;
    }
    renderWithProviders(
      <ShellProvider>
        <Where />
        <NewTaskDialog prefill={{ project_key: "SHOP" }} onClose={() => {}} />
      </ShellProvider>,
      { qc, route: "/p/SHOP" },
    );
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Title"), "Pause menu");
    await user.click(screen.getByRole("button", { name: "Create task" }));
    await waitFor(() => expect(search).toBe("?task=42"));
  });
});

describe("Two-factor setup", () => {
  it("shows the QR code and key, then enables with a code", async () => {
    const { SettingsPage } = await import("@/features/settings/SettingsPage");
    const enableBodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /auth/sessions": () => jsonResponse(200, { sessions: [] }),
        "POST /auth/totp/setup": () =>
          jsonResponse(200, { secret: "JBSWY3DPEHPK3PXP", otpauth_url: "otpauth://totp/Forge:ada?secret=JBSWY3DPEHPK3PXP&issuer=Forge" }),
        "POST /auth/totp/enable": (init) => {
          enableBodies.push(JSON.parse(String(init.body)));
          return new Response(null, { status: 204 });
        },
        "GET /auth/me": () => jsonResponse(200, { user: makeUser({ totp_enabled: true }), elevated_until: null }),
      }),
    );
    renderWithProviders(<SettingsPage />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Set up" }));
    const qr = await screen.findByAltText("QR code for your authenticator app");
    expect(qr.getAttribute("src")).toMatch(/^data:image\/svg\+xml/);
    expect(screen.getByText("JBSW Y3DP EHPK 3PXP")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Code"), "654321");
    await user.click(screen.getByRole("button", { name: "Turn on" }));
    await waitFor(() => expect(enableBodies).toEqual([{ code: "654321" }]));
  });
});
