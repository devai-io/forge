import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/client";
import type { AIUsage, SavingsSettings } from "@/api/types";
import { Switch } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { useToast } from "@/components/ui/Toast";

const controls: { key: keyof SavingsSettings; label: string; hint: string }[] = [
  { key: "compact_context", label: "Compact agent context", hint: "Start with essential project notes, fewer memories and six tasks plus active, blocked, urgent and focus tasks. Agents can fetch the full context when needed. No Jev call required." },
  { key: "early_summaries", label: "Summarize Assistant history earlier", hint: "Summarize older completed turns at 24,000 reported input tokens or about 96 KB of history. Keep recent turns and use the fast model for summaries. Applies to direct API chats; Claude and Codex manage their own conversation history." },
  { key: "background_focus_ai", label: "AI for automatic project focus", hint: "Reuse AI results when inputs are unchanged. Turn off to prioritize automatically using free rules; Recalculate can still ask AI." },
  { key: "proactive_ai", label: "Allow scheduled and event-driven bot turns", hint: "Turn off to pause background bot conversations across projects. Direct requests still work; pending events remain queued." },
];
const costLabels = { provider: "Provider reported", estimate: "Estimated", subscription_estimate: "Subscription API equivalent", unknown: "Unpriced" };

export function SavingsControls() {
  const qc = useQueryClient();
  const toast = useToast();
  const settings = useQuery({ queryKey: ["savings"], queryFn: () => api.get<SavingsSettings>("/savings") });
  const usage = useQuery({ queryKey: ["ai-usage"], queryFn: () => api.get<AIUsage>("/usage?days=30"), refetchInterval: 60_000 });
  const update = useMutation({
    mutationFn: (patch: Partial<SavingsSettings>) => api.patch<SavingsSettings>("/savings", patch),
    onSuccess: (data) => qc.setQueryData(["savings"], data),
    onError: (e) => toast.error(e),
  });
  return (
    <Panel title="Token savings & usage" id="settings-savings" bodyClassName="space-y-4 p-4">
      {settings.error ? <p role="alert">Could not load savings settings: {String(settings.error)}</p> : settings.data ? (
        <ul className="space-y-3">
          {controls.map((control) => <li key={control.key}>
            <Switch checked={settings.data[control.key]} label={control.label} disabled={update.isPending}
              onChange={(value) => update.mutate({ [control.key]: value })} />
            <p className="mt-1 pl-11 text-[12px] text-fg-3">{control.hint}</p>
          </li>)}
        </ul>
      ) : <p>Loading savings settings…</p>}
      <p className="text-[13px] text-fg-2">For unrelated work, start a new conversation with a short handoff: goal, decisions, files, checks and remaining work. Keep the same conversation for related follow-ups.</p>
      <h3 className="text-sm font-medium">Recorded usage · last 30 days</h3>
      {usage.error ? <p role="alert">Could not load usage: {String(usage.error)}</p> : usage.data ? <>
        <p className="text-[12px] text-fg-3">{usage.data.coverage}</p>
        {usage.data.groups.length ? <div className="overflow-x-auto">
          <table className="w-full text-left text-[12px]">
            <thead><tr>{["Feature / project", "Model", "Calls / failed", "Input / cached / output", "Cost basis", "USD"].map((title) => <th className="p-2" key={title}>{title}</th>)}</tr></thead>
            <tbody>{usage.data.groups.map((g, i) => <tr key={i} className="border-t border-line">
              <td className="p-2">{g.feature}{g.project ? ` / ${g.project}` : ""}</td>
              <td className="p-2">{g.model || "Not reported"}<span className="block text-fg-3">{g.api}</span></td>
              <td className="p-2 tabular">{g.calls} / {g.failed}</td>
              <td className="p-2 tabular">{g.input_tokens.toLocaleString()} / {g.cached_tokens.toLocaleString()} / {g.output_tokens.toLocaleString()}</td>
              <td className="p-2">{costLabels[g.cost_kind]}</td>
              <td className="p-2 tabular">{g.cost_usd == null ? "Unknown" : g.cost_usd.toFixed(4)}</td>
            </tr>)}</tbody>
          </table>
        </div> : <p className="text-[13px] text-fg-3">No usage recorded yet. Counts start with this upgrade.</p>}
      </> : <p>Loading usage…</p>}
    </Panel>
  );
}
