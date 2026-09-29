// Under each answer: what that turn spent, per API — the chat model's calls,
// Jev's, a Claude Code session's tokens (covered by the machine's Claude
// login, shown at API prices), and every run the turn queued (whose costs
// arrive as they finish). Collapsed to one line; opens into the detail.

import { ChevronRight } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import type { ChatTurn } from "@/api/types";
import { EFFORT_LABEL, ENGINE_NAME, formatSpend, turnSpend } from "@/lib/assistant";

export function TurnSpend({ turn }: { turn: ChatTurn }) {
  const [open, setOpen] = useState(false);
  const spend = turnSpend(turn);
  if (!spend.lines.length && turn.status !== "running") return null;

  const engine = [ENGINE_NAME[turn.engine], turn.model, turn.effort ? EFFORT_LABEL[turn.effort] ?? turn.effort : ""]
    .filter(Boolean)
    .join(" · ");
  const parts = [spend.unpriced && spend.billed === 0 ? "API price unknown" : `${formatSpend(spend.billed)} API${spend.unpriced ? " + unpriced calls" : ""}`];
  if (spend.subscription > 0) parts.push(`≈${formatSpend(spend.subscription)} on the Claude subscription`);
  if (spend.pending) parts.push("runs still going");

  return (
    <div className="ml-6.5 text-[11.5px] text-fg-3">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex max-w-full items-center gap-1 rounded text-left hover:text-fg-2"
      >
        <ChevronRight className={`size-3 shrink-0 transition-transform ${open ? "rotate-90" : ""}`} aria-hidden />
        <span className="truncate tabular">
          {engine} · {parts.join(" · ")}
        </span>
      </button>
      {turn.run_id ? (
        <Link to={`/agents/runs/${turn.run_id}`} className="ml-4 hover:text-fg-2 hover:underline">
          {turn.status === "running" ? "Follow the Claude Code session" : "Claude Code session log"}
        </Link>
      ) : null}
      {open ? (
        <table className="mt-1 w-full max-w-xl text-[11.5px]">
          <caption className="sr-only">What this turn spent</caption>
          <tbody>
            {spend.lines.map((l) => (
              <tr key={l.key} className="align-top">
                <th scope="row" className="py-0.5 pr-3 text-left font-normal text-fg-2">
                  {l.runId ? (
                    <Link to={`/agents/runs/${l.runId}`} className="hover:underline">
                      {l.label}
                    </Link>
                  ) : (
                    l.label
                  )}
                  <span className="block text-fg-3">{l.detail}</span>
                </th>
                <td className="py-0.5 text-right whitespace-nowrap tabular">
                  {l.pending ? "…" : `${l.approx && l.cost !== null ? "≈" : ""}${formatSpend(l.cost)}`}
                  {l.kind === "subscription" && l.cost !== null ? <span className="block text-fg-3">subscription</span> : null}
                </td>
              </tr>
            ))}
            {!spend.lines.length ? (
              <tr>
                <td className="py-0.5 text-fg-3">Nothing spent yet.</td>
              </tr>
            ) : null}
          </tbody>
        </table>
      ) : null}
    </div>
  );
}
