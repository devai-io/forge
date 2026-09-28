import { Plus, X } from "lucide-react";
import type { KV } from "@/lib/vault";
import { Button } from "./Button";

/**
 * Editable key/value rows. `secret` values use a monospace textarea with
 * spellcheck and autofill off — private keys and env files are multi-line.
 */
export function KeyValueRows({
  rows,
  onChange,
  secret = false,
  keyPlaceholder = "key",
  valuePlaceholder = "value",
  addLabel = "Add field",
  label,
}: {
  rows: KV[];
  onChange: (rows: KV[]) => void;
  secret?: boolean;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
  addLabel?: string;
  label: string;
}) {
  const set = (i: number, patch: Partial<KV>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  return (
    <div className="space-y-2" role="group" aria-label={label}>
      {rows.map((r, i) => (
        <div key={i} className="flex items-start gap-2">
          <input
            aria-label={`${label} ${i + 1} name`}
            value={r.key}
            onChange={(e) => set(i, { key: e.target.value })}
            placeholder={keyPlaceholder}
            spellCheck={false}
            autoComplete="off"
            className="h-8.5 w-36 shrink-0 rounded-md border border-line-strong bg-surface px-2.5 font-mono text-[13px] focus:border-accent focus:outline-none sm:w-44"
          />
          <textarea
            aria-label={`${label} ${i + 1} value`}
            value={r.value}
            onChange={(e) => set(i, { value: e.target.value })}
            placeholder={valuePlaceholder}
            rows={1}
            spellCheck={false}
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="off"
            data-1p-ignore={secret ? "" : undefined}
            className="field-sizing-content max-h-48 min-h-8.5 min-w-0 flex-1 resize-none rounded-md border border-line-strong bg-surface px-2.5 py-1.5 font-mono text-[13px] leading-snug focus:border-accent focus:outline-none"
          />
          <Button size="icon" variant="ghost" aria-label={`Remove ${label} ${i + 1}`} onClick={() => onChange(rows.filter((_, j) => j !== i))}>
            <X className="size-4" />
          </Button>
        </div>
      ))}
      <Button size="sm" variant="ghost" onClick={() => onChange([...rows, { key: "", value: "" }])}>
        <Plus className="size-3.5" aria-hidden /> {addLabel}
      </Button>
    </div>
  );
}
