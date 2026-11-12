// The state behind attaching images (ImageAttachments.tsx draws it): uploads
// as they are added, and the page-wide drop zone.

import { useCallback, useEffect, useRef, useState } from "react";
import { api, errorMessage } from "@/api/client";
import { useToast } from "@/components/ui/Toast";
import { dragHasFiles, imageFiles, MAX_IMAGE_BYTES, MAX_IMAGES, type UploadedImage } from "@/lib/images";

export type Attachment = {
  key: string;
  name: string;
  preview: string;
  status: "uploading" | "ready" | "error";
  upload?: UploadedImage;
  error?: string;
};

let seq = 0;

// Object URLs for the thumbnails (absent in some test environments).
const previewURL = (f: File) => (typeof URL.createObjectURL === "function" ? URL.createObjectURL(f) : "");
const revokePreview = (u: string) => {
  if (u && typeof URL.revokeObjectURL === "function") URL.revokeObjectURL(u);
};

export function useImageAttachments() {
  const toast = useToast();
  const [items, setItems] = useState<Attachment[]>([]);
  const live = useRef(items);
  live.current = items;

  // Object URLs belong to this composer: let them go with it.
  useEffect(() => () => live.current.forEach((a) => revokePreview(a.preview)), []);

  const add = useCallback(
    (files: File[]) => {
      const images = imageFiles(files);
      if (!images.length) {
        if (files.length) toast.error("Only images can be attached (PNG, JPEG, GIF or WebP).");
        return;
      }
      const room = MAX_IMAGES - live.current.length;
      if (images.length > room) toast.error(`At most ${MAX_IMAGES} images per message.`);
      for (const file of images.slice(0, Math.max(room, 0))) {
        const key = `img-${++seq}`;
        const item: Attachment = {
          key,
          name: file.name || "image",
          preview: previewURL(file),
          status: "uploading",
        };
        if (file.size > MAX_IMAGE_BYTES) {
          item.status = "error";
          item.error = "larger than 10 MB";
        }
        setItems((list) => [...list, item]);
        if (item.status === "error") continue;
        const form = new FormData();
        form.append("file", file, file.name || "image.png");
        api.upload<UploadedImage>("/uploads", form).then(
          (upload) => setItems((list) => list.map((a) => (a.key === key ? { ...a, status: "ready", upload } : a))),
          (err) => setItems((list) => list.map((a) => (a.key === key ? { ...a, status: "error", error: errorMessage(err) } : a))),
        );
      }
    },
    [toast],
  );

  const remove = useCallback((key: string) => {
    setItems((list) => {
      const gone = list.find((a) => a.key === key);
      if (gone) revokePreview(gone.preview);
      return list.filter((a) => a.key !== key);
    });
  }, []);

  const clear = useCallback(() => {
    setItems((list) => {
      list.forEach((a) => revokePreview(a.preview));
      return [];
    });
  }, []);

  const ready = items.filter((a) => a.status === "ready" && a.upload);
  return {
    items,
    add,
    remove,
    clear,
    /** Still uploading: sending waits. */
    busy: items.some((a) => a.status === "uploading"),
    markers: ready.map((a) => a.upload!.marker),
    /** For a textarea's onPaste: screenshots straight from the clipboard. */
    onPaste: (e: React.ClipboardEvent) => {
      const files = imageFiles(e.clipboardData?.files);
      if (files.length) {
        e.preventDefault();
        add(files);
      }
    },
  };
}

// ── Dropping anywhere on the page ──────────────────────────────────────────
//
// The innermost composer on screen takes the drop (a dialog's over the page's):
// targets stack in mount order and only the top one listens.

type DropTarget = {
  id: number;
  onFiles: (files: File[]) => void;
  setOver: (v: boolean) => void;
};
const targets: DropTarget[] = [];
let installed = false;
let depth = 0;

const top = () => targets[targets.length - 1];

function install() {
  if (installed || typeof window === "undefined") return;
  installed = true;
  window.addEventListener("dragenter", (e) => {
    if (!top() || !dragHasFiles(e)) return;
    e.preventDefault();
    depth++;
    top()?.setOver(true);
  });
  window.addEventListener("dragover", (e) => {
    if (!top() || !dragHasFiles(e)) return;
    e.preventDefault(); // allow the drop
    if (e.dataTransfer) e.dataTransfer.dropEffect = "copy";
  });
  window.addEventListener("dragleave", (e) => {
    if (!top() || !dragHasFiles(e)) return;
    depth = Math.max(0, depth - 1);
    if (depth === 0) top()?.setOver(false);
  });
  window.addEventListener("drop", (e) => {
    if (!top() || !dragHasFiles(e)) return;
    e.preventDefault(); // never let the browser open the file instead
    depth = 0;
    const t = top();
    t?.setOver(false);
    t?.onFiles(Array.from(e.dataTransfer?.files ?? []));
  });
}

let targetSeq = 0;

/** Lets images dropped anywhere on the page land here while it is mounted; true while a drag is over the page. */
export function usePageImageDrop(onFiles: (files: File[]) => void, enabled = true): boolean {
  const [over, setOver] = useState(false);
  const cb = useRef(onFiles);
  cb.current = onFiles;
  useEffect(() => {
    if (!enabled) return;
    install();
    const t: DropTarget = {
      id: ++targetSeq,
      onFiles: (f) => cb.current(f),
      setOver,
    };
    targets.forEach((x) => x.setOver(false));
    targets.push(t);
    return () => {
      const i = targets.findIndex((x) => x.id === t.id);
      if (i >= 0) targets.splice(i, 1);
      setOver(false);
    };
  }, [enabled]);
  return over;
}

