// Endpoint monitoring display: status with icon + word (never colour alone),
// latency, 24h uptime and a strip of the last day's checks.

import clsx from "clsx";
import { CircleAlert, CircleCheck, CircleDashed, MoreHorizontal, Pencil, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { useCheckEndpoint, useDeleteEndpoint, useEndpointChecks } from "@/api/hooks";
import type { Endpoint } from "@/api/types";
import { ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { Menu } from "@/components/ui/Menu";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { useToast } from "@/components/ui/Toast";
import { EndpointDialog } from "@/features/projects/dialogs";
import { formatTime, percent } from "@/lib/format";
import { bucketChecks } from "@/lib/infra";

export function EndpointStatus({ endpoint, compact = false }: { endpoint: Pick<Endpoint, "last_status" | "enabled">; compact?: boolean }) {
  if (!endpoint.enabled) {
    return (
      <span className="inline-flex items-center gap-1 text-[12px] text-fg-3">
        <CircleDashed className="size-3.5" aria-hidden /> {compact ? null : "Paused"}
        {compact ? <span className="sr-only">Paused</span> : null}
      </span>
    );
  }
  const s = endpoint.last_status;
  return (
    <span className="inline-flex items-center gap-1 text-[12px] text-fg-2">
      {s === "up" ? (
        <CircleCheck className="size-3.5 text-good" aria-hidden />
      ) : s === "down" ? (
        <CircleAlert className="size-3.5 text-critical" aria-hidden />
      ) : (
        <CircleDashed className="size-3.5 text-fg-3" aria-hidden />
      )}
      <span className={compact ? "sr-only" : undefined}>{s === "up" ? "Up" : s === "down" ? "Down" : "Unknown"}</span>
    </span>
  );
}

export function CheckStrip({ endpointId }: { endpointId: number }) {
  const checks = useEndpointChecks(endpointId, 24);
  const [now] = useState(() => Date.now());
  if (!checks.data) return <div className="h-4 w-full max-w-60 animate-pulse-soft rounded bg-surface-2" aria-hidden />;
  const buckets = bucketChecks(checks.data, now);
  const failed = buckets.filter((b) => b.fail).length;
  return (
    <div
      className="flex h-4 w-full max-w-60 items-stretch gap-px"
      role="img"
      aria-label={`Last 24 hours: ${failed ? `${failed} half-hour window${failed > 1 ? "s" : ""} with failures` : "no failures"}`}
    >
      {buckets.map((b) => (
        <span
          key={b.start}
          title={`${formatTime(new Date(b.start).toISOString()).slice(0, 5)} — ${b.ok} ok, ${b.fail} failed`}
          className={clsx(
            "flex-1 rounded-[1.5px]",
            b.fail ? "bg-critical" : b.ok ? "bg-good/70" : "bg-surface-3",
          )}
        />
      ))}
    </div>
  );
}

export function EndpointsTable({
  endpoints,
  showProject = false,
  strip = false,
}: {
  endpoints: Endpoint[];
  showProject?: boolean;
  strip?: boolean;
}) {
  const check = useCheckEndpoint();
  const del = useDeleteEndpoint();
  const toast = useToast();
  const [editing, setEditing] = useState<Endpoint | null>(null);
  const [deleting, setDeleting] = useState<Endpoint | null>(null);

  return (
    <>
      <ul className="divide-y divide-line">
        {endpoints.map((e) => (
          <li key={e.id} className="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3 py-2.5">
            <div className="flex min-w-0 flex-1 basis-56 items-center gap-2.5">
              <EndpointStatus endpoint={e} compact />
              <div className="min-w-0">
                <div className="flex items-center gap-2 text-[13px]">
                  <span className="truncate font-medium">{e.name}</span>
                  {showProject ? (
                    <span className="inline-flex shrink-0 items-center gap-1 text-[11.5px] text-fg-3">
                      <ColorDot color={e.project_color} className="size-2" />
                      {e.project_key}
                    </span>
                  ) : null}
                </div>
                <a
                  href={e.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="block truncate font-mono text-[11.5px] text-fg-3 hover:text-accent"
                >
                  {e.url}
                </a>
              </div>
            </div>
            {strip ? (
              <div className="order-last w-full sm:order-none sm:w-60">
                <CheckStrip endpointId={e.id} />
              </div>
            ) : null}
            <dl className="flex shrink-0 items-center gap-4 text-[12px]">
              <div className="text-right">
                <dt className="sr-only">Status</dt>
                <dd className={clsx("tabular", e.last_status === "down" ? "font-medium text-critical-ink" : "text-fg-2")}>
                  {e.last_code ?? (e.last_error ? "error" : "—")}
                </dd>
              </div>
              <div className="w-14 text-right">
                <dt className="sr-only">Latency</dt>
                <dd className="tabular text-fg-2">{e.last_latency_ms !== null ? `${e.last_latency_ms}ms` : "—"}</dd>
              </div>
              <div className="w-14 text-right" title="Uptime, last 24h">
                <dt className="sr-only">Uptime 24h</dt>
                <dd className="tabular text-fg-2">{percent(e.uptime_24h, e.uptime_24h !== null && e.uptime_24h < 1 ? 1 : 0)}</dd>
              </div>
              <div className="hidden w-16 text-right text-fg-3 sm:block">
                <dt className="sr-only">Last check</dt>
                <dd>
                  <RelativeTime iso={e.last_checked_at} />
                </dd>
              </div>
            </dl>
            <div className="flex shrink-0 items-center gap-0.5">
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={`Check ${e.name} now`}
                title="Check now"
                disabled={check.isPending && check.variables === e.id}
                onClick={() =>
                  check.mutate(e.id, {
                    onSuccess: (r) => toast.info(`${r.name}: ${r.last_status === "up" ? "up" : r.last_status}${r.last_code ? ` (${r.last_code})` : ""}`),
                    onError: (err) => toast.error(err),
                  })
                }
              >
                <RefreshCw className={clsx("size-3.5", check.isPending && check.variables === e.id && "animate-spin")} />
              </Button>
              <Menu
                label={`Actions for ${e.name}`}
                triggerClassName="grid size-7 place-items-center rounded-md text-fg-3 hover:bg-surface-2 hover:text-fg"
                trigger={<MoreHorizontal className="size-4" />}
                items={[
                  { label: "Edit", icon: <Pencil />, onSelect: () => setEditing(e) },
                  { label: "Delete", icon: <Trash2 />, danger: true, onSelect: () => setDeleting(e) },
                ]}
              />
            </div>
            {e.last_status === "down" && e.last_error ? (
              <p className="w-full pl-6 text-[11.5px] text-critical-ink">{e.last_error}</p>
            ) : null}
          </li>
        ))}
      </ul>
      {editing ? <EndpointDialog projectKey={editing.project_key} endpoint={editing} onClose={() => setEditing(null)} /> : null}
      <ConfirmDialog
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={`Delete ${deleting?.name ?? "endpoint"}?`}
        body="Its check history goes with it."
        loading={del.isPending}
        onConfirm={() =>
          deleting &&
          del.mutate(deleting.id, {
            onSuccess: () => {
              toast.success("Endpoint deleted");
              setDeleting(null);
            },
            onError: (err) => toast.error(err),
          })
        }
      />
    </>
  );
}
