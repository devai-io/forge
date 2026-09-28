// VS Code (web) helpers: what an editor target is, how it appears in the URL,
// and the per-tab memory of opened editors (sessionStorage — an open is bound
// to this login's forge_code cookie, so it should not outlive the tab).

import type { CodeOpenInput, CodeOpenResult } from "@/api/types";

export type EditorTarget =
  | { kind: "project"; projectKey: string; repoIds: number[] }
  | { kind: "folder"; path: string };

/** /editor?project=SHOP[&repos=1,2] or /editor?folder=/abs/path → target (null = landing page). */
export function parseEditorTarget(params: URLSearchParams): EditorTarget | null {
  const folder = params.get("folder");
  if (folder && folder.startsWith("/")) return { kind: "folder", path: folder };
  const project = params.get("project");
  if (project && /^[A-Za-z][A-Za-z0-9]{1,9}$/.test(project)) {
    const repoIds = (params.get("repos") ?? "")
      .split(",")
      .map(Number)
      .filter((n) => Number.isInteger(n) && n > 0);
    return { kind: "project", projectKey: project.toUpperCase(), repoIds: Array.from(new Set(repoIds)).sort((a, b) => a - b) };
  }
  return null;
}

export function editorPath(target: EditorTarget): string {
  const q = new URLSearchParams();
  if (target.kind === "folder") q.set("folder", target.path);
  else {
    q.set("project", target.projectKey);
    if (target.repoIds.length) q.set("repos", target.repoIds.join(","));
  }
  return `/editor?${q.toString()}`;
}

/** The body for POST /api/code/open. */
export function openBody(target: EditorTarget): CodeOpenInput {
  if (target.kind === "folder") return { folder: target.path };
  return target.repoIds.length ? { project_key: target.projectKey, repo_ids: target.repoIds } : { project_key: target.projectKey };
}

/** Stable storage key for a target. */
export function targetKey(target: EditorTarget): string {
  return target.kind === "folder" ? `folder:${target.path}` : `project:${target.projectKey}${target.repoIds.length ? `:${target.repoIds.join(",")}` : ""}`;
}

/** Only ever frame our own /code/ — never whatever URL a response carried. */
export function isSafeCodeUrl(url: string): boolean {
  return url.startsWith("/code/") && !url.startsWith("//") && !/[\s\\]/.test(url);
}

export type StoredOpen = CodeOpenResult & { key: string; label: string; path: string; opened_at: string };

const PREFIX = "forge.code.";
const RECENT = "forge.code.recent";
const MAX_RECENT = 8;

function storage(): Storage | null {
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

/** A stored open that has not expired (with a minute of slack), or null. */
export function readOpen(target: EditorTarget, now = Date.now()): StoredOpen | null {
  const raw = storage()?.getItem(PREFIX + targetKey(target));
  if (!raw) return null;
  try {
    const open = JSON.parse(raw) as StoredOpen;
    if (!isSafeCodeUrl(open.url)) return null;
    if (Date.parse(open.expires_at) - now < 60_000) return null;
    return open;
  } catch {
    return null;
  }
}

export function storeOpen(target: EditorTarget, result: CodeOpenResult, label: string, now = Date.now()): StoredOpen {
  const entry: StoredOpen = { ...result, key: targetKey(target), label, path: editorPath(target), opened_at: new Date(now).toISOString() };
  const s = storage();
  if (!s) return entry;
  try {
    s.setItem(PREFIX + entry.key, JSON.stringify(entry));
    const recent = recentOpens().filter((r) => r.key !== entry.key);
    s.setItem(RECENT, JSON.stringify([{ key: entry.key, label, path: entry.path, opened_at: entry.opened_at }, ...recent].slice(0, MAX_RECENT)));
  } catch {
    /* storage full or blocked: the editor still opens, it just won't be remembered */
  }
  return entry;
}

export function forgetOpen(target: EditorTarget) {
  try {
    storage()?.removeItem(PREFIX + targetKey(target));
  } catch {
    /* ignore */
  }
}

export type RecentOpen = { key: string; label: string; path: string; opened_at: string };

export function recentOpens(): RecentOpen[] {
  try {
    const raw = storage()?.getItem(RECENT);
    const list = raw ? (JSON.parse(raw) as RecentOpen[]) : [];
    return Array.isArray(list) ? list.filter((r) => typeof r.path === "string" && r.path.startsWith("/editor?")) : [];
  } catch {
    return [];
  }
}

/** Last path segment of a folder, for labels: "/home/ada/dev/forge/forge_api" → "forge_api". */
export const folderName = (path: string) => path.replace(/\/+$/, "").split("/").pop() || path;

/**
 * What an editor frame that loaded something other than VS Code tells us.
 * /code/ answers 401/403 JSON once the forge_code cookie is gone ("expired");
 * a not_found means /code/ isn't routed to the API at all. null = VS Code.
 */
export function frameFailure(doc: Pick<Document, "contentType"> & { body: { textContent: string | null } | null } | null):
  | "expired"
  | { message: string }
  | null {
  if (!doc || !doc.contentType.includes("json")) return null;
  let code = "";
  let message = "";
  try {
    const body = JSON.parse(doc.body?.textContent ?? "") as { error?: { code?: string; message?: string } };
    code = body.error?.code ?? "";
    message = body.error?.message ?? "";
  } catch {
    /* JSON content type with an unreadable body: treat as an expired session */
  }
  if (code === "not_found") return { message: message || "The editor is not reachable at /code/." };
  return "expired";
}

/**
 * The editor URL with Forge's resolved theme (and accent, as "rrggbb" without
 * the "#"), so VS Code's first paint already matches. Replaces earlier values;
 * never touches the rest.
 */
export function withTheme(url: string, theme: "light" | "dark", accentHex?: string): string {
  const [base, hash = ""] = url.split("#");
  const [path, query = ""] = base.split("?");
  const q = new URLSearchParams(query);
  q.set("forge_theme", theme);
  const accent = accentHex?.replace(/^#/, "").toLowerCase();
  if (accent && /^[0-9a-f]{6}$/.test(accent)) q.set("forge_accent", accent);
  else q.delete("forge_accent");
  return `${path}?${q.toString()}${hash ? `#${hash}` : ""}`;
}
