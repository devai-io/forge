import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes, useLocation } from "react-router-dom";
import type { AssistantStatus, Chat, ChatMessage, Run } from "@/api/types";
import { AssistantPage } from "@/features/assistant/AssistantPage";
import { AssistantPanel } from "@/features/settings/AssistantPanel";
import { ShellProvider } from "@/features/shell/context";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

const AT = "2026-09-28T10:00:00Z";

const status = (over: Partial<AssistantStatus> = {}): AssistantStatus => ({
  settings: { enabled: true, base_url: "https://llm.example.com", model: "demo-model" },
  key_configured: true,
  models: [],
  ...over,
});

const chat = (over: Partial<Chat> = {}): Chat => ({
  id: 7,
  title: "SHOP bugs",
  busy: false,
  last_error: "",
  usage: { input_tokens: 12_345, output_tokens: 1_100, cached_tokens: 0 },
  created_at: AT,
  updated_at: AT,
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

const run = (over: Partial<Run> = {}): Run => ({
  id: 42,
  runner_id: 1,
  runner_name: "desk",
  project_id: 1,
  project_key: "SHOP",
  project_color: "#3987e5",
  repo_id: 3,
  repo_name: "shop_api",
  task_id: null,
  task_ref: null,
  kind: "agent",
  prompt: "Fix the flaky checkout test",
  command: "",
  permission_mode: "acceptEdits",
  model: "sonnet",
  model_note: "",
  engine: "claude",
  worktree: false,
  resume_run_id: null,
  status: "running",
  cancel_requested: false,
  session_id: "",
  result: "",
  error: "",
  exit_code: null,
  cost_usd: null,
  num_turns: null,
  duration_ms: null,
  created_at: AT,
  started_at: AT,
  finished_at: null,
  ...over,
});

function Where() {
  const loc = useLocation();
  return <p>at {loc.pathname + loc.search}</p>;
}

function renderPage(route: string) {
  return renderWithProviders(
    <ShellProvider>
      <Routes>
        <Route path="/assistant" element={<AssistantPage />} />
        <Route path="/assistant/:chatId" element={<AssistantPage />} />
      </Routes>
      <Where />
    </ShellProvider>,
    { route },
  );
}

describe("Assistant page", () => {
  it("creates a chat from the first message, moves to it and shows the message", async () => {
    let created: unknown = null;
    let chats: Chat[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, status()),
        "GET /chats": () => jsonResponse(200, { chats }),
        "POST /chats": (init) => {
          created = JSON.parse(String(init.body));
          chats = [chat({ busy: true })];
          return jsonResponse(201, {
            chat: chat({ busy: true }),
            messages: [msg({ seq: 1, content: "Review the open SHOP bugs" })],
          });
        },
        "GET /chats/7": () => jsonResponse(200, { chat: chat({ busy: true }), messages: [] }),
      }),
    );
    renderPage("/assistant/new");
    expect(await screen.findByText("What should we work on?")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.type(screen.getByRole("textbox", { name: "Message" }), "Review the open SHOP bugs{Enter}");
    expect(await screen.findByText("at /assistant/7")).toBeInTheDocument();
    expect(created).toEqual({ content: "Review the open SHOP bugs" });
    expect(within(screen.getByRole("log")).getByText("Review the open SHOP bugs")).toBeInTheDocument();
    // The agent is on it: the composer waits.
    expect(screen.getByRole("status")).toHaveTextContent("Working…");
    expect(screen.getByRole("textbox", { name: "Message" })).toBeDisabled();
  });

  it("renders a delegation as a card with the run's live status and a link to the run", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, status()),
        "GET /chats": () => jsonResponse(200, { chats: [chat()] }),
        "GET /chats/7": () =>
          jsonResponse(200, {
            chat: chat(),
            messages: [
              msg({ seq: 1, content: "Fix the flaky checkout test on desk" }),
              msg({
                seq: 2,
                role: "assistant",
                tool_calls: [
                  { id: "c1", name: "list_projects", arguments: {} },
                  {
                    id: "c2",
                    name: "delegate_to_claude",
                    arguments: { project: "SHOP", repo: "shop_api", prompt: "Fix the flaky checkout test", machine: "desk" },
                  },
                ],
              }),
              msg({ seq: 3, role: "tool", tool_call_id: "c1", tool_name: "list_projects", content: "2 projects", result: { projects: ["SHOP", "GAME"] } }),
              msg({
                seq: 4,
                role: "tool",
                tool_call_id: "c2",
                tool_name: "delegate_to_claude",
                content: "Queued run #42",
                result: { run_id: 42, machine: "desk", project: "SHOP", repo: "shop_api", permission_mode: "acceptEdits", model: "sonnet", model_note: "" },
              }),
              msg({ seq: 5, role: "assistant", content: "Claude Code is **on it**." }),
            ],
          }),
        "GET /runs/42": () => jsonResponse(200, run()),
      }),
    );
    renderPage("/assistant/7");
    expect(await screen.findByText("Delegated to Claude Code on desk · SHOP/shop_api")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Run #42" })).toHaveAttribute("href", "/agents/runs/42");
    expect(await screen.findByText("Running")).toBeInTheDocument();
    expect(screen.getByText("acceptEdits")).toBeInTheDocument();
    expect(screen.getByText("on it").tagName).toBe("STRONG");
    expect(screen.getByText("12.3k in / 1.1k out tokens")).toBeInTheDocument();
    // Generic tools are one line that opens to the raw call.
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Looked at projects" }));
    expect(screen.getByText(/"GAME"/)).toBeInTheDocument();
    // Not busy: no Stop, and the composer is ready.
    expect(screen.queryByRole("button", { name: "Stop" })).not.toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Message" })).toBeEnabled();
  });

  it("shows Stop while the agent works, and stops it", async () => {
    let busy = true;
    let stopped = false;
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, status()),
        "GET /chats": () => jsonResponse(200, { chats: [chat({ busy })] }),
        "GET /chats/7": () => jsonResponse(200, { chat: chat({ busy }), messages: [msg({ seq: 1, content: "Run the GAME tests" })] }),
        "POST /chats/7/stop": () => {
          stopped = true;
          busy = false;
          return jsonResponse(200, { chat: chat({ busy: false }) });
        },
      }),
    );
    renderPage("/assistant/7");
    const stop = await screen.findByRole("button", { name: "Stop" });
    expect(screen.getByRole("status")).toHaveTextContent("Working…");
    expect(screen.getByRole("textbox", { name: "Message" })).toBeDisabled();
    await userEvent.click(stop);
    await waitFor(() => expect(screen.queryByRole("button", { name: "Stop" })).not.toBeInTheDocument());
    expect(stopped).toBe(true);
    expect(screen.getByRole("textbox", { name: "Message" })).toBeEnabled();
  });

  it("explains how to switch it on when the assistant is off", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, status({ settings: { enabled: false, base_url: "", model: "" }, key_configured: false })),
        "GET /chats": () => jsonResponse(503, { error: { code: "assistant_off", message: "The assistant is off." } }),
      }),
    );
    renderPage("/assistant");
    expect(await screen.findByText("The assistant is off")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Settings → Assistant" })).toHaveAttribute("href", "/settings#settings-assistant");
    expect(screen.queryByRole("textbox", { name: "Message" })).not.toBeInTheDocument();
  });
});

describe("Settings → Assistant", () => {
  it("stays off until a key is stored, then saves the key and the model", async () => {
    const sent: unknown[] = [];
    let current = status({ settings: { enabled: false, base_url: "https://llm.example.com", model: "demo-model" }, key_configured: false });
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /assistant": () => jsonResponse(200, current),
        "PUT /assistant/key": (init) => {
          sent.push(JSON.parse(String(init.body)));
          current = { ...current, key_configured: true, models: ["demo-model", "demo-model-large"] };
          return jsonResponse(200, current);
        },
        "PATCH /assistant": (init) => {
          const patch = JSON.parse(String(init.body));
          sent.push(patch);
          current = { ...current, settings: { ...current.settings, ...patch } };
          return jsonResponse(200, current);
        },
      }),
    );
    renderWithProviders(<AssistantPanel />);
    expect(await screen.findByText("no key yet")).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Use the assistant" })).toBeDisabled();
    // No model list yet: a free-text field.
    expect(screen.getByRole("textbox", { name: "Model" })).toHaveValue("demo-model");

    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Provider API key"), "not-a-real-key-for-tests");
    await user.click(screen.getByRole("button", { name: "Save key" }));
    expect(await screen.findByText("key in the vault")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("not-a-real-key-for-tests")).not.toBeInTheDocument();

    // The provider's models arrive with the key: now a list.
    await user.selectOptions(screen.getByRole("combobox", { name: "Model" }), "demo-model-large");
    await user.click(screen.getByRole("switch", { name: "Use the assistant" }));
    await waitFor(() =>
      expect(sent).toEqual([{ api_key: "not-a-real-key-for-tests" }, { model: "demo-model-large" }, { enabled: true }]),
    );
  });
});
