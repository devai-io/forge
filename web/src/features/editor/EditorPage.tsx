// VS Code (web) from the master machine, embedded at /code/ on Forge's own
// origin. /editor?project=KEY[&repos=…] opens a project (multi-root
// workspace), /editor?folder=/path one folder; bare /editor is a landing page.
//
// Opening is a POST that needs elevation (the client's confirm-it's-you retry
// handles it) and sets a 12-hour forge_code cookie for /code/. This tab
// remembers each target's URL in sessionStorage, so coming back reuses it
// instead of opening again. An iframe can't report its HTTP status, so a
// remembered URL is probed first, and the frame's own document is checked on
// load for the 401/403 JSON an expired cookie gets.

import { Code, FolderOpen, History, KeyRound, TriangleAlert } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { errorMessage, isElevationError } from "@/api/client";
import { useCodeStatus, useOpenCode, useProject, useProjects } from "@/api/hooks";
import { ColorDot } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Spinner } from "@/components/ui/Spinner";
import {
  folderName,
  forgetOpen,
  frameFailure,
  isSafeCodeUrl,
  openBody,
  parseEditorTarget,
  readOpen,
  recentOpens,
  storeOpen,
  targetKey,
  withTheme,
  type EditorTarget,
  type StoredOpen,
} from "@/lib/code";
import { EditorToolbar } from "./EditorToolbar";
import { accentHex } from "@/lib/accent";
import { useUser } from "@/lib/auth";
import { useTheme } from "@/lib/theme";
import { useEditorLauncher } from "./launcher";

export function EditorPage() {
  const [params] = useSearchParams();
  const target = parseEditorTarget(params);
  return target ? <EditorFrame key={targetKey(target)} target={target} /> : <EditorLanding />;
}

type State =
  | { kind: "opening" }
  | { kind: "probing"; open: StoredOpen }
  | { kind: "ready"; open: StoredOpen }
  | { kind: "expired" }
  | { kind: "cancelled" }
  | { kind: "error"; message: string }
  | { kind: "unavailable"; reason: string };

/** A GET of the editor URL: 401/403 means the forge_code cookie is gone. */
async function probe(url: string): Promise<"ok" | "expired"> {
  try {
    const res = await fetch(url, { credentials: "same-origin", headers: { Accept: "text/html" } });
    return res.status === 401 || res.status === 403 ? "expired" : "ok";
  } catch {
    return "ok"; // let the frame try; it reports its own failure
  }
}

function useTargetLabel(target: EditorTarget): { label: string; detail?: string } {
  const projects = useProjects();
  const project = useProject(target.kind === "project" ? target.projectKey : undefined);
  if (target.kind === "folder") return { label: folderName(target.path), detail: target.path };
  const name = projects.data?.find((p) => p.key === target.projectKey)?.name ?? target.projectKey;
  if (!target.repoIds.length) return { label: name, detail: "all repositories" };
  const repos = project.data?.repos.filter((r) => target.repoIds.includes(r.id)).map((r) => r.name) ?? [];
  return { label: name, detail: repos.join(", ") || `${target.repoIds.length} repositor${target.repoIds.length === 1 ? "y" : "ies"}` };
}

function EditorFrame({ target }: { target: EditorTarget }) {
  const navigate = useNavigate();
  const status = useCodeStatus();
  const openCode = useOpenCode();
  const { label, detail } = useTargetLabel(target);
  const [state, setState] = useState<State>({ kind: "opening" });
  const [frameKey, setFrameKey] = useState(0);
  const frame = useRef<HTMLIFrameElement>(null);
  const started = useRef(false);
  // The editor's first paint follows Forge's theme; a change of theme (a
  // toggle, or the OS at dusk) changes the src, which reloads the frame.
  const { resolved: theme } = useTheme();
  const accent = accentHex(useUser().accent, theme);

  const reopen = useCallback(() => {
    setState({ kind: "opening" });
    openCode.mutate(openBody(target), {
      onSuccess: (result) => {
        if (!isSafeCodeUrl(result.url)) {
          setState({ kind: "error", message: "The API answered with an editor URL outside /code/." });
          return;
        }
        setState({ kind: "ready", open: storeOpen(target, result, label + (detail && target.kind === "project" ? ` · ${detail}` : "")) });
        setFrameKey((k) => k + 1);
      },
      onError: (err) =>
        setState(isElevationError(err) ? { kind: "cancelled" } : { kind: "error", message: errorMessage(err) }),
    });
  }, [openCode, target, label, detail]);

  // First load: reuse this tab's open for the target if it is still valid,
  // otherwise open (once — StrictMode runs effects twice in development).
  useEffect(() => {
    if (started.current) return;
    if (status.isPending) return;
    started.current = true;
    const stored = readOpen(target);
    if (stored) {
      setState({ kind: "probing", open: stored });
      void probe(stored.url).then((r) => setState(r === "ok" ? { kind: "ready", open: stored } : { kind: "expired" }));
      return;
    }
    if (status.data && !status.data.available) {
      setState({ kind: "unavailable", reason: status.data.reason || "VS Code is not available right now." });
      return;
    }
    reopen();
  }, [status.isPending, status.data, target, reopen]);

  // The cookie's lifetime is known: flip to "expired" when it runs out.
  const ready = state.kind === "ready" ? state.open : null;
  useEffect(() => {
    if (!ready) return;
    const ms = Date.parse(ready.expires_at) - Date.now();
    if (!(ms > 0)) return;
    const t = window.setTimeout(() => setState({ kind: "expired" }), Math.min(ms, 2 ** 31 - 1));
    return () => window.clearTimeout(t);
  }, [ready]);

  const onFrameLoad = () => {
    // Same origin, so the frame's document is readable: an expired cookie
    // answers /code/ with a JSON error instead of VS Code's HTML.
    let failure: ReturnType<typeof frameFailure> = null;
    try {
      failure = frameFailure(frame.current?.contentDocument ?? null);
    } catch {
      /* not readable: VS Code loaded */
    }
    if (failure === "expired") {
      forgetOpen(target);
      setState({ kind: "expired" });
    } else if (failure) {
      setState({ kind: "error", message: failure.message });
    }
  };

  const reload = () => {
    if (state.kind === "ready") {
      void probe(state.open.url).then((r) => {
        if (r === "expired") {
          forgetOpen(target);
          setState({ kind: "expired" });
        } else setFrameKey((k) => k + 1);
      });
    } else reopen();
  };

  const close = () => {
    if (window.history.length > 1) navigate(-1);
    else navigate(target.kind === "project" ? `/p/${target.projectKey}` : "/editor");
  };

  const url = state.kind === "ready" || state.kind === "probing" ? withTheme(state.open.url, theme, accent) : null;

  return (
    <div className="fixed inset-x-0 top-0 bottom-0 z-50 flex flex-col bg-surface md:top-13 md:left-60 md:z-20">
      <EditorToolbar label={label} detail={detail} url={url} onReload={reload} onClose={close} />
      <div className="relative min-h-0 flex-1">
        {state.kind === "ready" ? (
          <iframe
            key={frameKey}
            ref={frame}
            src={withTheme(state.open.url, theme, accent)}
            title={`VS Code — ${label}`}
            allow="clipboard-read; clipboard-write"
            onLoad={onFrameLoad}
            // VS Code's own default backgrounds, so the moment before its CSS
            // lands is not a white (or black) flash against the app.
            style={{ colorScheme: theme, background: theme === "dark" ? "#1f1f1f" : "#ffffff" }}
            className="absolute inset-0 h-full w-full border-0"
          />
        ) : (
          <div className="absolute inset-0 grid place-items-center p-4">
            <EditorState state={state} runnerName={status.data?.runner_name ?? null} onReopen={() => {
              forgetOpen(target);
              reopen();
            }} onClose={close} />
          </div>
        )}
      </div>
    </div>
  );
}

function EditorState({
  state,
  runnerName,
  onReopen,
  onClose,
}: {
  state: Exclude<State, { kind: "ready" }>;
  runnerName: string | null;
  onReopen: () => void;
  onClose: () => void;
}) {
  if (state.kind === "opening" || state.kind === "probing") {
    return (
      <p className="flex items-center gap-2 text-[13px] text-fg-2" role="status">
        <Spinner className="size-4" /> {state.kind === "opening" ? `Opening VS Code on ${runnerName ?? "the master"}…` : "Checking the editor session…"}
      </p>
    );
  }
  const card = (icon: React.ReactNode, title: string, body: React.ReactNode, action?: React.ReactNode) => (
    <div className="w-full max-w-sm space-y-3 rounded-xl border border-line-strong bg-surface p-5 text-center shadow-pop" role="alert">
      <div className="flex justify-center">{icon}</div>
      <p className="text-[14px] font-semibold">{title}</p>
      <div className="text-[13px] text-fg-2">{body}</div>
      <div className="flex justify-center gap-2">
        <Button size="sm" variant="ghost" onClick={onClose}>
          Close
        </Button>
        {action}
      </div>
    </div>
  );
  switch (state.kind) {
    case "expired":
      return card(
        <KeyRound className="size-5 text-warning" aria-hidden />,
        "Session expired — reopen",
        "Editor access lasts 12 hours and ends when you sign out. Reopening asks for your password again.",
        <Button size="sm" variant="primary" onClick={onReopen}>
          Reopen
        </Button>,
      );
    case "cancelled":
      return card(
        <KeyRound className="size-5 text-fg-3" aria-hidden />,
        "Confirm it's you to open the editor",
        "VS Code is a full shell on the master machine, so opening it needs your password (and code).",
        <Button size="sm" variant="primary" onClick={onReopen}>
          Confirm and open
        </Button>,
      );
    case "unavailable":
      return card(<TriangleAlert className="size-5 text-warning" aria-hidden />, "VS Code is unavailable", state.reason);
    case "error":
      return card(
        <TriangleAlert className="size-5 text-critical" aria-hidden />,
        "Could not open the editor",
        state.message,
        <Button size="sm" variant="primary" onClick={onReopen}>
          Try again
        </Button>,
      );
  }
}

function EditorLanding() {
  const status = useCodeStatus();
  const projects = useProjects();
  const code = useEditorLauncher();
  const [recent] = useState(recentOpens);
  const available = status.data?.available ?? false;

  return (
    <div className="mx-auto max-w-[1000px] space-y-4">
      <PageHeader
        title="Editor"
        subtitle={
          status.data?.available
            ? `VS Code in the browser, running on ${status.data.runner_name ?? "the master"} — your repos, extensions and terminals.`
            : "VS Code in the browser, running on the master machine."
        }
      />
      {status.data && !status.data.available ? (
        <div role="status" className="flex items-start gap-3 rounded-xl border border-warning/50 bg-warning/10 px-4 py-3">
          <TriangleAlert className="mt-0.5 size-5 shrink-0 text-warning" aria-hidden />
          <div>
            <p className="text-[14px] font-semibold">VS Code is unavailable right now</p>
            <p className="mt-0.5 text-[13px] text-fg-2">{status.data.reason || "The master machine is not serving it."}</p>
            <p className="mt-1 text-[12px] text-fg-3">
              It runs on the master ({status.data.runner_name ?? "desk"}) and needs that machine online with{" "}
              VS Code enabled (<code className="font-mono">"code"</code>) in its <code className="font-mono">~/.config/forge/agent.json</code>.
            </p>
          </div>
        </div>
      ) : null}

      {recent.length ? (
        <Panel title="Recently opened in this tab" icon={<History />} id="editor-recent">
          <ul>
            {recent.map((r) => (
              <li key={r.key}>
                <Link to={r.path} className="flex items-center gap-2 rounded-md px-2 py-1.5 text-[13px] hover:bg-surface-2">
                  {r.key.startsWith("folder:") ? (
                    <FolderOpen className="size-3.5 text-fg-3" aria-hidden />
                  ) : (
                    <Code className="size-3.5 text-fg-3" aria-hidden />
                  )}
                  <span className="min-w-0 flex-1 truncate">{r.label}</span>
                  <RelativeTime iso={r.opened_at} className="shrink-0 text-[11.5px] text-fg-3" />
                </Link>
              </li>
            ))}
          </ul>
        </Panel>
      ) : null}

      <Panel title="Open a project" icon={<Code />} id="editor-projects">
        {projects.data?.length ? (
          <ul className="grid grid-cols-1 gap-1 sm:grid-cols-2">
            {projects.data.map((p) => (
              <li key={p.key}>
                <button
                  type="button"
                  disabled={!available}
                  title={code.reason}
                  onClick={() => code.open({ kind: "project", projectKey: p.key, repoIds: [] })}
                  className="flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left text-[13px] hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  <ColorDot color={p.color} />
                  <span className="min-w-0 flex-1 truncate font-medium">{p.name}</span>
                  <span className="font-mono text-[11px] text-fg-3">{p.key}</span>
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <EmptyState compact title="No projects yet" />
        )}
        <p className="px-2.5 pt-2 text-[11.5px] text-fg-3">
          A project opens as a multi-root workspace of all its repositories. Open a single repo from its row on the project
          page, or a tmux window's folder from the Terminal.
        </p>
      </Panel>
    </div>
  );
}
