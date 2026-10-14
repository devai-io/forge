import type { VaultItem } from "@/api/types";
import { baseName, defaultRepoPath, groupByRepo, inferRepoFileKind, repoFileContent, repoPathProblem } from "./vault";

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

describe("Add files", () => {
  it("infers a kind from the file name, like the server", () => {
    const cases: Record<string, string> = {
      ".env": "env_file",
      "config/.env.local": "env_file",
      "prod.env": "env_file",
      "certs/tls.pem": "private_key",
      "AuthKey_ABC.p8": "private_key",
      "id_ed25519": "private_key",
      "android/app/upload.jks": "keystore",
      "dist.p12": "keystore",
      "ca.crt": "certificate",
      "App.mobileprovision": "provisioning_profile",
      "android/app/google-services.json": "service_account",
      "ios/GoogleService-Info.plist": "service_account",
      "service-account-prod.json": "service_account",
      "credentials.json": "service_account",
      "package.json": "file",
      "key.properties": "file",
    };
    for (const [path, kind] of Object.entries(cases)) expect([path, inferRepoFileKind(path)]).toEqual([path, kind]);
  });

  it("keeps a picked folder's paths, without the folder itself", () => {
    expect(defaultRepoPath({ name: ".env", webkitRelativePath: "shop_api/config/.env" })).toBe("config/.env");
    expect(defaultRepoPath({ name: ".env", webkitRelativePath: "" })).toBe(".env");
    expect(defaultRepoPath({ name: "key.pem" })).toBe("key.pem");
  });

  it("flags paths the server would refuse", () => {
    expect(repoPathProblem("config/.env")).toBe("");
    expect(repoPathProblem("")).not.toBe("");
    expect(repoPathProblem("/etc/passwd")).not.toBe("");
    expect(repoPathProblem("a/../../x")).not.toBe("");
    expect(repoPathProblem("a\\b")).not.toBe("");
  });
});
