import type { VaultItem } from "@/api/types";
import { baseName, groupByRepo, repoFileContent } from "./vault";

const item = (over: Partial<VaultItem>): VaultItem => ({
  id: 1,
  name: "x",
  kind: "file",
  project_key: "SHOP",
  project_color: null,
  repo_id: null,
  repo_name: null,
  path: "",
  platform: "",
  host: "",
  identifier: "",
  fields: {},
  secret_keys: [],
  has_file: false,
  file_name: "",
  file_size: 0,
  location: "",
  expires_at: null,
  notes: "",
  tags: [],
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-01T00:00:00Z",
  last_revealed_at: null,
  ...over,
});

describe("repo files", () => {
  it("groups items with a repo and a path, by repo, paths sorted", () => {
    const groups = groupByRepo([
      item({ id: 1, repo_id: 2, repo_name: "shop_ui", path: ".env" }),
      item({ id: 2, repo_id: 1, repo_name: "shop_api", path: "certs/tls.pem" }),
      item({ id: 3, repo_id: 1, repo_name: "shop_api", path: ".env" }),
      item({ id: 4 }), // not a repo file
    ]);
    expect(groups.map((g) => g.repo)).toEqual(["shop_api", "shop_ui"]);
    expect(groups[0].items.map((i) => i.path)).toEqual([".env", "certs/tls.pem"]);
  });

  it("says what machine setup writes", () => {
    expect(repoFileContent(item({ has_file: true }))).toBe("file");
    expect(repoFileContent(item({ kind: "env_file", secret_keys: ["A", "B"] }))).toBe("fields");
    expect(repoFileContent(item({ kind: "password", secret_keys: ["value"] }))).toBe("value");
    expect(repoFileContent(item({ kind: "password", secret_keys: ["a", "b"] }))).toBeNull();
  });

  it("takes a path's file name", () => {
    expect(baseName("android/app/key.properties")).toBe("key.properties");
    expect(baseName(".env")).toBe(".env");
  });
});
