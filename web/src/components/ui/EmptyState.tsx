import clsx from "clsx";
import type { ReactNode } from "react";

export function EmptyState({
  icon,
  title,
  children,
  action,
  className,
  compact,
}: {
  icon?: ReactNode;
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
  className?: string;
  compact?: boolean;
}) {
  return (
    <div
      className={clsx(
        "flex flex-col items-center justify-center rounded-lg border border-dashed border-line-strong text-center",
        compact ? "gap-1.5 px-4 py-6" : "gap-2 px-6 py-10",
        className,
      )}
    >
      {icon ? <div className="text-fg-3 [&>svg]:size-5">{icon}</div> : null}
      <p className="text-sm font-medium text-fg-2">{title}</p>
      {children ? <div className="max-w-sm text-[13px] text-fg-3">{children}</div> : null}
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  );
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const message = error instanceof Error ? error.message : "Something went wrong.";
  return (
    <div role="alert" className="rounded-lg border border-critical/40 bg-critical/8 px-4 py-3 text-sm">
      <p className="text-fg">{message}</p>
      {onRetry ? (
        <button type="button" onClick={onRetry} className="mt-1 text-[13px] text-accent hover:underline">
          Try again
        </button>
      ) : null}
    </div>
  );
}
