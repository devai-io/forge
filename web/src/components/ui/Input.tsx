import clsx from "clsx";
import { useId, type ComponentProps, type ReactNode } from "react";

// Full width unless the caller sizes the control itself ("w-auto", "w-40"…):
// clsx doesn't dedupe, and w-full vs w-auto would resolve by stylesheet order.
const width = (className: string | undefined) => (className && /(^|\s)(sm:|md:)?w-/.test(className) ? "" : "w-full");

const control =
  "rounded-md border border-line-strong bg-surface px-2.5 text-sm text-fg placeholder:text-fg-3 " +
  "transition-colors focus:border-accent focus:outline-none focus-visible:outline-none focus:ring-2 focus:ring-accent/25 " +
  "disabled:opacity-60 aria-[invalid=true]:border-critical";

// `compact` instead of a height class from the caller: clsx does not dedupe,
// and two competing h-* utilities resolve by stylesheet order, not intent.
export function Input({ className, compact, ...props }: ComponentProps<"input"> & { compact?: boolean }) {
  return <input className={clsx(control, width(className), compact ? "h-7.5" : "h-8.5", className)} {...props} />;
}

export function Textarea({ className, ...props }: ComponentProps<"textarea">) {
  return <textarea className={clsx(control, width(className), "min-h-24 py-2 leading-relaxed", className)} {...props} />;
}

export function Select({ className, children, compact, ...props }: ComponentProps<"select"> & { compact?: boolean }) {
  return (
    <select
      className={clsx(
        control,
        width(className),
        compact ? "h-7.5 text-[13px]" : "h-8.5",
        "appearance-none bg-[length:14px] bg-[right_8px_center] bg-no-repeat pr-7",
        "bg-[url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='%23898781' stroke-width='2'%3E%3Cpath d='m6 9 6 6 6-6'/%3E%3C/svg%3E\")]",
        className,
      )}
      {...props}
    >
      {children}
    </select>
  );
}

export function Label({ className, ...props }: ComponentProps<"label">) {
  return <label className={clsx("block text-[13px] font-medium text-fg-2", className)} {...props} />;
}

/** Label + control + hint/error, wired together with ids. */
export function Field({
  label,
  hint,
  error,
  children,
  className,
}: {
  label: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  children: (id: string, describedBy: string | undefined) => ReactNode;
  className?: string;
}) {
  const id = useId();
  const descId = hint || error ? `${id}-desc` : undefined;
  return (
    <div className={clsx("space-y-1.5", className)}>
      <Label htmlFor={id}>{label}</Label>
      {children(id, descId)}
      {error ? (
        <p id={descId} className="text-xs text-critical-ink">
          {error}
        </p>
      ) : hint ? (
        <p id={descId} className="text-xs text-fg-3">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

export function Checkbox({ className, ...props }: Omit<ComponentProps<"input">, "type">) {
  return (
    <input
      type="checkbox"
      className={clsx("size-4 shrink-0 rounded border-line-strong accent-[var(--accent)]", className)}
      {...props}
    />
  );
}

export function Switch({
  checked,
  onChange,
  label,
  disabled,
  id,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  label: ReactNode;
  disabled?: boolean;
  id?: string;
}) {
  return (
    <label className="inline-flex cursor-pointer items-center gap-2 text-sm select-none" htmlFor={id}>
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={clsx(
          "relative h-5 w-9 shrink-0 rounded-full border border-line-strong transition-colors",
          checked ? "bg-accent" : "bg-surface-3",
        )}
      >
        <span
          className={clsx(
            "absolute top-0.5 size-3.5 rounded-full bg-white shadow transition-[left]",
            checked ? "left-[18px]" : "left-0.5",
          )}
        />
      </button>
      <span className="text-fg-2">{label}</span>
    </label>
  );
}
