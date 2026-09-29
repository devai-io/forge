import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { JevStatus } from "@/api/types";
import { TokenSavingPanel } from "@/features/settings/TokenSaving";
import { jsonResponse, makeUser, renderWithProviders, routeFetch, testClient } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

const status = (over: Partial<JevStatus> = {}): JevStatus => ({
  settings: { enabled: false, routing: true, context: true, compaction: true, match: true },
  key_configured: false,
  stats: { calls: 0, errors: 0, input_tokens: 0, last_at: null, last_error: "", last_model: "" },
  ...over,
});

describe("Settings → Token saving", () => {
  it("cannot be switched on before a key is stored", async () => {
    vi.stubGlobal("fetch", routeFetch({ "GET /jev": () => jsonResponse(200, status()) }));
    renderWithProviders(<TokenSavingPanel />, { qc: testClient({ user: makeUser(), elevated_until: null }) });
    expect(await screen.findByText("no key yet")).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Use Jev" })).toBeDisabled();
    expect(screen.getByRole("switch", { name: "Pick the model for agent runs" })).toBeDisabled();
  });

  it("stores a key and turns Jev on", async () => {
    const sent: unknown[] = [];
    let current = status();
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /jev": () => jsonResponse(200, current),
        "PUT /jev/key": (init) => {
          sent.push(JSON.parse(String(init.body)));
          current = status({ key_configured: true });
          return jsonResponse(200, current);
        },
        "PATCH /jev": (init) => {
          sent.push(JSON.parse(String(init.body)));
          current = status({ key_configured: true, settings: { ...current.settings, enabled: true } });
          return jsonResponse(200, current);
        },
      }),
    );
    renderWithProviders(<TokenSavingPanel />, { qc: testClient({ user: makeUser(), elevated_until: null }) });
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("TypeSafe API key"), "not-a-real-key-for-tests");
    await user.click(screen.getByRole("button", { name: "Save key" }));
    expect(await screen.findByText("key in the vault")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("not-a-real-key-for-tests")).not.toBeInTheDocument();
    await user.click(screen.getByRole("switch", { name: "Use Jev" }));
    await waitFor(() => expect(sent).toEqual([{ api_key: "not-a-real-key-for-tests" }, { enabled: true }]));
  });
});
