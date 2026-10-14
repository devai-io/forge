// Live state of the fleet from what already watches it: VictoriaMetrics host
// metrics, Grafana alerts/dashboards/probes and every host's Nomad jobs, all
// read by forge-api over the tailnet. Refreshes every 30 s; "Refresh" skips
// the API's cache. A project filter (?project=KEY) narrows everything to that
// project's servers, endpoints and dashboards.

import clsx from "clsx";
import { BellRing, CircleCheck, Gauge, LayoutDashboard, RefreshCw, ServerCog, ShieldCheck } from "lucide-react";
import { useMemo } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useEndpoints, useMonitoring, useProjects, useRefreshMonitoring, useServers } from "@/api/hooks";
import { HelpLink } from "@/features/docs/HelpLink";
import type { Monitoring, NomadHost, Probe } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Select } from "@/components/ui/Input";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { projectScope, scopeMonitoring, sortAlerts } from "@/lib/monitoring";
import { AlertRow, ConnectGrafana, DashboardLink, HostCard, NomadJobList, ProbeList } from "./parts";

export { DashboardLink, UsageBar } from "./parts";

export function MonitoringPage() {
  const mon = useMonitoring();
  const refresh = useRefreshMonitoring();
  const toast = useToast();
  const projects = useProjects();
  const servers = useServers();
  const endpoints = useEndpoints();
  const [params, setParams] = useSearchParams();
  const projectKey = params.get("project") ?? "";
  const project = projects.data?.find((p) => p.key === projectKey);

  const scoped = useMemo(() => {
    if (!mon.data || !project || !servers.data) return null;
    return scopeMonitoring(mon.data, projectScope(project, servers.data, endpoints.data ?? []));
  }, [mon.data, project, servers.data, endpoints.data]);

  const setProject = (key: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (key) next.set("project", key);
        else next.delete("project");
        return next;
      },
      { replace: true },
    );

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
            <Select compact aria-label="Project" value={projectKey} onChange={(e) => setProject(e.target.value)} className="w-52">
              <option value="">All projects</option>
              {projectKey && !project && !projects.isPending ? <option value={projectKey}>{projectKey} (unknown)</option> : null}
              {projects.data?.map((p) => (
                <option key={p.key} value={p.key}>
                  {p.key} · {p.name}
                </option>
              ))}
            </Select>
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
        <MonitoringBody
          m={scoped ?? mon.data}
          all={scoped ? mon.data : null}
          project={scoped && project ? project : null}
          onClear={() => setProject("")}
          stale={mon.isFetching || refresh.isPending}
        />
      )}
    </div>
  );
}

/** "3 of 11" when filtered, the plain count otherwise. */
function OfTotal({ n, total }: { n: number; total: number | null }) {
  return <>{total === null ? n : `${n} of ${total}`}</>;
}

function MonitoringBody({
  m,
  all,
  project,
  onClear,
  stale,
}: {
  m: Monitoring;
  all: Monitoring | null;
  project: { key: string; name: string } | null;
  onClear: () => void;
  stale: boolean;
}) {
  const down = m.hosts.filter((h) => !h.up).length;
  const nothing = project && !m.hosts.length && !m.nomad.length && !m.probes.length && !m.grafana.alerts.length && !m.grafana.dashboards.length;
  return (
    <div className={clsx("space-y-5 transition-opacity", stale && "opacity-80")}>
      {project ? (
        <p className="flex flex-wrap items-center gap-2 text-[12.5px] text-fg-2">
          Showing what belongs to{" "}
          <Link to={`/p/${project.key}`} className="font-medium text-fg hover:text-accent">
            {project.name}
          </Link>
          : its servers, its endpoints' probes and dashboards that name it.
          <Button size="sm" variant="ghost" onClick={onClear}>
            Show all
          </Button>
        </p>
      ) : null}
      {nothing ? (
        <EmptyState compact title={`Nothing monitored for ${project!.name}`}>
          Link servers to the project (its overview → Servers) and add endpoints, and they show up here.
        </EmptyState>
      ) : null}
      <section aria-labelledby="mon-hosts" className="space-y-3">
        <h2 id="mon-hosts" className="flex items-center gap-2 text-[13.5px] font-semibold">
          <Gauge className="size-4 text-fg-3" aria-hidden /> Hosts
          <span className="font-normal text-fg-3">
            · {m.hosts.length - down}/{m.hosts.length} reporting
            {all ? ` · ${m.hosts.length} of ${all.hosts.length} hosts` : null}
          </span>
        </h2>
        {m.hosts.length ? (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
            {m.hosts.map((h) => (
              <HostCard key={h.name} host={h} />
            ))}
          </div>
        ) : project ? (
          <EmptyState compact title="No hosts for this project">None of its linked servers report host metrics.</EmptyState>
        ) : (
          <EmptyState compact title="No host metrics">VictoriaMetrics returned nothing — is vmagent running on the hosts?</EmptyState>
        )}
      </section>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <AlertsPanel m={m} total={all ? all.grafana.alerts.length : null} />
        <DashboardsPanel m={m} total={all ? all.grafana.dashboards.length : null} />
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-5">
        <div className="lg:col-span-3">
          <NomadPanel hosts={m.nomad} total={all ? all.nomad.length : null} />
        </div>
        <div className="lg:col-span-2">
          <ProbesPanel probes={m.probes} total={all ? all.probes.length : null} />
        </div>
      </div>
    </div>
  );
}

function AlertsPanel({ m, total }: { m: Monitoring; total: number | null }) {
  const g = m.grafana;
  const alerts = sortAlerts(g.alerts);
  const firing = alerts.filter((a) => a.state === "firing").length;
  return (
    <Panel
      title={
        <>
          Grafana alerts{" "}
          {g.connected ? (
            <span className="font-normal text-fg-3">
              · {firing ? `${firing} firing` : "all quiet"}
              {total !== null ? ` · ${alerts.length} of ${total}` : null}
            </span>
          ) : null}
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
          <CircleCheck className="size-4 text-good" aria-hidden /> Nothing firing or pending{total !== null ? " here" : ""}.
        </p>
      )}
      {g.connected && g.error ? <p className="border-t border-line px-3.5 py-2 text-[12px] text-critical-ink">{g.error}</p> : null}
    </Panel>
  );
}

function DashboardsPanel({ m, total }: { m: Monitoring; total: number | null }) {
  const byFolder = new Map<string, typeof m.grafana.dashboards>();
  for (const d of m.grafana.dashboards) {
    const list = byFolder.get(d.folder || "General") ?? [];
    list.push(d);
    byFolder.set(d.folder || "General", list);
  }
  return (
    <Panel
      title={
        <>
          Dashboards{" "}
          {total !== null ? (
            <span className="font-normal text-fg-3">
              · <OfTotal n={m.grafana.dashboards.length} total={total} />
            </span>
          ) : null}
        </>
      }
      icon={<LayoutDashboard />}
      id="mon-dashboards"
    >
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

function NomadPanel({ hosts, total }: { hosts: NomadHost[]; total: number | null }) {
  return (
    <Panel
      title={
        <>
          Nomad jobs{" "}
          {total !== null ? (
            <span className="font-normal text-fg-3">
              · <OfTotal n={hosts.length} total={total} /> hosts
            </span>
          ) : null}
        </>
      }
      icon={<ServerCog />}
      id="mon-nomad"
      bodyClassName="p-0"
    >
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
              <NomadJobList host={h} />
            </div>
          ))}
        </div>
      ) : (
        <p className="p-4 text-[13px] text-fg-3">{total !== null ? "None of this project's servers run Nomad." : "No Nomad hosts configured."}</p>
      )}
    </Panel>
  );
}

function ProbesPanel({ probes, total }: { probes: Probe[]; total: number | null }) {
  return (
    <Panel
      title={
        <>
          Blackbox probes{" "}
          {total !== null ? (
            <span className="font-normal text-fg-3">
              · <OfTotal n={probes.length} total={total} />
            </span>
          ) : null}
        </>
      }
      icon={<ShieldCheck />}
      id="mon-probes"
      bodyClassName="p-0"
    >
      {probes.length ? <ProbeList probes={probes} /> : <p className="p-4 text-[13px] text-fg-3">No probes.</p>}
    </Panel>
  );
}

