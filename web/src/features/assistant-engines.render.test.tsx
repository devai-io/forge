import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import type { AssistantStatus, Chat, ChatMessage, ChatTurn, Run } from "@/api/types";
import { AssistantPage } from "@/features/assistant/AssistantPage";
import { ShellProvider } from "@/features/shell/context";
import { turnSpend } from "@/lib/assistant";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

const AT = "2026-09-29T10:00:00Z";

const status: AssistantStatus = {
  settings: { enabled: true, engine: "deepseek", base_url: "https://llm.example.com", model: "demo-flash" },
  key_configured: true,
  models: ["demo-flash", "demo-pro"],
  engines: {
    deepseek: { available: true, models: ["demo-flash", "demo-pro"], efforts: ["off", "low", "high", "max"] },
    claude: { available: true, machine: "desk", online: true, reason: "", models: ["opus", "sonnet"], efforts: ["low", "medium", "high", "xhigh", "max"] },
  },
};

const chat = (over: Partial<Chat> = {}): Chat => ({
  id: 7,
  title: "SHOP bugs",
  busy: false,
  last_error: "",
  usage: { input_tokens: 0, output_tokens: 0, cached_tokens: 0 },
  created_at: AT,
  updated_at: AT,
  engine: "deepseek",
  model: "",
  effort: "",
  edits: false,
  project_id: null,
  project_key: "",
  summary: "",
  summary_seq: 0,
  ...over,
});

const msg = (over: Partial<ChatMessage>): ChatMessage => ({
  origin: "",
  seq: 1,
  role: "user",
  content: "",
  tool_calls: [],
  tool_call_id: "",
  tool_name: "",
  result: null,
  is_error: false,
  created_at: AT,
  ...over,
});

const run = (over: Partial<Run>): Run =>
  ({
    id: 12,
    kind: "agent",
    engine: "deepseek",
    model: "deepseek-flash",
    repo_name: "shop_api",
    status: "succeeded",
    cost_usd: 0.0031,
    ...over,
  }) as Run;

const turn = (over: Partial<ChatTurn> = {}): ChatTurn => ({
  origin: "",
  id: 3,
  chat_id: 7,
  seq: 1,
  engine: "deepseek",
  model: "demo-flash",
  effort: "high",
  status: "done",
  error: "",
  usage: [
    { api: "deepseek", model: "demo-flash", calls: 2, input_tokens: 1200, cached_tokens: 800, output_tokens: 90, cost_usd: 0.0021 },
    { api: "jev", model: "jev-1", calls: 1, input_tokens: 300, cached_tokens: 0, output_tokens: 0, cost_usd: 0.000126 },
  ],
  run_id: null,
  started_at: AT,
  finished_at: AT,
  runs: [run({})],
  ...over,
});

function renderChat(route: string) {
  return renderWithProviders(
    <ShellProvider>
      <Routes>
        <Route path="/assistant" element={<AssistantPage />} />
        <Route path="/assistant/:chatId" element={<AssistantPage />} />
      </Routes>
    </ShellProvider>,
    { route },
  );
}

describe("turnSpend", () => {
  it("adds up API spend and keeps the Claude subscription apart", () => {
    const s = turnSpend(
      turn({
        usage: [
          ...turn().usage,
          { api: "claude-code", model: "claude-sonnet-5-5", calls: 1, input_tokens: 10, cached_tokens: 100, output_tokens: 5, cost_usd: 0.05 },
        ],
        runs: [run({}), run({ id: 13, engine: "claude", cost_usd: 0.4 }), run({ id: 14, status: "running", cost_usd: null })],
      }),
    );
    expect(s.billed).toBeCloseTo(0.0021 + 0.000126 + 0.0031);
    expect(s.subscription).toBeCloseTo(0.45);
    expect(s.pending).toBe(true);
    expect(s.unpriced).toBe(false);
    expect(s.lines.map((l) => l.label)).toEqual([
      "DeepSeek API · demo-flash",
      "Jev · jev-1",
      "Claude Code · claude-sonnet-5-5",
      "Run #12 · DeepSeek deepseek-flash",
      "Run #13 · Claude deepseek-flash",
      "Run #14 · DeepSeek deepseek-flash",
    ]);
  });
});

describe("Assistant engines", () => {
  it("starts a chat on Claude Code with a model and effort", async () => {
    let created: unknown = null;
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, status),
        "GET /chats": () => jsonResponse(200, { chats: [] }),
        "POST /chats": (init) => {
          created = JSON.parse(String(init.body));
          return jsonResponse(201, { chat: chat({ busy: true, engine: "claude" }), messages: [msg({ content: "hi" })], turns: [] });
        },
        "GET /chats/7": () => jsonResponse(200, { chat: chat({ busy: true, engine: "claude" }), messages: [], turns: [] }),
      }),
    );
    renderChat("/assistant/new");
    const user = userEvent.setup();
    await user.selectOptions(await screen.findByRole("combobox", { name: "Engine" }), "claude");
    expect(screen.getByRole("switch", { name: "Can make changes" })).toBeInTheDocument();
    await user.selectOptions(screen.getByRole("combobox", { name: "Model" }), "sonnet");
    await user.selectOptions(screen.getByRole("combobox", { name: "Effort" }), "xhigh");
    await user.type(screen.getByRole("textbox", { name: "Message" }), "What is open on SHOP?{Enter}");
    await waitFor(() =>
      expect(created).toEqual({ content: "What is open on SHOP?", engine: "claude", model: "sonnet", effort: "xhigh", edits: false, skip_permissions: false }),
    );
  });

  it("shows what each turn spent, per API, and changes the chat's settings", async () => {
    const patches: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, status),
        "GET /chats": () => jsonResponse(200, { chats: [chat()] }),
        "GET /chats/7": () =>
          jsonResponse(200, {
            chat: chat(),
            messages: [msg({ seq: 1, content: "Summarise the README" }), msg({ seq: 2, role: "assistant", content: "Queued." })],
            turns: [turn()],
          }),
        "PATCH /chats/7": (init) => {
          patches.push(JSON.parse(String(init.body)));
          return jsonResponse(200, { chat: chat({ effort: "off" }) });
        },
      }),
    );
    renderChat("/assistant/7");
    const user = userEvent.setup();
    const summary = await screen.findByRole("button", { name: /DeepSeek \(OpenRouter\) · demo-flash · High · \$0\.0053 API/ });
    await user.click(summary);
    expect(screen.getByText("Jev · jev-1")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Run #12 · DeepSeek deepseek-flash" })).toHaveAttribute("href", "/agents/runs/12");
    expect(screen.getByText("≈$0.0031")).toBeInTheDocument();

    await user.selectOptions(screen.getByRole("combobox", { name: "Effort" }), "off");
    await waitFor(() => expect(patches).toEqual([{ effort: "off" }]));
  });

  it("follows to the end of the page, below the composer, so it never covers the last lines", async () => {
    const targets: Element[] = [];
    const orig = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = function (this: Element) {
      targets.push(this);
    };
    try {
      vi.stubGlobal(
        "fetch",
        routeFetch({
          "GET /assistant": () => jsonResponse(200, status),
          "GET /chats": () => jsonResponse(200, { chats: [chat()] }),
          "GET /chats/7": () =>
            jsonResponse(200, {
              chat: chat(),
              messages: [msg({ seq: 1, content: "Summarise the README" }), msg({ seq: 2, role: "assistant", content: "Queued." })],
              turns: [turn()],
            }),
        }),
      );
      renderChat("/assistant/7");
      const composer = (await screen.findByRole("textbox", { name: "Message" })).closest("form")!;
      await waitFor(() => expect(targets.length).toBeGreaterThan(0));
      const end = targets[targets.length - 1];
      expect(composer.compareDocumentPosition(end) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    } finally {
      Element.prototype.scrollIntoView = orig;
    }
  });
});

describe("Assistant chat list and composer guards", () => {
  it("starts a chat from the list's one-line Ask, on the default engine", async () => {
    let created: unknown = null;
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, status),
        "GET /chats": () => jsonResponse(200, { chats: [] }),
        "POST /chats": (init) => {
          created = JSON.parse(String(init.body));
          return jsonResponse(201, { chat: chat({ busy: true }), messages: [msg({ content: "Anything failing?" })], turns: [] });
        },
        "GET /chats/7": () => jsonResponse(200, { chat: chat({ busy: true }), messages: [], turns: [] }),
      }),
    );
    renderChat("/assistant");
    await userEvent.setup().type(await screen.findByRole("textbox", { name: "Ask the assistant" }), "Anything failing?{Enter}");
    await waitFor(() =>
      expect(created).toEqual({ content: "Anything failing?", engine: "deepseek", model: "", effort: "", edits: false, skip_permissions: false }),
    );
  });

  it("filters a long chat list and deletes a chat from its row", async () => {
    let chats = Array.from({ length: 12 }, (_, i) => chat({ id: i + 1, title: i === 4 ? "Kart release notes" : `Chat ${i + 1}` }));
    const deleted: string[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, status),
        "GET /chats": () => jsonResponse(200, { chats }),
        "DELETE /chats/5": () => {
          deleted.push("5");
          chats = chats.filter((c) => c.id !== 5);
          return new Response(null, { status: 204 });
        },
      }),
    );
    renderChat("/assistant");
    const user = userEvent.setup();
    await user.type(await screen.findByRole("textbox", { name: "Filter chats" }), "kart");
    expect(screen.queryAllByRole("link", { name: /Chat \d+/ })).toHaveLength(0);
    expect(screen.getByRole("link", { name: /Kart release notes/ })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete chat Kart release notes" }));
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    await waitFor(() => expect(deleted).toEqual(["5"]));
    await waitFor(() => expect(screen.queryByRole("link", { name: /Kart release notes/ })).not.toBeInTheDocument());
  });

  it("offers Skip permissions only once the chat can make changes, and turns it off with them", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () =>
          jsonResponse(200, { ...status, engines: { ...status.engines, claude: { ...status.engines.claude, skip_permissions: true } } }),
        "GET /chats": () => jsonResponse(200, { chats: [] }),
      }),
    );
    renderChat("/assistant/new");
    const user = userEvent.setup();
    await user.selectOptions(await screen.findByRole("combobox", { name: "Engine" }), "claude");
    expect(screen.queryByRole("switch", { name: /Skip permissions/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("switch", { name: "Can make changes" }));
    const skip = screen.getByRole("switch", { name: /Skip permissions/ });
    expect(skip).toHaveAttribute("aria-checked", "false");
    await user.click(skip);
    expect(screen.getByRole("switch", { name: /Skip permissions — no prompts at all/ })).toHaveAttribute("aria-checked", "true");
    await user.click(screen.getByRole("switch", { name: "Can make changes" }));
    expect(screen.queryByRole("switch", { name: /Skip permissions/ })).not.toBeInTheDocument();
  });

  it("prefers an engine whose machine is online, and says when the chosen one's machine is offline", async () => {
    const offline = {
      ...status,
      settings: { ...status.settings, engine: "claude" as const },
      engines: { ...status.engines, claude: { ...status.engines.claude, machine: "box1", online: false } },
    };
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, offline),
        "GET /chats": () => jsonResponse(200, { chats: [] }),
      }),
    );
    renderChat("/assistant/new");
    const engine = await screen.findByRole("combobox", { name: "Engine" });
    await waitFor(() => expect(engine).toHaveValue("deepseek"));
    expect(screen.queryByText(/is offline/)).not.toBeInTheDocument();
    await userEvent.setup().selectOptions(engine, "claude");
    expect(screen.getByText("box1 is offline — pick another engine")).toBeInTheDocument();
  });
});
