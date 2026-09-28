// Settings → Security: the security log (every sign-in, confirmation, reveal,
// attach…) and "sign out other sessions".

import clsx from "clsx";
import { CircleX, Cpu, KeyRound, LogIn, LogOut, ShieldAlert, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { useRevokeOtherSessions, useSecurityLog } from "@/api/hooks";
import type { SecurityEvent } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { ErrorState } from "@/components/ui/EmptyState";
import { Select } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { describeAgent, isAlarming, KIND_GROUPS, kindLabel, SECURITY_KINDS } from "@/lib/security";

const PAGE = 100;
const MAX = 500;

function KindIcon({ kind }: { kind: string }) {
  const cls = "size-4 shrink-0";
  switch (kind) {
    case "login_new_device":
      return <TriangleAlert className={clsx(cls, "text-warning")} aria-hidden />;
    case "login_failed":
    case "elevate_failed":
      return <CircleX className={clsx(cls, "text-critical")} aria-hidden />;
    case "logout":
    case "session_revoked":
    case "sessions_revoked_others":
      return <LogOut className={clsx(cls, "text-fg-3")} aria-hidden />;
    case "login":
    case "elevate":
      return <LogIn className={clsx(cls, "text-fg-3")} aria-hidden />;
    case "runner_created":
    case "runner_rotated":
      return <Cpu className={clsx(cls, "text-fg-3")} aria-hidden />;
    default:
      return SECURITY_KINDS[kind as keyof typeof SECURITY_KINDS]?.group === "sensitive" ? (
        <ShieldAlert className={clsx(cls, "text-fg-3")} aria-hidden />
      ) : (
        <KeyRound className={clsx(cls, "text-fg-3")} aria-hidden />
      );
  }
}

export function SecurityLogPanel() {
  const [kind, setKind] = useState("");
  const [limit, setLimit] = useState(PAGE);
  const log = useSecurityLog(kind, limit);
  const events = log.data ?? [];
  const canLoadMore = events.length >= limit && limit < MAX;

  return (
    <Panel
      title="Security log"
      icon={<ShieldAlert />}
      id="settings-security-log"
      bodyClassName="p-0"
      actions={
        <Select aria-label="Filter by kind" compact className="w-auto" value={kind} onChange={(e) => { setKind(e.target.value); setLimit(PAGE); }}>
          <option value="">Everything</option>
          {KIND_GROUPS.map((g) => (
            <optgroup key={g.label} label={g.label}>
              {g.kinds.map((k) => (
                <option key={k} value={k}>
                  {kindLabel(k)}
                </option>
              ))}
            </optgroup>
          ))}
        </Select>
      }
    >
      {log.isPending ? (
        <SkeletonRows rows={6} className="p-3" />
      ) : log.error ? (
        <div className="p-3">
          <ErrorState error={log.error} onRetry={() => log.refetch()} />
        </div>
      ) : events.length ? (
        <>
          <ul className={clsx("divide-y divide-line", log.isPlaceholderData && "opacity-60")}>
            {events.map((e) => (
              <SecurityRow key={e.id} event={e} />
            ))}
          </ul>
          <div className="flex items-center justify-between border-t border-line px-4 py-2 text-[12px] text-fg-3">
            <span>
              {events.length} event{events.length === 1 ? "" : "s"}
              {kind ? ` · ${kindLabel(kind)}` : ""}
            </span>
            {canLoadMore ? (
              <Button size="sm" variant="ghost" onClick={() => setLimit((l) => Math.min(MAX, l + PAGE))} loading={log.isFetching}>
                Load more
              </Button>
            ) : null}
          </div>
        </>
      ) : (
        <p className="p-4 text-[13px] text-fg-3">{kind ? "Nothing of that kind yet." : "No security events yet."}</p>
      )}
    </Panel>
  );
}

function SecurityRow({ event: e }: { event: SecurityEvent }) {
  const alarming = isAlarming(e.kind);
  const meta = SECURITY_KINDS[e.kind as keyof typeof SECURITY_KINDS];
  return (
    <li className={clsx("flex items-start gap-3 px-4 py-2.5", alarming && "bg-warning/8")}>
      <span className="mt-0.5">
        <KindIcon kind={e.kind} />
      </span>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px]">
          <span className={clsx("font-medium", alarming && "text-fg")}>{kindLabel(e.kind)}</span>
          {meta && e.kind === "login_new_device" ? <Badge tone={meta.tone}>new device</Badge> : null}
          {e.detail ? <span className="text-fg-2">{e.detail}</span> : null}
        </p>
        <p className="mt-0.5 flex flex-wrap items-center gap-x-2 text-[11.5px] text-fg-3">
          <span className="font-mono">{e.ip || "unknown IP"}</span>
          <span>{describeAgent(e.user_agent)}</span>
          {e.session_id !== null ? <span className="font-mono">session {e.session_id}</span> : null}
        </p>
      </div>
      <RelativeTime iso={e.at} className="shrink-0 text-[11.5px] text-fg-3" />
    </li>
  );
}

/** "Sign out other sessions" with a confirmation; the current one stays. */
export function RevokeOthersButton({ count }: { count: number }) {
  const revoke = useRevokeOtherSessions();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button size="sm" variant="subtle" disabled={count === 0} onClick={() => setOpen(true)} title={count ? undefined : "No other sessions"}>
        <LogOut className="size-3.5" aria-hidden /> Sign out other sessions
      </Button>
      <ConfirmDialog
        open={open}
        onClose={() => setOpen(false)}
        title="Sign out every other session?"
        confirmLabel="Sign them out"
        loading={revoke.isPending}
        body={`${count} other session${count === 1 ? "" : "s"} will be signed out now. This device stays signed in. Anyone using those sessions has to sign in again — with your password and code.`}
        onConfirm={() =>
          revoke.mutate(undefined, {
            onSuccess: ({ revoked }) => {
              toast.success(`Signed out ${revoked} session${revoked === 1 ? "" : "s"}`);
              setOpen(false);
            },
            onError: (e) => toast.error(e),
          })
        }
      />
    </>
  );
}
