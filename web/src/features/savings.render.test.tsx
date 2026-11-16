import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SavingsControls } from "@/features/settings/SavingsControls";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

it("saves controls without Jev and distinguishes unknown costs", async () => {
  let settings = { compact_context: true, early_summaries: true, background_focus_ai: true, proactive_ai: true };
  const patches: unknown[] = [];
  vi.stubGlobal("fetch", routeFetch({
    "GET /savings": () => jsonResponse(200, settings),
    "PATCH /savings": (init) => {
      const patch = JSON.parse(String(init.body)); patches.push(patch);
      settings = { ...settings, ...patch }; return jsonResponse(200, settings);
    },
    "GET /usage": () => jsonResponse(200, { days: 30, coverage: "Counts start with this upgrade.", groups: [{ feature: "agent", project: "SHOP", api: "codex", model: "test", cost_kind: "unknown", calls: 1, failed: 0, input_tokens: 100, cached_tokens: 50, output_tokens: 10, cost_usd: null }] }),
  }));
  renderWithProviders(<SavingsControls />);
  const toggle = await screen.findByRole("switch", { name: "AI for automatic project focus" });
  await userEvent.click(toggle);
  await waitFor(() => expect(patches).toEqual([{ background_focus_ai: false }]));
  expect(await screen.findByText("Unknown")).toBeInTheDocument();
  expect(screen.getByText("Unpriced")).toBeInTheDocument();
  expect(screen.getByRole("switch", { name: "Compact agent context" })).toBeChecked();
});
