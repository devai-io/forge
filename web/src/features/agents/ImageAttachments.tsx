// Attaching images to what goes to an agent: drag them anywhere onto the
// page, paste them, or pick them with the image button. Each one uploads at
// once (POST /api/uploads) and shows as a thumbnail; sending adds their
// markers to the text, and the machine hands Claude the files.

import clsx from "clsx";
import { ImagePlus, X } from "lucide-react";
import { useRef } from "react";
import { Spinner } from "@/components/ui/Spinner";
import { splitImageMarkers, stripImageMarkers } from "@/lib/images";
import type { Attachment } from "./imageHooks";

export function DropOverlay({ show, label = "Drop images to attach them" }: { show: boolean; label?: string }) {
  if (!show) return null;
  return (
    <div
      className="pointer-events-none fixed inset-0 z-[70] grid place-items-center bg-accent/10 p-6 backdrop-blur-[1px]"
      aria-hidden
    >
      <div className="flex items-center gap-2 rounded-xl border-2 border-dashed border-accent bg-surface px-6 py-4 text-sm font-medium text-fg shadow-pop">
        <ImagePlus className="size-5 text-accent" /> {label}
      </div>
    </div>
  );
}

/** The image button: pick files. */
export function AttachImageButton({
  onFiles,
  disabled,
  className,
}: {
  onFiles: (files: File[]) => void;
  disabled?: boolean;
  className?: string;
}) {
  const input = useRef<HTMLInputElement>(null);
  return (
    <>
      <button
        type="button"
        disabled={disabled}
        onClick={() => input.current?.click()}
        aria-label="Attach images"
        title="Attach images (or drop / paste them)"
        className={clsx(
          "inline-flex size-8.5 shrink-0 items-center justify-center rounded-md text-fg-3 hover:bg-surface-2 hover:text-fg disabled:opacity-50",
          className,
        )}
      >
        <ImagePlus className="size-4" />
      </button>
      <input
        ref={input}
        type="file"
        accept="image/png,image/jpeg,image/gif,image/webp"
        multiple
        hidden
        data-testid="attach-images-input"
        onChange={(e) => {
          onFiles(Array.from(e.target.files ?? []));
          e.target.value = "";
        }}
      />
    </>
  );
}

/** Thumbnails of what is attached, each removable. */
export function AttachmentStrip({
  items,
  onRemove,
  className,
}: {
  items: Attachment[];
  onRemove: (key: string) => void;
  className?: string;
}) {
  if (!items.length) return null;
  return (
    <ul className={clsx("flex flex-wrap gap-2", className)} aria-label="Attached images">
      {items.map((a) => (
        <li key={a.key} className="group relative" title={a.error ? `${a.name}: ${a.error}` : a.name}>
          <img
            src={a.preview}
            alt={a.name}
            className={clsx(
              "size-14 rounded-md border object-cover",
              a.status === "error" ? "border-critical opacity-50" : "border-line",
              a.status === "uploading" && "opacity-60",
            )}
          />
          {a.status === "uploading" ? (
            <span className="absolute inset-0 grid place-items-center">
              <Spinner className="size-4" />
            </span>
          ) : null}
          {a.status === "error" ? (
            <span className="absolute inset-x-0 bottom-0 rounded-b-md bg-critical px-1 text-center text-[9.5px] text-white">
              failed
            </span>
          ) : null}
          <button
            type="button"
            onClick={() => onRemove(a.key)}
            aria-label={`Remove ${a.name}`}
            className="absolute -top-1.5 -right-1.5 grid size-5 place-items-center rounded-full border border-line bg-surface text-fg-2 shadow-sm hover:text-fg"
          >
            <X className="size-3" />
          </button>
        </li>
      ))}
    </ul>
  );
}

/** Thumbnails of images named in a prompt or message; each opens full size. */
export function ImageThumbs({ images, className }: { images: { name: string; token: string }[]; className?: string }) {
  if (!images.length) return null;
  return (
    <div className={clsx("flex flex-wrap gap-2", className)}>
      {images.map((p) => (
        <a key={p.token} href={`/api/uploads/${p.token}`} target="_blank" rel="noreferrer" title={p.name}>
          <img src={`/api/uploads/${p.token}`} alt={p.name} loading="lazy" className="h-20 max-w-40 rounded-md border border-line object-cover" />
        </a>
      ))}
    </div>
  );
}

/** Text that may name attached images: the images as thumbnails, the rest as it is. */
export function TextWithImages({ text, className }: { text: string; className?: string }) {
  const parts = splitImageMarkers(text);
  const images = parts.flatMap((p) => (p.kind === "image" ? [p] : []));
  const plain = stripImageMarkers(text);
  return (
    <div className={className}>
      {plain ? <p className="break-words whitespace-pre-wrap">{plain}</p> : null}
      <ImageThumbs images={images} className={plain ? "mt-2" : undefined} />
    </div>
  );
}
