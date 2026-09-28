// Vault display helpers. Nothing here ever sees a secret value except
// `maskValue`, which only measures it.

import type { VaultItem, VaultKind } from "@/api/types";
import { daysBetween } from "./format";

export const VAULT_KINDS: { value: VaultKind; label: string }[] = [
  { value: "password", label: "Password" },
  { value: "api_key", label: "API key" },
  { value: "token", label: "Token" },
  { value: "private_key", label: "Private key" },
  { value: "keystore", label: "Keystore" },
  { value: "certificate", label: "Certificate" },
  { value: "provisioning_profile", label: "Provisioning profile" },
  { value: "service_account", label: "Service account" },
  { value: "env_file", label: "Env file" },
  { value: "note", label: "Secure note" },
  { value: "reference", label: "Reference" },
];

export const VAULT_PLATFORMS = ["ios", "android", "web", "ci", "infra"];
export const VAULT_HOSTS = ["desk", "mac", "sops", "github", "1password", "desktop"];

export const kindLabel = (k: VaultKind) => VAULT_KINDS.find((x) => x.value === k)?.label ?? k;

/** 1 KB = 1024 B; one decimal above a kilobyte. */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

/** Holds nothing Forge can reveal — it only says where the original lives. */
export const isReference = (item: Pick<VaultItem, "secret_keys" | "has_file">) => item.secret_keys.length === 0 && !item.has_file;

export type ExpiryState = { state: "expired" | "soon" | "ok"; days: number; label: string };

/** Expired → red; under 30 days → amber (the check-up warns at the same line). */
export function expiryState(expires: string | null, today: string): ExpiryState | null {
  if (!expires) return null;
  const days = daysBetween(today, expires);
  if (days < 0) return { state: "expired", days, label: `expired ${-days}d ago` };
  if (days === 0) return { state: "soon", days, label: "expires today" };
  if (days < 30) return { state: "soon", days, label: `expires in ${days}d` };
  return { state: "ok", days, label: `expires ${expires}` };
}

/** "JBSWY3DPEHPK3PXP" → "JBSW Y3DP EHPK 3PXP", for reading a key off a screen. */
export function groupSecret(secret: string): string {
  return secret.replace(/\s+/g, "").replace(/(.{4})(?=.)/g, "$1 ");
}

/** Bullets roughly as long as the value, capped, so a mask hints at size only. */
export function maskValue(value: string): string {
  return "•".repeat(Math.min(24, Math.max(8, value.length)));
}

/** Group items by project, projects first (shopbetically by key), shared last. */
export function groupByProject(items: VaultItem[]): { key: string | null; color: string | null; items: VaultItem[] }[] {
  const map = new Map<string | null, VaultItem[]>();
  for (const item of items) {
    const list = map.get(item.project_key) ?? [];
    list.push(item);
    map.set(item.project_key, list);
  }
  return Array.from(map.entries())
    .sort(([a], [b]) => (a === null ? 1 : b === null ? -1 : a.localeCompare(b)))
    .map(([key, list]) => ({
      key,
      color: list[0]?.project_color ?? null,
      items: list.sort((x, y) => x.name.localeCompare(y.name)),
    }));
}

/** Read a File as base64 (no data: prefix) for the API's `content_base64`. */
export function fileToBase64(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = String(reader.result ?? "");
      resolve(result.slice(result.indexOf(",") + 1));
    };
    reader.onerror = () => reject(reader.error ?? new Error("Couldn't read the file"));
    reader.readAsDataURL(file);
  });
}

/** Rows ↔ record for the key/value editors; blank keys are dropped, last duplicate wins. */
export type KV = { key: string; value: string };
export function rowsToRecord(rows: KV[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const r of rows) {
    const k = r.key.trim();
    if (k) out[k] = r.value;
  }
  return out;
}
export function recordToRows(record: Record<string, string>): KV[] {
  return Object.entries(record).map(([key, value]) => ({ key, value }));
}

/** Save a Blob under a filename via a temporary object URL. */
export function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
}
