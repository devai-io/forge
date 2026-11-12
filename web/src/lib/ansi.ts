// Terminal colours in run output: SGR escape codes (colour, bold, dim,
// italic, underline) become styled segments, every other escape sequence
// (cursor moves, titles, hyperlinks) is dropped. What a test runner or git
// prints in colour keeps its colour in the transcript.

export type AnsiStyle = {
  fg?: string;
  bg?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
};
export type AnsiSegment = { text: string; style: AnsiStyle };

// The 16 basic colours, tuned to read on the dark terminal blocks.
const BASIC = [
  "#4b4b4b", "#f07178", "#9ece6a", "#e0af68", "#7aa2f7", "#c792ea", "#7dcfff", "#c0caf5",
  "#737373", "#ff8b92", "#b9f27c", "#ffc777", "#91b4ff", "#dcb0ff", "#a4e4ff", "#ffffff",
];

function color256(n: number): string | undefined {
  if (n < 0 || n > 255) return undefined;
  if (n < 16) return BASIC[n];
  if (n >= 232) {
    const v = 8 + (n - 232) * 10;
    return `rgb(${v},${v},${v})`;
  }
  const i = n - 16;
  const c = (x: number) => (x === 0 ? 0 : 55 + x * 40);
  return `rgb(${c(Math.floor(i / 36))},${c(Math.floor(i / 6) % 6)},${c(i % 6)})`;
}

// CSI sequences (ESC [ … final byte), OSC (ESC ] … BEL or ESC \) and lone
// two-byte escapes.
// eslint-disable-next-line no-control-regex -- escape sequences are what this parses
const ESCAPES = /\x1b\[([0-?]*)[ -/]*([@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]/g;

export function hasAnsi(text: string): boolean {
  return text.includes("\x1b");
}

export function stripAnsi(text: string): string {
  return text.replace(ESCAPES, "");
}

export function parseAnsi(text: string): AnsiSegment[] {
  const out: AnsiSegment[] = [];
  let style: AnsiStyle = {};
  let last = 0;
  const push = (t: string) => {
    if (!t) return;
    const prev = out[out.length - 1];
    if (prev && sameStyle(prev.style, style)) prev.text += t;
    else out.push({ text: t, style: { ...style } });
  };
  for (const m of text.matchAll(ESCAPES)) {
    push(text.slice(last, m.index));
    last = (m.index ?? 0) + m[0].length;
    if (m[2] !== "m") continue; // not a colour: dropped
    style = applySGR(style, m[1]);
  }
  push(text.slice(last));
  return out;
}

function sameStyle(a: AnsiStyle, b: AnsiStyle): boolean {
  return a.fg === b.fg && a.bg === b.bg && !!a.bold === !!b.bold && !!a.dim === !!b.dim && !!a.italic === !!b.italic && !!a.underline === !!b.underline;
}

function applySGR(prev: AnsiStyle, params: string): AnsiStyle {
  const s: AnsiStyle = { ...prev };
  const codes = params === "" ? [0] : params.split(";").map((p) => Number(p) || 0);
  for (let i = 0; i < codes.length; i++) {
    const c = codes[i];
    if (c === 0) {
      for (const k of Object.keys(s) as (keyof AnsiStyle)[]) delete s[k];
    } else if (c === 1) s.bold = true;
    else if (c === 2) s.dim = true;
    else if (c === 3) s.italic = true;
    else if (c === 4) s.underline = true;
    else if (c === 22) {
      s.bold = false;
      s.dim = false;
    } else if (c === 23) s.italic = false;
    else if (c === 24) s.underline = false;
    else if (c >= 30 && c <= 37) s.fg = BASIC[c - 30];
    else if (c >= 90 && c <= 97) s.fg = BASIC[c - 90 + 8];
    else if (c === 39) delete s.fg;
    else if (c >= 40 && c <= 47) s.bg = BASIC[c - 40];
    else if (c >= 100 && c <= 107) s.bg = BASIC[c - 100 + 8];
    else if (c === 49) delete s.bg;
    else if (c === 38 || c === 48) {
      const key = c === 38 ? "fg" : "bg";
      if (codes[i + 1] === 5) {
        const v = color256(codes[i + 2]);
        if (v) s[key] = v;
        i += 2;
      } else if (codes[i + 1] === 2) {
        s[key] = `rgb(${codes[i + 2] ?? 0},${codes[i + 3] ?? 0},${codes[i + 4] ?? 0})`;
        i += 4;
      }
    }
  }
  return s;
}
