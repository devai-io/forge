// "Add files": several git-ignored files (.env, keys, keystores) at once, as
// repo files of one of the project's repos — the browser's twin of
// `forge agent secrets push`. Each file is checked first (a dry run says
// new / changed / unchanged); a file that would replace one already in the
// vault waits for an explicit Replace.

import { useQueryClient } from "@tanstack/react-query";
import { FolderOpen, Trash2, Upload } from "lucide-react";
import { useId, useRef, useState } from "react";
import { invalidateVault, pushVaultFiles, useProject } from "@/api/hooks";
import { errorMessage } from "@/api/client";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Input, Select } from "@/components/ui/Input";
import { defaultRepoPath, fileToBase64, formatBytes, inferRepoFileKind, kindLabel, MAX_VAULT_FILE, repoPathProblem } from "@/lib/vault";
import { VaultKindIcon } from "./bits";

type Status = "ready" | "working" | "added" | "same" | "exists" | "replaced" | "error";
type Row = { id: number; file: File; path: string; status: Status; error?: string };

const STATUS: Record<Status, { label: string; tone: "neutral" | "good" | "warning" | "critical" | "outline" }> = {
  ready: { label: "ready", tone: "outline" },
  working: { label: "uploading…", tone: "neutral" },
  added: { label: "added", tone: "good" },
  same: { label: "unchanged", tone: "neutral" },
  exists: { label: "already in the vault", tone: "warning" },
  replaced: { label: "replaced", tone: "good" },
  error: { label: "failed", tone: "critical" },
};

let nextId = 1;

export function AddFilesDialog({ projectKey, onClose }: { projectKey: string; onClose: () => void }) {
  const project = useProject(projectKey);
  const qc = useQueryClient();
  const repos = project.data?.repos ?? [];
  const [repoId, setRepoId] = useState("");
  const [rows, setRows] = useState<Row[]>([]);
  const [busy, setBusy] = useState(false);
  const [dragging, setDragging] = useState(false);
  const filesRef = useRef<HTMLInputElement>(null);
  const folderRef = useRef<HTMLInputElement>(null);
  const repoSelect = useId();
  const effectiveRepo = repoId || (repos.length === 1 ? String(repos[0].id) : "");

  const add = (list: FileList | File[] | null) => {
    if (!list) return;
    const picked = Array.from(list).map((file) => ({ id: nextId++, file, path: defaultRepoPath(file), status: "ready" as Status }));
    setRows((rs) => [...rs, ...picked]);
  };
  const patch = (id: number, p: Partial<Row>) => setRows((rs) => rs.map((r) => (r.id === id ? { ...r, ...p } : r)));

  const problem = (row: Row): string => {
    if (row.file.size === 0) return "empty file";
    if (row.file.size > MAX_VAULT_FILE) return "larger than 1 MB";
    const p = repoPathProblem(row.path);
    if (p) return p;
    if (rows.some((r) => r.id !== row.id && r.path.trim() === row.path.trim())) return "path listed twice";
    return "";
  };

  /** One file: a dry run, then the write unless it would replace (and replace is not asked). */
  const send = async (row: Row, replace: boolean) => {
    patch(row.id, { status: "working", error: undefined });
    try {
      const files = [{ path: row.path.trim(), content_base64: await fileToBase64(row.file) }];
      const repo = Number(effectiveRepo);
      const check = await pushVaultFiles(projectKey, { repo_id: repo, files, dry_run: true });
      const action = check.files[0]?.action;
      if (action === "same") return patch(row.id, { status: "same" });
      if (action === "update" && !replace) return patch(row.id, { status: "exists" });
      await pushVaultFiles(projectKey, { repo_id: repo, files });
      patch(row.id, { status: action === "update" ? "replaced" : "added" });
    } catch (err) {
      patch(row.id, { status: "error", error: errorMessage(err) });
    }
  };

  const uploadAll = async () => {
    setBusy(true);
    for (const row of rows) {
      if (row.status !== "ready" && row.status !== "error") continue;
      const p = problem(row);
      if (p) {
        patch(row.id, { status: "error", error: p });
        continue;
      }
      await send(row, false);
    }
    invalidateVault(qc);
    setBusy(false);
  };
  const replace = async (row: Row) => {
    await send(row, true);
    invalidateVault(qc);
  };

  const pending = rows.filter((r) => r.status === "ready" || r.status === "error").length;
  const waiting = rows.filter((r) => r.status === "exists");

  return (
    <Dialog
      open
      onClose={onClose}
      size="lg"
      title="Add files"
      description={`Git-ignored files a fresh clone of ${projectKey} needs — .env files, keys, keystores. Each is stored encrypted, at its path in the repo.`}
      footer={
        <>
          {waiting.length > 1 ? (
            <Button variant="ghost" className="mr-auto" disabled={busy} onClick={() => waiting.forEach((r) => void replace(r))}>
              Replace all {waiting.length}
            </Button>
          ) : null}
          <Button variant="ghost" onClick={onClose}>
            {rows.length && !pending ? "Done" : "Cancel"}
          </Button>
          <Button variant="primary" onClick={uploadAll} loading={busy} disabled={!effectiveRepo || !pending}>
            <Upload className="size-3.5" aria-hidden /> Upload {pending || ""}
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-[13px]">
        <label htmlFor={repoSelect} className="flex flex-wrap items-center gap-2">
          <span className="text-fg-2">Repo</span>
          <Select id={repoSelect} className="w-auto" value={effectiveRepo} onChange={(e) => setRepoId(e.target.value)}>
            {repos.length !== 1 ? <option value="">Choose a repo…</option> : null}
            {repos.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </Select>
          {project.data && !repos.length ? <span className="text-fg-3">This project has no repos yet — add one on its page.</span> : null}
        </label>

        <div
          onDragOver={(e) => {
            e.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragging(false);
            add(e.dataTransfer.files);
          }}
          className={
            "flex flex-col items-center gap-2 rounded-lg border border-dashed p-4 text-center text-fg-2 " +
            (dragging ? "border-accent bg-accent/5" : "border-line-strong")
          }
        >
          <p>Drop files here, or</p>
          <div className="flex flex-wrap justify-center gap-2">
            <Button size="sm" variant="subtle" onClick={() => filesRef.current?.click()}>
              <Upload className="size-3.5" aria-hidden /> Pick files
            </Button>
            <Button size="sm" variant="subtle" onClick={() => folderRef.current?.click()}>
              <FolderOpen className="size-3.5" aria-hidden /> Pick a folder
            </Button>
          </div>
          <p className="text-[11.5px] text-fg-3">
            A picked folder stands for the repo: its files keep their paths inside it. Up to 1 MB per file.
          </p>
          <input
            ref={filesRef}
            type="file"
            multiple
            hidden
            aria-label="Files to add"
            onChange={(e) => {
              add(e.target.files);
              e.target.value = "";
            }}
          />
          <input
            ref={folderRef}
            type="file"
            multiple
            hidden
            aria-label="Folder to add"
            {...{ webkitdirectory: "" }}
            onChange={(e) => {
              add(e.target.files);
              e.target.value = "";
            }}
          />
        </div>

        {rows.length ? (
          <ul className="divide-y divide-line rounded-lg border border-line">
            {rows.map((row) => {
              const kind = inferRepoFileKind(row.path);
              const st = STATUS[row.status];
              const editable = row.status === "ready" || row.status === "error";
              return (
                <li key={row.id} className="flex flex-wrap items-center gap-2 px-3 py-2">
                  <VaultKindIcon kind={kind} className="size-4" />
                  <Input
                    compact
                    aria-label={`Path of ${row.file.name}`}
                    value={row.path}
                    disabled={!editable}
                    onChange={(e) => patch(row.id, { path: e.target.value, status: "ready", error: undefined })}
                    className="min-w-0 flex-1 basis-48 font-mono text-[12.5px]"
                  />
                  <span className="text-[11.5px] text-fg-3" title={kindLabel(kind)}>
                    {kindLabel(kind)} · {formatBytes(row.file.size)}
                  </span>
                  <Badge tone={st.tone}>{st.label}</Badge>
                  {row.status === "exists" ? (
                    <Button size="sm" variant="subtle" onClick={() => void replace(row)}>
                      Replace
                    </Button>
                  ) : null}
                  {editable ? (
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Remove ${row.file.name}`}
                      onClick={() => setRows((rs) => rs.filter((r) => r.id !== row.id))}
                    >
                      <Trash2 className="size-3.5" />
                    </Button>
                  ) : null}
                  {row.error ? <p className="basis-full text-[12px] text-critical-ink">{row.error}</p> : null}
                </li>
              );
            })}
          </ul>
        ) : null}
      </div>
    </Dialog>
  );
}
