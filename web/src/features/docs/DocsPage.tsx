// "What Forge does — and what it doesn't": the content in ./content.ts,
// rendered with the four tiers as a legend and colour-coded chips, plus a
// strip of live facts from GET /api/system so the numbers on this page are
// the server's, not the prose's.

import clsx from "clsx";
import { BookOpen, CircleCheck, CircleDashed, CircleX, Link as LinkIcon, TriangleAlert } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { useSystemFacts } from "@/api/hooks";
import type { SystemFacts } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton } from "@/components/ui/Skeleton";
import { formatInterval, groupByCategory, groupByTier, prettyDuration, severities, slug, TIER_ORDER, TIER_TONE, useHashScroll } from "@/lib/docs";
import { markDocsRead } from "@/lib/gettingStarted";
import { CHECKS, NOT_AUTOMATED, SECURITY, SETUP, SOURCES, STORED, TIERS, WORKSPACE, type Tier } from "./content";

const SECTIONS = [
  { id: "overview", title: "Overview" },
  { id: "now", title: "Right now" },
  { id: "setup", title: "Setup and storage" },
  { id: "sources", title: "Where the numbers come from" },
  { id: "checkup", title: "The daily check-up" },
  { id: "stored", title: "What is only configuration" },
  { id: "security", title: "Security model" },
  { id: "limits", title: "Not automated / limits" },
] as const;

export function TierChip({ tier, className }: { tier: Tier; className?: string }) {
  return (
    <Badge tone={TIER_TONE[tier]} dot className={className} title={TIERS[tier].blurb}>
      {TIERS[tier].label}
    </Badge>
  );
}

/** A heading that is also its own link, so any section can be pointed at. */
function H2({ id, children }: { id: string; children: ReactNode }) {
  return (
    <h2 id={id} className="group flex scroll-mt-20 items-center gap-2 text-[17px] font-semibold tracking-tight">
      {children}
      <a href={`#${id}`} className="text-fg-3 opacity-0 transition-opacity group-hover:opacity-100 focus:opacity-100" aria-label={`Link to ${id}`}>
        <LinkIcon className="size-3.5" />
      </a>
    </h2>
  );
}

function useActiveSection(): string {
  const [active, setActive] = useState<string>(SECTIONS[0].id);
  useEffect(() => {
    if (typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver(
      (entries) => {
        const visible = entries.filter((e) => e.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        if (visible[0]) setActive(visible[0].target.id);
      },
      { rootMargin: "-80px 0px -70% 0px" },
    );
    for (const s of SECTIONS) {
      const el = document.getElementById(s.id);
      if (el) io.observe(el);
    }
    return () => io.disconnect();
  }, []);
  return active;
}

export function DocsPage() {
  const facts = useSystemFacts();
  useHashScroll(!facts.isPending);
  const active = useActiveSection();
  useEffect(() => markDocsRead(), []);

  return (
    <div className="mx-auto max-w-[1200px] lg:grid lg:grid-cols-[200px_minmax(0,1fr)] lg:gap-8">
      <nav aria-label="On this page" className="mb-6 lg:sticky lg:top-16 lg:mb-0 lg:self-start">
        <p className="mb-2 text-[11px] font-semibold tracking-wide text-fg-3 uppercase">On this page</p>
        <ol className="flex flex-wrap gap-x-3 gap-y-1 lg:block lg:space-y-0.5">
          {SECTIONS.map((s) => (
            <li key={s.id}>
              <a
                href={`#${s.id}`}
                aria-current={active === s.id ? "location" : undefined}
                className={clsx(
                  "block rounded-md text-[13px] lg:px-2 lg:py-1",
                  active === s.id ? "font-medium text-fg lg:bg-surface-2" : "text-fg-3 hover:text-fg-2",
                )}
              >
                {s.title}
              </a>
            </li>
          ))}
        </ol>
      </nav>

      <div className="min-w-0 space-y-10">
        <section id="overview" className="scroll-mt-20 space-y-4">
          <div>
            <p className="flex items-center gap-2 text-[12px] font-medium text-fg-3">
              <BookOpen className="size-4" aria-hidden /> Documentation
            </p>
            <h1 className="mt-1 text-2xl font-semibold tracking-tight">What Forge does — and what it doesn't</h1>
            <p className="mt-2 max-w-2xl text-[14px] text-fg-2">
              Everything Forge shows falls into one of four tiers. The chips below are used throughout this page (and
              nowhere else in the app) to say which one you are looking at.
            </p>
          </div>
          <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {TIER_ORDER.map((tier) => (
              <div key={tier} className="rounded-xl border border-line bg-surface p-3.5">
                <dt>
                  <TierChip tier={tier} />
                </dt>
                <dd className="mt-2 text-[13px] text-fg-2">{TIERS[tier].blurb}</dd>
              </div>
            ))}
          </dl>
        </section>

        <section id="now" className="scroll-mt-20 space-y-3">
          <H2 id="now">Right now</H2>
          <FactsStrip facts={facts.data} loading={facts.isPending} error={!!facts.error} />
        </section>

        <section id="setup" className="scroll-mt-20 space-y-4">
          <H2 id="setup">Setup and storage</H2>
          <p className="max-w-2xl text-[13.5px] text-fg-2">
            Forge keeps everything in one folder, its workspace. Storage is a single SQLite file in it — there is no
            separate database server to run or back up.
          </p>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            {SETUP.map((s) => (
              <article key={s.title} id={`setup-${slug(s.title)}`} className="scroll-mt-20 rounded-xl border border-line bg-surface p-4">
                <h3 className="text-[14px] font-semibold">{s.title}</h3>
                <Prose className="mt-1.5 block text-[12.5px] text-fg-2">{s.body}</Prose>
              </article>
            ))}
          </div>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            {WORKSPACE.map((w) => (
              <article key={w.where} className="rounded-xl border border-line bg-surface p-4">
                <h3 className="text-[14px] font-semibold">{w.where}</h3>
                <Prose className="mt-0.5 block text-[12.5px] text-fg-3">{w.root}</Prose>
                <dl className="mt-3 grid grid-cols-[minmax(0,auto)_1fr] gap-x-3 gap-y-1.5 text-[12.5px]">
                  {w.entries.map((e) => (
                    <div key={e.path} className="contents">
                      <dt className="font-mono text-fg break-all">{e.path}</dt>
                      <dd className="text-fg-2">{e.what}</dd>
                    </div>
                  ))}
                </dl>
              </article>
            ))}
          </div>
        </section>

        <section id="sources" className="scroll-mt-20 space-y-4">
          <H2 id="sources">Where the numbers come from</H2>
          {groupByTier(SOURCES).map((g) => (
            <div key={g.tier} className="space-y-2">
              <h3 className="flex items-center gap-2 text-[13px] font-semibold text-fg-2">
                <TierChip tier={g.tier} /> <span className="font-normal text-fg-3">{TIERS[g.tier].blurb}</span>
              </h3>
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                {g.items.map((s) => {
                  const live = s.factKey ? formatInterval(s.factKey, facts.data?.intervals) : null;
                  return (
                    <article key={s.name} id={slug(s.name)} className="scroll-mt-20 space-y-2 rounded-xl border border-line bg-surface p-4">
                      <div className="flex flex-wrap items-center gap-2">
                        <h4 className="text-[14px] font-semibold">{s.name}</h4>
                        <TierChip tier={s.tier} />
                      </div>
                      <dl className="grid grid-cols-[52px_1fr] gap-x-2 gap-y-1 text-[12.5px]">
                        <dt className="text-fg-3">Who</dt>
                        <dd className="text-fg-2">{s.who}</dd>
                        <dt className="text-fg-3">When</dt>
                        <dd className="text-fg-2">
                          {s.when}
                          {live ? (
                            <Badge tone="accent" className="ml-1.5 align-middle" title="Live value from the server's settings">
                              {live}
                            </Badge>
                          ) : null}
                        </dd>
                        <dt className="text-fg-3">What</dt>
                        <dd className="text-fg-2">
                          <Prose>{s.what}</Prose>
                        </dd>
                      </dl>
                    </article>
                  );
                })}
              </div>
            </div>
          ))}
        </section>

        <section id="checkup" className="scroll-mt-20 space-y-3">
          <H2 id="checkup">The daily check-up</H2>
          <p className="max-w-2xl text-[13.5px] text-fg-2">
            Results are stored per day. Ticking an item off and turning it into a task are yours to do; the check-up
            never acts on anything by itself.
          </p>
          <div className="overflow-hidden rounded-xl border border-line bg-surface">
            <table className="w-full text-[12.5px]">
              <thead className="bg-surface-2/60 text-left text-[11px] tracking-wide text-fg-3 uppercase">
                <tr>
                  <th className="px-3 py-2 font-semibold">Check</th>
                  <th className="w-28 px-3 py-2 font-semibold">Severity</th>
                  <th className="hidden w-44 px-3 py-2 font-semibold sm:table-cell">Source</th>
                </tr>
              </thead>
              {groupByCategory(CHECKS).map((g) => (
                <tbody key={g.category} className="border-t border-line">
                  <tr>
                    <th colSpan={3} scope="rowgroup" className="bg-surface-2/40 px-3 py-1.5 text-left text-[12px] font-semibold text-fg-2">
                      {g.category}
                    </th>
                  </tr>
                  {g.items.map((c, i) => (
                    <tr key={i} className="border-t border-line align-top">
                      <td className="px-3 py-2 text-fg">
                        <Prose>{c.rule}</Prose>
                        <span className="mt-0.5 block text-[11.5px] text-fg-3 sm:hidden">{c.source}</span>
                      </td>
                      <td className="px-3 py-2">
                        <span className="flex flex-wrap gap-1">
                          {severities(c.severity).map((sev) => (
                            <SeverityBadge key={sev} severity={sev} />
                          ))}
                        </span>
                      </td>
                      <td className="hidden px-3 py-2 text-fg-2 sm:table-cell">{c.source}</td>
                    </tr>
                  ))}
                </tbody>
              ))}
            </table>
          </div>
        </section>

        <section id="stored" className="scroll-mt-20 space-y-3">
          <H2 id="stored">What is only configuration</H2>
          <p className="max-w-2xl text-[13.5px] text-fg-2">
            <TierChip tier="stored" className="mr-1.5 align-middle" /> These were typed in — by you or by the agent pass that
            set Forge up — and change only when edited.
          </p>
          <dl className="divide-y divide-line rounded-xl border border-line bg-surface">
            {STORED.map((s) => (
              <div key={s.name} className="grid grid-cols-1 gap-1 px-4 py-3 md:grid-cols-[220px_1fr] md:gap-4">
                <dt className="text-[13.5px] font-semibold">{s.name}</dt>
                <dd className="space-y-1 text-[12.5px]">
                  <p className="text-fg-3">
                    <span className="font-medium text-fg-2">Origin:</span> {s.origin}
                  </p>
                  <Prose className="text-fg-2">{s.note}</Prose>
                </dd>
              </div>
            ))}
          </dl>
        </section>

        <section id="security" className="scroll-mt-20 space-y-3">
          <H2 id="security">Security model</H2>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            {SECURITY.map((s) => (
              <article key={s.title} id={slug(s.title)} className="scroll-mt-20 rounded-xl border border-line bg-surface p-4">
                <h3 className="text-[14px] font-semibold">{s.title}</h3>
                <Prose className="mt-1.5 text-[12.5px] text-fg-2">{s.body}</Prose>
              </article>
            ))}
          </div>
          <p className="text-[12.5px] text-fg-3">
            Your own sessions and the security log are under{" "}
            <Link to="/settings#security" className="text-accent hover:underline">
              Settings → Security
            </Link>
            .
          </p>
        </section>

        <section id="limits" className="scroll-mt-20 space-y-3 pb-10">
          <H2 id="limits">Not automated / limits</H2>
          <ul className="space-y-2">
            {NOT_AUTOMATED.map((line, i) => (
              <li key={i} className="flex items-start gap-2.5 rounded-lg border border-line bg-surface px-3.5 py-2.5 text-[13px] text-fg-2">
                <CircleDashed className="mt-0.5 size-4 shrink-0 text-fg-3" aria-hidden />
                <Prose>{line}</Prose>
              </li>
            ))}
          </ul>
        </section>
      </div>
    </div>
  );
}

export function SeverityBadge({ severity }: { severity: string }) {
  if (severity === "fail")
    return (
      <Badge tone="critical" dot>
        fail
      </Badge>
    );
  if (severity === "warn")
    return (
      <Badge tone="warning" dot>
        warn
      </Badge>
    );
  return <Badge tone="outline">{severity}</Badge>;
}

/** Inline `code` spans in the prose, nothing else — the content is plain text with backticks. */
function Prose({ children, className }: { children: string; className?: string }) {
  const parts = children.split(/(`[^`]+`)/g);
  return (
    <span className={className}>
      {parts.map((p, i) =>
        p.startsWith("`") && p.endsWith("`") ? (
          <code key={i} className="rounded border border-line bg-surface-2 px-1 py-px font-mono text-[0.92em]">
            {p.slice(1, -1)}
          </code>
        ) : (
          <span key={i}>{p}</span>
        ),
      )}
    </span>
  );
}

function OnOff({ on, label, title }: { on: boolean; label: string; title?: string }) {
  return (
    <Badge tone={on ? "good" : "outline"} title={title}>
      {on ? <CircleCheck className="size-3 text-good" aria-hidden /> : <CircleX className="size-3 text-fg-3" aria-hidden />}
      {label} {on ? "on" : "off"}
    </Badge>
  );
}

function FactsStrip({ facts, loading, error }: { facts: SystemFacts | undefined; loading: boolean; error: boolean }) {
  if (loading) return <Skeleton className="h-32 w-full" />;
  if (!facts) {
    return (
      <p className="flex items-center gap-2 rounded-xl border border-line bg-surface px-4 py-3 text-[13px] text-fg-3">
        <TriangleAlert className="size-4 text-warning" aria-hidden />
        {error ? "The live facts could not be read from the API." : "No facts yet."}
      </p>
    );
  }
  const c = facts.counts;
  const tiles: { label: string; value: ReactNode; sub?: ReactNode }[] = [
    { label: "Forge", value: facts.version || "dev", sub: <>up <RelativeTime iso={facts.started_at} /></> },
    {
      label: "Master",
      value: facts.master ? facts.master.name : "none",
      sub: facts.master ? (
        <span className={facts.master.online ? "text-good-ink" : "text-critical-ink"}>{facts.master.online ? "online" : "offline"}</span>
      ) : (
        "no master elected"
      ),
    },
    {
      label: "Runners",
      value: `${facts.runners.filter((r) => r.online).length}/${facts.runners.length}`,
      sub: facts.runners.map((r) => `${r.name}${r.online ? "" : " (asleep)"}`).join(", ") || "none",
    },
    { label: "Projects", value: c.projects, sub: `${c.servers} servers · ${c.endpoints} endpoints` },
    { label: "Repos scanned", value: `${c.repos_scanned}/${c.repos}`, sub: facts.master ? `by ${facts.master.name}` : "no master" },
    { label: "Open tasks", value: c.tasks_open, sub: `${c.tasks_done} done` },
    { label: "Vault items", value: c.vault_items, sub: `${c.vault_with_values} with values` },
    {
      label: "Check-up",
      value: facts.checkup.last_at ? <RelativeTime iso={facts.checkup.last_at} fallback="never" /> : "never",
      sub: (
        <>
          next <RelativeTime iso={facts.checkup.next_at} />
          {facts.checkup.last_trigger ? ` · last ${facts.checkup.last_trigger}` : ""}
        </>
      ),
    },
  ];
  return (
    <div className="space-y-3 rounded-xl border border-line bg-surface p-4">
      <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {tiles.map((t) => (
          <div key={t.label} className="flex flex-col-reverse">
            <dt className="text-[11px] text-fg-3">{t.label}</dt>
            <dd className="truncate text-[17px] font-semibold" title={typeof t.value === "string" ? t.value : undefined}>
              {t.value}
            </dd>
            {t.sub ? <dd className="order-first truncate text-[11.5px] text-fg-3">{t.sub}</dd> : null}
          </div>
        ))}
      </dl>
      <div className="flex flex-wrap gap-1.5">
        <OnOff on={facts.features.smtp} label="SMTP" title="E-mail: check-ups, password resets, new-device sign-ins" />
        <OnOff on={facts.features.vault} label="Vault" title="A vault key is configured on the server" />
        <OnOff on={facts.features.totp_enabled} label="Two-factor" />
        <OnOff on={facts.features.grafana_connected} label="Grafana" title="A Grafana token is in the vault and works" />
        <OnOff on={facts.features.victoriametrics} label="VictoriaMetrics" title="Host metrics are reachable" />
        <Badge tone="outline" title="Elevation window · session sliding TTL / hard maximum · VS Code session">
          step-up {prettyDuration(facts.intervals.elevation)} · session {prettyDuration(facts.intervals.session_ttl)} /{" "}
          {prettyDuration(facts.intervals.session_max)} · editor {prettyDuration(facts.intervals.code_session)}
        </Badge>
        {facts.seeded_at ? (
          <Badge tone="outline" title={facts.seeded_at}>
            seeded <RelativeTime iso={facts.seeded_at} />
          </Badge>
        ) : null}
      </div>
    </div>
  );
}
