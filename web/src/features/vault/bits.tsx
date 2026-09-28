import clsx from "clsx";
import {
  BadgeCheck,
  Bot,
  FileCode,
  FileKey,
  Key,
  KeyRound,
  Link2,
  Paperclip,
  Smartphone,
  StickyNote,
  Ticket,
  Vault,
} from "lucide-react";
import type { VaultItem, VaultKind } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { expiryState, formatBytes, kindLabel } from "@/lib/vault";

export function VaultKindIcon({ kind, className }: { kind: VaultKind; className?: string }) {
  const props = { className: clsx("shrink-0 text-fg-3", className ?? "size-4"), role: "img", "aria-label": kindLabel(kind) };
  switch (kind) {
    case "password":
      return <KeyRound {...props} />;
    case "api_key":
      return <Key {...props} />;
    case "token":
      return <Ticket {...props} />;
    case "private_key":
      return <FileKey {...props} />;
    case "keystore":
      return <Vault {...props} />;
    case "certificate":
      return <BadgeCheck {...props} />;
    case "provisioning_profile":
      return <Smartphone {...props} />;
    case "service_account":
      return <Bot {...props} />;
    case "env_file":
      return <FileCode {...props} />;
    case "note":
      return <StickyNote {...props} />;
    default:
      return <Link2 {...props} />;
  }
}

export function ExpiryBadge({ expires, today }: { expires: string | null; today: string }) {
  const e = expiryState(expires, today);
  if (!e || e.state === "ok") return null;
  return (
    <Badge tone={e.state === "expired" ? "critical" : "warning"} dot title={`Expires ${expires}`}>
      {e.label}
    </Badge>
  );
}

export function FileBadge({ item }: { item: Pick<VaultItem, "has_file" | "file_name" | "file_size"> }) {
  if (!item.has_file) return null;
  return (
    <Badge tone="outline" title={item.file_name}>
      <Paperclip className="size-3" aria-hidden />
      <span className="max-w-40 truncate">{item.file_name || "file"}</span>
      <span className="text-fg-3">{formatBytes(item.file_size)}</span>
    </Badge>
  );
}

export function SecretKeyChips({ keys }: { keys: string[] }) {
  if (!keys.length) return null;
  return (
    <span className="inline-flex flex-wrap gap-1">
      {keys.map((k) => (
        <span key={k} className="rounded bg-surface-2 px-1.5 font-mono text-[10.5px] text-fg-2">
          {k}
        </span>
      ))}
    </span>
  );
}
