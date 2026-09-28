import clsx from "clsx";
import { CircleCheck, CircleSlash, CircleX, LoaderCircle } from "lucide-react";
import type { CIStatus } from "@/api/types";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { ciState } from "@/lib/projects";

const LABEL = { success: "passing", failure: "failing", running: "running", cancelled: "cancelled", neutral: "" } as const;

/** Latest GitHub Actions run for a repo's default branch: icon + word + workflow, linking to the run. */
export function CIBadge({ ci }: { ci: CIStatus }) {
  const state = ciState(ci);
  const icon =
    state === "success" ? (
      <CircleCheck className="size-3.5 text-good" aria-hidden />
    ) : state === "failure" ? (
      <CircleX className="size-3.5 text-critical" aria-hidden />
    ) : state === "running" ? (
      <LoaderCircle className="size-3.5 animate-spin text-accent" aria-hidden />
    ) : (
      <CircleSlash className="size-3.5 text-fg-3" aria-hidden />
    );
  const content = (
    <>
      {icon}
      <span className={clsx("font-medium", state === "failure" && "text-critical-ink")}>{LABEL[state] || ci.conclusion || ci.status}</span>
      <span className="min-w-0 truncate text-fg-3">
        {ci.workflow}
        {ci.at ? (
          <>
            {" "}
            · <RelativeTime iso={ci.at} />
          </>
        ) : null}
      </span>
    </>
  );
  const cls = "inline-flex min-w-0 max-w-full items-center gap-1 text-[11.5px] text-fg-2";
  return ci.url ? (
    <a href={ci.url} target="_blank" rel="noopener noreferrer" className={clsx(cls, "hover:text-fg")} title={ci.title || ci.workflow}>
      {content}
    </a>
  ) : (
    <span className={cls} title={ci.title || ci.workflow}>
      {content}
    </span>
  );
}
