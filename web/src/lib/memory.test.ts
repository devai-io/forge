import { memorySource, savedMessage } from "./memory";

describe("memory helpers", () => {
  it("names the source", () => {
    expect(memorySource({ source: "claude", origin: "Claude on desk" })).toBe("Claude on desk");
    expect(memorySource({ source: "claude", origin: "" })).toBe("Claude");
    expect(memorySource({ source: "user", origin: "web" })).toBe("you");
    expect(memorySource({ source: "telegram", origin: "telegram" })).toBe("you, on Telegram");
  });
  it("words the save", () => {
    expect(savedMessage(true, 0)).toMatch(/word for word/);
    expect(savedMessage(false, 0)).toBe("Remembered");
    expect(savedMessage(false, 1)).toMatch(/1 similar memory/);
    expect(savedMessage(false, 2)).toMatch(/2 similar memories/);
  });
});
