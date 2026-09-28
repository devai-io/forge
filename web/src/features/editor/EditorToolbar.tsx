import { Code, ExternalLink, RefreshCw, X } from "lucide-react";
import { Button } from "@/components/ui/Button";

/** The slim bar above the embedded editor. */
export function EditorToolbar({
  label,
  detail,
  url,
  onReload,
  onClose,
}: {
  label: string;
  detail?: string;
  url: string | null;
  onReload: () => void;
  onClose: () => void;
}) {
  return (
    <header className="flex h-11 shrink-0 items-center gap-2 border-b border-line bg-surface px-2 pt-[env(safe-area-inset-top)] md:px-3">
      <Code className="size-4 shrink-0 text-fg-3" aria-hidden />
      <div className="min-w-0 flex-1 text-[13px]">
        <span className="font-medium">{label}</span>
        {detail ? (
          <span className="ml-2 truncate font-mono text-[11.5px] text-fg-3" title={detail}>
            {detail}
          </span>
        ) : null}
      </div>
      <Button
        size="sm"
        variant="ghost"
        disabled={!url}
        onClick={() => url && window.open(url, "_blank", "noopener")}
        title="Open this editor in its own browser tab"
      >
        <ExternalLink className="size-3.5" aria-hidden /> <span className="hidden sm:inline">Open in new tab</span>
      </Button>
      <Button size="sm" variant="ghost" onClick={onReload} title="Reload the editor">
        <RefreshCw className="size-3.5" aria-hidden /> <span className="hidden sm:inline">Reload</span>
      </Button>
      <Button size="sm" variant="subtle" onClick={onClose} title="Close the editor (VS Code keeps your files; unsaved edits stay in its own storage)">
        <X className="size-3.5" aria-hidden /> <span className="hidden sm:inline">Close</span>
      </Button>
    </header>
  );
}
