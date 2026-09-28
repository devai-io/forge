import type { CheckItem, GrafanaDashboard, VaultItem } from "@/api/types";
import { commandDetails, commandsForRepo } from "./agents";
import { groupCheckItems, isInternalLink } from "./checkup";
import { dashboardsForProject, formatUptime, sortAlerts, sslLevel, usageLevel } from "./monitoring";
import { ciState } from "./projects";
import { expiryState, formatBytes, groupByProject, groupSecret, maskValue, recordToRows, rowsToRecord } from "./vault";

describe("runner commands", () => {
  const caps = {
    commands: ["git-pull", "deploy", "legacy"],
    command_details: [
      { name: "git-pull", description: "Fast-forward", repos: [], confirm: false },
      { name: "deploy", description: "Ship it", repos: ["shop_api"], confirm: true },
    ],
  };
  it("fills in details for names the runner only lists", () => {
    expect(commandDetails(caps).map((c) => [c.name, c.confirm, c.repos.length])).toEqual([
      ["git-pull", false, 0],
      ["deploy", true, 1],
      ["legacy", false, 0],
    ]);
    expect(commandDetails(undefined)).toEqual([]);
  });
  it("offers scoped commands only in their repos", () => {
    const d = commandDetails(caps);
    expect(commandsForRepo(d, "shop_api").map((c) => c.name)).toEqual(["git-pull", "deploy", "legacy"]);
    expect(commandsForRepo(d, "shop_ui").map((c) => c.name)).toEqual(["git-pull", "legacy"]);
    expect(commandsForRepo(d, undefined).map((c) => c.name)).toEqual(["git-pull", "legacy"]);
  });
});

describe("vault helpers", () => {
  it("formats sizes", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(2048)).toBe("2.0 KB");
    expect(formatBytes(5 * 1024 * 1024)).toBe("5.0 MB");
    expect(formatBytes(150 * 1024)).toBe("150 KB");
  });
  it("classifies expiry: red when expired, amber inside 30 days", () => {
    expect(expiryState(null, "2026-09-27")).toBeNull();
    expect(expiryState("2026-09-20", "2026-09-27")).toMatchObject({ state: "expired", label: "expired 7d ago" });
    expect(expiryState("2026-09-27", "2026-09-27")).toMatchObject({ state: "soon" });
    expect(expiryState("2026-10-20", "2026-09-27")).toMatchObject({ state: "soon", days: 23 });
    expect(expiryState("2027-01-01", "2026-09-27")?.state).toBe("ok");
  });
  it("groups keys and masks values without echoing them", () => {
    expect(groupSecret("JBSWY3DPEHPK3PXP")).toBe("JBSW Y3DP EHPK 3PXP");
    const masked = maskValue("hunter2-very-long-password-indeed-yes");
    expect(masked).toMatch(/^•+$/);
    expect(masked).toHaveLength(24);
    expect(maskValue("ab")).toHaveLength(8);
  });
  it("round-trips key/value rows, dropping blank keys", () => {
    expect(rowsToRecord([{ key: " value ", value: "x" }, { key: "", value: "lost" }, { key: "b", value: "" }])).toEqual({ value: "x", b: "" });
    expect(recordToRows({ a: "1" })).toEqual([{ key: "a", value: "1" }]);
  });
  it("groups items by project with shared last", () => {
    const item = (id: number, name: string, project_key: string | null) => ({ id, name, project_key, project_color: null }) as VaultItem;
    const groups = groupByProject([item(1, "b", "GAME"), item(2, "z", null), item(3, "a", "SHOP"), item(4, "a", "GAME")]);
    expect(groups.map((g) => [g.key, g.items.map((i) => i.id)])).toEqual([
      ["GAME", [4, 1]],
      ["SHOP", [3]],
      [null, [2]],
    ]);
  });
});

describe("monitoring helpers", () => {
  it("applies the 80/90 and TLS thresholds", () => {
    expect([usageLevel(79.9), usageLevel(80), usageLevel(90), usageLevel(null)]).toEqual(["ok", "warn", "fail", "ok"]);
    expect([sslLevel(3), sslLevel(10), sslLevel(60), sslLevel(null)]).toEqual(["fail", "warn", "ok", "ok"]);
  });
  it("formats uptime", () => {
    expect(formatUptime(93784)).toBe("1d 2h");
    expect(formatUptime(3700)).toBe("1h 1m");
    expect(formatUptime(59)).toBe("59s");
    expect(formatUptime(null)).toBe("—");
  });
  it("puts firing alerts first", () => {
    const a = (name: string, state: "firing" | "pending", since: string) => ({ name, state, severity: "", summary: "", labels: {}, since });
    expect(sortAlerts([a("p", "pending", "2026-09-27"), a("f1", "firing", "2026-09-25"), a("f2", "firing", "2026-09-26")]).map((x) => x.name)).toEqual([
      "f2",
      "f1",
      "p",
    ]);
  });
  it("matches dashboards to a project by whole words", () => {
    const d = (title: string, folder = ""): GrafanaDashboard => ({ uid: title, title, folder, url: "http://g/" + title });
    const all = [d("Shop launch funnel"), d("Node exporter", "Ops"), d("Data sources overview"), d("KART telemetry"), d("Shopbet soup")];
    expect(dashboardsForProject(all, { key: "SHOP", name: "Shop" }).map((x) => x.title)).toEqual(["Shop launch funnel"]);
    expect(dashboardsForProject(all, { key: "OPS", name: "Ops" }).map((x) => x.title)).toEqual(["Node exporter"]);
    expect(dashboardsForProject(all, { key: "KART", name: "KART App" }).map((x) => x.title)).toEqual(["KART telemetry"]);
  });
});

describe("check-up grouping", () => {
  const item = (key: string, category: CheckItem["category"], severity: CheckItem["severity"], done = false): CheckItem => ({
    key,
    category,
    severity,
    title: key,
    detail: "",
    action: "",
    link: null,
    project_key: null,
    done,
    task_id: null,
    task_ref: null,
  });
  it("groups non-ok items by category in a fixed order, fails first, done last", () => {
    const { groups, ok } = groupCheckItems([
      item("disk:a", "hosts", "warn"),
      item("ep:1", "endpoints", "warn"),
      item("ep:2", "endpoints", "fail"),
      item("ep:3", "endpoints", "fail", true),
      item("ci:x", "ci", "ok"),
    ]);
    expect(groups.map((g) => g.category)).toEqual(["endpoints", "hosts"]);
    expect(groups[0].items.map((i) => i.key)).toEqual(["ep:2", "ep:1", "ep:3"]);
    expect(groups[0].label).toBe("Endpoints");
    expect(ok.map((i) => i.key)).toEqual(["ci:x"]);
  });
  it("tells in-app routes from external links", () => {
    expect(isInternalLink("/p/SHOP")).toBe(true);
    expect(isInternalLink("//evil.example")).toBe(false);
    expect(isInternalLink("https://grafana")).toBe(false);
  });
});

describe("ciState", () => {
  it("maps GitHub status/conclusion", () => {
    expect(ciState({ status: "in_progress", conclusion: "" })).toBe("running");
    expect(ciState({ status: "queued", conclusion: "" })).toBe("running");
    expect(ciState({ status: "completed", conclusion: "success" })).toBe("success");
    expect(ciState({ status: "completed", conclusion: "failure" })).toBe("failure");
    expect(ciState({ status: "completed", conclusion: "cancelled" })).toBe("cancelled");
    expect(ciState({ status: "completed", conclusion: "skipped" })).toBe("neutral");
  });
});
