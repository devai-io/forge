import type { TerminalHost, TmuxSession } from "@/api/types";
import { makeSession, makeWindow } from "@/test/fixtures";
import {
  allWindows,
  attachPath,
  ctrlChar,
  deriveSessionName,
  KEYBAR,
  pickHost,
  sanitizeSessionName,
  SESSION_NAME_RE,
  sessionWindows,
  sortSessions,
  terminalWsUrl,
  truncateMiddle,
  windowParam,
  windowTarget,
} from "./terminal";

const host = (id: number, name: string, sessions: TmuxSession[]): TerminalHost => ({
  runner_id: id,
  runner_name: name,
  hostname: name,
  online: true,
  terminal: true,
  sessions,
  updated_at: "2026-09-27T10:00:00Z",
  role: "worker",
});

describe("sortSessions", () => {
  it("puts Claude sessions first, then the most recently active", () => {
    const sorted = sortSessions([
      makeSession({ name: "old-shell", activity: "2026-09-27T08:00:00Z" }),
      makeSession({ name: "new-shell", activity: "2026-09-27T11:00:00Z" }),
      makeSession({ name: "claude-old", claude: true, activity: "2026-09-26T08:00:00Z" }),
      makeSession({ name: "claude-new", claude: true, activity: "2026-09-27T09:00:00Z" }),
    ]);
    expect(sorted.map((s) => s.name)).toEqual(["claude-new", "claude-old", "new-shell", "old-shell"]);
  });
});

describe("session names", () => {
  it("derives project-repo names and avoids ones already on the host", () => {
    expect(deriveSessionName("CAFE", "cafe_web")).toBe("CAFE-cafe_web");
    expect(deriveSessionName("CAFE", "cafe_web", ["CAFE-cafe_web", "CAFE-cafe_web-2"])).toBe("CAFE-cafe_web-3");
    expect(deriveSessionName("SHOP", null)).toBe("SHOP");
    expect(deriveSessionName(null, null)).toBe("claude");
    expect(deriveSessionName(null, null, [], "shell")).toBe("shell");
  });
  it("keeps names tmux-safe and within 40 characters", () => {
    expect(sanitizeSessionName("my repo/feature: x")).toBe("my-repo-feature-x");
    const long = deriveSessionName("PROJECT", "a".repeat(60), ["PROJECT-" + "a".repeat(32)]);
    expect(long.length).toBeLessThanOrEqual(40);
    expect(long.endsWith("-2")).toBe(true);
    expect(SESSION_NAME_RE.test(long)).toBe(true);
    expect(SESSION_NAME_RE.test("bad name")).toBe(false);
  });
});

describe("key bar", () => {
  it("maps Ctrl+key to control characters", () => {
    expect(ctrlChar("c")).toBe("\u0003");
    expect(ctrlChar("C")).toBe("\u0003");
    expect(ctrlChar("a")).toBe("\u0001");
    expect(ctrlChar("z")).toBe("\u001a");
    expect(ctrlChar("[")).toBe("\u001b");
    expect(ctrlChar("@")).toBe("\u0000");
    expect(ctrlChar("?")).toBe("\u007f");
    expect(ctrlChar("\\")).toBe("\u001c");
    expect(ctrlChar("é")).toBe("é");
    expect(ctrlChar("ab")).toBe("ab");
  });
  it("sends the standard sequences", () => {
    expect(KEYBAR.up).toBe("\u001b[A");
    expect(KEYBAR.left).toBe("\u001b[D");
    expect(KEYBAR.ctrlC).toBe("\u0003");
    expect(KEYBAR.enter).toBe("\r");
  });
});

describe("URLs", () => {
  it("builds the attach WebSocket URL on the app's own origin", () => {
    expect(terminalWsUrl({ protocol: "https:", host: "forge.example.com" }, 3, "Work", 120, 40)).toBe(
      "wss://forge.example.com/api/terminal/3/attach?session=Work&cols=120&rows=40",
    );
    expect(terminalWsUrl({ protocol: "http:", host: "localhost:5175" }, 1, "a b", 0, 0, 2)).toBe(
      "ws://localhost:5175/api/terminal/1/attach?session=a+b&cols=1&rows=1&window=2",
    );
  });
  it("routes to a session, optionally a window", () => {
    expect(attachPath(3, "Work")).toBe("/terminal/3/Work");
    expect(attachPath(3, "Work", 0)).toBe("/terminal/3/Work?window=0");
    expect(attachPath(3, "x.y", null)).toBe("/terminal/3/x.y");
  });
  it("truncates long paths in the middle", () => {
    expect(truncateMiddle("/short")).toBe("/short");
    const t = truncateMiddle("/home/me/dev/work/forge/forge_api/pkg/terminal", 30);
    expect(t).toHaveLength(30);
    expect(t.startsWith("/home/me/d")).toBe(true);
    expect(t.endsWith("pkg/terminal")).toBe(true);
    expect(t).toContain("…");
  });
});

describe("windows", () => {
  it("stands in the session itself when a runner reports no windows", () => {
    const [w] = sessionWindows(makeSession({ claude: true, project_key: "GAME", repo_name: "game" }));
    expect(w).toMatchObject({ index: -1, claude: true, project_key: "GAME", repo_name: "game" });
    expect(windowParam(w)).toBeNull();
    expect(windowTarget("s", w)).toBe("s");
    expect(windowTarget("Work", makeWindow({ index: 2 }))).toBe("Work:2");
  });

  it("matches per window across hosts, Claude windows first", () => {
    const work = makeSession({
      name: "Work",
      windows: 3,
      activity: "2026-09-27T10:00:00Z",
      window_list: [
        makeWindow({ index: 0, name: "infra", project_key: "SRV", claude: true }),
        makeWindow({ index: 1, name: "forge", project_key: "WORK", claude: true, active: false }),
        makeWindow({ index: 2, name: "logs", project_key: "WORK", active: false }),
      ],
      // Session-level fields describe only the active window (SRV).
      project_key: "SRV",
    });
    const shop = makeSession({ name: "shop", claude: true, project_key: "SHOP", activity: "2026-09-27T11:00:00Z" });
    const hosts = [host(1, "desk", [work]), host(2, "buildbox", [shop])];

    const workWindows = allWindows(hosts, (w) => w.project_key === "WORK");
    expect(workWindows.map((x) => [x.host.runner_name, x.session.name, x.window.index])).toEqual([
      ["desk", "Work", 1],
      ["desk", "Work", 2],
    ]);
    const claude = allWindows(hosts, (w) => w.claude);
    expect(claude.map((x) => windowTarget(x.session.name, x.window))).toEqual(["shop", "Work:0", "Work:1"]);
  });
});

describe("pickHost", () => {
  it("prefers the remembered host, then the API default, then the first", () => {
    expect(pickHost([1, 2, 3], 3, 2)).toBe(3);
    expect(pickHost([1, 2, 3], 9, 2)).toBe(2);
    expect(pickHost([1, 2, 3], null, null)).toBe(1);
    expect(pickHost([], 1, 1)).toBeNull();
  });
});
