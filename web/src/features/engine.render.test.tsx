import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { EngineStatus } from "@/api/types";
import { EnginePanel } from "@/features/settings/EnginePanel";
import { jsonResponse, makeUser, renderWithProviders, routeFetch, testClient } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

const status = (over: Partial<EngineStatus> = {}): EngineStatus => ({
  settings: { default: "deepseek", model: "deepseek-flash", heavy_model: "deepseek-v4-pro" },
  deepseek_key: false,
  models: { deepseek: ["deepseek-flash", "deepseek-v4-pro"], claude: ["claude-opus-5-5"] },
  efforts: ["low", "medium", "high", "xhigh", "max"],
  ...over,
});

describe("Settings → Agent engine", () => {
  it("warns that runs fall back to Claude without a DeepSeek key", async () => {
    vi.stubGlobal("fetch", routeFetch({ "GET /engine": () => jsonResponse(200, status()) }));
    renderWithProviders(<EnginePanel />, { qc: testClient({ user: makeUser(), elevated_until: null }) });
    expect(await screen.findByText("no DeepSeek key: runs fall back to Claude")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "DeepSeek" })).toHaveAttribute("aria-checked", "true");
  });

  it("stores a key and switches the default engine", async () => {
    const sent: unknown[] = [];
    let current = status();
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /engine": () => jsonResponse(200, current),
        "PUT /engine/key": (init) => {
          sent.push(JSON.parse(String(init.body)));
          current = status({ deepseek_key: true });
          return jsonResponse(200, current);
        },
        "PATCH /engine": (init) => {
          sent.push(JSON.parse(String(init.body)));
          current = status({ deepseek_key: true, settings: { ...current.settings, default: "claude" } });
          return jsonResponse(200, current);
        },
      }),
    );
    renderWithProviders(<EnginePanel />, { qc: testClient({ user: makeUser(), elevated_until: null }) });
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("DeepSeek API key"), "not-a-real-key-for-tests");
    await user.click(screen.getByRole("button", { name: "Save key" }));
    expect(await screen.findByText("DeepSeek key in the vault")).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "Claude" }));
    await waitFor(() => expect(sent).toEqual([{ api_key: "not-a-real-key-for-tests" }, { default: "claude" }]));
  });
});
