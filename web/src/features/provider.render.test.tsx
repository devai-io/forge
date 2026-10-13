import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ProviderStatus } from "@/api/types";
import { ProviderPanel } from "@/features/settings/ProviderPanel";
import { jsonResponse, makeUser, renderWithProviders, routeFetch, testClient } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

const status = (over: Partial<ProviderStatus> = {}): ProviderStatus => ({
  settings: { base_url: "https://openrouter.ai/api/v1" },
  key_configured: false,
  anthropic_url: "https://openrouter.ai/api",
  default_url: "https://openrouter.ai/api/v1",
  ...over,
});

describe("Settings → AI provider", () => {
  it("stores the one key and changes the base URL", async () => {
    const sent: unknown[] = [];
    let current = status();
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /provider": () => jsonResponse(200, current),
        "PUT /provider/key": (init) => {
          sent.push(JSON.parse(String(init.body)));
          current = status({ key_configured: true });
          return jsonResponse(200, current);
        },
        "PATCH /provider": (init) => {
          sent.push(JSON.parse(String(init.body)));
          current = status({ key_configured: true, settings: { base_url: "https://gateway.example.com/v1" }, anthropic_url: "https://gateway.example.com" });
          return jsonResponse(200, current);
        },
      }),
    );
    renderWithProviders(<ProviderPanel />, { qc: testClient({ user: makeUser(), elevated_until: null }) });
    const user = userEvent.setup();
    expect(await screen.findByText("no key yet: DeepSeek and Jev are off")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Test the key" })).toBeDisabled();
    await user.type(screen.getByLabelText("OpenRouter API key"), "not-a-real-key-for-tests");
    await user.click(screen.getByRole("button", { name: "Save key" }));
    expect(await screen.findByText("key in the vault")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("not-a-real-key-for-tests")).not.toBeInTheDocument();
    const url = screen.getByLabelText("API base URL");
    await user.clear(url);
    await user.type(url, "https://gateway.example.com/v1");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(sent).toEqual([{ api_key: "not-a-real-key-for-tests" }, { base_url: "https://gateway.example.com/v1" }]),
    );
  });
});
