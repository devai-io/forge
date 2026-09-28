// One vault item: its metadata in the clear, its secrets behind Reveal.
//
// Revealed values live in this component's state and nowhere else — not in
// React Query, not in a toast, not in the console — and are wiped after 60
// seconds or when the sheet closes, whichever comes first.

import clsx from "clsx";
import { Copy, Download, Eye, EyeOff, Link2, Lock, Pencil, Trash2, X } from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { requestBlob } from "@/api/client";
import { revealVaultItem, useDeleteVaultItem, useVaultItem } from "@/api/hooks";
import type { VaultItem } from "@/api/types";
import { Badge, ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog, Sheet } from "@/components/ui/Dialog";
import { EmptyState } from "@/components/ui/EmptyState";
import { Input } from "@/components/ui/Input";
import { Markdown } from "@/components/ui/Markdown";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Skeleton } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { useUser } from "@/lib/auth";
import { formatDateTime, todayInTz } from "@/lib/format";
import { isReference, kindLabel, maskValue, saveBlob } from "@/lib/vault";
import { ExpiryBadge, FileBadge, VaultKindIcon } from "./bits";
import { VaultItemDialog } from "./VaultItemDialog";

export const REVEAL_SECONDS = 60;

export function VaultItemSheet({ id, onClose }: { id: number | null; onClose: () => void }) {
  return (
    <Sheet open={id !== null} onClose={onClose} label="Vault item">
      {id !== null ? <ItemPanel key={id} id={id} onClose={onClose} /> : null}
    </Sheet>
  );
}

function ItemPanel({ id, onClose }: { id: number; onClose: () => void }) {
  const item = useVaultItem(id);
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  return (
    <>
      <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-3">
        <Lock className="size-4 text-fg-3" aria-hidden />
        <span className="text-[13px] text-fg-2">Vault</span>
        <div className="ml-auto flex items-center gap-1">
          {item.data ? (
            <>
              <Button size="sm" variant="subtle" onClick={() => setEditing(true)}>
                <Pencil className="size-3.5" aria-hidden /> Edit
              </Button>
              <Button size="icon-sm" variant="ghost" aria-label="Delete item" onClick={() => setDeleting(true)}>
                <Trash2 className="size-3.5" />
              </Button>
            </>
          ) : null}
          <button type="button" onClick={onClose} className="rounded-md p-1.5 text-fg-3 hover:bg-surface-2 hover:text-fg" aria-label="Close">
            <X className="size-4" />
          </button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {item.error ? (
          <div className="p-6">
            <EmptyState title="Item not found">It may have been deleted.</EmptyState>
          </div>
        ) : item.data ? (
          <ItemBody item={item.data} />
        ) : (
          <div className="space-y-4 p-5">
            <Skeleton className="h-7 w-2/3" />
            <Skeleton className="h-32 w-full" />
          </div>
        )}
      </div>
      {editing && item.data ? <VaultItemDialog item={item.data} onClose={() => setEditing(false)} /> : null}
      {deleting && item.data ? (
        <DeleteItemDialog
          item={item.data}
          onClose={() => setDeleting(false)}
          onDeleted={() => {
            setDeleting(false);
            onClose();
          }}
        />
      ) : null}
    </>
  );
}

function ItemBody({ item }: { item: VaultItem }) {
  const user = useUser();
  const today = todayInTz(user.timezone);
  const fields = Object.entries(item.fields);
  return (
    <div className="space-y-6 px-4 py-4 sm:px-5">
      <div className="flex items-start gap-3">
        <span className="mt-0.5 grid size-9 shrink-0 place-items-center rounded-lg bg-surface-2">
          <VaultKindIcon kind={item.kind} />
        </span>
        <div className="min-w-0">
          <h2 className="text-lg leading-snug font-semibold break-words">{item.name}</h2>
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            <Badge tone="outline">{kindLabel(item.kind)}</Badge>
            {item.project_key ? (
              <Link to={`/p/${item.project_key}`} className="inline-flex items-center gap-1 text-[12px] text-fg-2 hover:text-fg">
                <ColorDot color={item.project_color} className="size-2" /> {item.project_key}
              </Link>
            ) : (
              <Badge tone="neutral">shared</Badge>
            )}
            {item.platform ? <Badge tone="neutral">{item.platform}</Badge> : null}
            {item.host ? <Badge tone="neutral">on {item.host}</Badge> : null}
            <ExpiryBadge expires={item.expires_at} today={today} />
          </div>
        </div>
      </div>

      <dl className="grid grid-cols-[110px_1fr] gap-x-3 gap-y-2 text-[13px]">
        {item.identifier ? (
          <>
            <dt className="text-fg-3">Identifier</dt>
            <dd className="font-mono break-all">{item.identifier}</dd>
          </>
        ) : null}
        {fields.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="truncate font-mono text-[12px] text-fg-3" title={k}>
              {k}
            </dt>
            <dd className="font-mono break-all">{v}</dd>
          </div>
        ))}
        {item.location ? (
          <>
            <dt className="text-fg-3">Location</dt>
            <dd className="font-mono text-[12.5px] break-all">{item.location}</dd>
          </>
        ) : null}
        {item.expires_at ? (
          <>
            <dt className="text-fg-3">Expires</dt>
            <dd>{item.expires_at}</dd>
          </>
        ) : null}
        {item.tags.length ? (
          <>
            <dt className="text-fg-3">Tags</dt>
            <dd className="flex flex-wrap gap-1">
              {item.tags.map((t) => (
                <span key={t} className="rounded bg-surface-2 px-1.5 text-[11.5px] text-fg-2">
                  {t}
                </span>
              ))}
            </dd>
          </>
        ) : null}
      </dl>

      {isReference(item) ? (
        <p className="flex items-start gap-2 rounded-lg border border-line bg-surface-2/60 px-3 py-2.5 text-[13px]">
          <Link2 className="mt-0.5 size-4 shrink-0 text-fg-3" aria-hidden />
          <span>
            <span className="font-medium">Reference only</span> — Forge holds no value or file for this item.
            {item.location ? (
              <>
                {" "}
                The original lives at <code className="font-mono text-[12px] break-all">{item.location}</code>.
              </>
            ) : (
              " No location was recorded."
            )}
          </span>
        </p>
      ) : null}
      {item.secret_keys.length ? <RevealSection item={item} /> : null}
      {item.has_file ? <FileSection item={item} /> : null}

      {item.notes.trim() ? (
        <section aria-labelledby="vault-notes" className="space-y-1.5">
          <h3 id="vault-notes" className="text-[13px] font-semibold text-fg-2">
            Notes
          </h3>
          <Markdown>{item.notes}</Markdown>
        </section>
      ) : null}

      <p className="border-t border-line pt-3 text-xs text-fg-3">
        Added <RelativeTime iso={item.created_at} /> · updated <RelativeTime iso={item.updated_at} /> · last revealed{" "}
        <RelativeTime iso={item.last_revealed_at} fallback="never" />
      </p>
    </div>
  );
}

function RevealSection({ item }: { item: VaultItem }) {
  const toast = useToast();
  const [values, setValues] = useState<Record<string, string> | null>(null);
  const [shown, setShown] = useState<Record<string, boolean>>({});
  const [left, setLeft] = useState(0);
  const [loading, setLoading] = useState(false);

  // Countdown, then wipe. Unmounting (sheet closed) wipes too: the state dies with it.
  useEffect(() => {
    if (!values) return;
    const started = Date.now();
    setLeft(REVEAL_SECONDS);
    const t = window.setInterval(() => {
      const remaining = REVEAL_SECONDS - Math.floor((Date.now() - started) / 1000);
      if (remaining <= 0) {
        setValues(null);
        setShown({});
      } else {
        setLeft(remaining);
      }
    }, 1000);
    return () => window.clearInterval(t);
  }, [values]);

  const reveal = async () => {
    setLoading(true);
    try {
      setValues(await revealVaultItem(item.id));
      setShown({});
    } catch (err) {
      toast.error(err);
    } finally {
      setLoading(false);
    }
  };

  const hide = () => {
    setValues(null);
    setShown({});
  };

  return (
    <section aria-labelledby="vault-secrets" className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <h3 id="vault-secrets" className="text-[13px] font-semibold text-fg-2">
          Secret values
        </h3>
        {values ? (
          <div className="flex items-center gap-2 text-xs text-fg-3">
            <span className="tabular" role="timer" aria-live="off">
              hides in {left}s
            </span>
            <Button size="sm" variant="subtle" onClick={hide}>
              <EyeOff className="size-3.5" aria-hidden /> Hide now
            </Button>
          </div>
        ) : (
          <Button size="sm" variant="primary" onClick={reveal} loading={loading}>
            <Eye className="size-3.5" aria-hidden /> Reveal
          </Button>
        )}
      </div>
      <ul className="divide-y divide-line rounded-lg border border-line">
        {item.secret_keys.map((k) => {
          const v = values?.[k];
          const visible = v !== undefined && shown[k];
          return (
            <li key={k} className="flex items-start gap-2 px-3 py-2">
              <span className="w-28 shrink-0 truncate pt-0.5 font-mono text-[12px] text-fg-3" title={k}>
                {k}
              </span>
              <span
                className={clsx(
                  "min-w-0 flex-1 font-mono text-[12.5px]",
                  visible ? "max-h-60 overflow-auto break-all whitespace-pre-wrap" : "truncate text-fg-3",
                )}
              >
                {v === undefined ? "••••••••••••" : visible ? v : maskValue(v)}
              </span>
              {v !== undefined ? (
                <span className="flex shrink-0 items-center gap-0.5">
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    aria-label={visible ? `Hide ${k}` : `Show ${k}`}
                    onClick={() => setShown((s) => ({ ...s, [k]: !s[k] }))}
                  >
                    {visible ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                  </Button>
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    aria-label={`Copy ${k}`}
                    onClick={() =>
                      void navigator.clipboard?.writeText(v).then(
                        () => toast.success(`Copied ${k}`),
                        () => toast.error("Couldn't copy to the clipboard"),
                      )
                    }
                  >
                    <Copy className="size-3.5" />
                  </Button>
                </span>
              ) : null}
            </li>
          );
        })}
      </ul>
      <p className="text-xs text-fg-3">Every reveal is recorded in the audit log.</p>
    </section>
  );
}

function FileSection({ item }: { item: VaultItem }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const download = async () => {
    setBusy(true);
    try {
      const { blob, filename } = await requestBlob(`/vault/${item.id}/file`);
      saveBlob(blob, filename || item.file_name || `vault-${item.id}`);
    } catch (err) {
      toast.error(err);
    } finally {
      setBusy(false);
    }
  };
  return (
    <section aria-labelledby="vault-file" className="space-y-2">
      <h3 id="vault-file" className="text-[13px] font-semibold text-fg-2">
        File
      </h3>
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-line px-3 py-2">
        <FileBadge item={item} />
        <Button size="sm" variant="subtle" onClick={download} loading={busy}>
          <Download className="size-3.5" aria-hidden /> Download
        </Button>
      </div>
    </section>
  );
}

function DeleteItemDialog({ item, onClose, onDeleted }: { item: VaultItem; onClose: () => void; onDeleted: () => void }) {
  const del = useDeleteVaultItem();
  const toast = useToast();
  const [typed, setTyped] = useState("");
  return (
    <ConfirmDialog
      open
      onClose={onClose}
      title={`Delete ${item.name}?`}
      confirmLabel="Delete forever"
      loading={del.isPending}
      body={
        <div className="space-y-3">
          <p>
            The encrypted values{item.has_file ? " and the file" : ""} are destroyed. Make sure the original still exists
            {item.location ? (
              <>
                {" "}
                at <code className="font-mono text-[12px]">{item.location}</code>
              </>
            ) : null}
            . Last change {formatDateTime(item.updated_at)}.
          </p>
          <Input
            aria-label={`Type ${item.name} to confirm`}
            placeholder={`Type "${item.name}" to confirm`}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
          />
        </div>
      }
      onConfirm={() => {
        if (typed.trim() !== item.name) {
          toast.error(`Type "${item.name}" to confirm`);
          return;
        }
        del.mutate(item.id, {
          onSuccess: () => {
            toast.success("Deleted from the vault");
            onDeleted();
          },
          onError: (err) => toast.error(err),
        });
      }}
    />
  );
}
