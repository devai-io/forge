// What Infrastructure shows about a server's live state, and the order the
// endpoint list comes in. Plain helpers, kept out of the components so Fast
// Refresh stays happy.

import type { Endpoint, HostMetrics, Monitoring, NomadJob, Server } from "@/api/types";
import { jobLevel, metricsOutage, serverLive, usageLevel, worseLevel, type Level } from "@/lib/monitoring";

const ENDPOINT_RANK: Record<Endpoint["last_status"], number> = { down: 0, warning: 1, unknown: 2, up: 3 };

/** Down first, then failing-being-rechecked, then unknown and paused, then up; by project and name within. */
export function sortEndpoints(endpoints: Endpoint[]): Endpoint[] {
  const rank = (e: Endpoint) => (e.enabled ? ENDPOINT_RANK[e.last_status] : 2);
  return [...endpoints].sort((a, b) => rank(a) - rank(b) || a.project_key.localeCompare(b.project_key) || a.name.localeCompare(b.name));
}

export type ServerLiveStatus = {
  /** fail: broken now (not reporting, a job down, Nomad unreachable, an endpoint down); warn: pressure or waiting jobs. */
  level: Level;
  host: HostMetrics | undefined;
  /** true reporting, false not reporting, null no metrics for it (or most hosts are silent: the source is the suspect). */
  reporting: boolean | null;
  failingJobs: NomadJob[];
  waitingJobs: NomadJob[];
  nomadUnreachable: boolean;
  downEndpoints: Endpoint[];
};

/**
 * A server's state right now from the monitoring snapshot and its projects'
 * endpoints. A silent host counts as broken unless most hosts are silent at
 * once — then the metrics source is the likely cause, and every card going
 * red would hide what is actually wrong.
 */
export function serverLiveStatus(s: Server, m: Monitoring | undefined, endpoints: Endpoint[]): ServerLiveStatus {
  const keys = new Set(s.projects.map((p) => p.key));
  const downEndpoints = endpoints.filter((e) => e.enabled && e.last_status === "down" && keys.has(e.project_key));
  const empty = { host: undefined, reporting: null, failingJobs: [], waitingJobs: [], nomadUnreachable: false };
  if (!m) return { ...empty, level: downEndpoints.length ? "fail" : "ok", downEndpoints };
  const { host, nomad } = serverLive(m, s);
  const outage = metricsOutage(m.hosts.filter((h) => !h.up).length, m.hosts.length);
  const reporting = !host ? null : host.up ? true : outage ? null : false;
  const jobs = nomad?.reachable ? nomad.jobs : [];
  const failingJobs = jobs.filter((j) => jobLevel(j) === "fail");
  const waitingJobs = jobs.filter((j) => jobLevel(j) === "warn");
  const nomadUnreachable = !!nomad && !nomad.reachable;
  let level: Level = reporting === false || failingJobs.length || nomadUnreachable || downEndpoints.length ? "fail" : "ok";
  if (waitingJobs.length) level = worseLevel(level, "warn");
  if (host?.up) for (const pct of [host.cpu_pct, host.mem_pct, host.disk_pct]) level = worseLevel(level, usageLevel(pct) === "ok" ? "ok" : "warn");
  return { level, host, reporting, failingJobs, waitingJobs, nomadUnreachable, downEndpoints };
}
