import { formatDateTime, relativeTime } from "@/lib/format";
import { useNow } from "@/lib/now";


export function RelativeTime({ iso, className, fallback = "never" }: { iso: string | null | undefined; className?: string; fallback?: string }) {
  const t = useNow();
  if (!iso) return <span className={className}>{fallback}</span>;
  return (
    <time dateTime={iso} title={formatDateTime(iso)} className={className}>
      {relativeTime(iso, new Date(t))}
    </time>
  );
}
