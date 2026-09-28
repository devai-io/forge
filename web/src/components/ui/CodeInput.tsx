import clsx from "clsx";
import type { ComponentProps } from "react";

/** A 6-digit TOTP field: numeric keypad on phones, one-time-code autofill, digits only. */
export function CodeInput({
  value,
  onChange,
  className,
  ...props
}: Omit<ComponentProps<"input">, "value" | "onChange"> & { value: string; onChange: (v: string) => void }) {
  return (
    <input
      type="text"
      inputMode="numeric"
      autoComplete="one-time-code"
      pattern="[0-9]{6}"
      maxLength={6}
      placeholder="123456"
      value={value}
      onChange={(e) => onChange(e.target.value.replace(/\D/g, "").slice(0, 6))}
      className={clsx(
        "h-10 w-full rounded-md border border-line-strong bg-surface px-3 text-center font-mono text-lg tracking-[0.4em] text-fg",
        "placeholder:text-fg-3/60 focus:border-accent focus:ring-2 focus:ring-accent/25 focus:outline-none focus-visible:outline-none",
        className,
      )}
      {...props}
    />
  );
}
