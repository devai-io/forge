// The vault: every credential, key file and signing asset the projects need,
// grouped by project. The list shows metadata only; values sit behind Reveal
// in the item sheet (`?item=<id>`, deep-linkable).

import { History, KeyRound, Lock, Plus, Search } from "lucide-react";
import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useProjects, useVault, useVaultAudit } from "@/api/hooks";
import type { VaultAudit, VaultItem } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Input, Select } from "@/components/ui/Input";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { Tabs } from "@/components/ui/Tabs";
import { ElevationBadge } from "@/features/auth/ElevationBadge";
import { useUser } from "@/lib/auth";
import { todayInTz } from "@/lib/format";
import { groupByProject, isReference, VAULT_KINDS } from "@/lib/vault";
import { ExpiryBadge, FileBadge, SecretKeyChips, VaultKindIcon } from "./bits";
import { VaultItemDialog } from "./VaultItemDialog";
import { VaultItemSheet } from "./VaultItemSheet";

export function VaultPage() {
  const [params, setParams] = useSearchParams();
  const tab = params.get("tab") === "audit" ? "audit" : "items";
  const itemParam = Number(params.get("item"));
  const openId = Number.isInteger(itemParam) && itemParam > 0 ? itemParam : null;
  const [adding, setAdding] = useState(false);

  const setParam = (key: string, value: string | null) =>
    setParams((p) => {
      const next = new URLSearchParams(p);
      if (value) next.set(key, value);
      else next.delete(key);
      return next;
    });

  return (
    <div className="mx-auto max-w-[1200px]">
      <PageHeader
        title="Vault"
        subtitle="Keys, tokens, signing files and where their originals live — encrypted, revealed on demand."
        actions={
          <>
            <ElevationBadge />
            <Button variant="primary" size="sm" onClick={() => setAdding(true)}>
              <Plus className="size-3.5" aria-hidden /> Add item
            </Button>
          </>
        }
      />
      <Tabs
        label="Vault views"
        value={tab}
        onChange={(t) => setParam("tab", t === "items" ? null : t)}
        className="mb-4"
        items={[
          { value: "items", label: "Items" },
          { value: "audit", label: "Audit log" },
        ]}
      />
      {tab === "items" ? <ItemsTab onOpen={(id) => setParam("item", String(id))} onAdd={() => setAdding(true)} /> : <AuditTab onOpen={(id) => setParam("item", String(id))} />}
      <VaultItemSheet id={openId} onClose={() => setParam("item", null)} />
      {adding ? (
        <VaultItemDialog
          defaults={{ project_key: params.get("project") }}
          onClose={() => setAdding(false)}
          onSaved={(item) => setParam("item", String(item.id))}
        />
      ) : null}
    </div>
  );
}

function ItemsTab({ onOpen, onAdd }: { onOpen: (id: number) => void; onAdd: () => void }) {
  const [project, setProject] = useState("");
  const [kind, setKind] = useState("");
  const [platform, setPlatform] = useState("");
  const [q, setQ] = useState("");
  const projects = useProjects();
  const user = useUser();
  const today = todayInTz(user.timezone);
  // Project and kind filter server-side; platform and search are instant, client-side.
  const vault = useVault({ project: project || undefined, kind: kind || undefined });

  const items = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (vault.data?.items ?? []).filter(
      (i) =>
        (!platform || i.platform === platform) &&
        (!needle ||
          [i.name, i.identifier, i.host, i.location, ...i.tags, ...Object.values(i.fields)].some((s) =>
            s.toLowerCase().includes(needle),
          )),
    );
  }, [vault.data, platform, q]);
  const platforms = useMemo(
    () => Array.from(new Set((vault.data?.items ?? []).map((i) => i.platform).filter(Boolean))).sort(),
    [vault.data],
  );

  if (vault.isPending) return <SkeletonRows rows={6} />;
  if (vault.error) return <ErrorState error={vault.error} onRetry={() => vault.refetch()} />;
  if (!vault.data.available) {
    return (
      <EmptyState icon={<Lock />} title="The vault is not available on this server">
        Forge encrypts vault items with a key that lives outside the database (a sops secret on the host). This server
        has no vault key configured, so nothing can be stored or revealed. Add the key to the host's secrets and restart
        forge-api.
      </EmptyState>
    );
  }

  const groups = groupByProject(items);
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-0 flex-1 basis-56">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-fg-3" aria-hidden />
          <Input aria-label="Search the vault" placeholder="Search name, identifier, host, tag…" value={q} onChange={(e) => setQ(e.target.value)} className="pl-8" />
        </div>
        <Select aria-label="Project" className="w-auto" value={project} onChange={(e) => setProject(e.target.value)}>
          <option value="">All projects</option>
          {projects.data?.map((p) => (
            <option key={p.key} value={p.key}>
              {p.key} · {p.name}
            </option>
          ))}
        </Select>
        <Select aria-label="Kind" className="w-auto" value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="">Any kind</option>
          {VAULT_KINDS.map((k) => (
            <option key={k.value} value={k.value}>
              {k.label}
            </option>
          ))}
        </Select>
        {platforms.length ? (
          <Select aria-label="Platform" className="w-auto" value={platform} onChange={(e) => setPlatform(e.target.value)}>
            <option value="">Any platform</option>
            {platforms.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </Select>
        ) : null}
      </div>

      {groups.length === 0 ? (
        <EmptyState
          icon={<KeyRound />}
          title={vault.data.items.length ? "Nothing matches" : "The vault is empty"}
          action={
            vault.data.items.length ? null : (
              <Button variant="primary" size="sm" onClick={onAdd}>
                Add the first item
              </Button>
            )
          }
        >
          {vault.data.items.length ? null : "Store API keys, keystores, .p8 files and where each original lives."}
        </EmptyState>
      ) : (
        groups.map((g) => (
          <Panel
            key={g.key ?? "shared"}
            id={`vault-${g.key ?? "shared"}`}
            title={
              <span className="flex items-center gap-2">
                {g.key ? <ColorDot color={g.color} /> : null}
                {g.key ? (projects.data?.find((p) => p.key === g.key)?.name ?? g.key) : "Shared"}
                <span className="font-normal text-fg-3">· {g.items.length}</span>
              </span>
            }
            bodyClassName="p-0"
          >
            <ul className="divide-y divide-line">
              {g.items.map((item) => (
                <li key={item.id}>
                  <VaultRow item={item} today={today} onOpen={onOpen} />
                </li>
              ))}
            </ul>
          </Panel>
        ))
      )}
    </div>
  );
}

function VaultRow({ item, today, onOpen }: { item: VaultItem; today: string; onOpen: (id: number) => void }) {
  return (
    <button type="button" onClick={() => onOpen(item.id)} className="flex w-full min-w-0 items-start gap-3 px-3.5 py-2.5 text-left hover:bg-surface-2/60">
      <VaultKindIcon kind={item.kind} className="mt-0.5 size-4" />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-[13.5px] font-medium">{item.name}</span>
          {item.platform ? <Badge tone="neutral">{item.platform}</Badge> : null}
          {item.host ? <Badge tone="outline">{item.host}</Badge> : null}
          <FileBadge item={item} />
          {isReference(item) ? (
            <Badge tone="outline" title="Reference only: Forge holds no value or file for this item">
              reference
            </Badge>
          ) : null}
          <ExpiryBadge expires={item.expires_at} today={today} />
        </div>
        <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-fg-3">
          {item.identifier ? <span className="truncate font-mono">{item.identifier}</span> : null}
          <SecretKeyChips keys={item.secret_keys} />
        </div>
      </div>
      <RelativeTime iso={item.updated_at} className="hidden shrink-0 text-[11px] text-fg-3 sm:block" />
    </button>
  );
}

const ACTION_TONE: Record<VaultAudit["action"], "neutral" | "accent" | "warning" | "critical" | "good"> = {
  create: "good",
  update: "neutral",
  reveal: "warning",
  download: "warning",
  delete: "critical",
};

function AuditTab({ onOpen }: { onOpen: (id: number) => void }) {
  const audit = useVaultAudit(200);
  if (audit.isPending) return <SkeletonRows rows={6} />;
  if (audit.error) return <ErrorState error={audit.error} onRetry={() => audit.refetch()} />;
  return (
    <Panel title="Audit log" icon={<History />} id="vault-audit" bodyClassName="p-0">
      {audit.data.length ? (
        <ul className="divide-y divide-line">
          {audit.data.map((e) => (
            <li key={e.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3.5 py-2 text-[13px]">
              <Badge tone={ACTION_TONE[e.action]}>{e.action}</Badge>
              {e.item_id ? (
                <button type="button" onClick={() => onOpen(e.item_id!)} className="min-w-0 truncate font-medium hover:text-accent hover:underline">
                  {e.item_name}
                </button>
              ) : (
                <span className="min-w-0 truncate text-fg-2">{e.item_name}</span>
              )}
              <span className="ml-auto font-mono text-[11.5px] text-fg-3">{e.ip}</span>
              <RelativeTime iso={e.at} className="w-16 text-right text-[11.5px] text-fg-3" />
            </li>
          ))}
        </ul>
      ) : (
        <p className="p-4 text-[13px] text-fg-3">No vault activity yet.</p>
      )}
    </Panel>
  );
}
