// The dashboard's "Get started" checklist, shown while the account has no
// machines. Done-ness comes from data the dashboard already has; the two bits
// that don't (dismissed, docs opened) are per-browser in localStorage.

export type StartStep = { id: "machine" | "project" | "2fa" | "docs"; label: string; hint: string; to: string; done: boolean };

export function gettingStartedSteps(s: { machines: number; projects: number; totp: boolean; docsRead: boolean }): StartStep[] {
  return [
    { id: "machine", label: "Add a machine", hint: "Pair your desktop or a server with one command", to: "/agents?add=1", done: s.machines > 0 },
    { id: "project", label: "Create a project", hint: "Or explore the demo data", to: "/projects", done: s.projects > 0 },
    { id: "2fa", label: "Turn on two-factor", hint: "A code from an authenticator app", to: "/settings#settings-2fa", done: s.totp },
    { id: "docs", label: "Read the docs", hint: "What Forge does, and what it doesn't", to: "/docs", done: s.docsRead },
  ];
}

const DISMISSED = "forge.getting-started.dismissed";
const DOCS_READ = "forge.docs-read";

function read(key: string): boolean {
  try {
    return localStorage.getItem(key) === "1";
  } catch {
    return false;
  }
}

function write(key: string) {
  try {
    localStorage.setItem(key, "1");
  } catch {
    /* storage blocked: it just shows again next time */
  }
}

export const gettingStartedDismissed = () => read(DISMISSED);
export const dismissGettingStarted = () => write(DISMISSED);
export const docsRead = () => read(DOCS_READ);
export const markDocsRead = () => write(DOCS_READ);
