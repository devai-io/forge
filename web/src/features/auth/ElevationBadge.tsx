import { ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/Badge";
import { useElevatedUntil } from "@/lib/auth";
import { useNow } from "@/lib/now";

/** "Unlocked · 8 min": shown while a recent password re-entry still covers step-up actions. */
export function ElevationBadge() {
  const until = useElevatedUntil();
  const now = useNow();
  if (!until) return null;
  const left = new Date(until).getTime() - now;
  if (!(left > 0)) return null;
  const minutes = Math.max(1, Math.round(left / 60_000));
  return (
    <Badge tone="good" title="Recently confirmed with your password — reveals and deletes won't ask again until this runs out">
      <ShieldCheck className="size-3" aria-hidden /> unlocked · {minutes} min
    </Badge>
  );
}
