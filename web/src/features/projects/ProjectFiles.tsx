// A project's files: whatever you keep next to it (specs, exports,
// screenshots), stored in the project's folder on the server. Upload with the
// button or by dropping files anywhere on the panel; one request per file, and
// the same name replaces the old copy.

import clsx from "clsx";
import { Download, FileText, Trash2, Upload } from "lucide-react";
import { useRef, useState, type DragEvent } from "react";
import { projectFileUrl, useDeleteProjectFile, useProjectFiles, useUploadProjectFile } from "@/api/hooks";
import type { ProjectFile } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { Spinner } from "@/components/ui/Spinner";
import { useToast } from "@/components/ui/Toast";
import { formatBytes } from "@/lib/format";
import { fileProblem } from "@/lib/projects";

const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes("Files");

export function ProjectFiles({ projectKey }: { projectKey: string }) {
  const files = useProjectFiles(projectKey);
  const upload = useUploadProjectFile(projectKey);
  const toast = useToast();
  const input = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState<string[]>([]);
  const [dragging, setDragging] = useState(false);
  const [deleting, setDeleting] = useState<ProjectFile | null>(null);

  const uploadAll = async (list: File[]) => {
    for (const file of list) {
      const problem = fileProblem(file);
      if (problem) {
        toast.error(problem);
        continue;
      }
      const replacing = files.data?.some((f) => f.name === file.name);
      setUploading((u) => [...u, file.name]);
      try {
        await upload.mutateAsync(file);
        toast.success(`${replacing ? "Replaced" : "Uploaded"} ${file.name}`);
      } catch (err) {
        toast.error(err);
      } finally {
        setUploading((u) => u.filter((n) => n !== file.name));
      }
    }
  };

  const sorted = [...(files.data ?? [])].sort((a, b) => b.modified_at.localeCompare(a.modified_at));

  return (
    <div
      onDragEnter={(e) => {
        if (!hasFiles(e)) return;
        e.preventDefault();
        setDragging(true);
      }}
      onDragOver={(e) => {
        if (!hasFiles(e)) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = "copy";
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false);
      }}
      onDrop={(e) => {
        if (!hasFiles(e)) return;
        e.preventDefault();
        setDragging(false);
        void uploadAll(Array.from(e.dataTransfer.files));
      }}
      className="relative"
    >
      <Panel
        title={files.data ? `Files · ${files.data.length}` : "Files"}
        icon={<FileText />}
        id="proj-files"
        bodyClassName="p-0"
        actions={
          <>
            <input
              ref={input}
              type="file"
              multiple
              className="hidden"
              tabIndex={-1}
              aria-hidden
              onChange={(e) => {
                const list = Array.from(e.target.files ?? []);
                e.target.value = ""; // picking the same file again should upload again
                void uploadAll(list);
              }}
            />
            <Button size="sm" variant="ghost" onClick={() => input.current?.click()}>
              <Upload className="size-3.5" aria-hidden /> Upload
            </Button>
          </>
        }
      >
        {uploading.length ? (
          <ul aria-label="Uploading" className="divide-y divide-line border-b border-line">
            {uploading.map((name) => (
              <li key={name} className="flex items-center gap-2.5 px-3.5 py-2.5 text-[13px] text-fg-2">
                <Spinner className="size-3.5" />
                <span className="min-w-0 flex-1 truncate">{name}</span>
                <span className="text-[12px] text-fg-3">uploading…</span>
              </li>
            ))}
          </ul>
        ) : null}
        {files.isPending ? (
          <SkeletonRows rows={3} className="p-3" />
        ) : files.error ? (
          <div className="p-3">
            <ErrorState error={files.error} onRetry={() => files.refetch()} />
          </div>
        ) : sorted.length ? (
          <ul className="divide-y divide-line">
            {sorted.map((f) => (
              <li key={f.name} className="flex items-center gap-2.5 px-3.5 py-2.5">
                <FileText className="size-4 shrink-0 text-fg-3" aria-hidden />
                <div className="min-w-0 flex-1">
                  <a
                    href={projectFileUrl(projectKey, f.name)}
                    download={f.name}
                    className="block truncate text-[13.5px] font-medium hover:text-accent hover:underline"
                    title={`Download ${f.name}`}
                  >
                    {f.name}
                  </a>
                  <p className="text-[11.5px] text-fg-3">
                    <span className="tabular">{formatBytes(f.size)}</span> · <RelativeTime iso={f.modified_at} />
                  </p>
                </div>
                <a
                  href={projectFileUrl(projectKey, f.name)}
                  download={f.name}
                  aria-label={`Download ${f.name}`}
                  title="Download"
                  className="grid size-7 shrink-0 place-items-center rounded-md text-fg-3 hover:bg-surface-2 hover:text-fg"
                >
                  <Download className="size-3.5" />
                </a>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={`Delete ${f.name}`}
                  title="Delete"
                  onClick={() => setDeleting(f)}
                >
                  <Trash2 className="size-3.5" />
                </Button>
              </li>
            ))}
          </ul>
        ) : !uploading.length ? (
          <div className="p-3">
            <EmptyState
              compact
              icon={<FileText />}
              title="No files yet"
              action={
                <Button size="sm" variant="subtle" onClick={() => input.current?.click()}>
                  <Upload className="size-3.5" aria-hidden /> Upload a file
                </Button>
              }
            >
              Files you keep with this project — specs, exports, screenshots. Stored in the project's folder on the
              server. Drop them here, up to 100 MB each.
            </EmptyState>
          </div>
        ) : null}
      </Panel>
      <div
        aria-hidden
        className={clsx(
          "pointer-events-none absolute inset-0 grid place-items-center rounded-xl border-2 border-dashed border-accent bg-accent/8 text-[13px] font-medium transition-opacity",
          dragging ? "opacity-100" : "opacity-0",
        )}
      >
        <span className="rounded-md bg-surface px-3 py-1.5 shadow-pop">Drop to upload</span>
      </div>
      <DeleteFile projectKey={projectKey} file={deleting} onClose={() => setDeleting(null)} />
    </div>
  );
}

function DeleteFile({ projectKey, file, onClose }: { projectKey: string; file: ProjectFile | null; onClose: () => void }) {
  const del = useDeleteProjectFile(projectKey);
  const toast = useToast();
  return (
    <ConfirmDialog
      open={!!file}
      onClose={onClose}
      title={`Delete ${file?.name ?? "file"}?`}
      body="It is removed from the project's folder on the server. There is no undo."
      loading={del.isPending}
      onConfirm={() =>
        file &&
        del.mutate(file.name, {
          onSuccess: () => {
            toast.success(`Deleted ${file.name}`);
            onClose();
          },
          onError: (e) => toast.error(e),
        })
      }
    />
  );
}
