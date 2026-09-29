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
  settings: { enabled: true, base_url: "https://llm.example.com", model: "demo-flash" },
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
  ...over,
});

const msg = (over: Partial<ChatMessage>): ChatMessage => ({
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
      expect(created).toEqual({ content: "What is open on SHOP?", engine: "claude", model: "sonnet", effort: "xhigh", edits: false }),
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
    const summary = await screen.findByRole("button", { name: /DeepSeek API · demo-flash · High · \$0\.0053 API/ });
    await user.click(summary);
    expect(screen.getByText("Jev · jev-1")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Run #12 · DeepSeek deepseek-flash" })).toHaveAttribute("href", "/agents/runs/12");
    expect(screen.getByText("≈$0.0031")).toBeInTheDocument();

    await user.selectOptions(screen.getByRole("combobox", { name: "Effort" }), "off");
    await waitFor(() => expect(patches).toEqual([{ effort: "off" }]));
  });
});
