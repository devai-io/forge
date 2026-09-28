import { Link } from "react-router-dom";

/**
 * "How this works" — a page header's link into a section of the docs. Its own
 * tiny module so pages that show it don't pull the whole documentation page
 * (and its content) into the main bundle.
 */
export function HelpLink({ anchor, label = "How this works" }: { anchor: string; label?: string }) {
  return (
    <Link
      to={`/docs#${anchor}`}
      className="inline-flex h-7 items-center gap-1 rounded-full border border-line px-2 text-[12px] text-fg-3 hover:border-line-strong hover:text-fg"
      title={label}
    >
      <span aria-hidden className="grid size-4 place-items-center rounded-full bg-surface-2 text-[11px] font-semibold">
        ?
      </span>
      <span className="hidden sm:inline">{label}</span>
      <span className="sr-only sm:hidden">{label}</span>
    </Link>
  );
}
