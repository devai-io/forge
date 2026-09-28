// Dropdown menu: a trigger button and a list of actions, keyboard-usable
// (ArrowUp/Down, Home/End, Enter, Escape) and closed by an outside click.

import clsx from "clsx";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";

export type MenuItem =
  | {
      label: ReactNode;
      onSelect: () => void;
      icon?: ReactNode;
      danger?: boolean;
      disabled?: boolean;
      checked?: boolean;
    }
  | "separator";

export function Menu({
  trigger,
  items,
  align = "end",
  label,
  className,
  triggerClassName,
}: {
  trigger: ReactNode;
  items: MenuItem[];
  align?: "start" | "end";
  label: string;
  className?: string;
  triggerClassName?: string;
}) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const itemRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const id = useId();
  const actionable = items
    .map((item, index) => ({ item, index }))
    .filter((x): x is { item: Exclude<MenuItem, "separator">; index: number } => x.item !== "separator" && !x.item.disabled);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", onDown);
    return () => document.removeEventListener("pointerdown", onDown);
  }, [open]);

  useEffect(() => {
    if (open) itemRefs.current[actionable[active]?.index]?.focus();
  }, [open, active, actionable]);

  const close = (focusTrigger = true) => {
    setOpen(false);
    if (focusTrigger) button.current?.focus();
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (!open) return;
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close();
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((a) => (a + 1) % actionable.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => (a - 1 + actionable.length) % actionable.length);
    } else if (e.key === "Home") {
      e.preventDefault();
      setActive(0);
    } else if (e.key === "End") {
      e.preventDefault();
      setActive(actionable.length - 1);
    } else if (e.key === "Tab") {
      setOpen(false);
    }
  };

  return (
    <div ref={root} className={clsx("relative inline-flex", className)} onKeyDown={onKeyDown}>
      <button
        ref={button}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        aria-label={label}
        onClick={(e) => {
          e.stopPropagation();
          setActive(0);
          setOpen((o) => !o);
        }}
        onKeyDown={(e) => {
          if (!open && (e.key === "ArrowDown" || e.key === "Enter" || e.key === " ")) {
            e.preventDefault();
            setActive(0);
            setOpen(true);
          }
        }}
        className={triggerClassName}
      >
        {trigger}
      </button>
      {open ? (
        <div
          id={id}
          role="menu"
          aria-label={label}
          onClick={(e) => e.stopPropagation()}
          className={clsx(
            "animate-in absolute top-full z-50 mt-1 min-w-44 rounded-lg border border-line-strong bg-surface p-1 shadow-pop",
            align === "end" ? "right-0" : "left-0",
          )}
        >
          {items.map((item, i) =>
            item === "separator" ? (
              <div key={`sep-${i}`} role="separator" className="my-1 h-px bg-line" />
            ) : (
              <button
                key={i}
                ref={(el) => {
                  itemRefs.current[i] = el;
                }}
                type="button"
                role={item.checked !== undefined ? "menuitemcheckbox" : "menuitem"}
                aria-checked={item.checked}
                disabled={item.disabled}
                tabIndex={-1}
                onClick={() => {
                  close(false);
                  item.onSelect();
                }}
                className={clsx(
                  "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] outline-none",
                  "hover:bg-surface-2 focus:bg-surface-2 disabled:opacity-50",
                  item.danger ? "text-critical-ink" : "text-fg",
                )}
              >
                {item.icon ? <span className="text-fg-3 [&>svg]:size-3.5">{item.icon}</span> : null}
                <span className="flex-1">{item.label}</span>
                {item.checked ? <span aria-hidden className="text-accent">✓</span> : null}
              </button>
            ),
          )}
        </div>
      ) : null}
    </div>
  );
}
