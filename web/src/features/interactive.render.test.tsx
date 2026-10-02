import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import type { Run, RunEvent, RunPrompt } from "@/api/types";
import { RunPage } from "@/features/agents/RunPage";
import { RunStatusBadge } from "@/features/agents/RunBits";
import { ShellProvider } from "@/features/shell/context";
import { mapRunEvents } from "@/lib/runlog";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn(); // jsdom has none; the run page follows the tail
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
  ...over,
});

const prompt = (over: Partial<RunPrompt>): RunPrompt => ({
  id: 1,
  run_id: 42,
  request_id: "r1",
  kind: "question",
  tool_name: "AskUserQuestion",
  input: {},
  suggestions: [],
  description: "",
  status: "pending",
  answer: {},
  created_at: AT,
  answered_at: null,
  ...over,
});

const replay: RunEvent = {
  seq: 1,
  at: AT,
  kind: "claude",
  data: { type: "user", isReplay: true, message: { role: "user", content: "Pick a color" } },
};

function renderRun(prompts: RunPrompt[], over: Partial<Run> = {}) {
  const sent: { path: string; body: unknown }[] = [];
  const record = (path: string) => (init: RequestInit) => {
    sent.push({ path, body: init.body ? JSON.parse(String(init.body)) : null });
    return jsonResponse(200, { prompt: { ...prompts[0], status: "answered" } });
  };
  vi.stubGlobal(
    "fetch",
    routeFetch({
      "GET /runs/42": () => jsonResponse(200, run(over)),
      "GET /runs/42/events": () => jsonResponse(200, { run: run(over), events: [replay], prompts }),
      "POST /runs/42/prompts/1/answer": record("answer"),
      "POST /runs/42/prompts/2/answer": record("answer"),
      "POST /runs/42/messages": record("message"),
      "POST /runs/42/end": record("end"),
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
  return sent;
}

describe("interactive runs", () => {
  it("answers a question with an option or the user's own words", async () => {
    const sent = renderRun([
      prompt({
        input: {
          questions: [
            { question: "Color?", header: "Color", options: [{ label: "Red" }, { label: "Blue" }], multiSelect: false },
            { question: "Size?", options: [{ label: "S" }, { label: "L" }], multiSelect: true },
          ],
        },
      }),
    ]);
    const user = userEvent.setup();
    expect(await screen.findByText("Claude has a question")).toBeInTheDocument();
    expect(screen.getAllByText("Needs you").length).toBeGreaterThan(0);
    const answer = screen.getByRole("button", { name: "Answer" });
    await user.click(screen.getByRole("button", { name: "Blue" }));
    expect(answer).toBeDisabled(); // Size? is still open
    await user.click(screen.getByRole("button", { name: "S" }));
    await user.click(screen.getByRole("button", { name: "L" }));
    await user.click(answer);
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0].body).toEqual({ decision: "answer", answers: { "Color?": "Blue", "Size?": "S, L" } });
    await waitFor(() => expect(screen.queryByText("Claude has a question")).not.toBeInTheDocument());
  });

  it("allows or denies a tool, and approves a plan", async () => {
    const sent = renderRun([
      prompt({ id: 1, kind: "permission", tool_name: "Bash", input: { command: "make deploy" }, suggestions: [{ type: "addRules" }] }),
      prompt({ id: 2, request_id: "r2", kind: "plan", tool_name: "ExitPlanMode", input: { plan: "## Plan\n\nShip it." } }),
    ]);
    const user = userEvent.setup();
    expect(await screen.findByText("Claude wants to use Bash")).toBeInTheDocument();
    expect(screen.getByText("make deploy")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Note for Claude"), "not on Fridays");
    await user.click(screen.getByRole("button", { name: "Deny" }));
    expect(screen.getByRole("heading", { name: "Plan" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Approve, auto-accept edits" }));
    await waitFor(() => expect(sent).toHaveLength(2));
    expect(sent[0].body).toEqual({ decision: "deny", message: "not on Fridays" });
    expect(sent[1].body).toEqual({ decision: "approve_edits" });
  });

  it("shows what the user said and sends follow-ups", async () => {
    const sent = renderRun([], { awaiting: "reply" });
    const user = userEvent.setup();
    expect(await screen.findByText("Your turn — reply, or end the session")).toBeInTheDocument();
    expect(screen.getByText("You:", { exact: false }).parentElement).toHaveTextContent("Pick a color");
    await user.type(screen.getByLabelText("Message"), "Now make it green{Enter}");
    await waitFor(() => expect(sent).toEqual([{ path: "message", body: { text: "Now make it green" } }]));
    expect(screen.getByLabelText("Message")).toHaveValue("");
    await user.click(screen.getByRole("button", { name: "End session" }));
    await waitFor(() => expect(sent.map((s) => s.path)).toEqual(["message", "end"]));
  });

  it("has no composer for a plain run", async () => {
    renderRun([], { interactive: false, awaiting: "" });
    expect(await screen.findByText("Live")).toBeInTheDocument();
    expect(screen.queryByLabelText("Message")).not.toBeInTheDocument();
  });

  it("labels a waiting run and maps replayed messages", () => {
    renderWithProviders(<RunStatusBadge run={{ status: "running", cancel_requested: false, awaiting: "reply" }} />);
    expect(screen.getByText("Your turn")).toBeInTheDocument();
    expect(mapRunEvents([replay])).toEqual([{ kind: "user", key: "user-1", text: "Pick a color" }]);
  });
});
