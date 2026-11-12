import { hasAnsi, parseAnsi, stripAnsi } from "./ansi";

describe("ansi", () => {
  it("turns SGR colours into styled segments and drops other escapes", () => {
    const text = "\x1b[1m\x1b[32mPASS\x1b[0m src/a.test.ts \x1b[2K\x1b]0;title\x07\x1b[38;5;196mred\x1b[39m done";
    expect(hasAnsi(text)).toBe(true);
    expect(stripAnsi(text)).toBe("PASS src/a.test.ts red done");
    const segs = parseAnsi(text);
    expect(segs.map((s) => s.text)).toEqual(["PASS", " src/a.test.ts ", "red", " done"]);
    expect(segs[0].style).toMatchObject({ bold: true, fg: "#9ece6a" });
    expect(segs[1].style).toEqual({});
    expect(segs[2].style.fg).toBe("rgb(255,0,0)");
    expect(segs[3].style.fg).toBeUndefined();
  });

  it("handles truecolor, backgrounds and plain text", () => {
    expect(parseAnsi("\x1b[38;2;1;2;3;41mx")[0].style).toEqual({ fg: "rgb(1,2,3)", bg: "#f07178" });
    expect(parseAnsi("plain")).toEqual([{ text: "plain", style: {} }]);
    expect(hasAnsi("plain")).toBe(false);
  });
});
