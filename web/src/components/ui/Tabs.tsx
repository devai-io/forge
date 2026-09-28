import clsx from "clsx";
import { useRef, type ReactNode } from "react";

export type TabItem<T extends string> = { value: T; label: ReactNode; count?: number };

/** A tablist with arrow-key navigation. Panels are rendered by the caller. */
export function Tabs<T extends string>({
  items,
  value,
  onChange,
  className,
  label,
}: {
  items: TabItem<T>[];
  value: T;
  onChange: (v: T) => void;
  className?: string;
  label: string;
}) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const onKeyDown = (e: React.KeyboardEvent, index: number) => {
    let next = -1;
    if (e.key === "ArrowRight") next = (index + 1) % items.length;
    else if (e.key === "ArrowLeft") next = (index - 1 + items.length) % items.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = items.length - 1;
    if (next < 0) return;
    e.preventDefault();
    onChange(items[next].value);
    refs.current[next]?.focus();
  };
  return (
    <div
      role="tablist"
      aria-label={label}
      // Scrolls sideways on narrow screens, never clips: the fade at the right
      // edge is the hint that there is more. (mask-image, so the border stays.)
      className={clsx(
        "flex min-w-0 gap-1 overflow-x-auto border-b border-line [scrollbar-width:none] [&::-webkit-scrollbar]:hidden",
        "max-sm:[mask-image:linear-gradient(to_right,black_calc(100%-28px),transparent)]",
        className,
      )}
    >
      {items.map((item, i) => {
        const active = item.value === value;
        return (
          <button
            key={item.value}
            ref={(el) => {
              refs.current[i] = el;
            }}
            role="tab"
            type="button"
            aria-selected={active}
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(item.value)}
            onKeyDown={(e) => onKeyDown(e, i)}
            className={clsx(
              "relative -mb-px flex h-9 shrink-0 items-center gap-1.5 border-b-2 px-2.5 text-sm transition-colors",
              active ? "border-accent font-medium text-fg" : "border-transparent text-fg-3 hover:text-fg-2",
            )}
          >
            {item.label}
            {item.count !== undefined ? (
              <span className="tabular rounded bg-surface-2 px-1 text-[11px] text-fg-3">{item.count}</span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

/** Compact segmented control for view toggles and quick filters. */
export function Segmented<T extends string>({
  items,
  value,
  onChange,
  label,
  size = "md",
}: {
  items: { value: T; label: ReactNode; title?: string }[];
  value: T;
  onChange: (v: T) => void;
  label: string;
  size?: "sm" | "md";
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex rounded-md border border-line-strong bg-surface p-0.5">
      {items.map((item) => (
        <button
          key={item.value}
          type="button"
          role="radio"
          aria-checked={item.value === value}
          title={item.title}
          onClick={() => onChange(item.value)}
          className={clsx(
            "inline-flex items-center gap-1.5 rounded-[5px] px-2 font-medium transition-colors",
            size === "sm" ? "h-6 text-xs" : "h-7 text-[13px]",
            item.value === value ? "bg-surface-3 text-fg" : "text-fg-3 hover:text-fg-2",
          )}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
