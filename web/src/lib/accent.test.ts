import css from "@/index.css?raw";
import {
  ACCENT_STORAGE_KEY,
  ACCENTS,
  accentAttributes,
  accentHex,
  accentName,
  applyAccent,
  contrast,
  deriveAccent,
  ensureContrast,
  mix,
  normalizeHex,
  readableOn,
  resolveAccent,
} from "./accent";

/** The four accent tokens declared in the first CSS block whose selector is exactly `selector`. */
function cssTokens(selector: string) {
  const start = css.indexOf(`${selector} {`);
  if (start < 0) return null;
  const body = css.slice(start, css.indexOf("}", start));
  const get = (name: string) => new RegExp(`${name}:\\s*(#[0-9a-f]{6})`).exec(body)?.[1];
  return { accent: get("--accent"), strong: get("--accent-strong"), soft: get("--accent-soft"), fg: get("--accent-fg") };
}

describe("colour maths", () => {
  it("normalises hex spellings and rejects the rest", () => {
    expect(normalizeHex("#ABC")).toBe("#aabbcc");
    expect(normalizeHex("12ab34")).toBe("#12ab34");
    expect(normalizeHex(" #0F766E ")).toBe("#0f766e");
    expect(normalizeHex("#12ab3")).toBeNull();
    expect(normalizeHex("teal")).toBeNull();
    expect(normalizeHex("")).toBeNull();
  });
  it("computes WCAG contrast", () => {
    expect(contrast("#ffffff", "#000000")).toBeCloseTo(21, 5);
    expect(contrast("#777777", "#777777")).toBeCloseTo(1, 5);
    expect(contrast("#2a78d6", "#ffffff")).toBeCloseTo(4.42, 1);
  });
  it("mixes in sRGB", () => {
    expect(mix("#000000", "#ffffff", 0.5)).toBe("#808080");
    expect(mix("#2a78d6", "#2a78d6", 0.3)).toBe("#2a78d6");
  });
  it("puts white on dark colours and near-black on light ones", () => {
    expect(readableOn("#0f766e")).toBe("#ffffff");
    expect(readableOn("#1e3a8a")).toBe("#ffffff");
    expect(readableOn("#fbbf24")).toBe("#0b0b0b");
    expect(readableOn("#ffff00")).toBe("#0b0b0b");
  });
  it("only nudges a colour that lacks contrast", () => {
    expect(ensureContrast("#0f766e", "#fcfcfb", 3, "#0b0b0b")).toBe("#0f766e");
    const nudged = ensureContrast("#ffff00", "#fcfcfb", 3, "#0b0b0b");
    expect(nudged).not.toBe("#ffff00");
    expect(contrast(nudged, "#fcfcfb")).toBeGreaterThanOrEqual(3);
  });
});

describe("deriveAccent", () => {
  it.each(["#ffff00", "#000080", "#ff0000", "#808080", "#ffffff", "#000000", "#12ab34"])(
    "keeps %s usable in both themes",
    (hex) => {
      const d = deriveAccent(hex);
      expect(contrast(d.light.accent, "#fcfcfb")).toBeGreaterThanOrEqual(3);
      expect(contrast(d.dark.accent, "#151517")).toBeGreaterThanOrEqual(3);
      // Text on the accent reads at least as well as the better of white/black allows.
      for (const t of [d.light, d.dark]) {
        expect(contrast(t.accent, t.fg)).toBeGreaterThanOrEqual(Math.max(contrast(t.accent, "#ffffff"), contrast(t.accent, "#0b0b0b")) - 1e-9);
        expect(contrast(t.accent, t.fg)).toBeGreaterThanOrEqual(4.5);
      }
    },
  );
});

describe("presets", () => {
  it("has the twelve ids, indigo first as the default", () => {
    expect(ACCENTS.map((a) => a.id)).toEqual(["indigo", "blue", "sky", "teal", "green", "lime", "amber", "orange", "red", "rose", "pink", "violet"]);
  });
  it("reads well: text on the accent, and the accent as text on the surface", () => {
    for (const a of ACCENTS) {
      expect(contrast(a.light.accent, a.light.fg), `${a.id} light fg`).toBeGreaterThanOrEqual(4.4);
      expect(contrast(a.light.accent, "#fcfcfb"), `${a.id} light on surface`).toBeGreaterThanOrEqual(4.3);
      expect(contrast(a.dark.accent, a.dark.fg), `${a.id} dark fg`).toBeGreaterThanOrEqual(3.6);
      expect(contrast(a.dark.accent, "#151517"), `${a.id} dark on surface`).toBeGreaterThanOrEqual(5);
    }
  });
  it("matches the CSS blocks in index.css (indigo = the :root defaults)", () => {
    const root = cssTokens(':root,\n:root[data-theme="light"]');
    expect(root).toEqual(ACCENTS[0].light);
    expect(cssTokens(':root[data-theme="dark"]')).toEqual(ACCENTS[0].dark);
    for (const a of ACCENTS.slice(1)) {
      expect(cssTokens(`:root[data-accent="${a.id}"]`), a.id).toEqual(a.light);
      expect(cssTokens(`:root[data-theme="dark"][data-accent="${a.id}"]`), a.id).toEqual(a.dark);
    }
  });
});

describe("User.accent", () => {
  it("resolves defaults, presets and custom colours", () => {
    expect(resolveAccent("")).toEqual({ kind: "default" });
    expect(resolveAccent("indigo")).toEqual({ kind: "default" });
    expect(resolveAccent(undefined)).toEqual({ kind: "default" });
    expect(resolveAccent("nonsense")).toEqual({ kind: "default" });
    expect(resolveAccent("teal")).toMatchObject({ kind: "preset", preset: { id: "teal" } });
    expect(resolveAccent("#12AB34")).toEqual({ kind: "custom", hex: "#12ab34" });
  });
  it("names and resolves the hex per theme", () => {
    expect(accentName("")).toBe("Indigo");
    expect(accentName("violet")).toBe("Violet");
    expect(accentName("#12ab34")).toBe("Custom #12ab34");
    expect(accentHex("", "light")).toBe("#2a78d6");
    expect(accentHex("", "dark")).toBe("#3987e5");
    expect(accentHex("teal", "dark")).toBe("#2dd4bf");
    expect(accentHex("#0f766e", "light")).toBe("#0f766e");
  });
  it("maps to the attribute and inline variables", () => {
    expect(accentAttributes("")).toEqual({ attr: null, vars: {} });
    expect(accentAttributes("amber")).toEqual({ attr: "amber", vars: {} });
    const custom = accentAttributes("#0f766e");
    expect(custom.attr).toBe("custom");
    expect(Object.keys(custom.vars).sort()).toEqual(
      ["accent", "fg", "soft", "strong"].flatMap((k) => [`--accent-c-${k}-d`, `--accent-c-${k}-l`]).sort(),
    );
    expect(custom.vars["--accent-c-accent-l"]).toBe("#0f766e");
  });
});

describe("applyAccent", () => {
  afterEach(() => {
    applyAccent("");
    localStorage.clear();
  });
  it("sets the attribute and variables, caches them, and clears on default", () => {
    const root = document.documentElement;
    applyAccent("#0f766e");
    expect(root.getAttribute("data-accent")).toBe("custom");
    expect(root.style.getPropertyValue("--accent-c-accent-l")).toBe("#0f766e");
    expect(JSON.parse(localStorage.getItem(ACCENT_STORAGE_KEY)!)).toMatchObject({ attr: "custom" });

    applyAccent("rose");
    expect(root.getAttribute("data-accent")).toBe("rose");
    expect(root.style.getPropertyValue("--accent-c-accent-l")).toBe("");
    expect(JSON.parse(localStorage.getItem(ACCENT_STORAGE_KEY)!)).toEqual({ attr: "rose", vars: {} });

    applyAccent("");
    expect(root.hasAttribute("data-accent")).toBe(false);
    expect(localStorage.getItem(ACCENT_STORAGE_KEY)).toBeNull();
  });
});
