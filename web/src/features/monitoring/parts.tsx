// Pieces shared by the Monitoring page and a server's page: host meters,
// alert rows, Nomad job lists, probes, dashboard links and the Grafana
// connect form.

import clsx from "clsx";
import { CircleAlert, CircleDashed, CircleCheck, ExternalLink, LayoutDashboard, Plug } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { useRefreshMonitoring, useSaveVaultItem } from "@/api/hooks";
import type { Alert, HostMetrics, NomadHost, Probe } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { useToast } from "@/components/ui/Toast";
import { serverPath } from "@/lib/infra";
import { formatUptime, sslLevel, usageLevel, type Level } from "@/lib/monitoring";

const LEVEL_FILL: Record<Level, string> = { ok: "bg-series-1", warn: "bg-warning", fail: "bg-critical" };

/** A resource meter: severity carries the fill; the value is always printed. */
export function UsageBar({ label, pct }: { label: string; pct: number | null }) {
  const level = usageLevel(pct);
  return (
    <div className="space-y-1">
      <div className="flex items-baseline justify-between text-[11.5px]">
        <span className="text-fg-3">{label}</span>
        <span className={clsx("tabular font-medium", level === "fail" ? "text-critical-ink" : "text-fg-2")}>
          {pct === null ? "—" : `${Math.round(pct)}%`}
          {level !== "ok" ? <span className="sr-only"> ({level === "fail" ? "critical" : "warning"})</span> : null}
        </span>
      </div>
      <div
        role="meter"
        aria-label={`${label} usage`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct ?? undefined}
        className="h-1.5 overflow-hidden rounded-full bg-surface-3"
      >
        {pct !== null ? (
          <div className={clsx("h-full rounded-full transition-[width] duration-500", LEVEL_FILL[level])} style={{ width: `${Math.min(100, Math.max(0, pct))}%` }} />
        ) : null}
      </div>
    </div>
  );
}

/** One host's live metrics; the name links to its server page when Forge knows the server. */
export function HostCard({ host: h }: { host: HostMetrics }) {
  return (
    <article className={clsx("space-y-3 rounded-xl border bg-surface p-3.5", h.up ? "border-line" : "border-critical/45")}>
      <div className="flex items-center gap-2">
        <span className={clsx("size-2 shrink-0 rounded-full", h.up ? "bg-good" : "bg-critical")} aria-hidden />
        <h3 className="font-mono text-[13.5px] font-semibold">
          {h.server_id !== null ? (
            <Link to={serverPath(h.server_id)} className="hover:text-accent hover:underline">
              {h.name}
            </Link>
          ) : (
            h.name
          )}
        </h3>
        <span className={clsx("text-[11.5px]", h.up ? "text-fg-3" : "font-medium text-critical-ink")}>{h.up ? "up" : "not reporting"}</span>
        <span className="ml-auto text-[11.5px] text-fg-3" title="Uptime">
          {formatUptime(h.uptime_seconds)}
        </span>
      </div>
      <UsageBar label="CPU" pct={h.cpu_pct} />
      <UsageBar label="Memory" pct={h.mem_pct} />
      <UsageBar label="Disk /" pct={h.disk_pct} />
      <div className="flex items-center justify-between text-[11.5px] text-fg-3">
        <span>
          load <span className="tabular text-fg-2">{h.load1 === null ? "—" : h.load1.toFixed(2)}</span>
        </span>
        {!h.up && h.last_seen ? (
          <span>
            last seen <RelativeTime iso={h.last_seen} />
          </span>
        ) : null}
      </div>
    </article>
  );
}

export function AlertRow({ alert: a }: { alert: Alert }) {
  const labels = Object.entries(a.labels).filter(([k]) => !["alertname", "grafana_folder", "__alert_rule_uid__"].includes(k));
  return (
    <li className="space-y-1 px-3.5 py-2.5">
      <div className="flex flex-wrap items-center gap-2">
        {a.state === "firing" ? (
          <CircleAlert className="size-4 text-critical" aria-hidden />
        ) : (
          <CircleDashed className="size-4 text-warning" aria-hidden />
        )}
        <span className="text-[13px] font-medium">{a.name}</span>
        <Badge tone={a.state === "firing" ? "critical" : "warning"}>{a.state}</Badge>
        {a.severity ? <Badge tone="outline">{a.severity}</Badge> : null}
        {a.since ? (
          <span className="ml-auto text-[11.5px] text-fg-3">
            since <RelativeTime iso={a.since} />
          </span>
        ) : null}
      </div>
      {a.summary ? <p className="text-[12.5px] text-fg-2">{a.summary}</p> : null}
      {labels.length ? (
        <div className="flex flex-wrap gap-1">
          {labels.slice(0, 6).map(([k, v]) => (
            <span key={k} className="rounded bg-surface-2 px-1.5 font-mono text-[10.5px] text-fg-2">
              {k}={v}
            </span>
          ))}
        </div>
      ) : null}
    </li>
  );
}

export const GRAFANA_ITEM_NAME = "Grafana service account (Forge)";

export function ConnectGrafana({ url, error }: { url: string; error: string }) {
  const save = useSaveVaultItem();
  const refresh = useRefreshMonitoring();
  const toast = useToast();
  const [token, setToken] = useState("");
  const base = url.replace(/\/$/, "");
  return (
    <div className="space-y-3 p-4 text-[13px]">
      <div className="flex items-start gap-2.5">
        <Plug className="mt-0.5 size-4 shrink-0 text-fg-3" aria-hidden />
        <div className="space-y-1.5 text-fg-2">
          <p className="font-medium text-fg">Connect Grafana to see alerts and dashboards here.</p>
          <ol className="list-decimal space-y-1 pl-4">
            <li>
              Open{" "}
              <a href={`${base}/org/serviceaccounts`} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">
                {base}/org/serviceaccounts
              </a>{" "}
              (needs Tailscale).
            </li>
            <li>
              Add a service account with the <strong>Viewer</strong> role, then <em>Add service account token</em>.
            </li>
            <li>Paste the token below. It is stored encrypted in the vault, tagged integration:grafana.</li>
          </ol>
          {error ? <p className="text-[12px] text-critical-ink">{error}</p> : null}
        </div>
      </div>
      <form
        className="flex flex-wrap items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (!token.trim()) return;
          save.mutate(
            { name: GRAFANA_ITEM_NAME, kind: "token", tags: ["integration:grafana"], secret: { value: token.trim() } },
            {
              onSuccess: () => {
                setToken("");
                toast.success("Grafana token saved — connecting…");
                refresh.mutate();
              },
              onError: (err) => toast.error(err),
            },
          );
        }}
      >
        <Field label="Service account token" className="min-w-0 flex-1 basis-64">
          {(id) => (
            <Input
              id={id}
              type="password"
              autoComplete="off"
              spellCheck={false}
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="glsa_…"
              className="font-mono"
            />
          )}
        </Field>
        <Button type="submit" variant="primary" loading={save.isPending || refresh.isPending} disabled={!token.trim()}>
          Connect
        </Button>
      </form>
    </div>
  );
}

export function DashboardLink({ title, url }: { title: string; url: string }) {
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      className="flex items-center gap-2 rounded-md px-2 py-1.5 text-[13px] hover:bg-surface-2"
    >
      <LayoutDashboard className="size-3.5 shrink-0 text-fg-3" aria-hidden />
      <span className="min-w-0 flex-1 truncate">{title}</span>
      <ExternalLink className="size-3 shrink-0 text-fg-3" aria-hidden />
    </a>
  );
}

const JOB_TONE: Record<string, "good" | "warning" | "outline" | "critical"> = {
  running: "good",
  pending: "warning",
  dead: "outline",
};

/** One host's Nomad jobs as a compact two-column list. */
export function NomadJobList({ host: h }: { host: NomadHost }) {
  if (!h.reachable) return <p className="text-[12px] text-critical-ink">{h.error || "Nomad API did not answer."}</p>;
  if (!h.jobs.length) return <p className="text-[12px] text-fg-3">No jobs.</p>;
  return (
    <ul className="grid grid-cols-1 gap-x-4 gap-y-1 sm:grid-cols-2">
      {h.jobs.map((j) => {
        const failing = j.failed > 0 || (j.status === "running" && j.running === 0);
        return (
          <li key={j.id} className="flex min-w-0 items-center gap-2 text-[12.5px]">
            <Badge tone={failing ? "critical" : (JOB_TONE[j.status] ?? "neutral")} dot>
              {j.status}
            </Badge>
            <span className="min-w-0 flex-1 truncate font-mono" title={`${j.id} (${j.type})`}>
              {j.id}
            </span>
            <span className="tabular shrink-0 text-[11.5px] text-fg-3" title="running / failed / queued">
              {j.running}
              {j.failed ? <span className="text-critical-ink"> · {j.failed} failed</span> : null}
              {j.queued ? <span> · {j.queued} queued</span> : null}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/** Blackbox probes, failing first, then by TLS days left. */
export function ProbeList({ probes }: { probes: Probe[] }) {
  const sorted = [...probes].sort((a, b) => Number(a.up) - Number(b.up) || (a.ssl_days_left ?? 9999) - (b.ssl_days_left ?? 9999));
  return (
    <ul className="divide-y divide-line">
      {sorted.map((p) => {
        const ssl = sslLevel(p.ssl_days_left);
        return (
          <li key={p.url} className="flex items-center gap-2 px-3.5 py-2 text-[12.5px]">
            {p.up ? <CircleCheck className="size-3.5 shrink-0 text-good" aria-hidden /> : <CircleAlert className="size-3.5 shrink-0 text-critical" aria-hidden />}
            <span className="sr-only">{p.up ? "up" : "down"}</span>
            <a href={p.url} target="_blank" rel="noopener noreferrer" className="min-w-0 flex-1 truncate font-mono hover:text-accent">
              {p.url.replace(/^https?:\/\//, "")}
            </a>
            {p.ssl_days_left !== null ? (
              <Badge tone={ssl === "fail" ? "critical" : ssl === "warn" ? "warning" : "outline"} title="TLS certificate days left">
                TLS {Math.floor(p.ssl_days_left)}d
              </Badge>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}
