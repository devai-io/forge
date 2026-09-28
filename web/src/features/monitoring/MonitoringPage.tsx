// Live state of the fleet from what already watches it: VictoriaMetrics host
// metrics, Grafana alerts/dashboards/probes and every host's Nomad jobs, all
// read by forge-api over the tailnet. Refreshes every 30 s; "Refresh" skips
// the API's cache.

import clsx from "clsx";
import {
  BellRing,
  CircleAlert,
  CircleCheck,
  CircleDashed,
  ExternalLink,
  Gauge,
  LayoutDashboard,
  Plug,
  RefreshCw,
  ServerCog,
  ShieldCheck,
} from "lucide-react";
import { useState } from "react";
import { useMonitoring, useRefreshMonitoring, useSaveVaultItem } from "@/api/hooks";
import { HelpLink } from "@/features/docs/HelpLink";
import type { Alert, HostMetrics, Monitoring, NomadHost, Probe } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Field, Input } from "@/components/ui/Input";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { formatUptime, sortAlerts, sslLevel, usageLevel, type Level } from "@/lib/monitoring";

export function MonitoringPage() {
  const mon = useMonitoring();
  const refresh = useRefreshMonitoring();
  const toast = useToast();
  return (
    <div className="mx-auto max-w-[1400px] space-y-5">
      <PageHeader
        title="Monitoring"
        subtitle={
          mon.data ? (
            <>
              Host metrics, alerts, Nomad jobs and probes · fetched <RelativeTime iso={mon.data.fetched_at} />
            </>
          ) : (
            "Host metrics, alerts, Nomad jobs and probes"
          )
        }
        actions={
          <>
            <HelpLink anchor="monitoring-page" />
            <Button size="sm" variant="subtle" onClick={() => refresh.mutate(undefined, { onError: (e) => toast.error(e) })} loading={refresh.isPending}>
              {!refresh.isPending ? <RefreshCw className="size-3.5" aria-hidden /> : null} Refresh
            </Button>
          </>
        }
      />
      {mon.isPending ? (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {Array.from({ length: 8 }, (_, i) => (
            <Skeleton key={i} className="h-40" />
          ))}
        </div>
      ) : mon.error ? (
        <ErrorState error={mon.error} onRetry={() => mon.refetch()} />
      ) : (
        <MonitoringBody m={mon.data} stale={mon.isFetching || refresh.isPending} />
      )}
    </div>
  );
}

function MonitoringBody({ m, stale }: { m: Monitoring; stale: boolean }) {
  const down = m.hosts.filter((h) => !h.up).length;
  return (
    <div className={clsx("space-y-5 transition-opacity", stale && "opacity-80")}>
      <section aria-labelledby="mon-hosts" className="space-y-3">
        <h2 id="mon-hosts" className="flex items-center gap-2 text-[13.5px] font-semibold">
          <Gauge className="size-4 text-fg-3" aria-hidden /> Hosts
          <span className="font-normal text-fg-3">
            · {m.hosts.length - down}/{m.hosts.length} reporting
          </span>
        </h2>
        {m.hosts.length ? (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
            {m.hosts.map((h) => (
              <HostCard key={h.name} host={h} />
            ))}
          </div>
        ) : (
          <EmptyState compact title="No host metrics">VictoriaMetrics returned nothing — is vmagent running on the hosts?</EmptyState>
        )}
      </section>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <AlertsPanel m={m} />
        <DashboardsPanel m={m} />
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-5">
        <div className="lg:col-span-3">
          <NomadPanel hosts={m.nomad} />
        </div>
        <div className="lg:col-span-2">
          <ProbesPanel probes={m.probes} />
        </div>
      </div>
    </div>
  );
}

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

function HostCard({ host: h }: { host: HostMetrics }) {
  return (
    <article className={clsx("space-y-3 rounded-xl border bg-surface p-3.5", h.up ? "border-line" : "border-critical/45")}>
      <div className="flex items-center gap-2">
        <span className={clsx("size-2 shrink-0 rounded-full", h.up ? "bg-good" : "bg-critical")} aria-hidden />
        <h3 className="font-mono text-[13.5px] font-semibold">{h.name}</h3>
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

function AlertsPanel({ m }: { m: Monitoring }) {
  const g = m.grafana;
  const alerts = sortAlerts(g.alerts);
  const firing = alerts.filter((a) => a.state === "firing").length;
  return (
    <Panel
      title={
        <>
          Grafana alerts{" "}
          {g.connected ? <span className="font-normal text-fg-3">· {firing ? `${firing} firing` : "all quiet"}</span> : null}
        </>
      }
      icon={<BellRing />}
      id="mon-alerts"
      bodyClassName="p-0"
    >
      {!g.connected ? (
        <ConnectGrafana url={g.url} error={g.error} />
      ) : alerts.length ? (
        <ul className="divide-y divide-line">
          {alerts.map((a, i) => (
            <AlertRow key={`${a.name}-${i}`} alert={a} />
          ))}
        </ul>
      ) : (
        <p className="flex items-center gap-2 p-4 text-[13px] text-fg-2">
          <CircleCheck className="size-4 text-good" aria-hidden /> Nothing firing or pending.
        </p>
      )}
      {g.connected && g.error ? <p className="border-t border-line px-3.5 py-2 text-[12px] text-critical-ink">{g.error}</p> : null}
    </Panel>
  );
}

function AlertRow({ alert: a }: { alert: Alert }) {
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

function ConnectGrafana({ url, error }: { url: string; error: string }) {
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

function DashboardsPanel({ m }: { m: Monitoring }) {
  const byFolder = new Map<string, typeof m.grafana.dashboards>();
  for (const d of m.grafana.dashboards) {
    const list = byFolder.get(d.folder || "General") ?? [];
    list.push(d);
    byFolder.set(d.folder || "General", list);
  }
  return (
    <Panel title="Dashboards" icon={<LayoutDashboard />} id="mon-dashboards">
      <p className="px-2 pb-1 text-[11.5px] text-fg-3">Open in Grafana — reachable over Tailscale only.</p>
      {m.grafana.dashboards.length ? (
        Array.from(byFolder.entries()).map(([folder, list]) => (
          <div key={folder} className="mb-1">
            <div className="px-2 pt-1.5 pb-0.5 text-[11px] font-semibold tracking-wide text-fg-3 uppercase">{folder}</div>
            {list.map((d) => (
              <DashboardLink key={d.uid || d.url} title={d.title} url={d.url} />
            ))}
          </div>
        ))
      ) : (
        <p className="px-2 py-3 text-[13px] text-fg-3">No dashboards found.</p>
      )}
    </Panel>
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

function NomadPanel({ hosts }: { hosts: NomadHost[] }) {
  return (
    <Panel title="Nomad jobs" icon={<ServerCog />} id="mon-nomad" bodyClassName="p-0">
      {hosts.length ? (
        <div className="divide-y divide-line">
          {hosts.map((h) => (
            <div key={h.host} className="px-3.5 py-2.5">
              <div className="mb-1.5 flex items-center gap-2">
                <span className="font-mono text-[13px] font-semibold">{h.host}</span>
                {h.reachable ? (
                  <span className="text-[11.5px] text-fg-3">{h.jobs.length} jobs</span>
                ) : (
                  <Badge tone="critical" dot>
                    unreachable
                  </Badge>
                )}
              </div>
              {!h.reachable ? (
                <p className="text-[12px] text-critical-ink">{h.error || "Nomad API did not answer."}</p>
              ) : h.jobs.length ? (
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
              ) : (
                <p className="text-[12px] text-fg-3">No jobs.</p>
              )}
            </div>
          ))}
        </div>
      ) : (
        <p className="p-4 text-[13px] text-fg-3">No Nomad hosts configured.</p>
      )}
    </Panel>
  );
}

function ProbesPanel({ probes }: { probes: Probe[] }) {
  const sorted = [...probes].sort((a, b) => Number(a.up) - Number(b.up) || (a.ssl_days_left ?? 9999) - (b.ssl_days_left ?? 9999));
  return (
    <Panel title="Blackbox probes" icon={<ShieldCheck />} id="mon-probes" bodyClassName="p-0">
      {sorted.length ? (
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
      ) : (
        <p className="p-4 text-[13px] text-fg-3">No probes.</p>
      )}
    </Panel>
  );
}
