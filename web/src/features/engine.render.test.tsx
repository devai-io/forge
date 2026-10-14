import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { EngineStatus } from "@/api/types";
import { EnginePanel } from "@/features/settings/EnginePanel";
import { jsonResponse, makeUser, renderWithProviders, routeFetch, testClient } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

const status = (over: Partial<EngineStatus> = {}): EngineStatus => ({
  settings: { default: "claude", model: "deepseek/deepseek-v4-flash", heavy_model: "deepseek/deepseek-v4-pro", max_concurrent: 4 },
  provider_key: false,
  models: { openrouter: ["deepseek/deepseek-v4-flash", "deepseek/deepseek-v4-pro"], claude: ["claude-opus-5-5"] },
  efforts: ["low", "medium", "high", "xhigh", "max"],
  ...over,
});

describe("Settings → Agent engine", () => {
  it("defaults to Claude and warns that DeepSeek runs need the OpenRouter key", async () => {
    vi.stubGlobal("fetch", routeFetch({ "GET /engine": () => jsonResponse(200, status()) }));
    renderWithProviders(<EnginePanel />, { qc: testClient({ user: makeUser(), elevated_until: null }) });
    expect(await screen.findByText("no OpenRouter key: DeepSeek runs fall back to Claude")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Claude" })).toHaveAttribute("aria-checked", "true");
    expect(screen.queryByLabelText(/API key/)).not.toBeInTheDocument();
  });

  it("switches the default engine", async () => {
    const sent: unknown[] = [];
    let current = status({ provider_key: true });
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /engine": () => jsonResponse(200, current),
        "PATCH /engine": (init) => {
          sent.push(JSON.parse(String(init.body)));
          current = status({ provider_key: true, settings: { ...current.settings, default: "openrouter" } });
          return jsonResponse(200, current);
        },
      }),
    );
    renderWithProviders(<EnginePanel />, { qc: testClient({ user: makeUser(), elevated_until: null }) });
    const user = userEvent.setup();
    expect(await screen.findByText("OpenRouter key in the vault")).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "DeepSeek (OpenRouter)" }));
    await waitFor(() => expect(sent).toEqual([{ default: "openrouter" }]));
    await waitFor(() => expect(screen.getByRole("radio", { name: "DeepSeek (OpenRouter)" })).toHaveAttribute("aria-checked", "true"));
  });

  it("sets how many runs each machine takes at once", async () => {
    const sent: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /engine": () => jsonResponse(200, status()),
        "PATCH /engine": (init) => {
          sent.push(JSON.parse(String(init.body)));
          return jsonResponse(200, status({ settings: { ...status().settings, max_concurrent: 6 } }));
        },
      }),
    );
    renderWithProviders(<EnginePanel />, { qc: testClient({ user: makeUser(), elevated_until: null }) });
    const user = userEvent.setup();
    const input = await screen.findByLabelText("Concurrent runs per machine");
    expect(input).toHaveValue(4);
    await user.clear(input);
    await user.type(input, "20");
    expect(screen.getByText("A whole number from 1 to 16")).toBeInTheDocument();
    await user.clear(input);
    await user.type(input, "6");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent).toEqual([{ max_concurrent: 6 }]));
    expect(screen.getByLabelText("Concurrent runs per machine")).toHaveValue(6);
  });
});
