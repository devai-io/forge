import type { Runner } from "@/api/types";
import { commandOptions, defaultRunner, roleOf, runnerForCommand } from "./agents";
import {
  editorPath,
  folderName,
  frameFailure,
  isSafeCodeUrl,
  openBody,
  parseEditorTarget,
  readOpen,
  recentOpens,
  storeOpen,
  targetKey,
} from "./code";

const runner = (id: number, name: string, role: Runner["role"], online: boolean, commands: string[] = []) =>
  ({
    id,
    name,
    role,
    online,
    capabilities: {
      commands,
      command_details: commands.map((c) => ({ name: c, description: "", repos: [], confirm: false })),
    },
  }) as unknown as Runner;

describe("runner roles", () => {
  const desk = runner(1, "desk", "master", true, ["git-pull", "test"]);
  const laptop = runner(2, "laptop", "worker", true, ["git-pull"]);
  const mac = runner(3, "mac", "ios", true, ["git-pull", "ios-testflight", "ios-build"]);

  it("defaults to the master, else the first online runner", () => {
    expect(defaultRunner([laptop, mac, desk])?.name).toBe("desk");
    const asleep = { ...desk, online: false };
    expect(defaultRunner([asleep, laptop])?.name).toBe("laptop");
    expect(defaultRunner([asleep])?.name).toBe("desk");
    expect(defaultRunner([])).toBeUndefined();
    expect(roleOf({})).toBe("worker");
  });

  it("sends iOS commands to the iOS runner and keeps others where they are", () => {
    expect(runnerForCommand([desk, laptop, mac], "ios-testflight", 1)?.name).toBe("mac");
    expect(runnerForCommand([desk, laptop, mac], "git-pull", 2)?.name).toBe("laptop");
    expect(runnerForCommand([desk, laptop, mac], "test", 3)?.name).toBe("desk");
    expect(runnerForCommand([desk, laptop, mac], "nope", 1)).toBeUndefined();
  });

  it("offers every runner's commands, current runner first", () => {
    const opts = commandOptions([desk, laptop, mac], undefined, 1);
    expect(opts.map((o) => o.name)).toEqual(["git-pull", "test", "ios-testflight", "ios-build"]);
    expect(opts.find((o) => o.name === "git-pull")?.runners).toEqual(["desk", "laptop", "mac"]);
    expect(opts.find((o) => o.name === "ios-build")?.runners).toEqual(["mac"]);
  });
});

describe("editor targets", () => {
  it("parses and prints /editor URLs", () => {
    expect(parseEditorTarget(new URLSearchParams(""))).toBeNull();
    const t = parseEditorTarget(new URLSearchParams("project=shop&repos=3,1,3,x"));
    expect(t).toEqual({ kind: "project", projectKey: "SHOP", repoIds: [1, 3] });
    expect(editorPath(t!)).toBe("/editor?project=SHOP&repos=1%2C3");
    expect(parseEditorTarget(new URLSearchParams(editorPath(t!).split("?")[1]))).toEqual(t);
    expect(parseEditorTarget(new URLSearchParams("folder=/home/ada/dev/forge"))).toEqual({ kind: "folder", path: "/home/ada/dev/forge" });
    expect(parseEditorTarget(new URLSearchParams("folder=relative/path"))).toBeNull();
    expect(parseEditorTarget(new URLSearchParams("project=../x"))).toBeNull();
  });

  it("builds the open body and a stable key", () => {
    expect(openBody({ kind: "project", projectKey: "GAME", repoIds: [] })).toEqual({ project_key: "GAME" });
    expect(openBody({ kind: "project", projectKey: "GAME", repoIds: [4] })).toEqual({ project_key: "GAME", repo_ids: [4] });
    expect(openBody({ kind: "folder", path: "/x" })).toEqual({ folder: "/x" });
    expect(targetKey({ kind: "project", projectKey: "GAME", repoIds: [4, 5] })).toBe("project:GAME:4,5");
    expect(targetKey({ kind: "folder", path: "/x" })).toBe("folder:/x");
  });

  it("only frames Forge's own /code/", () => {
    expect(isSafeCodeUrl("/code/?workspace=/home/ada/.forge/SHOP.code-workspace")).toBe(true);
    expect(isSafeCodeUrl("https://evil.example/code/")).toBe(false);
    expect(isSafeCodeUrl("//evil.example/code/")).toBe(false);
    expect(isSafeCodeUrl("/api/x")).toBe(false);
    expect(folderName("/home/ada/dev/forge/forge_api/")).toBe("forge_api");
  });
});

describe("remembered opens", () => {
  beforeEach(() => sessionStorage.clear());
  const shop = { kind: "project" as const, projectKey: "SHOP", repoIds: [] };
  const now = Date.parse("2026-09-28T10:00:00Z");

  it("reuses an open until a minute before it expires", () => {
    storeOpen(shop, { url: "/code/?workspace=a", workspace: "a", expires_at: "2026-09-28T22:00:00Z" }, "Shop", now);
    expect(readOpen(shop, now)?.url).toBe("/code/?workspace=a");
    expect(readOpen(shop, Date.parse("2026-09-28T21:59:30Z"))).toBeNull();
    expect(readOpen({ kind: "folder", path: "/x" }, now)).toBeNull();
  });

  it("never reuses an unsafe URL", () => {
    storeOpen(shop, { url: "https://evil.example/", workspace: "", expires_at: "2026-09-29T00:00:00Z" }, "Shop", now);
    expect(readOpen(shop, now)).toBeNull();
  });

  it("keeps the most recent opens first, without duplicates", () => {
    const later = { url: "/code/?w", workspace: "w", expires_at: "2026-09-29T00:00:00Z" };
    storeOpen(shop, later, "Shop", now);
    storeOpen({ kind: "folder", path: "/home/ada/x" }, later, "x", now + 1000);
    storeOpen(shop, later, "Shop", now + 2000);
    expect(recentOpens().map((r) => r.label)).toEqual(["Shop", "x"]);
    expect(recentOpens()[0].path).toBe("/editor?project=SHOP");
  });
});

describe("frameFailure", () => {
  const doc = (contentType: string, text: string) => ({ contentType, body: { textContent: text } });
  it("reads what the frame loaded instead of VS Code", () => {
    expect(frameFailure(null)).toBeNull();
    expect(frameFailure(doc("text/html", "<html>"))).toBeNull();
    expect(frameFailure(doc("application/json", '{"error":{"code":"unauthorized","message":"no"}}'))).toBe("expired");
    expect(frameFailure(doc("application/json", "garbage"))).toBe("expired");
    expect(frameFailure(doc("application/json", '{"error":{"code":"not_found","message":"no editor behind this host"}}'))).toEqual({
      message: "no editor behind this host",
    });
  });
});
