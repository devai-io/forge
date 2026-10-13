import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { JevStatus } from "@/api/types";
import { TokenSavingPanel } from "@/features/settings/TokenSaving";
import { jsonResponse, makeUser, renderWithProviders, routeFetch, testClient } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

const status = (over: Partial<JevStatus> = {}): JevStatus => ({
  settings: { enabled: false, model: "typesafe/jev-router", routing: true, context: true, match: true },
  key_configured: false,
  stats: { calls: 0, errors: 0, input_tokens: 0, last_at: null, last_error: "", last_model: "" },
  ...over,
});

describe("Settings → Token saving", () => {
  it("cannot be switched on before the provider key is stored", async () => {
    vi.stubGlobal("fetch", routeFetch({ "GET /jev": () => jsonResponse(200, status()) }));
    renderWithProviders(<TokenSavingPanel />, { qc: testClient({ user: makeUser(), elevated_until: null }) });
    expect(await screen.findByText("no OpenRouter key yet (AI provider, above)")).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Use Jev" })).toBeDisabled();
    expect(screen.getByRole("switch", { name: "Pick the model for agent runs" })).toBeDisabled();
    expect(screen.queryByRole("switch", { name: /compaction/ })).not.toBeInTheDocument();
  });

  it("turns Jev on with the provider key", async () => {
    const sent: unknown[] = [];
    let current = status({ key_configured: true });
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /jev": () => jsonResponse(200, current),
        "PATCH /jev": (init) => {
          sent.push(JSON.parse(String(init.body)));
          current = status({ key_configured: true, settings: { ...current.settings, enabled: true } });
          return jsonResponse(200, current);
        },
      }),
    );
    renderWithProviders(<TokenSavingPanel />, { qc: testClient({ user: makeUser(), elevated_until: null }) });
    const user = userEvent.setup();
    expect(await screen.findByText("OpenRouter key in the vault")).toBeInTheDocument();
    expect(screen.getByText(/typesafe\/jev-router on OpenRouter/)).toBeInTheDocument();
    await user.click(screen.getByRole("switch", { name: "Use Jev" }));
    await waitFor(() => expect(sent).toEqual([{ enabled: true }]));
  });
});
