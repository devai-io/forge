import clsx from "clsx";
import type { ReactNode } from "react";

/** A titled card section. */
export function Panel({
  title,
  icon,
  actions,
  children,
  className,
  bodyClassName,
  id,
}: {
  title: ReactNode;
  icon?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
  id?: string;
}) {
  return (
    <section aria-labelledby={id} className={clsx("flex min-w-0 flex-col rounded-xl border border-line bg-surface", className)}>
      <header className="flex h-11 items-center gap-2 border-b border-line px-3.5">
        {icon ? <span className="text-fg-3 [&>svg]:size-4">{icon}</span> : null}
        <h2 id={id} className="min-w-0 flex-1 truncate text-[13.5px] font-semibold">
          {title}
        </h2>
        {actions}
      </header>
      <div className={clsx("min-w-0 flex-1 p-2", bodyClassName)}>{children}</div>
    </section>
  );
}

export function PageHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {subtitle ? <p className="mt-0.5 text-[13px] text-fg-3">{subtitle}</p> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  );
}
