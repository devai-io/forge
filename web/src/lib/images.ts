// Images attached to agent prompts and messages. The text carries one marker
// per image, [image: NAME](forge-upload:TOKEN); the machine running the agent
// swaps it for the downloaded file before Claude sees the text.

export const IMAGE_MARKER = /\[image: ([^\]\n]{0,200})\]\(forge-upload:([A-Za-z0-9_-]{16,64})\)/g;

export const MAX_IMAGE_BYTES = 10 << 20;
export const MAX_IMAGES = 10;

export type UploadedImage = {
  token: string;
  name: string;
  content_type: string;
  size: number;
  marker: string;
};

/** The text with the images' markers after it (a blank line between). */
export function withImageMarkers(text: string, markers: string[]): string {
  const t = text.trim();
  if (!markers.length) return t;
  return [t, markers.join("\n")].filter(Boolean).join("\n\n");
}

/** The text with the markers taken out (titles, one-line summaries). */
export function stripImageMarkers(text: string): string {
  return text
    .replace(IMAGE_MARKER, "")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

export type TextPart = { kind: "text"; text: string } | { kind: "image"; name: string; token: string };

/** Splits text into plain runs and the images named in it. */
export function splitImageMarkers(text: string): TextPart[] {
  const out: TextPart[] = [];
  let last = 0;
  for (const m of text.matchAll(IMAGE_MARKER)) {
    const at = m.index ?? 0;
    if (at > last) out.push({ kind: "text", text: text.slice(last, at) });
    out.push({ kind: "image", name: m[1], token: m[2] });
    last = at + m[0].length;
  }
  if (last < text.length) out.push({ kind: "text", text: text.slice(last) });
  return out;
}

/** The image files among dropped / pasted / picked files. */
export function imageFiles(list: FileList | File[] | null | undefined): File[] {
  return Array.from(list ?? []).filter((f) => f.type.startsWith("image/"));
}

/** A drag that carries files (not text or a link being dragged around the page). */
export function dragHasFiles(e: DragEvent | React.DragEvent): boolean {
  return Array.from(e.dataTransfer?.types ?? []).includes("Files");
}
