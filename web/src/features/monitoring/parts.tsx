// Pieces shared by the Monitoring page and a server's page: host meters (and
// the one-line rows for hosts that are not reporting), alert rows, Nomad job
// lists, probes, dashboard links and the Grafana connect form (full, behind
// a banner, or as a one-line pointer).

import clsx from "clsx";
import { ChevronRight, CircleAlert, CircleDashed, CircleCheck, ExternalLink, LayoutDashboard, Plug } from "lucide-react";
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
import { formatUptime, jobLevel, metricsOutage, sortNomadJobs, splitHosts, sslLevel, usageLevel, type Level } from "@/lib/monitoring";

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

/** Where the full connect form lives: the Monitoring page opens it from this link. */
export const CONNECT_GRAFANA_PATH = "/monitoring?connect=grafana#mon-alerts";

/** One line where alerts would be, for pages that are not the place to set Grafana up (a server's page). */
export function GrafanaUnavailable({ what = "Alerts" }: { what?: string }) {
  return (
    <p className="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 p-3.5 text-[12.5px] text-fg-3">
      <Plug className="size-3.5 shrink-0" aria-hidden />
      {what} unavailable — Grafana not connected ·{" "}
      <Link to={CONNECT_GRAFANA_PATH} className="font-medium text-accent hover:underline">
        Connect →
      </Link>
    </p>
  );
}

/** The connect form behind a one-line banner; open from the start when `open` (a "Connect →" link landed here). */
export function ConnectGrafanaDisclosure({ url, error, open = false }: { url: string; error: string; open?: boolean }) {
  return (
    <details className="group" open={open || undefined}>
      <summary className="flex cursor-pointer list-none flex-wrap items-center gap-x-1.5 gap-y-0.5 p-3.5 text-[12.5px] text-fg-2">
        <Plug className="size-3.5 shrink-0 text-fg-3" aria-hidden />
        Grafana not connected — alerts and dashboards unavailable.
        <span className="inline-flex items-center gap-0.5 font-medium text-accent">
          Connect Grafana <ChevronRight className="size-3.5 transition-transform group-open:rotate-90" aria-hidden />
        </span>
      </summary>
      <div className="border-t border-line">
        <ConnectGrafana url={url} error={error} />
      </div>
    </details>
  );
}

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

/**
 * One host's Nomad jobs, the ones in trouble first. Red means broken now
 * (dead, nothing running), amber allocations waiting; Nomad's lifetime count
 * of failed allocations is shown muted — a healthy job collects those over
 * months of deploys. Two columns from `sm` unless `columns={1}` (narrow
 * panels); `onlyProblems` hides the healthy ones.
 */
export function NomadJobList({ host: h, columns = 2, onlyProblems = false }: { host: NomadHost; columns?: 1 | 2; onlyProblems?: boolean }) {
  if (!h.reachable) return <p className="text-[12px] text-critical-ink">{h.error || "Nomad API did not answer."}</p>;
  if (!h.jobs.length) return <p className="text-[12px] text-fg-3">No jobs.</p>;
  const jobs = sortNomadJobs(h.jobs).filter((j) => !onlyProblems || jobLevel(j) !== "ok");
  if (!jobs.length) return <p className="text-[12px] text-fg-3">All {h.jobs.length} jobs healthy.</p>;
  return (
    <ul className={clsx("grid grid-cols-1 gap-x-4 gap-y-1", columns === 2 && "sm:grid-cols-2")}>
      {jobs.map((j) => {
        const level = jobLevel(j);
        return (
          <li key={j.id} className="flex min-w-0 items-center gap-2 text-[12.5px]" data-level={level}>
            <Badge tone={level === "fail" ? "critical" : level === "warn" ? "warning" : (JOB_TONE[j.status] ?? "neutral")} dot>
              {j.status}
            </Badge>
            <span className="min-w-0 flex-1 truncate font-mono" title={`${j.id} (${j.type})`}>
              {j.id}
            </span>
            <span className="tabular shrink-0 text-[11.5px] text-fg-3">
              <span title="Allocations running" className={level === "fail" ? "font-medium text-critical-ink" : undefined}>
                {j.running} running
              </span>
              {j.queued ? <span className={level === "warn" ? "font-medium text-fg" : undefined}> · {j.queued} queued</span> : null}
              {j.failed ? <span title={`${j.failed} failed allocation${j.failed === 1 ? "" : "s"} (all time)`}> · {j.failed} failed</span> : null}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/** A host that is not reporting, as one line: no point drawing empty meters. */
export function HostDownRow({ host: h }: { host: HostMetrics }) {
  return (
    <li className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 px-3.5 py-2 text-[12.5px]">
      <span className="size-2 shrink-0 rounded-full bg-critical" aria-hidden />
      <span className="min-w-0 truncate font-mono font-semibold">
        {h.server_id !== null ? (
          <Link to={serverPath(h.server_id)} className="hover:text-accent hover:underline">
            {h.name}
          </Link>
        ) : (
          h.name
        )}
      </span>
      <span className="font-medium text-critical-ink">not reporting</span>
      {h.last_seen ? (
        <span className="text-fg-3">
          · last seen <RelativeTime iso={h.last_seen} />
        </span>
      ) : null}
    </li>
  );
}

/** Most hosts silent at once: say where to look instead of listing them as broken. */
export function MetricsOutageBanner({ down, total }: { down: number; total: number }) {
  return (
    <p role="status" className="flex items-start gap-2 rounded-lg border border-critical/40 bg-critical/8 px-3 py-2 text-[12.5px] text-fg-2">
      <CircleAlert className="mt-0.5 size-4 shrink-0 text-critical" aria-hidden />
      <span>
        <strong className="font-semibold text-fg">
          {down === total ? `None of the ${total} hosts are` : `${down} of ${total} hosts are`} reporting{down === total ? "" : " nothing"}.
        </strong>{" "}
        When most go quiet together, the metrics source (vmagent on the hosts → VictoriaMetrics, and Forge's link to it) is the likely
        cause — check it before the hosts.
      </span>
    </p>
  );
}

/** Hosts for a grid: silent ones as one-line rows first, then a meter card per reporting host, heaviest use first. */
export function HostsGrid({ hosts }: { hosts: HostMetrics[] }) {
  const { down, up } = splitHosts(hosts);
  return (
    <div className="space-y-3">
      {metricsOutage(down.length, hosts.length) ? <MetricsOutageBanner down={down.length} total={hosts.length} /> : null}
      {down.length ? (
        <ul aria-label="Hosts not reporting" className="divide-y divide-line rounded-xl border border-critical/35 bg-surface">
          {down.map((h) => (
            <HostDownRow key={h.name} host={h} />
          ))}
        </ul>
      ) : null}
      {up.length ? (
        <div className="grid grid-cols-1 items-start gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {up.map((h) => (
            <HostCard key={h.name} host={h} />
          ))}
        </div>
      ) : null}
    </div>
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
