import clsx from "clsx";
import { Cpu, Crown, Smartphone } from "lucide-react";
import type { RunnerRole } from "@/api/types";
import { ROLE_LABEL } from "@/lib/agents";

const TITLE: Record<RunnerRole, string> = {
  master: "Master: the primary machine — default for runs and terminals, and the only source of repo git/CI state",
  ios: "iOS: the Mac, for iOS builds",
  worker: "Worker: runs what it's given; may sleep",
};

/** Master is loud on purpose — it's the machine everything defaults to. */
export function RoleBadge({ role, className }: { role: RunnerRole; className?: string }) {
  const Icon = role === "master" ? Crown : role === "ios" ? Smartphone : Cpu;
  return (
    <span
      title={TITLE[role]}
      className={clsx(
        "inline-flex h-5 shrink-0 items-center gap-1 rounded-full border px-2 text-[11px] font-semibold whitespace-nowrap",
        role === "master"
          ? "border-transparent bg-warning text-[#2b1d00]"
          : role === "ios"
            ? "border-line-strong bg-surface-2 text-fg"
            : "border-line bg-transparent text-fg-3 font-medium",
        className,
      )}
    >
      <Icon className="size-3" aria-hidden />
      {ROLE_LABEL[role]}
    </span>
  );
}
