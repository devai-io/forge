// Monitoring display helpers.

import type { Alert, Endpoint, GrafanaDashboard, HostMetrics, Monitoring, NomadHost, NomadJob, Probe, Server } from "@/api/types";

export type Level = "ok" | "warn" | "fail";

/** Resource usage thresholds: warn at 80 %, fail at 90 % (same as the check-up). */
export function usageLevel(pct: number | null | undefined): Level {
  if (pct === null || pct === undefined) return "ok";
  if (pct >= 90) return "fail";
  if (pct >= 80) return "warn";
  return "ok";
}

/** TLS certificates: fail under a week, warn under three. */
export function sslLevel(days: number | null | undefined): Level {
  if (days === null || days === undefined) return "ok";
  if (days < 7) return "fail";
  if (days < 21) return "warn";
  return "ok";
}

/** 93784 → "1d 2h", 3700 → "1h 1m", 59 → "59s". */
export function formatUptime(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined) return "—";
  const s = Math.max(0, Math.floor(seconds));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${s}s`;
}

const ALERT_RANK: Record<Alert["state"], number> = { firing: 0, pending: 1, normal: 2 };

/** Firing first, then pending; newest first within a state. */
export function sortAlerts(alerts: Alert[]): Alert[] {
  return [...alerts].sort(
    (a, b) => ALERT_RANK[a.state] - ALERT_RANK[b.state] || (b.since ?? "").localeCompare(a.since ?? ""),
  );
}

/**
 * Best-effort: dashboards whose title or folder mentions the project's name or
 * key as a word ("Shop launch funnel", folder "Ops"). Two-letter keys
 * only match whole words so "KART" does not pick up "sources".
 */
export function dashboardsForProject(
  dashboards: GrafanaDashboard[],
  project: { key: string; name: string },
): GrafanaDashboard[] {
  return dashboardsMentioning(dashboards, [project.name, project.key]);
}

/** Dashboards whose title or folder names one of the terms as a word. */
export function dashboardsMentioning(dashboards: GrafanaDashboard[], names: string[]): GrafanaDashboard[] {
  const terms = names
    .map((t) => t.trim().toLowerCase())
    .filter((t, i, all) => t.length >= 2 && all.indexOf(t) === i);
  const escape = (t: string) => t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const res = terms.map((t) => new RegExp(`(^|[^a-z0-9])${escape(t)}([^a-z0-9]|$)`, "i"));
  return dashboards.filter((d) => res.some((re) => re.test(d.title) || re.test(d.folder)));
}

// ── Scoping to a project or a server ───────────────────────────────────────

const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/;

/**
 * The names a host label or URL may refer to a machine by: "web-1.example.com:9100"
 * → ["web-1.example.com", "web-1"]. Lowercased, port and scheme dropped; the
 * first DNS label is added for hostnames (never for IP addresses).
 */
export function hostAliases(value: string): string[] {
  let v = value.trim().toLowerCase();
  if (!v) return [];
  v = v.replace(/^[a-z][a-z0-9+.-]*:\/\//, "").replace(/[/?#].*$/, "").replace(/^[^@]*@/, "");
  if (v.startsWith("[")) v = v.slice(1, v.indexOf("]") > 0 ? v.indexOf("]") : undefined);
  else if ((v.match(/:/g) ?? []).length === 1) v = v.split(":")[0]!;
  if (!v) return [];
  const out = [v];
  if (!IPV4.test(v) && !v.includes(":") && v.includes(".")) out.push(v.split(".")[0]!);
  return out;
}

/** Every name a server goes by: its name, addresses and SSH target host. */
export function serverAliases(server: Pick<Server, "name" | "public_address" | "tailscale_ip"> & { ssh_target?: string }): Set<string> {
  const out = new Set<string>();
  for (const v of [server.name, server.public_address, server.tailscale_ip, server.ssh_target ?? ""]) {
    for (const a of hostAliases(v)) out.add(a);
  }
  return out;
}

const ALERT_HOST_LABELS = ["host", "instance", "nodename", "hostname", "node", "server", "job"];

/** Whether an alert's host-ish labels name one of these aliases. */
export function alertMentions(alert: Alert, aliases: Set<string>): boolean {
  return ALERT_HOST_LABELS.some((k) => {
    const v = alert.labels[k];
    return !!v && hostAliases(v).some((a) => aliases.has(a));
  });
}

/** The URL's host, lowercased; "" when it does not parse. */
export function urlHost(url: string): string {
  try {
    return new URL(url).hostname.toLowerCase();
  } catch {
    return "";
  }
}

export type MonitoringScope = {
  /** Hosts with one of these server ids (or names) are in scope. */
  serverIds: Set<number>;
  aliases: Set<string>;
  /** Probes whose URL host is one of these. */
  endpointHosts: Set<string>;
  /** Dashboards naming one of these terms. */
  terms: string[];
};

/** What on the Monitoring page belongs to a project: its servers, endpoints and name. */
export function projectScope(
  project: { key: string; name: string },
  servers: Server[],
  endpoints: Pick<Endpoint, "url" | "project_key">[],
): MonitoringScope {
  const mine = servers.filter((s) => s.projects.some((p) => p.key === project.key));
  const aliases = new Set<string>();
  for (const s of mine) for (const a of serverAliases(s)) aliases.add(a);
  return {
    serverIds: new Set(mine.map((s) => s.id)),
    aliases,
    endpointHosts: new Set(endpoints.filter((e) => e.project_key === project.key).map((e) => urlHost(e.url)).filter(Boolean)),
    terms: [project.name, project.key],
  };
}

export const hostInScope = (h: HostMetrics, scope: MonitoringScope) =>
  (h.server_id !== null && scope.serverIds.has(h.server_id)) || hostAliases(h.name).some((a) => scope.aliases.has(a));
export const nomadInScope = (h: NomadHost, scope: MonitoringScope) => hostAliases(h.host).some((a) => scope.aliases.has(a));
export const probeInScope = (p: Probe, scope: MonitoringScope) => {
  const host = urlHost(p.url);
  return !!host && (scope.endpointHosts.has(host) || scope.aliases.has(host));
};

/** The monitoring snapshot narrowed to a scope; the shape stays the same. */
export function scopeMonitoring(m: Monitoring, scope: MonitoringScope): Monitoring {
  return {
    ...m,
    hosts: m.hosts.filter((h) => hostInScope(h, scope)),
    nomad: m.nomad.filter((h) => nomadInScope(h, scope)),
    probes: m.probes.filter((p) => probeInScope(p, scope)),
    grafana: {
      ...m.grafana,
      alerts: m.grafana.alerts.filter((a) => alertMentions(a, scope.aliases)),
      dashboards: dashboardsMentioning(m.grafana.dashboards, scope.terms),
    },
  };
}

/** One service in trouble, for the dashboard's status widget. */
export type ServiceIssue = {
  key: string;
  level: "warn" | "fail";
  title: string;
  detail: string;
  to: string;
  /** The endpoint's project, for endpoint issues. */
  project_key?: string;
  /** The host it is about (a server name or address), for host, Nomad and alert issues. */
  host?: string;
  /** The host is a production or critical server (set by prodFirst). */
  prod?: boolean;
};

/**
 * Marks issues on production or critical servers and sorts them first within
 * their level: a failing production host outranks a failing build box.
 */
export function prodFirst(issues: ServiceIssue[], servers: Pick<Server, "name" | "tailscale_ip" | "public_address" | "environment" | "critical">[]): ServiceIssue[] {
  const prod = new Set<string>();
  for (const s of servers)
    if (s.critical || s.environment === "production") for (const n of [s.name, s.tailscale_ip, s.public_address]) if (n) prod.add(n);
  const marked = issues.map((i) => ({ ...i, prod: !!i.host && prod.has(i.host) }));
  const rank = (i: ServiceIssue) => (i.level === "fail" ? 0 : 2) + (i.prod ? 0 : 1);
  return marked.sort((a, b) => rank(a) - rank(b));
}

/**
 * Every service that is failing or about to: endpoints (down, or failing and
 * being rechecked), hosts not reporting or out of disk, Nomad service jobs
 * not running, and Grafana alerts — the same rules as the check-up. Failing
 * first.
 */
export function serviceIssues(
  endpoints: { down: Endpoint[]; warning: Endpoint[] },
  mon: Monitoring | undefined,
): ServiceIssue[] {
  const out: ServiceIssue[] = [];
  const why = (e: Endpoint) => (e.last_code ? `HTTP ${e.last_code}` : e.last_error || "");
  for (const e of endpoints.down)
    out.push({ key: `e${e.id}`, level: "fail", title: `${e.name} is down`, detail: [e.project_key, why(e)].filter(Boolean).join(" · "), to: `/p/${e.project_key}`, project_key: e.project_key });
  for (const e of endpoints.warning)
    out.push({ key: `w${e.id}`, level: "warn", title: `${e.name} is failing`, detail: [e.project_key, why(e), "rechecking"].filter(Boolean).join(" · "), to: `/p/${e.project_key}`, project_key: e.project_key });
  if (mon) {
    for (const h of mon.hosts) {
      if (!h.up) out.push({ key: `h${h.name}`, level: "fail", title: `${h.name} is not reporting`, detail: "host", to: "/monitoring", host: h.name });
      else if (usageLevel(h.disk_pct) !== "ok")
        out.push({ key: `d${h.name}`, level: usageLevel(h.disk_pct) as "warn" | "fail", title: `${h.name} disk ${Math.round(h.disk_pct ?? 0)}% full`, detail: "host", to: "/monitoring", host: h.name });
    }
    for (const n of mon.nomad) {
      if (!n.reachable) {
        out.push({ key: `n${n.host}`, level: "fail", title: `Nomad on ${n.host} is unreachable`, detail: n.error, to: "/monitoring", host: n.host });
        continue;
      }
      for (const j of n.jobs) {
        if (j.type !== "service" && j.type !== "system") continue; // batch jobs are "dead" when done
        const key = `j${n.host}:${j.id}`;
        const host = n.host;
        if (j.status === "dead") out.push({ key, level: "fail", title: `${j.id} is dead`, detail: `Nomad · ${n.host}`, to: "/monitoring", host });
        else if (j.running === 0) out.push({ key, level: "fail", title: `${j.id} has nothing running`, detail: `Nomad · ${n.host} · ${j.status}`, to: "/monitoring", host });
        else if (j.queued > 0) out.push({ key, level: "warn", title: `${j.id} has ${j.queued} waiting`, detail: `Nomad · ${n.host}`, to: "/monitoring", host });
      }
    }
    for (const a of mon.grafana.alerts) {
      if (a.state === "normal") continue;
      const where = a.labels?.host || a.labels?.instance || "";
      out.push({
        key: `a${a.name}:${where}`,
        level: a.state === "firing" && (a.severity === "critical" || a.severity === "") ? "fail" : "warn",
        title: where ? `${a.name} on ${where}` : a.name,
        detail: [`alert · ${a.state}`, a.summary].filter(Boolean).join(" · "),
        to: "/monitoring",
        ...(where ? { host: where } : {}),
      });
    }
  }
  return out.sort((a, b) => Number(a.level === "warn") - Number(b.level === "warn"));
}

/**
 * The project an investigation of these issues runs in: the one most of them
 * belong to — an endpoint's own project, or the projects linked to the server
 * a host, Nomad or alert issue is about. Ties go to the project linked to the
 * most servers (usually the one that holds the infrastructure).
 */
export function investigationProject(issues: ServiceIssue[], servers: Server[]): string | undefined {
  const votes = new Map<string, number>();
  const vote = (key: string) => votes.set(key, (votes.get(key) ?? 0) + 1);
  const byAlias = new Map<string, Server>();
  for (const s of servers) for (const a of serverAliases(s)) byAlias.set(a, s);
  for (const i of issues) {
    if (i.project_key) vote(i.project_key);
    else if (i.host) {
      const server = hostAliases(i.host).map((a) => byAlias.get(a)).find(Boolean);
      for (const p of server?.projects ?? []) vote(p.key);
    }
  }
  const reach = (key: string) => servers.filter((s) => s.projects.some((p) => p.key === key)).length;
  let best: string | undefined;
  for (const [key, n] of votes) {
    const b = best ? (votes.get(best) ?? 0) : -1;
    if (n > b || (n === b && best && reach(key) > reach(best))) best = key;
  }
  return best;
}

/** The prompt for an agent run that looks into these issues. */
export function investigationPrompt(issues: ServiceIssue[], now = new Date()): string {
  const one = issues.length === 1;
  const lines = issues.map((i) => `- [${i.level === "fail" ? "failing" : "warning"}] ${i.title}${i.detail ? ` (${i.detail})` : ""}`);
  return [
    `Forge reports ${one ? "this service issue" : `these ${issues.length} service issues`} (as of ${now.toISOString().slice(0, 16).replace("T", " ")} UTC):`,
    "",
    ...lines,
    "",
    `Look into ${one ? "it" : "each one"}:`,
    "1. Find out what is actually wrong and why: logs, recent deploys and commits, CI, the host and its Nomad jobs. This project's Forge context (forge_context) and its infra notes say how to reach the servers; forge_checkup shows today's check-up.",
    "2. Say whether it is still happening, the most likely root cause, and how sure you are.",
    "3. Propose the fix. Read-only checks are fine; make no production change (deploys, restarts, database writes, host rebuilds) without my explicit go-ahead, and follow the project's change-control notes.",
    "4. File a Forge task (forge_create_task) for each fix that needs doing.",
    "",
    `End with a short summary${one ? "" : " per issue"}: status, cause, next step.`,
  ].join("\n");
}

// ── Severity of live state (Nomad jobs, hosts, a server) ───────────────────

const LEVEL_RANK: Record<Level, number> = { fail: 0, warn: 1, ok: 2 };

/** The worse of two levels. */
export const worseLevel = (a: Level, b: Level): Level => (LEVEL_RANK[a] <= LEVEL_RANK[b] ? a : b);

/**
 * Whether a Nomad job is in trouble right now — the same rules as the
 * check-up: a service or system job that is dead or has nothing running
 * fails, one with allocations waiting warns. Nomad's `failed` count is
 * lifetime (every crashed allocation ever), so it says nothing on its own.
 * Batch jobs are "dead" when they are done and never count.
 */
export function jobLevel(j: Pick<NomadJob, "type" | "status" | "running" | "queued">): Level {
  if (j.type !== "service" && j.type !== "system") return "ok";
  if (j.status === "dead" || j.running === 0) return "fail";
  if (j.queued > 0) return "warn";
  return "ok";
}

/** A Nomad host's worst: unreachable fails, otherwise its worst job. */
export function nomadHostLevel(h: NomadHost): Level {
  if (!h.reachable) return "fail";
  return h.jobs.reduce<Level>((acc, j) => worseLevel(acc, jobLevel(j)), "ok");
}

/** The jobs in trouble right now (see jobLevel). */
export const problemJobs = (h: NomadHost) => (h.reachable ? h.jobs.filter((j) => jobLevel(j) !== "ok") : []);

/** Failing jobs first, then waiting ones, then the rest by name. */
export function sortNomadJobs(jobs: NomadJob[]): NomadJob[] {
  return [...jobs].sort((a, b) => LEVEL_RANK[jobLevel(a)] - LEVEL_RANK[jobLevel(b)] || a.id.localeCompare(b.id));
}

/** Hosts with problems first (unreachable or failing jobs, then waiting ones), then by name. */
export function sortNomadHosts(hosts: NomadHost[]): NomadHost[] {
  return [...hosts].sort((a, b) => LEVEL_RANK[nomadHostLevel(a)] - LEVEL_RANK[nomadHostLevel(b)] || a.host.localeCompare(b.host));
}

/** The highest of CPU, memory and disk use, for sorting hosts by pressure; -1 when nothing is known. */
export const worstUsage = (h: Pick<HostMetrics, "cpu_pct" | "mem_pct" | "disk_pct">) => Math.max(-1, ...[h.cpu_pct, h.mem_pct, h.disk_pct].map((p) => p ?? -1));

/** Hosts split for display: the ones not reporting (by name), then the reporting ones, heaviest use first. */
export function splitHosts(hosts: HostMetrics[]): { down: HostMetrics[]; up: HostMetrics[] } {
  return {
    down: hosts.filter((h) => !h.up).sort((a, b) => a.name.localeCompare(b.name)),
    up: hosts.filter((h) => h.up).sort((a, b) => worstUsage(b) - worstUsage(a) || a.name.localeCompare(b.name)),
  };
}

/**
 * Most hosts not reporting at once (more than half, at least two): far more
 * likely the shared metrics pipeline (vmagent → VictoriaMetrics, or Forge's
 * view of it) than that many hosts failing together.
 */
export const metricsOutage = (down: number, total: number) => down >= 2 && down * 2 > total;

/** The host metrics and the Nomad host that belong to a server, by its id or any name it goes by. */
export function serverLive(
  m: Monitoring,
  server: Pick<Server, "id" | "name" | "public_address" | "tailscale_ip"> & { ssh_target?: string },
): { host: HostMetrics | undefined; nomad: NomadHost | undefined; aliases: Set<string> } {
  const aliases = serverAliases(server);
  return {
    host: m.hosts.find((h) => h.server_id === server.id) ?? m.hosts.find((h) => hostAliases(h.name).some((a) => aliases.has(a))),
    nomad: m.nomad.find((n) => hostAliases(n.host).some((a) => aliases.has(a))),
    aliases,
  };
}
