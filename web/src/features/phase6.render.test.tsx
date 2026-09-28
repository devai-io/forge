import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes, useLocation } from "react-router-dom";
import type { MeResponse, Runner } from "@/api/types";
import { PairingDialog } from "@/features/agents/PairingDialog";
import { LoginPage } from "@/features/auth/AuthPages";
import { SetupPage } from "@/features/auth/SetupPage";
import { ProjectFiles } from "@/features/projects/ProjectFiles";
import { AccentPicker } from "@/features/settings/AccentPicker";
import { applyAccent } from "@/lib/accent";
import { jsonResponse, makeUser, renderWithProviders, routeFetch, testClient } from "@/test/render";

afterEach(() => {
  vi.unstubAllGlobals();
  applyAccent("");
  localStorage.clear();
});

function Where() {
  const loc = useLocation();
  return <p>at {loc.pathname + loc.search}</p>;
}

const me: MeResponse = { user: makeUser(), elevated_until: null };

describe("first-run setup", () => {
  it("sends /login to /setup while the server has no account, keeping ?token", async () => {
    vi.stubGlobal("fetch", routeFetch({ "GET /setup": () => jsonResponse(200, { needed: true }) }));
    renderWithProviders(
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/setup" element={<Where />} />
      </Routes>,
      { qc: testClient(null), route: "/login?token=tok-1" },
    );
    expect(await screen.findByText("at /setup?token=tok-1")).toBeInTheDocument();
  });

  it("creates the account with the token from the link, signs in and lands on the dashboard", async () => {
    let body: Record<string, unknown> | null = null;
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /setup": () => jsonResponse(200, { needed: true }),
        "POST /setup": (init) => {
          body = JSON.parse(String(init.body));
          return jsonResponse(201, me);
        },
      }),
    );
    const { qc } = renderWithProviders(
      <Routes>
        <Route path="/setup" element={<SetupPage />} />
        <Route path="/" element={<Where />} />
      </Routes>,
      { qc: testClient(null), route: "/setup?token=tok-1" },
    );
    await screen.findByRole("heading", { name: "Welcome to Forge" });
    // The link carried the token: no field for it.
    expect(screen.queryByLabelText("Setup token")).not.toBeInTheDocument();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Username"), "ada");
    await user.type(screen.getByLabelText("E-mail"), "ada@example.com");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery");
    await user.click(screen.getByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByText("at /")).toBeInTheDocument();
    expect(body).toMatchObject({ token: "tok-1", username: "ada", email: "ada@example.com", demo_data: true });
    expect(qc.getQueryData(["me"])).toEqual(me);
  });

  it("asks for the token when the API turns it down", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /setup": () => jsonResponse(200, { needed: true }),
        "POST /setup": () => jsonResponse(403, { error: { code: "bad_setup_token", message: "bad token" } }),
      }),
    );
    renderWithProviders(<SetupPage />, { qc: testClient(null), route: "/setup?token=stale" });
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Username"), "ada");
    await user.type(screen.getByLabelText("E-mail"), "ada@example.com");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByLabelText("Setup token")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText(/not the one this server printed/)).toBeInTheDocument();
  });
});

describe("PairingDialog", () => {
  it("shows the code and commands, then turns into 'connected' when the machine checks in", async () => {
    const expires = new Date(Date.now() + 14 * 60_000).toISOString();
    const runner = {
      id: 7,
      name: "desk",
      hostname: "",
      os: "",
      version: "",
      online: false,
      last_seen_at: null,
      running: 0,
      created_at: new Date().toISOString(),
      role: "master",
      pair_expires_at: expires,
      capabilities: { claude: false, permission_modes: [], commands: [], max_concurrent: 0, command_details: [], ci: false, terminal: false, code: false },
    } as Runner;
    let polls = 0;
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /runners": () => {
          polls++;
          // First poll: still waiting. Then the machine has paired and heartbeated.
          const now = new Date().toISOString();
          return jsonResponse(200, {
            runners: [polls === 1 ? runner : { ...runner, pair_expires_at: null, last_seen_at: now, online: true, hostname: "desk.example.com" }],
          });
        },
      }),
    );
    renderWithProviders(<PairingDialog runner={runner} pairing={{ code: "K7QD-M3XP", expires_at: expires }} onClose={() => {}} />);
    expect(screen.getByText("K7QD-M3XP")).toBeInTheDocument();
    expect(screen.getByText(/expires in 1[34]:\d\d/)).toBeInTheDocument();
    expect(screen.getByText(/install\.sh \| sh -s -- --pair http:\/\/localhost(:\d+)? K7QD-M3XP/)).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Waiting for desk to pair");
    await userEvent.click(screen.getByRole("tab", { name: "Forge already installed" }));
    expect(screen.getByText(/forge agent pair http:\/\/localhost(:\d+)? K7QD-M3XP/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("desk is connected"), { timeout: 5000 });
    expect(screen.getByRole("button", { name: "Done" })).toBeInTheDocument();
  }, 10_000);
});

describe("AccentPicker", () => {
  it("previews and saves a preset, and marks it pressed", async () => {
    let patched: unknown = null;
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "PATCH /auth/me": (init) => {
          patched = JSON.parse(String(init.body));
          return jsonResponse(200, { user: makeUser({ accent: "teal" }) });
        },
      }),
    );
    renderWithProviders(<AccentPicker />);
    expect(screen.getByRole("button", { name: "Indigo (default)" })).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(screen.getByRole("button", { name: "Teal" }));
    expect(document.documentElement.getAttribute("data-accent")).toBe("teal");
    await waitFor(() => expect(patched).toEqual({ accent: "teal" }));
    expect(screen.getByRole("button", { name: "Teal" })).toHaveAttribute("aria-pressed", "true");
  });
});

describe("ProjectFiles", () => {
  it("shows the empty state, uploads a picked file and lists it", async () => {
    let files: { name: string; size: number; modified_at: string }[] = [];
    let uploaded: FormData | null = null;
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /projects/SHOP/files": () => jsonResponse(200, { files }),
        "POST /projects/SHOP/files": (init) => {
          uploaded = init.body as FormData;
          const f = { name: "spec.pdf", size: 4300, modified_at: new Date().toISOString() };
          files = [f];
          return jsonResponse(201, { file: f });
        },
      }),
    );
    const { container } = renderWithProviders(<ProjectFiles projectKey="SHOP" />);
    expect(await screen.findByText(/specs, exports, screenshots/)).toBeInTheDocument();
    const input = container.querySelector<HTMLInputElement>('input[type="file"]')!;
    await userEvent.upload(input, new File(["%PDF"], "spec.pdf", { type: "application/pdf" }));
    const link = await screen.findByRole("link", { name: "spec.pdf" });
    expect(link).toHaveAttribute("href", "/api/projects/SHOP/files/spec.pdf");
    expect(screen.getByText("4.2 KB")).toBeInTheDocument();
    expect((uploaded as FormData | null)?.get("file")).toBeInstanceOf(File);
  });
});
