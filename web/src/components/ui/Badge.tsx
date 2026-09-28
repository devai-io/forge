import clsx from "clsx";
import type { ReactNode } from "react";

type Tone = "neutral" | "accent" | "good" | "warning" | "serious" | "critical" | "outline";

const tones: Record<Tone, string> = {
  neutral: "bg-surface-2 text-fg-2 border-line",
  outline: "bg-transparent text-fg-2 border-line-strong",
  accent: "bg-accent/12 text-fg border-accent/30",
  good: "bg-good/12 text-fg border-good/35",
  warning: "bg-warning/15 text-fg border-warning/40",
  serious: "bg-serious/15 text-fg border-serious/40",
  critical: "bg-critical/12 text-fg border-critical/40",
};

const dots: Partial<Record<Tone, string>> = {
  accent: "bg-accent",
  good: "bg-good",
  warning: "bg-warning",
  serious: "bg-serious",
  critical: "bg-critical",
};

/**
 * A small pill. Status tones carry a dot of the status colour next to ink
 * text — the text never wears the status colour, so the label stays legible
 * and colour is never the only signal.
 */
export function Badge({
  tone = "neutral",
  dot,
  children,
  className,
  title,
}: {
  tone?: Tone;
  dot?: boolean | string;
  children: ReactNode;
  className?: string;
  title?: string;
}) {
  const dotClass = typeof dot === "string" ? undefined : dots[tone];
  return (
    <span
      title={title}
      className={clsx(
        "inline-flex h-5 shrink-0 items-center gap-1.5 rounded-full border px-2 text-[11.5px] font-medium whitespace-nowrap",
        tones[tone],
        className,
      )}
    >
      {dot ? (
        <span
          aria-hidden
          className={clsx("size-1.5 rounded-full", dotClass ?? "bg-fg-3")}
          style={typeof dot === "string" ? { background: dot } : undefined}
        />
      ) : null}
      {children}
    </span>
  );
}

/** A project's colour as a small square — identity beside ink text. */
export function ColorDot({ color, className }: { color: string | null | undefined; className?: string }) {
  return (
    <span
      aria-hidden
      className={clsx("inline-block size-2.5 shrink-0 rounded-[3px]", className)}
      style={{ background: color || "var(--fg-3)" }}
    />
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="rounded border border-line-strong bg-surface-2 px-1 font-mono text-[10.5px] text-fg-3">
      {children}
    </kbd>
  );
}
