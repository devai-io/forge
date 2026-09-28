import { X } from "lucide-react";
import { useState } from "react";
import { normalizeLabel } from "@/lib/tasks";

/** Label chips with an inline input: Enter or comma adds, Backspace on empty removes the last. */
export function LabelsEditor({ labels, onChange, id }: { labels: string[]; onChange: (next: string[]) => void; id?: string }) {
  const [draft, setDraft] = useState("");
  const add = (raw: string) => {
    const l = normalizeLabel(raw);
    setDraft("");
    if (!l || labels.includes(l)) return;
    onChange([...labels, l]);
  };
  return (
    <div className="flex min-h-8.5 flex-wrap items-center gap-1 rounded-md border border-line-strong bg-surface px-1.5 py-1 focus-within:border-accent">
      {labels.map((l) => (
        <span key={l} className="inline-flex h-6 items-center gap-1 rounded bg-surface-2 pr-0.5 pl-1.5 text-xs text-fg-2">
          {l}
          <button
            type="button"
            onClick={() => onChange(labels.filter((x) => x !== l))}
            className="rounded p-0.5 text-fg-3 hover:text-fg"
            aria-label={`Remove label ${l}`}
          >
            <X className="size-3" />
          </button>
        </span>
      ))}
      <input
        id={id}
        value={draft}
        onChange={(e) => {
          const v = e.target.value;
          if (v.endsWith(",")) add(v.slice(0, -1));
          else setDraft(v);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            add(draft);
          } else if (e.key === "Backspace" && !draft && labels.length) {
            onChange(labels.slice(0, -1));
          }
        }}
        onBlur={() => draft && add(draft)}
        placeholder={labels.length ? "" : "Add label…"}
        className="h-6 min-w-20 flex-1 bg-transparent px-1 text-xs outline-none placeholder:text-fg-3"
        aria-label="Add label"
      />
    </div>
  );
}
