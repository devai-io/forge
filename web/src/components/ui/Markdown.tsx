import { QueryClientContext, type QueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { useContext, useMemo } from "react";
import ReactMarkdown, { type Components, type Options } from "react-markdown";
import { useInRouterContext, useNavigate, useSearchParams } from "react-router-dom";
import remarkGfm from "remark-gfm";
import { api } from "@/api/client";
import type { Project, Task } from "@/api/types";

// Links leave the app in a new tab. No raw HTML: react-markdown escapes it by
// default and that is kept — descriptions and agent output are untrusted text.
//
// Task refs ("SHOP-12") in prose become buttons that open the task drawer —
// never inside code or an existing link. When the project list is cached only
// the keys of real projects count, so "UTF-8" or "SHA-256" stay text.

const REF_RE = /\b([A-Z][A-Z0-9]{1,9})-(\d{1,6})\b/g;
// Without the project list: words that look like refs and never are.
const NOT_REFS = new Set(["UTF", "SHA", "ISO", "RFC", "CVE", "COVID", "HTTP", "TLS", "SSL", "AES", "RSA", "GPT", "PEP", "ES", "UTC", "GMT"]);

type MdNode = { type: string; value?: string; url?: string; children?: MdNode[]; data?: { hProperties?: Record<string, string> } };

function splitRefs(node: MdNode, isKey: (key: string) => boolean): MdNode[] {
  const text = node.value ?? "";
  const out: MdNode[] = [];
  let last = 0;
  for (const m of text.matchAll(REF_RE)) {
    if (!isKey(m[1])) continue;
    if (m.index > last) out.push({ type: "text", value: text.slice(last, m.index) });
    out.push({
      type: "link",
      url: `/tasks?q=${m[0]}`,
      children: [{ type: "text", value: m[0] }],
      data: { hProperties: { "data-task-ref": m[0] } },
    });
    last = m.index + m[0].length;
  }
  if (!out.length) return [node];
  if (last < text.length) out.push({ type: "text", value: text.slice(last) });
  return out;
}

// Only text nodes are touched: inline code and code blocks carry their text as
// a value of their own, and links are not descended into.
function remarkTaskRefs(options: { keys: Set<string> | null }) {
  const isKey = (k: string) => (options.keys ? options.keys.has(k) : !NOT_REFS.has(k));
  const walk = (node: MdNode) => {
    if (!node.children || node.type === "link" || node.type === "linkReference") return;
    node.children = node.children.flatMap((c) => {
      if (c.type === "text") return splitRefs(c, isKey);
      walk(c);
      return [c];
    });
  };
  return (tree: MdNode) => walk(tree);
}

/** Project keys from the cached project list, or null when it is not loaded. */
function cachedKeys(qc: QueryClient | undefined): Set<string> | null {
  const lists = qc?.getQueriesData<Project[]>({ queryKey: ["projects"] }) ?? [];
  const keys = new Set<string>();
  for (const [, data] of lists) if (Array.isArray(data)) for (const p of data) if (p?.key) keys.add(p.key.toUpperCase());
  return keys.size ? keys : null;
}

function cachedTaskId(qc: QueryClient | undefined, ref: string): number | undefined {
  for (const [, data] of qc?.getQueriesData<Task[]>({ queryKey: ["tasks"] }) ?? []) {
    const hit = Array.isArray(data) ? data.find((t) => t?.ref === ref) : undefined;
    if (hit) return hit.id;
  }
  return undefined;
}

const refClass = "rounded font-mono text-[0.92em] text-accent hover:underline";

function TaskRefButton({ taskRef }: { taskRef: string }) {
  const qc = useContext(QueryClientContext);
  const [, setParams] = useSearchParams();
  const navigate = useNavigate();
  // Same URL contract as the shell's openTask (?task=<id>); a ref no task has goes to the list, searched.
  const open = async () => {
    const id =
      cachedTaskId(qc, taskRef) ??
      (await api
        .get<{ tasks: Task[] }>("/tasks", { q: taskRef, limit: 20 })
        .then((r) => r.tasks.find((t) => t.ref === taskRef)?.id)
        .catch(() => undefined));
    if (id) {
      setParams((p) => {
        const next = new URLSearchParams(p);
        next.set("task", String(id));
        return next;
      });
    } else navigate(`/tasks?q=${encodeURIComponent(taskRef)}`);
  };
  return (
    <button type="button" className={refClass} onClick={() => void open()} title={`Open ${taskRef}`}>
      {taskRef}
    </button>
  );
}

function TaskRef({ taskRef }: { taskRef: string }) {
  return useInRouterContext() ? <TaskRefButton taskRef={taskRef} /> : <span className="font-mono">{taskRef}</span>;
}

const components: Components = {
  a: ({ href, children, node, ...props }) => {
    void node;
    const ref = (props as Record<string, unknown>)["data-task-ref"];
    if (typeof ref === "string") return <TaskRef taskRef={ref} />;
    return (
      <a href={href} target="_blank" rel="noopener noreferrer" {...props}>
        {children}
      </a>
    );
  },
};

export function Markdown({ children, className }: { children: string; className?: string }) {
  const qc = useContext(QueryClientContext);
  const keys = cachedKeys(qc);
  const keyList = keys ? [...keys].sort().join(",") : "";
  const plugins = useMemo<Options["remarkPlugins"]>(
    () => [remarkGfm, [remarkTaskRefs, { keys: keyList ? new Set(keyList.split(",")) : null }]],
    [keyList],
  );
  if (!children?.trim()) return null;
  return (
    <div className={clsx("md text-sm", className)}>
      <ReactMarkdown remarkPlugins={plugins} components={components}>
        {children}
      </ReactMarkdown>
    </div>
  );
}
