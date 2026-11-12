import { createEvent, fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import type { Run } from "@/api/types";
import { RunPage } from "@/features/agents/RunPage";
import { ShellProvider } from "@/features/shell/context";
import { runTitle } from "@/lib/agents";
import { splitImageMarkers, stripImageMarkers, withImageMarkers } from "@/lib/images";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});
afterEach(() => vi.unstubAllGlobals());

const AT = "2026-10-02T10:00:00Z";

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
  prompt: "Pick a color",
  command: "",
  permission_mode: "plan",
  model: "",
  model_note: "",
  engine: "claude",
  effort: "",
  interactive: true,
  awaiting: "answer",
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
  inbox_after: 0,
  resumes: 0,
  finish_requested: false,
  one_turn: false,
  handoff: "",
  resume_hint: "", tmux_session: "", origin: "forge",
  ...over,
});

const TOKEN = "AbCdEfGhIjKlMnOpQrStUvWx";

function renderWaitingRun(over: Partial<Run> = {}) {
  const sent: unknown[] = [];
  const uploads: string[] = [];
  vi.stubGlobal(
    "fetch",
    routeFetch({
      "GET /runs/42": () => jsonResponse(200, run(over)),
      "GET /runs/42/events": () => jsonResponse(200, { run: run(over), events: [], prompts: [] }),
      "POST /uploads": (init) => {
        const file = (init.body as FormData).get("file") as File;
        uploads.push(file.name);
        return jsonResponse(201, {
          token: TOKEN, name: file.name, content_type: "image/png", size: file.size,
          marker: `[image: ${file.name}](forge-upload:${TOKEN})`,
        });
      },
      "POST /runs/42/messages": (init) => {
        sent.push(JSON.parse(String(init.body)));
        return jsonResponse(200, run(over));
      },
    }),
  );
  renderWithProviders(
    <ShellProvider>
      <Routes>
        <Route path="/agents/runs/:id" element={<RunPage />} />
      </Routes>
    </ShellProvider>,
    { route: "/agents/runs/42" },
  );
  return { sent, uploads };
}

const png = (name = "shot.png") => new File([new Uint8Array([137, 80, 78, 71])], name, { type: "image/png" });

describe("images for the agent", () => {
  const waiting: Partial<Run> = { status: "waiting", awaiting: "", session_id: "s-1", finished_at: AT };

  it("sends a picked image with the message, as a marker after the text", async () => {
    const { sent, uploads } = renderWaitingRun(waiting);
    const user = userEvent.setup();
    await user.upload(await screen.findByTestId("attach-images-input"), png());
    expect(await screen.findByRole("img", { name: "shot.png" })).toBeInTheDocument();
    await waitFor(() => expect(uploads).toEqual(["shot.png"]));
    await user.type(screen.getByLabelText("Message"), "what is wrong here?{Enter}");
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toEqual({ text: `what is wrong here?\n\n[image: shot.png](forge-upload:${TOKEN})` });
    await waitFor(() => expect(screen.queryByRole("img", { name: "shot.png" })).not.toBeInTheDocument());
  });

  it("takes images dropped anywhere on the page, and sends an image on its own", async () => {
    const { sent } = renderWaitingRun(waiting);
    await screen.findByLabelText("Message");
    const dataTransfer = { types: ["Files"], files: [png("drop.png")], dropEffect: "none" };
    fireEvent(window, Object.assign(createEvent.dragEnter(window), { dataTransfer }));
    expect(await screen.findByText(/Drop images to send them to Claude/)).toBeInTheDocument();
    fireEvent(window, Object.assign(createEvent.drop(window), { dataTransfer }));
    expect(screen.queryByText(/Drop images to send them to Claude/)).not.toBeInTheDocument();
    expect(await screen.findByRole("img", { name: "drop.png" })).toBeInTheDocument();
    const send = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect(send).toBeEnabled());
    await userEvent.setup().click(send);
    await waitFor(() => expect(sent).toEqual([{ text: `[image: drop.png](forge-upload:${TOKEN})` }]));
  });

  it("shows a prompt's images as thumbnails, and titles an images-only run", async () => {
    const prompt = `Fix the header\n\n[image: header.png](forge-upload:${TOKEN})`;
    renderWaitingRun({ ...waiting, prompt });
    const img = await screen.findByRole("img", { name: "header.png" });
    expect(img).toHaveAttribute("src", `/api/uploads/${TOKEN}`);
    expect(runTitle(run({ prompt: `[image: a.png](forge-upload:${TOKEN})` }))).toBe("(images)");
    expect(runTitle(run({ prompt }))).toBe("Fix the header");
  });
});

describe("image markers", () => {
  it("adds, strips and splits them", () => {
    const m = `[image: a.png](forge-upload:${TOKEN})`;
    expect(withImageMarkers("  hi ", [])).toBe("hi");
    expect(withImageMarkers("hi", [m, m])).toBe(`hi\n\n${m}\n${m}`);
    expect(withImageMarkers("", [m])).toBe(m);
    expect(stripImageMarkers(`hi\n\n${m}`)).toBe("hi");
    expect(splitImageMarkers(`see ${m} ok`)).toEqual([
      { kind: "text", text: "see " },
      { kind: "image", name: "a.png", token: TOKEN },
      { kind: "text", text: " ok" },
    ]);
  });
});
