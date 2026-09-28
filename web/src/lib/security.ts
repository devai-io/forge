// Security-log vocabulary and the user-agent summary the sessions and log use.

import type { SecurityKind } from "@/api/types";

export type KindMeta = { label: string; tone: "neutral" | "good" | "warning" | "critical" | "accent"; group: "sign-in" | "account" | "sensitive" | "machines" };

export const SECURITY_KINDS: Record<SecurityKind, KindMeta> = {
  login: { label: "Signed in", tone: "good", group: "sign-in" },
  login_new_device: { label: "Signed in from a new device", tone: "warning", group: "sign-in" },
  login_failed: { label: "Failed sign-in", tone: "critical", group: "sign-in" },
  logout: { label: "Signed out", tone: "neutral", group: "sign-in" },
  elevate: { label: "Confirmed password", tone: "accent", group: "sign-in" },
  elevate_failed: { label: "Wrong password at confirmation", tone: "critical", group: "sign-in" },
  password_changed: { label: "Password changed", tone: "accent", group: "account" },
  password_reset: { label: "Password reset by e-mail link", tone: "accent", group: "account" },
  totp_enabled: { label: "Two-factor turned on", tone: "good", group: "account" },
  totp_disabled: { label: "Two-factor turned off", tone: "warning", group: "account" },
  session_revoked: { label: "Session signed out", tone: "neutral", group: "account" },
  sessions_revoked_others: { label: "Other sessions signed out", tone: "neutral", group: "account" },
  vault_reveal: { label: "Vault secret revealed", tone: "warning", group: "sensitive" },
  vault_download: { label: "Vault file downloaded", tone: "warning", group: "sensitive" },
  terminal_attach: { label: "Terminal attached", tone: "accent", group: "sensitive" },
  terminal_keys: { label: "Keys sent to a session", tone: "accent", group: "sensitive" },
  terminal_create: { label: "Session started", tone: "accent", group: "sensitive" },
  code_open: { label: "VS Code opened", tone: "accent", group: "sensitive" },
  setup: { label: "Account created (first-run setup)", tone: "accent", group: "account" },
  runner_created: { label: "Machine added", tone: "neutral", group: "machines" },
  runner_pair_code: { label: "Pairing code issued", tone: "neutral", group: "machines" },
  runner_paired: { label: "Machine paired", tone: "accent", group: "machines" },
  runner_rotated: { label: "Machine token rotated", tone: "neutral", group: "machines" },
};

/** Label for a kind; unknown kinds (a newer API) are shown as typed. */
export function kindLabel(kind: string): string {
  return (SECURITY_KINDS as Record<string, KindMeta>)[kind]?.label ?? kind.replace(/_/g, " ");
}

/** Rows worth a second look: someone new got in, or someone tried. */
export const isAlarming = (kind: string) => kind === "login_new_device" || kind === "login_failed" || kind === "elevate_failed";

/** Filter options for the log: every known kind, grouped. */
export const KIND_GROUPS: { label: string; kinds: SecurityKind[] }[] = (["sign-in", "account", "sensitive", "machines"] as const).map((group) => ({
  label: { "sign-in": "Sign-in", account: "Account", sensitive: "Sensitive actions", machines: "Machines" }[group],
  kinds: (Object.keys(SECURITY_KINDS) as SecurityKind[]).filter((k) => SECURITY_KINDS[k].group === group),
}));

/** "Chrome · Linux", "Safari · iOS", "Firefox · macOS" — enough to recognise a device. */
export function describeAgent(ua: string): string {
  if (!ua) return "Unknown device";
  const browser = /Edg\//.test(ua)
    ? "Edge"
    : /OPR\//.test(ua)
      ? "Opera"
      : /Firefox\//.test(ua)
        ? "Firefox"
        : /CriOS\//.test(ua)
          ? "Chrome"
          : /Chrome\//.test(ua)
            ? "Chrome"
            : /Safari\//.test(ua)
              ? "Safari"
              : /curl\//.test(ua)
                ? "curl"
                : "Browser";
  const os = /iPhone|iPad|iPod/.test(ua)
    ? "iOS"
    : /Android/.test(ua)
      ? "Android"
      : /Mac OS X/.test(ua)
        ? "macOS"
        : /Windows/.test(ua)
          ? "Windows"
          : /CrOS/.test(ua)
            ? "ChromeOS"
            : /Linux/.test(ua)
              ? "Linux"
              : "";
  return os ? `${browser} · ${os}` : browser;
}
