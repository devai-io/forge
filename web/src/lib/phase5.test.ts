import { withTheme } from "./code";
import { formatInterval, groupByCategory, groupByTier, prettyDuration, severities, slug } from "./docs";
import { describeAgent, isAlarming, KIND_GROUPS, kindLabel } from "./security";
import { isReference } from "./vault";

describe("describeAgent", () => {
  it("names the browser and OS people recognise", () => {
    expect(describeAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36")).toBe("Chrome · Linux");
    expect(describeAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 Version/19.0 Mobile/15E148 Safari/604.1")).toBe("Safari · iOS");
    expect(describeAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) CriOS/140.0 Mobile/15E148 Safari/604.1")).toBe("Chrome · iOS");
    expect(describeAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 15_0) Gecko/20100101 Firefox/143.0")).toBe("Firefox · macOS");
    expect(describeAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/140.0 Safari/537.36 Edg/140.0")).toBe("Edge · Windows");
    expect(describeAgent("Mozilla/5.0 (Linux; Android 15) Chrome/140.0 Mobile Safari/537.36")).toBe("Chrome · Android");
    expect(describeAgent("curl/8.9.1")).toBe("curl");
    expect(describeAgent("")).toBe("Unknown device");
  });
});

describe("security kinds", () => {
  it("labels every kind readably and flags the alarming ones", () => {
    expect(kindLabel("login_new_device")).toBe("Signed in from a new device");
    expect(kindLabel("vault_reveal")).toBe("Vault secret revealed");
    expect(kindLabel("some_future_kind")).toBe("some future kind");
    expect(isAlarming("login_failed")).toBe(true);
    expect(isAlarming("login_new_device")).toBe(true);
    expect(isAlarming("login")).toBe(false);
  });
  it("groups every kind exactly once for the filter", () => {
    const all = KIND_GROUPS.flatMap((g) => g.kinds);
    expect(new Set(all).size).toBe(all.length);
    expect(all).toContain("code_open");
    expect(all).toContain("runner_rotated");
  });
});

describe("withTheme", () => {
  it("appends or replaces forge_theme without touching the rest", () => {
    expect(withTheme("/code/?workspace=/w.code-workspace", "dark")).toBe("/code/?workspace=%2Fw.code-workspace&forge_theme=dark");
    expect(withTheme("/code/?folder=%2Fx&forge_theme=dark", "light")).toBe("/code/?folder=%2Fx&forge_theme=light");
    expect(withTheme("/code/", "light")).toBe("/code/?forge_theme=light");
    expect(withTheme("/code/?a=1#frag", "dark")).toBe("/code/?a=1&forge_theme=dark#frag");
  });
});

describe("docs helpers", () => {
  it("prints Go durations as people say them", () => {
    expect(prettyDuration("2m0s")).toBe("2m");
    expect(prettyDuration("1h30m0s")).toBe("1h 30m");
    expect(prettyDuration("10s")).toBe("10s");
    expect(prettyDuration("90s")).toBe("90s");
    expect(prettyDuration("720h0m0s")).toBe("720h");
    expect(prettyDuration("0s")).toBe("0s");
    expect(prettyDuration("weird")).toBe("weird");
  });
  it("overlays the live interval per source key", () => {
    const intervals = { endpoint_check: "2m0s", checkup_time: "07:30", timezone: "Europe/Lisbon" };
    expect(formatInterval("endpoint_check", intervals)).toBe("every 2m");
    expect(formatInterval("checkup_time", intervals)).toBe("daily at 07:30 Europe/Lisbon");
    expect(formatInterval("repo_scan", intervals)).toBeNull();
    expect(formatInterval("repo_scan", undefined)).toBeNull();
  });
  it("groups and slugs", () => {
    expect(groupByTier([{ tier: "stored" as const }, { tier: "automated" as const }, { tier: "automated" as const }]).map((g) => [g.tier, g.items.length])).toEqual([
      ["automated", 2],
      ["stored", 1],
    ]);
    expect(groupByCategory([{ category: "B" }, { category: "A" }, { category: "B" }]).map((g) => [g.category, g.items.length])).toEqual([
      ["B", 2],
      ["A", 1],
    ]);
    expect(severities("fail / warn")).toEqual(["fail", "warn"]);
    expect(severities("warn")).toEqual(["warn"]);
    expect(slug("The daily check-up")).toBe("the-daily-check-up");
    expect(slug("Step-up for the dangerous things")).toBe("step-up-for-the-dangerous-things");
  });
});

describe("isReference", () => {
  it("is true only when Forge holds nothing to reveal", () => {
    expect(isReference({ secret_keys: [], has_file: false })).toBe(true);
    expect(isReference({ secret_keys: ["value"], has_file: false })).toBe(false);
    expect(isReference({ secret_keys: [], has_file: true })).toBe(false);
  });
});
