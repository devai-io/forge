import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes, useLocation } from "react-router-dom";
import type { AssistantStatus, Bot, Chat, ChatMessage, Project } from "@/api/types";
import { AssistantPage } from "@/features/assistant/AssistantPage";
import { ShellProvider } from "@/features/shell/context";
import { upsertChat } from "@/lib/chats";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

const AT = "2026-09-28T10:00:00Z";

const status: AssistantStatus = {
  settings: { enabled: true, engine: "deepseek", base_url: "https://llm.example.com", model: "demo-model" },
  key_configured: true,
  models: [],
  engines: {
    deepseek: { available: true, models: ["demo-model"], efforts: ["off", "low", "high", "max"] },
    claude: { available: false, machine: "", online: false, reason: "no master machine", models: [], efforts: [] },
  },
} as AssistantStatus;

const chat = (over: Partial<Chat> = {}): Chat => ({
  id: 5,
  title: "Shop bot",
  busy: false,
  last_error: "",
  usage: { input_tokens: 0, output_tokens: 0, cached_tokens: 0 },
  created_at: AT,
  updated_at: AT,
  engine: "deepseek",
  model: "",
  effort: "",
  edits: false,
  project_id: 1,
  project_key: "SHOP",
  summary: "",
  summary_seq: 0,
  ...over,
});

const project = (key: string, name: string) => ({ id: key === "SHOP" ? 1 : 2, key, name, color: "#3987e5", status: "active" }) as unknown as Project;

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

function Where() {
  const loc = useLocation();
  return <p>at {loc.pathname}</p>;
}

function renderPage(route: string) {
  return renderWithProviders(
    <ShellProvider>
      <Routes>
        <Route path="/assistant" element={<AssistantPage />} />
        <Route path="/assistant/:chatId" element={<AssistantPage />} />
        <Route path="/assistant/bot/:botKey" element={<AssistantPage />} />
      </Routes>
      <Where />
    </ShellProvider>,
    { route },
  );
}

describe("Project bots", () => {
  it("lists the bots that talked, with the others behind a fold", async () => {
    const bots: Bot[] = [
      { project: project("SHOP", "Shop"), chat: chat(), last: { role: "assistant", content: "Two tasks are open.", created_at: AT } },
      { project: project("KART", "Kart"), chat: null, last: null },
    ];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, status),
        "GET /chats": () => jsonResponse(200, { chats: [] }),
        "GET /bots": () => jsonResponse(200, { bots }),
      }),
    );
    renderPage("/assistant");
    const list = await screen.findByRole("region", { name: "Project bots" });
    expect(within(list).getByText("Two tasks are open.")).toBeInTheDocument();
    expect(within(list).getByRole("link", { name: /Shop/ })).toHaveAttribute("href", "/assistant/5");
    expect(within(list).queryByText("Kart")).not.toBeInTheDocument();
    await userEvent.setup().click(within(list).getByRole("button", { name: "1 more project" }));
    expect(within(list).getByRole("link", { name: /Kart/ })).toHaveAttribute("href", "/assistant/bot/KART");
  });

  it("starts a bot with its first message and opens its chat", async () => {
    let posted: unknown = null;
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, status),
        "GET /chats": () => jsonResponse(200, { chats: [] }),
        "GET /bots": () => jsonResponse(200, { bots: [{ project: project("KART", "Kart"), chat: null, last: null }] }),
        "POST /bots/KART/messages": (init) => {
          posted = JSON.parse(String(init.body));
          return jsonResponse(201, {
            chat: chat({ id: 9, title: "Kart bot", project_id: 2, project_key: "KART", busy: true }),
            messages: [msg({ content: "What is left before launch?" })],
            turns: [],
          });
        },
        "GET /chats/9": () =>
          jsonResponse(200, { chat: chat({ id: 9, title: "Kart bot", project_id: 2, project_key: "KART" }), messages: [], turns: [] }),
      }),
    );
    renderPage("/assistant/bot/KART");
    expect(await screen.findByText("Kart's bot")).toBeInTheDocument();
    await userEvent.setup().type(screen.getByRole("textbox", { name: "Message" }), "What is left before launch?{Enter}");
    expect(await screen.findByText("at /assistant/9")).toBeInTheDocument();
    expect(posted).toMatchObject({ content: "What is left before launch?" });
    expect(await screen.findAllByText("KART bot")).not.toHaveLength(0);
  });

  it("opens a bot's chat, shows the folded summary and offers a reset", async () => {
    const folded = chat({ summary: "- The checkout flow is being reworked.", summary_seq: 40 });
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, status),
        "GET /chats": () => jsonResponse(200, { chats: [] }),
        "GET /bots": () => jsonResponse(200, { bots: [{ project: project("SHOP", "Shop"), chat: folded, last: null }] }),
        "GET /chats/5": () => jsonResponse(200, { chat: folded, messages: [msg({ seq: 41, content: "Where were we?" })], turns: [] }),
      }),
    );
    renderPage("/assistant/bot/SHOP");
    expect(await screen.findByText("at /assistant/5")).toBeInTheDocument();
    const fold = await screen.findByRole("button", { name: /folded into a summary/ });
    expect(screen.queryByText("The checkout flow is being reworked.")).not.toBeInTheDocument();
    await userEvent.setup().click(fold);
    expect(screen.getByText("The checkout flow is being reworked.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reset the SHOP bot" })).toBeInTheDocument();
  });

  it("keeps bot chats out of the Assistant's chat list", () => {
    const plain = chat({ id: 1, project_id: null, project_key: "" });
    expect(upsertChat([plain], chat())).toEqual([plain]);
    expect(upsertChat([], plain)).toEqual([plain]);
  });
});
