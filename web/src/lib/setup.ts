// First-run setup: the rules the /setup form checks before the API does, and
// how the API's answers map back onto the form.

import { ApiError } from "@/api/client";

export type SetupField = "token" | "username" | "email" | "password" | "confirm" | "display_name" | "timezone";
export type SetupForm = {
  token: string;
  username: string;
  email: string;
  password: string;
  confirm: string;
  display_name: string;
  timezone: string;
};

// The API's username rule.
const USERNAME = /^[A-Za-z0-9._-]{2,32}$/;

// bcrypt reads at most 72 bytes; the API refuses longer rather than truncate.
export const PASSWORD_MIN = 12;
export const PASSWORD_MAX = 72;
const byteLength = (s: string) => new TextEncoder().encode(s).length;

/** Where /login sends a browser while the server has no account yet (keeps ?token=). */
export function setupRedirect(needed: boolean | undefined, search: string): string | null {
  return needed ? `/setup${search}` : null;
}

/** Field problems worth showing before a round trip. Empty object = submit. */
export function validateSetup(f: SetupForm): Partial<Record<SetupField, string>> {
  const errors: Partial<Record<SetupField, string>> = {};
  if (!f.token.trim()) errors.token = "Paste the setup token from the server log";
  if (!f.username.trim()) errors.username = "Pick a username";
  else if (!USERNAME.test(f.username.trim())) errors.username = "2–32 letters, digits, dots, dashes or underscores";
  if (!/^[^\s@]+@[^\s@]+$/.test(f.email.trim())) errors.email = "An e-mail address, for password resets";
  if (f.password.length < PASSWORD_MIN) errors.password = `At least ${PASSWORD_MIN} characters`;
  else if (byteLength(f.password) > PASSWORD_MAX) errors.password = `At most ${PASSWORD_MAX} characters`;
  if (f.confirm !== f.password) errors.confirm = "Passwords don't match";
  if (f.display_name.trim().length > 100) errors.display_name = "At most 100 characters";
  if (!f.timezone) errors.timezone = "Pick a timezone";
  return errors;
}

export type SetupFailure =
  | { kind: "done" } // an account exists: go and sign in
  | { kind: "field"; field: SetupField; message: string }
  | { kind: "form"; message: string };

export function setupFailure(err: unknown): SetupFailure {
  if (!(err instanceof ApiError)) return { kind: "form", message: err instanceof Error ? err.message : "Setup failed." };
  if (err.code === "setup_done" || err.status === 409) return { kind: "done" };
  if (err.code === "bad_setup_token")
    return { kind: "field", field: "token", message: "That setup token is not the one this server printed." };
  if (err.code === "rate_limited") return { kind: "form", message: "Too many attempts. Wait a few minutes and try again." };
  if (err.code === "validation" && err.field && isSetupField(err.field))
    return { kind: "field", field: err.field, message: err.message };
  return { kind: "form", message: err.message };
}

const FIELDS: SetupField[] = ["token", "username", "email", "password", "confirm", "display_name", "timezone"];
const isSetupField = (f: string): f is SetupField => (FIELDS as string[]).includes(f);

/** Every IANA zone the browser knows, with `current` and UTC always present. */
export function timezoneOptions(current: string): string[] {
  let zones: string[] = [];
  try {
    zones = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.("timeZone") ?? [];
  } catch {
    zones = [];
  }
  if (current && !zones.includes(current)) zones = [current, ...zones];
  if (!zones.includes("UTC")) zones.push("UTC");
  return zones;
}

export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}
