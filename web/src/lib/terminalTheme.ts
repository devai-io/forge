// xterm colours from the app's tokens. Background, foreground, cursor and
// selection come from the live CSS variables (so the terminal sits on the same
// surface as the rest of the app); the 16 ANSI colours are fixed per theme,
// chosen to stay legible on that surface.

import type { ITheme } from "@xterm/xterm";

const DARK_ANSI = {
  black: "#1c1c1f",
  red: "#ef6b6b",
  green: "#4cc38a",
  yellow: "#e5b33d",
  blue: "#5598e7",
  magenta: "#d582c4",
  cyan: "#4fb8c9",
  white: "#d8d7d2",
  brightBlack: "#6e6d68",
  brightRed: "#ff8f8f",
  brightGreen: "#6fdca6",
  brightYellow: "#f5cc62",
  brightBlue: "#86b6ef",
  brightMagenta: "#e9a5dc",
  brightCyan: "#79d3e1",
  brightWhite: "#ffffff",
};

const LIGHT_ANSI = {
  black: "#1a1a19",
  red: "#c02f2f",
  green: "#17803d",
  yellow: "#9a6700",
  blue: "#1c5cab",
  magenta: "#9c3f8f",
  cyan: "#137a8a",
  white: "#c3c2b7",
  brightBlack: "#6e6d68",
  brightRed: "#d03b3b",
  brightGreen: "#008300",
  brightYellow: "#b07a00",
  brightBlue: "#2a78d6",
  brightMagenta: "#b3509f",
  brightCyan: "#1a93a6",
  brightWhite: "#52514e",
};

export function terminalTheme(mode: "light" | "dark", css?: (name: string) => string): ITheme {
  const read = (name: string, fallback: string) => (css?.(name).trim() || fallback);
  const dark = mode === "dark";
  const accent = read("--accent", dark ? "#3987e5" : "#2a78d6");
  return {
    background: read("--surface", dark ? "#151517" : "#fcfcfb"),
    foreground: read("--fg", dark ? "#ededeb" : "#0b0b0b"),
    cursor: accent,
    cursorAccent: read("--surface", dark ? "#151517" : "#fcfcfb"),
    selectionBackground: dark ? "rgba(57, 135, 229, 0.38)" : "rgba(42, 120, 214, 0.25)",
    ...(dark ? DARK_ANSI : LIGHT_ANSI),
  };
}

/** Read CSS custom properties from <html>. */
export function cssVars(): (name: string) => string {
  const style = getComputedStyle(document.documentElement);
  return (name) => style.getPropertyValue(name);
}
