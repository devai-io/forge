// Agent versions against the server's: the label ("v0.1.42", never "vv…")
// and whether a machine is behind.

/** "0.1.42" or "v0.1.42" → "v0.1.42"; anything else ("dev") as it is. */
export function versionLabel(v: string): string {
  const t = v.trim();
  return /^\d+(\.\d+)*([-+].*)?$/.test(t) ? `v${t}` : t;
}

/** [0, 1, 42] for "v0.1.42" (a "-rc1" or "+meta" suffix ignored); null when it is not a release version. */
function parts(v: string): number[] | null {
  const m = v.trim().replace(/^v/, "").match(/^(\d+(?:\.\d+)*)(?:[-+].*)?$/);
  return m ? m[1].split(".").map(Number) : null;
}

/** Whether `server` is a newer release than `agent`; false when either is not a release version (a dev build). */
export function newerVersion(server: string, agent: string): boolean {
  const a = parts(agent);
  const b = parts(server);
  if (!a || !b) return false;
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    const d = (b[i] ?? 0) - (a[i] ?? 0);
    if (d) return d > 0;
  }
  return false;
}
