// One server: what Infrastructure knows about it (addresses, projects,
// notes) beside what Monitoring sees on it (metrics, Nomad jobs, alerts,
// dashboards), and a shell on it — a tmux session on one of your machines
// that runs ssh to the server.

import {
  ArrowLeft,
  BellRing,
  CircleCheck,
  Cpu,
  FolderKanban,
  Gauge,
  Globe,
  KeyRound,
  LayoutDashboard,
  Network,
  Pencil,
  ServerCog,
  ShieldAlert,
  StickyNote,
} from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useEndpoints, useMonitoring, useServers } from "@/api/hooks";
import type { Monitoring, Server } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Markdown } from "@/components/ui/Markdown";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton, SkeletonRows } from "@/components/ui/Skeleton";
import { AlertRow, ConnectGrafana, DashboardLink, NomadJobList, UsageBar } from "@/features/monitoring/parts";
import { environmentTone } from "@/lib/infra";
import { alertMentions, dashboardsMentioning, formatUptime, hostAliases, serverAliases, sortAlerts } from "@/lib/monitoring";
import { EndpointsTable } from "./endpoints";
import { ServerDialog } from "./ServerDialog";
import { ShellButton } from "./shell";

export function ServerPage() {
  const { id } = useParams();
  const servers = useServers();
  const server = servers.data?.find((s) => String(s.id) === id);
  return (
    <div className="mx-auto max-w-[1400px] space-y-5">
      <Link to="/infra" className="inline-flex items-center gap-1 text-[12.5px] text-fg-3 hover:text-fg-2">
        <ArrowLeft className="size-3.5" aria-hidden /> Infrastructure
      </Link>
      {servers.isPending ? (
        <div className="space-y-3">
          <Skeleton className="h-10 w-72" />
          <Skeleton className="h-48" />
        </div>
      ) : servers.error ? (
        <ErrorState error={servers.error} onRetry={() => servers.refetch()} />
      ) : !server ? (
        <EmptyState title="No such server" className="mx-auto mt-10 max-w-md">
          It may have been deleted. <Link to="/infra" className="text-accent hover:underline">See all servers</Link>.
        </EmptyState>
      ) : (
        <ServerBody server={server} />
      )}
    </div>
  );
}

function ServerBody({ server: s }: { server: Server }) {
  const navigate = useNavigate();
  const mon = useMonitoring();
  const endpoints = useEndpoints();
  const [editing, setEditing] = useState(false);
  const keys = new Set(s.projects.map((p) => p.key));
  const eps = (endpoints.data ?? []).filter((e) => keys.has(e.project_key));

  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span className="font-mono">{s.name}</span>
            <Badge tone={environmentTone(s.environment)} dot>
              {s.environment}
            </Badge>
            {s.critical ? (
              <Badge tone="critical" title="Production-critical: confirm before any change">
                <ShieldAlert className="size-3" aria-hidden /> critical
              </Badge>
            ) : null}
          </span>
        }
        subtitle={s.role || "No role written down yet."}
        actions={
          <>
            <ShellButton server={s} />
            <Button size="sm" variant="subtle" onClick={() => setEditing(true)}>
              <Pencil className="size-3.5" aria-hidden /> Edit
            </Button>
          </>
        }
      />
      {s.critical ? (
        <p className="flex items-center gap-2 rounded-lg border border-critical/40 bg-critical/8 px-3 py-2 text-[12.5px] text-critical-ink">
          <ShieldAlert className="size-4 shrink-0" aria-hidden /> Production-critical: confirm before changing anything on this server.
        </p>
      ) : null}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          {mon.isPending ? (
            <SkeletonRows rows={4} />
          ) : mon.error ? (
            <ErrorState error={mon.error} onRetry={() => mon.refetch()} />
          ) : (
            <LiveState server={s} m={mon.data} />
          )}
          <Panel title="Endpoints of its projects" icon={<Globe />} id="server-endpoints" bodyClassName="p-0">
            {endpoints.isPending ? (
              <SkeletonRows rows={3} className="p-3" />
            ) : eps.length ? (
              <EndpointsTable endpoints={eps} showProject />
            ) : (
              <p className="p-4 text-[13px] text-fg-3">{s.projects.length ? "Its projects have no endpoints." : "No project uses this server yet."}</p>
            )}
          </Panel>
        </div>

        <div className="space-y-4">
          <DetailsPanel server={s} />
          <Panel title="Projects" icon={<FolderKanban />} id="server-projects">
            {s.projects.length ? (
              <ul className="space-y-0.5">
                {s.projects.map((p) => (
                  <li key={p.key}>
                    <Link to={`/p/${p.key}`} className="flex items-center gap-2 rounded-md px-2 py-1.5 text-[13px] hover:bg-surface-2">
                      <ColorDot color={p.color} className="size-2.5" />
                      <span className="min-w-0 flex-1 truncate">{p.name}</span>
                      {p.role ? <span className="truncate text-[12px] text-fg-3">{p.role}</span> : null}
                    </Link>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="px-2 py-1.5 text-[13px] text-fg-3">Not linked to a project. Link it from a project's overview.</p>
            )}
          </Panel>
          {mon.data ? <ServerDashboards server={s} m={mon.data} /> : null}
          <Panel title="Notes" icon={<StickyNote />} id="server-notes" bodyClassName="px-4 py-3">
            {s.notes.trim() ? <Markdown className="text-[13px]">{s.notes}</Markdown> : <p className="text-[13px] text-fg-3">No notes.</p>}
          </Panel>
        </div>
      </div>
      {editing ? <ServerDialog server={s} onClose={() => setEditing(false)} onDeleted={() => navigate("/infra")} /> : null}
    </>
  );
}

function DetailsPanel({ server: s }: { server: Server }) {
  const rows: [React.ReactNode, string, string][] = [
    [<Globe key="i" className="inline size-3" aria-hidden />, "Public", s.public_address],
    [<Network key="i" className="inline size-3" aria-hidden />, "Tailnet", s.tailscale_ip],
    [<Cpu key="i" className="inline size-3" aria-hidden />, "Host", [s.arch, s.provider].filter(Boolean).join(" · ")],
    [<KeyRound key="i" className="inline size-3" aria-hidden />, "SSH", s.ssh_target || s.name],
  ];
  return (
    <Panel title="Details" icon={<ServerCog />} id="server-details" bodyClassName="px-4 py-3 space-y-2.5">
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[12.5px]">
        {rows
          .filter(([, , v]) => v)
          .map(([icon, label, value]) => (
            <div key={label} className="contents">
              <dt className="text-fg-3">
                {icon} {label}
              </dt>
              <dd className="truncate font-mono text-fg-2" title={value}>
                {value}
              </dd>
            </div>
          ))}
      </dl>
      {s.tags.length ? (
        <div className="flex flex-wrap gap-1">
          {s.tags.map((t) => (
            <span key={t} className="rounded bg-surface-2 px-1.5 text-[11px] text-fg-2">
              {t}
            </span>
          ))}
        </div>
      ) : null}
    </Panel>
  );
}

/** Metrics, Nomad jobs and alerts for this one host. */
function LiveState({ server: s, m }: { server: Server; m: Monitoring }) {
  const aliases = serverAliases(s);
  const host = m.hosts.find((h) => h.server_id === s.id) ?? m.hosts.find((h) => hostAliases(h.name).some((a) => aliases.has(a)));
  const nomad = m.nomad.find((n) => hostAliases(n.host).some((a) => aliases.has(a)));
  const alerts = sortAlerts(m.grafana.alerts.filter((a) => alertMentions(a, aliases)));
  const firing = alerts.filter((a) => a.state === "firing").length;
  return (
    <>
      <Panel
        title={
          <>
            Host metrics{" "}
            <span className="font-normal text-fg-3">
              · fetched <RelativeTime iso={m.fetched_at} />
            </span>
          </>
        }
        icon={<Gauge />}
        id="server-metrics"
        bodyClassName="px-4 py-3"
      >
        {host ? (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12.5px]">
              <span className="inline-flex items-center gap-1.5">
                <span className={host.up ? "size-2 rounded-full bg-good" : "size-2 rounded-full bg-critical"} aria-hidden />
                <span className={host.up ? "text-fg-2" : "font-medium text-critical-ink"}>{host.up ? "up" : "not reporting"}</span>
              </span>
              <span className="text-fg-3">
                uptime <span className="tabular text-fg-2">{formatUptime(host.uptime_seconds)}</span>
              </span>
              <span className="text-fg-3">
                load <span className="tabular text-fg-2">{host.load1 === null ? "—" : host.load1.toFixed(2)}</span>
              </span>
              {!host.up && host.last_seen ? (
                <span className="text-fg-3">
                  last seen <RelativeTime iso={host.last_seen} />
                </span>
              ) : null}
            </div>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
              <UsageBar label="CPU" pct={host.cpu_pct} />
              <UsageBar label="Memory" pct={host.mem_pct} />
              <UsageBar label="Disk /" pct={host.disk_pct} />
            </div>
          </div>
        ) : (
          <p className="text-[13px] text-fg-3">No host metrics for {s.name} — VictoriaMetrics has no series with host="{s.name}".</p>
        )}
      </Panel>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <Panel
          title={
            <>
              Nomad jobs {nomad?.reachable ? <span className="font-normal text-fg-3">· {nomad.jobs.length}</span> : null}
            </>
          }
          icon={<ServerCog />}
          id="server-nomad"
          bodyClassName="px-3.5 py-2.5"
        >
          {nomad ? <NomadJobList host={nomad} /> : <p className="text-[13px] text-fg-3">No Nomad here (or no tailnet IP to reach it on).</p>}
        </Panel>
        <Panel
          title={
            <>
              Alerts{" "}
              {m.grafana.connected ? <span className="font-normal text-fg-3">· {firing ? `${firing} firing` : "all quiet"}</span> : null}
            </>
          }
          icon={<BellRing />}
          id="server-alerts"
          bodyClassName="p-0"
        >
          {!m.grafana.connected ? (
            <ConnectGrafana url={m.grafana.url} error={m.grafana.error} />
          ) : alerts.length ? (
            <ul className="divide-y divide-line">
              {alerts.map((a, i) => (
                <AlertRow key={`${a.name}-${i}`} alert={a} />
              ))}
            </ul>
          ) : (
            <p className="flex items-center gap-2 p-4 text-[13px] text-fg-2">
              <CircleCheck className="size-4 text-good" aria-hidden /> Nothing firing or pending on {s.name}.
            </p>
          )}
        </Panel>
      </div>
    </>
  );
}

function ServerDashboards({ server: s, m }: { server: Server; m: Monitoring }) {
  const matches = dashboardsMentioning(m.grafana.dashboards, [s.name]);
  if (!matches.length) return null;
  return (
    <Panel title="Dashboards" icon={<LayoutDashboard />} id="server-dashboards">
      {matches.map((d) => (
        <DashboardLink key={d.uid || d.url} title={d.folder ? `${d.title} · ${d.folder}` : d.title} url={d.url} />
      ))}
      <p className="px-2 pt-1 text-[11px] text-fg-3">Grafana — over Tailscale.</p>
    </Panel>
  );
}

