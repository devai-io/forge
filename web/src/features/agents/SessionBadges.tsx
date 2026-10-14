import type { CLISession } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SESSION_CLI_LABEL, SESSION_STATE_LABEL, sessionsCheckedAt } from "@/lib/agents";

const TONE = { logged_in: "good", logged_out: "warning", unknown: "neutral", missing: "outline" } as const;

/**
 * The coding-agent CLIs a machine has (Claude Code, Codex, Cursor) and
 * whether each is logged in, as of the agent's last check.
 */
export function SessionBadges({ sessions }: { sessions: CLISession[] | undefined }) {
  if (!sessions || sessions.length === 0) {
    return <p className="mt-1.5 text-[11.5px] text-fg-3">CLI sessions: not reported (agent older than v0.1.30)</p>;
  }
  const checked = sessionsCheckedAt(sessions);
  return (
    <div className="mt-1.5 flex flex-wrap items-center gap-1" aria-label="CLI sessions">
      {sessions.map((s) => {
        const label = `${SESSION_CLI_LABEL[s.cli] ?? s.cli}: ${SESSION_STATE_LABEL[s.state] ?? s.state}`;
        const title = [label, s.version ? `v${s.version}` : "", s.detail].filter(Boolean).join(" · ");
        return (
          <Badge key={s.cli} tone={TONE[s.state] ?? "neutral"} dot={s.state !== "missing"} title={title}>
            {label}
            {s.version && s.state !== "missing" ? <span className="text-fg-3"> v{s.version}</span> : null}
          </Badge>
        );
      })}
      {checked ? (
        <span className="text-[11px] text-fg-3">
          checked <RelativeTime iso={checked} />
        </span>
      ) : null}
    </div>
  );
}
