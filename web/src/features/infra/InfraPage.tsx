// Shared infrastructure: the hosts every project runs on, and every endpoint
// being watched, in one place. A server lists the projects that use it, which
// is the question to ask before touching one, and says how it is doing right
// now (metrics, Nomad jobs, its projects' endpoints) — red only when
// something is broken now; production-critical is a badge, not a colour.

import clsx from "clsx";
import { ChevronDown, ChevronRight, Cpu, Globe, Network, Pencil, Plus, Server as ServerIcon, ShieldAlert } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { useEndpoints, useMonitoring, useServers } from "@/api/hooks";
import type { Endpoint, Monitoring, Server } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Markdown } from "@/components/ui/Markdown";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { Skeleton, SkeletonRows } from "@/components/ui/Skeleton";
import { Segmented } from "@/components/ui/Tabs";
import { environmentTone, isFailing, serverPath } from "@/lib/infra";
import { metricsOutage } from "@/lib/monitoring";
import { EndpointsTable } from "./endpoints";
import { serverLiveStatus, sortEndpoints, type ServerLiveStatus } from "./live";
import { ServerDialog } from "./ServerDialog";
import { ShellIconButton } from "./shell";

/** Projects a card lists before "+N more". */
const CARD_PROJECTS = 3;

export function InfraPage() {
  const servers = useServers();
  const endpoints = useEndpoints();
  const mon = useMonitoring();
  const [creating, setCreating] = useState(false);
  // null: not picked yet — "Failing" while anything is failing, "All" otherwise.
  const [picked, setFilter] = useState<"all" | "down" | null>(null);
  const all = endpoints.data ?? [];
  const down = all.filter((e) => e.last_status === "down").length;
  const failing = all.filter(isFailing).length;
  const filter = picked ?? (failing ? "down" : "all");
  // "Failing": confirmed down, and those still being rechecked (warning).
  const eps = sortEndpoints(all.filter((e) => filter === "all" || isFailing(e)));

  return (
    <div className="mx-auto max-w-[1400px] space-y-6">
      <PageHeader
        title="Infrastructure"
        subtitle="Shared hosts across every project, and what is watching the public edges."
        actions={
          <Button variant="primary" size="sm" onClick={() => setCreating(true)}>
            <Plus className="size-3.5" aria-hidden /> Server
          </Button>
        }
      />

      {servers.data || endpoints.data ? <InfraSummary servers={servers.data} endpoints={endpoints.data} m={mon.data} /> : null}

      <section aria-labelledby="infra-servers" className="scroll-mt-4 space-y-3">
        <h2 id="infra-servers" className="flex items-center gap-2 text-[13.5px] font-semibold">
          <ServerIcon className="size-4 text-fg-3" aria-hidden /> Servers
          {servers.data ? <span className="text-fg-3">· {servers.data.length}</span> : null}
        </h2>
        {servers.isPending ? (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {Array.from({ length: 6 }, (_, i) => (
              <Skeleton key={i} className="h-40" />
            ))}
          </div>
        ) : servers.error ? (
          <ErrorState error={servers.error} onRetry={() => servers.refetch()} />
        ) : servers.data.length ? (
          <div className="grid grid-cols-1 items-start gap-2 sm:grid-cols-2 sm:gap-3 xl:grid-cols-3">
            {servers.data.map((s) => (
              <ServerCard key={s.id} server={s} live={serverLiveStatus(s, mon.data, all)} hasLive={!!mon.data} />
            ))}
          </div>
        ) : (
          <EmptyState icon={<ServerIcon />} title="No servers yet" />
        )}
      </section>

      <Panel
        title={
          <>
            Endpoints{" "}
            {endpoints.data ? (
              <span className="font-normal text-fg-3">
                · {endpoints.data.length - down}/{endpoints.data.length} up
              </span>
            ) : null}
          </>
        }
        icon={<Globe />}
        id="infra-endpoints"
        className="scroll-mt-4"
        bodyClassName="p-0"
        actions={
          <Segmented
            size="sm"
            label="Endpoint filter"
            value={filter}
            onChange={setFilter}
            items={[
              { value: "all", label: "All" },
              { value: "down", label: `Failing${failing ? ` · ${failing}` : ""}` },
            ]}
          />
        }
      >
        {endpoints.isPending ? (
          <SkeletonRows rows={5} className="p-3" />
        ) : eps.length ? (
          <>
            <p className="border-b border-line px-3 py-1.5 text-[11.5px] text-fg-3">
              Strip: last 24 hours in half-hour windows — green all checks passed, amber a check failed but passed on a recheck (or is being rechecked), red the endpoint was down, grey no checks.
            </p>
            <EndpointsTable endpoints={eps} showProject strip />
          </>
        ) : (
          <p className="p-4 text-[13px] text-fg-3">{filter === "down" ? "Nothing is failing." : "No endpoints yet — add them from a project's overview."}</p>
        )}
      </Panel>

      {creating ? <ServerDialog onClose={() => setCreating(false)} /> : null}
    </div>
  );
}

/** "10 servers · 2 not reporting | Endpoints 24/25 up · 1 down", each half a link to its section. */
function InfraSummary({ servers, endpoints, m }: { servers: Server[] | undefined; endpoints: Endpoint[] | undefined; m: Monitoring | undefined }) {
  const silent = m ? m.hosts.filter((h) => !h.up).length : 0;
  const outage = m ? metricsOutage(silent, m.hosts.length) : false;
  const down = endpoints?.filter((e) => e.last_status === "down").length ?? 0;
  const warning = endpoints?.filter((e) => e.last_status === "warning").length ?? 0;
  const link = "rounded-sm hover:text-fg hover:underline";
  return (
    <p aria-label="Summary" className="-mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px] text-fg-2">
      {servers ? (
        <a href="#infra-servers" className={link}>
          {servers.length} server{servers.length === 1 ? "" : "s"}
          {silent ? (
            <span className={outage ? "text-fg-3" : "font-medium text-critical-ink"} title={outage ? "Most hosts are silent: likely the metrics source" : undefined}>
              {" "}
              · {silent} not reporting
            </span>
          ) : null}
        </a>
      ) : null}
      {servers && endpoints ? <span className="text-line-strong" aria-hidden>|</span> : null}
      {endpoints ? (
        <a href="#infra-endpoints" className={link}>
          Endpoints {endpoints.length - down}/{endpoints.length} up
          {down ? <span className="font-medium text-critical-ink"> · {down} down</span> : null}
          {warning ? <span className="font-medium text-fg"> · {warning} failing</span> : null}
        </a>
      ) : null}
    </p>
  );
}

/** The live line of a card: reporting dot, usage, and what is broken. */
function LiveLine({ live }: { live: ServerLiveStatus }) {
  const h = live.host;
  const pct = (label: string, v: number | null) => (v === null ? null : `${label} ${Math.round(v)}%`);
  const usage = h?.up ? [pct("CPU", h.cpu_pct), pct("mem", h.mem_pct), pct("disk", h.disk_pct)].filter(Boolean).join(" · ") : "";
  const problems = [
    live.reporting === false ? "not reporting" : "",
    live.nomadUnreachable ? "Nomad unreachable" : "",
    live.failingJobs.length ? `${live.failingJobs.length} job${live.failingJobs.length === 1 ? "" : "s"} down` : "",
    live.downEndpoints.length ? `${live.downEndpoints.length} endpoint${live.downEndpoints.length === 1 ? "" : "s"} down` : "",
  ].filter(Boolean);
  return (
    <p className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[12px] text-fg-3">
      <LiveDot live={live} />
      <span>{live.reporting === true ? "reporting" : live.reporting === false ? null : "no live metrics"}</span>
      {usage ? <span className="tabular text-fg-2">{usage}</span> : null}
      {problems.length ? (
        <span className="font-medium text-critical-ink" title={[...live.failingJobs.map((j) => j.id), ...live.downEndpoints.map((e) => e.name)].join(", ") || undefined}>
          {problems.join(" · ")}
        </span>
      ) : null}
      {live.waitingJobs.length ? <span className="text-fg-2">{live.waitingJobs.length} job{live.waitingJobs.length === 1 ? "" : "s"} waiting</span> : null}
    </p>
  );
}

function LiveDot({ live }: { live: ServerLiveStatus }) {
  const label =
    live.level === "fail" ? "Broken now" : live.level === "warn" ? "Under pressure" : live.reporting === null ? "No live metrics" : "Healthy";
  return (
    <span
      role="img"
      aria-label={label}
      title={label}
      className={clsx(
        "size-2 shrink-0 rounded-full",
        live.level === "fail" ? "bg-critical" : live.level === "warn" ? "bg-warning" : live.reporting === null ? "bg-fg-3" : "bg-good",
      )}
    />
  );
}

/**
 * One server. From `sm` the full card; below it a compact row (name, badges,
 * live dot, one line of role, chevron to its page). The border is red only
 * when something is broken now, amber under pressure; a critical server gets
 * a strong neutral border and its badge.
 */
function ServerCard({ server: s, live, hasLive }: { server: Server; live: ServerLiveStatus; hasLive: boolean }) {
  const [editing, setEditing] = useState(false);
  const [showNotes, setShowNotes] = useState(false);
  const extra = s.projects.length - CARD_PROJECTS;
  return (
    <article
      data-level={live.level}
      className={clsx(
        "flex flex-col gap-2.5 rounded-xl border bg-surface px-3 py-2.5 sm:p-4",
        live.level === "fail" ? "border-critical/50" : live.level === "warn" ? "border-warning/60" : s.critical ? "border-line-strong" : "border-line",
      )}
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            {hasLive ? (
              <span className="sm:hidden">
                <LiveDot live={live} />
              </span>
            ) : null}
            <h3 className="font-mono text-[14px] font-semibold">
              <Link to={serverPath(s.id)} className="hover:text-accent hover:underline">
                {s.name}
              </Link>
            </h3>
            <Badge tone={environmentTone(s.environment)} dot>
              {s.environment}
            </Badge>
            {s.critical ? (
              <Badge tone="critical" title="Production-critical: confirm before any change">
                <ShieldAlert className="size-3" aria-hidden /> critical
              </Badge>
            ) : null}
          </div>
          {s.role ? <p className="mt-1 truncate text-[12.5px] text-fg-2 sm:line-clamp-2 sm:whitespace-normal">{s.role}</p> : null}
        </div>
        <div className="hidden items-center sm:flex">
          <ShellIconButton server={s} />
          <Button size="icon-sm" variant="ghost" aria-label={`Edit ${s.name}`} onClick={() => setEditing(true)}>
            <Pencil className="size-3.5" />
          </Button>
        </div>
        <Link to={serverPath(s.id)} aria-label={`Open ${s.name}`} className="-mr-1 grid size-7 shrink-0 place-items-center self-center rounded-md text-fg-3 hover:bg-surface-2 sm:hidden">
          <ChevronRight className="size-4" aria-hidden />
        </Link>
      </div>
      {hasLive ? (
        <div className="hidden sm:block">
          <LiveLine live={live} />
        </div>
      ) : null}
      <dl className="hidden grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-[12px] sm:grid">
        {s.public_address ? (
          <>
            <dt className="text-fg-3">
              <Globe className="inline size-3" aria-hidden /> Public
            </dt>
            <dd className="truncate font-mono text-fg-2">{s.public_address}</dd>
          </>
        ) : null}
        {s.tailscale_ip ? (
          <>
            <dt className="text-fg-3">
              <Network className="inline size-3" aria-hidden /> Tailnet
            </dt>
            <dd className="truncate font-mono text-fg-2">{s.tailscale_ip}</dd>
          </>
        ) : null}
        {s.arch || s.provider ? (
          <>
            <dt className="text-fg-3">
              <Cpu className="inline size-3" aria-hidden /> Host
            </dt>
            <dd className="truncate text-fg-2">{[s.arch, s.provider].filter(Boolean).join(" · ")}</dd>
          </>
        ) : null}
      </dl>
      {s.tags.length ? (
        <div className="hidden flex-wrap gap-1 sm:flex">
          {s.tags.map((t) => (
            <span key={t} className="rounded bg-surface-2 px-1.5 text-[11px] text-fg-2">
              {t}
            </span>
          ))}
        </div>
      ) : null}
      {s.projects.length ? (
        <div className="hidden flex-wrap gap-x-3 gap-y-1 border-t border-line pt-2 sm:flex">
          {s.projects.slice(0, CARD_PROJECTS).map((p) => (
            <Link key={p.key} to={`/p/${p.key}`} className="inline-flex items-center gap-1.5 text-[12px] text-fg-2 hover:text-fg" title={p.role}>
              <ColorDot color={p.color} className="size-2" />
              {p.name}
              {p.role ? <span className="text-fg-3">· {p.role}</span> : null}
            </Link>
          ))}
          {extra > 0 ? (
            <Link to={serverPath(s.id)} className="text-[12px] text-fg-3 hover:text-fg-2" title={s.projects.slice(CARD_PROJECTS).map((p) => p.name).join(", ")}>
              +{extra} more
            </Link>
          ) : null}
        </div>
      ) : null}
      {s.notes.trim() ? (
        <div className="hidden sm:block">
          <button
            type="button"
            onClick={() => setShowNotes((v) => !v)}
            aria-expanded={showNotes}
            className="inline-flex items-center gap-1 text-[12px] text-fg-3 hover:text-fg-2"
          >
            <ChevronDown className={clsx("size-3.5 transition-transform", showNotes && "rotate-180")} aria-hidden />
            Notes
          </button>
          {showNotes ? <Markdown className="mt-1.5 text-[12.5px]">{s.notes}</Markdown> : null}
        </div>
      ) : null}
      {editing ? <ServerDialog server={s} onClose={() => setEditing(false)} /> : null}
    </article>
  );
}
