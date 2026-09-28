// Accent colour: the interactive role (primary buttons, links, focus rings,
// selection) — never the data colours, which stay on the dataviz tokens.
//
// User.accent is "" (the default), a preset id, or a custom "#rrggbb". Presets
// are CSS blocks in index.css keyed on <html data-accent="…">; the values
// below mirror them (swatches and the VS Code hint need the hex, and a test
// keeps the two in sync). A custom colour is derived here into light and dark
// variants and set as inline --accent-c-* variables, which index.css maps onto
// the role tokens under data-accent="custom".
//
// The last applied accent is cached in localStorage so public/theme-init.js
// can paint it before React mounts; the server's value wins once /me loads.

export type AccentTokens = { accent: string; strong: string; soft: string; fg: string };
export type AccentPreset = { id: string; name: string; light: AccentTokens; dark: AccentTokens };

// Light: text-safe shades (≥ 4.5:1 on the surface) with white on top.
// Dark: lighter shades (≥ 6:1 on the surface) with near-black on top.
// "indigo" is the default look: the same values as :root in index.css.
export const ACCENTS: AccentPreset[] = [
  {
    id: "indigo",
    name: "Indigo",
    light: { accent: "#2a78d6", strong: "#1f64b8", soft: "#cde2fb", fg: "#ffffff" },
    dark: { accent: "#3987e5", strong: "#5a9cec", soft: "#184f95", fg: "#ffffff" },
  },
  {
    id: "blue",
    name: "Blue",
    light: { accent: "#2563eb", strong: "#1d4ed8", soft: "#dbeafe", fg: "#ffffff" },
    dark: { accent: "#60a5fa", strong: "#93c5fd", soft: "#1e3a8a", fg: "#0b0b0b" },
  },
  {
    id: "sky",
    name: "Sky",
    light: { accent: "#0277b6", strong: "#075985", soft: "#e0f2fe", fg: "#ffffff" },
    dark: { accent: "#38bdf8", strong: "#7dd3fc", soft: "#0c4a6e", fg: "#0b0b0b" },
  },
  {
    id: "teal",
    name: "Teal",
    light: { accent: "#0f766e", strong: "#115e59", soft: "#ccfbf1", fg: "#ffffff" },
    dark: { accent: "#2dd4bf", strong: "#5eead4", soft: "#134e4a", fg: "#0b0b0b" },
  },
  {
    id: "green",
    name: "Green",
    light: { accent: "#15803d", strong: "#166534", soft: "#dcfce7", fg: "#ffffff" },
    dark: { accent: "#4ade80", strong: "#86efac", soft: "#14532d", fg: "#0b0b0b" },
  },
  {
    id: "lime",
    name: "Lime",
    light: { accent: "#4d7c0f", strong: "#3f6212", soft: "#ecfccb", fg: "#ffffff" },
    dark: { accent: "#a3e635", strong: "#bef264", soft: "#365314", fg: "#0b0b0b" },
  },
  {
    id: "amber",
    name: "Amber",
    light: { accent: "#b45309", strong: "#92400e", soft: "#fef3c7", fg: "#ffffff" },
    dark: { accent: "#fbbf24", strong: "#fcd34d", soft: "#78350f", fg: "#0b0b0b" },
  },
  {
    id: "orange",
    name: "Orange",
    light: { accent: "#c2410c", strong: "#9a3412", soft: "#ffedd5", fg: "#ffffff" },
    dark: { accent: "#fb923c", strong: "#fdba74", soft: "#7c2d12", fg: "#0b0b0b" },
  },
  {
    id: "red",
    name: "Red",
    light: { accent: "#dc2626", strong: "#b91c1c", soft: "#fee2e2", fg: "#ffffff" },
    dark: { accent: "#f87171", strong: "#fca5a5", soft: "#7f1d1d", fg: "#0b0b0b" },
  },
  {
    id: "rose",
    name: "Rose",
    light: { accent: "#e11d48", strong: "#be123c", soft: "#ffe4e6", fg: "#ffffff" },
    dark: { accent: "#fb7185", strong: "#fda4af", soft: "#881337", fg: "#0b0b0b" },
  },
  {
    id: "pink",
    name: "Pink",
    light: { accent: "#c8246e", strong: "#9d174d", soft: "#fce7f3", fg: "#ffffff" },
    dark: { accent: "#f472b6", strong: "#f9a8d4", soft: "#831843", fg: "#0b0b0b" },
  },
  {
    id: "violet",
    name: "Violet",
    light: { accent: "#7c3aed", strong: "#6d28d9", soft: "#ede9fe", fg: "#ffffff" },
    dark: { accent: "#a78bfa", strong: "#c4b5fd", soft: "#4c1d95", fg: "#0b0b0b" },
  },
];

export const DEFAULT_ACCENT = ACCENTS[0];

// The surfaces accents sit on (index.css --surface) and the two inks on top.
const SURFACE = { light: "#fcfcfb", dark: "#151517" } as const;
const WHITE = "#ffffff";
const INK = "#0b0b0b";

// ── Colour maths ───────────────────────────────────────────────────────────

/** "#abc", "abc", "#AABBCC" → "#aabbcc"; anything else → null. */
export function normalizeHex(input: string): string | null {
  const m = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(input.trim());
  if (!m) return null;
  const h = m[1].toLowerCase();
  return `#${h.length === 3 ? [...h].map((c) => c + c).join("") : h}`;
}

function rgb(hex: string): [number, number, number] {
  const n = parseInt(hex.slice(1), 16);
  return [n >> 16, (n >> 8) & 255, n & 255];
}

function toHex([r, g, b]: [number, number, number]): string {
  return `#${[r, g, b].map((c) => Math.round(Math.min(255, Math.max(0, c))).toString(16).padStart(2, "0")).join("")}`;
}

/** WCAG relative luminance, 0 (black) .. 1 (white). */
export function luminance(hex: string): number {
  const lin = (c: number) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  const [r, g, b] = rgb(hex);
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

/** WCAG contrast ratio, 1 .. 21. */
export function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** `t` of the way from `a` to `b` (0 = a, 1 = b), in sRGB. */
export function mix(a: string, b: string, t: number): string {
  const [x, y] = [rgb(a), rgb(b)];
  return toHex([0, 1, 2].map((i) => x[i] + (y[i] - x[i]) * t) as [number, number, number]);
}

/** White or near-black, whichever reads better on `bg`. */
export function readableOn(bg: string): string {
  return contrast(bg, WHITE) >= contrast(bg, INK) ? WHITE : INK;
}

/**
 * Nudge `hex` towards `toward` until it reaches `min` contrast against `bg`
 * (3:1 — what a button edge or a focus ring needs). A colour that already
 * passes is returned untouched, so a chosen colour is kept whenever possible.
 */
export function ensureContrast(hex: string, bg: string, min: number, toward: string): string {
  let out = hex;
  for (let t = 0.05; contrast(out, bg) < min && t <= 1; t += 0.05) out = mix(hex, toward, t);
  return out;
}

/** Light and dark token sets for a custom colour. */
export function deriveAccent(hex: string): { light: AccentTokens; dark: AccentTokens } {
  const light = ensureContrast(hex, SURFACE.light, 3, INK);
  const dark = ensureContrast(hex, SURFACE.dark, 3, WHITE);
  return {
    light: { accent: light, strong: mix(light, INK, 0.15), soft: mix(light, WHITE, 0.8), fg: readableOn(light) },
    dark: { accent: dark, strong: mix(dark, WHITE, 0.2), soft: mix(dark, "#000000", 0.55), fg: readableOn(dark) },
  };
}

// ── User.accent → what the page applies ───────────────────────────────────

export type ResolvedAccent =
  | { kind: "default" }
  | { kind: "preset"; preset: AccentPreset }
  | { kind: "custom"; hex: string };

export function resolveAccent(value: string | null | undefined): ResolvedAccent {
  if (!value || value === DEFAULT_ACCENT.id) return { kind: "default" };
  const preset = ACCENTS.find((a) => a.id === value);
  if (preset) return { kind: "preset", preset };
  const hex = normalizeHex(value);
  return hex ? { kind: "custom", hex } : { kind: "default" };
}

/** The tokens in effect for `value` in `theme`. */
export function accentTokens(value: string | null | undefined, theme: "light" | "dark"): AccentTokens {
  const r = resolveAccent(value);
  if (r.kind === "custom") return deriveAccent(r.hex)[theme];
  return (r.kind === "preset" ? r.preset : DEFAULT_ACCENT)[theme];
}

/** The accent hex for `theme` ("#rrggbb"). */
export const accentHex = (value: string | null | undefined, theme: "light" | "dark") => accentTokens(value, theme).accent;

/** Display name: "Teal", "Custom #12ab34", "Indigo" for the default. */
export function accentName(value: string | null | undefined): string {
  const r = resolveAccent(value);
  if (r.kind === "custom") return `Custom ${r.hex}`;
  return r.kind === "preset" ? r.preset.name : DEFAULT_ACCENT.name;
}

const CUSTOM_KEYS = ["accent", "strong", "soft", "fg"] as const;

/**
 * What goes on <html>: the data-accent value (null = remove it) and the inline
 * variables a custom colour needs. Also the shape cached for theme-init.js.
 */
export function accentAttributes(value: string | null | undefined): { attr: string | null; vars: Record<string, string> } {
  const r = resolveAccent(value);
  if (r.kind === "default") return { attr: null, vars: {} };
  if (r.kind === "preset") return { attr: r.preset.id, vars: {} };
  const d = deriveAccent(r.hex);
  const vars: Record<string, string> = {};
  for (const k of CUSTOM_KEYS) {
    vars[`--accent-c-${k}-l`] = d.light[k];
    vars[`--accent-c-${k}-d`] = d.dark[k];
  }
  return { attr: "custom", vars };
}

export const ACCENT_STORAGE_KEY = "forge.accent";

/** Paint `value` now and remember it for the next first paint. */
export function applyAccent(value: string | null | undefined, root: HTMLElement = document.documentElement) {
  const { attr, vars } = accentAttributes(value);
  if (attr) root.setAttribute("data-accent", attr);
  else root.removeAttribute("data-accent");
  for (const k of CUSTOM_KEYS) {
    for (const side of ["l", "d"]) {
      const name = `--accent-c-${k}-${side}`;
      if (vars[name]) root.style.setProperty(name, vars[name]);
      else root.style.removeProperty(name);
    }
  }
  try {
    if (attr) localStorage.setItem(ACCENT_STORAGE_KEY, JSON.stringify({ attr, vars }));
    else localStorage.removeItem(ACCENT_STORAGE_KEY);
  } catch {
    /* storage blocked: the accent still applies, the next load just starts on the default */
  }
}
