import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Memory, TelegramStatus } from "@/api/types";
import { MemoryPanel } from "@/features/memory/MemoryPanel";
import { TelegramPanel } from "@/features/settings/TelegramPanel";
import { jsonResponse, makeUser, renderWithProviders, routeFetch, testClient } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

const memory = (over: Partial<Memory> = {}): Memory => ({
  id: 7,
  project_key: "SHOP",
  repo_id: null,
  repo_name: null,
  kind: "gotcha",
  content: "Checkout deploys need the payments flag flipped first",
  source: "claude",
  origin: "Claude on desk",
  pinned: false,
  recalls: 2,
  last_recalled_at: null,
  created_at: "2026-10-01T10:00:00.000Z",
  updated_at: "2026-10-01T10:00:00.000Z",
  ...over,
});

describe("Memory panel", () => {
  it("lists, adds, pins and forgets", async () => {
    const sent: { method: string; body: unknown }[] = [];
    let list = [memory()];
    const record = (method: string) => (init: RequestInit) => {
      sent.push({ method, body: init.body ? JSON.parse(String(init.body)) : null });
      return jsonResponse(200, method === "POST" ? { memory: memory({ id: 8 }), duplicate: false, similar: [] } : memory());
    };
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /memories": () => jsonResponse(200, { memories: list, counts: { SHOP: list.length } }),
        "POST /memories": record("POST"),
        "PATCH /memories/7": record("PATCH"),
        "DELETE /memories/7": () => {
          list = [];
          sent.push({ method: "DELETE", body: null });
          return jsonResponse(204);
        },
      }),
    );
    renderWithProviders(<MemoryPanel project="SHOP" title="Memory" />, { qc: testClient({ user: makeUser(), elevated_until: null }) });
    const user = userEvent.setup();

    expect(await screen.findByText(/Checkout deploys need/)).toBeInTheDocument();
    expect(screen.getByText(/by Claude on desk/)).toBeInTheDocument();
    expect(screen.getByText(/recalled 2×/)).toBeInTheDocument();

    await user.type(screen.getByLabelText("New memory"), "Use pnpm, never npm");
    await user.selectOptions(screen.getAllByLabelText("Kind")[0], "convention");
    await user.click(screen.getByRole("button", { name: "Remember" }));
    await user.click(screen.getByRole("button", { name: "Pin" }));
    await user.click(screen.getByRole("button", { name: "Forget" }));
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: /delete|forget|confirm/i }));
    await waitFor(() =>
      expect(sent).toEqual([
        { method: "POST", body: { project_key: "SHOP", kind: "convention", content: "Use pnpm, never npm", pinned: false } },
        { method: "PATCH", body: { pinned: true } },
        { method: "DELETE", body: null },
      ]),
    );
    expect(await screen.findByText("Nothing remembered yet")).toBeInTheDocument();
  });
});

const tgStatus = (over: Partial<TelegramStatus> = {}): TelegramStatus => ({
  settings: { enabled: false, chat_id: 0, prompts: true, runs: true, checkup: true, assistant: true },
  token_configured: false,
  bot: "",
  last_error: "",
  last_error_at: null,
  last_poll_at: null,
  ...over,
});

describe("Settings → Telegram", () => {
  it("stores the token, links the chat and turns it on", async () => {
    const sent: unknown[] = [];
    let current = tgStatus();
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /telegram": () => jsonResponse(200, current),
        "PUT /telegram/token": (init) => {
          sent.push(JSON.parse(String(init.body)));
          current = tgStatus({ token_configured: true });
          return jsonResponse(200, current);
        },
        "PATCH /telegram": (init) => {
          const patch = JSON.parse(String(init.body));
          sent.push(patch);
          current = { ...current, settings: { ...current.settings, ...patch } };
          return jsonResponse(200, current);
        },
      }),
    );
    renderWithProviders(<TelegramPanel />, { qc: testClient({ user: makeUser(), elevated_until: null }) });
    const user = userEvent.setup();
    expect(await screen.findByText("no token yet")).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Use Telegram" })).toBeDisabled();

    await user.type(screen.getByLabelText("Bot token"), "123456789:AAnot-a-real-token");
    await user.click(screen.getByRole("button", { name: "Save token" }));
    expect(await screen.findByText("token in the vault")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Chat id"), "4242");
    await user.click(screen.getByRole("button", { name: "Link chat" }));
    expect(await screen.findByText("chat linked")).toBeInTheDocument();
    await user.click(screen.getByRole("switch", { name: "Use Telegram" }));
    await waitFor(() =>
      expect(sent).toEqual([{ token: "123456789:AAnot-a-real-token" }, { chat_id: 4242 }, { enabled: true }]),
    );
  });
});
