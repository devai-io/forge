import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Project, ProjectDetail, VaultItem } from "@/api/types";
import { AddFilesDialog } from "@/features/vault/AddFilesDialog";
import { VaultItemDialog } from "@/features/vault/VaultItemDialog";
import { RepoFilesPanel } from "@/features/vault/VaultPage";
import { jsonResponse, renderWithProviders, routeFetch } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

const base: VaultItem = {
  id: 1,
  name: "shop_api · .env",
  kind: "env_file",
  project_key: "SHOP",
  project_color: "#2a7",
  repo_id: 11,
  repo_name: "shop_api",
  path: ".env",
  platform: "",
  host: "",
  identifier: "",
  fields: {},
  secret_keys: [],
  has_file: true,
  file_name: ".env",
  file_size: 40,
  location: "",
  expires_at: null,
  notes: "",
  tags: ["repo-file"],
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-01T00:00:00Z",
  last_revealed_at: null,
};

const project = {
  key: "SHOP",
  name: "Shop",
  repos: [
    { id: 11, name: "shop_api" },
    { id: 12, name: "shop_ui" },
  ],
} as unknown as ProjectDetail;

describe("Repo files and machine setup", () => {
  it("lists repo files, flags one with nothing to write, and hands out a one-time command", async () => {
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /projects/SHOP": () => jsonResponse(200, project),
        "POST /projects/SHOP/vault/setup-code": () =>
          jsonResponse(201, {
            code: "c0de",
            url: "https://forge.example.com/api/vault-setup/c0de",
            command: "curl -fsSL 'https://forge.example.com/api/vault-setup/c0de' | tar -xzvf -",
            expires_at: new Date(Date.now() + 600_000).toISOString(),
          }),
      }),
    );
    const onOpen = vi.fn();
    renderWithProviders(
      <RepoFilesPanel
        projectKey="SHOP"
        onOpen={onOpen}
        items={[
          base,
          { ...base, id: 2, path: "creds.txt", kind: "password", has_file: false, secret_keys: ["a", "b"] },
          { ...base, id: 3, repo_id: null, repo_name: null, path: "" }, // not a repo file
        ]}
      />,
    );
    expect(screen.getByText("shop_api/")).toBeInTheDocument();
    expect(screen.getByText("nothing to write")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: ".env" }));
    expect(onOpen).toHaveBeenCalledWith(1);

    await user.click(screen.getByRole("button", { name: /Set up a machine/ }));
    await user.click(await screen.findByRole("button", { name: /Make a one-time command/ }));
    expect(await screen.findByText(/vault-setup\/c0de' \| tar -xzvf -/)).toBeInTheDocument();
    expect(screen.getByText(/Expires in \d+:\d\d/)).toBeInTheDocument();
  });

  it("saves an item's repo and path", async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /projects": () => jsonResponse(200, { projects: [{ key: "SHOP", name: "Shop" } as Project] }),
        "GET /projects/SHOP": () => jsonResponse(200, project),
        "POST /vault": (init) => {
          bodies.push(JSON.parse(String(init.body)));
          return jsonResponse(201, base);
        },
      }),
    );
    renderWithProviders(<VaultItemDialog defaults={{ project_key: "SHOP" }} onClose={() => {}} />);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Name"), "api env");
    await user.selectOptions(await screen.findByLabelText("Repo"), await screen.findByRole("option", { name: "shop_ui" }));
    await user.type(screen.getByLabelText(/Path in the repo/), "config/.env");
    await user.click(screen.getByRole("button", { name: "Add item" }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toMatchObject({ project_key: "SHOP", repo_id: 12, path: "config/.env" });
  });

  it("adds several files: new ones go in, a changed one waits for Replace", async () => {
    const calls: { dry_run?: boolean; repo_id: number; files: { path: string }[] }[] = [];
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /projects/SHOP": () => jsonResponse(200, project),
        "POST /projects/SHOP/vault/files": (init) => {
          const body = JSON.parse(String(init.body));
          calls.push(body);
          const path = body.files[0].path;
          const action = path === ".env" ? "update" : "create";
          return jsonResponse(200, { project_key: "SHOP", repo: "shop_ui", repo_id: 12, files: [{ path, action, item_id: body.dry_run ? null : 9 }] });
        },
      }),
    );
    renderWithProviders(<AddFilesDialog projectKey="SHOP" onClose={() => {}} />);
    const user = userEvent.setup();
    await user.selectOptions(await screen.findByLabelText("Repo"), await screen.findByRole("option", { name: "shop_ui" }));
    await user.upload(screen.getByLabelText("Files to add"), [
      new File(["A=1\n"], ".env"),
      new File(["-----BEGIN-----"], "tls.pem"),
    ]);
    const pem = screen.getByLabelText("Path of tls.pem");
    await user.clear(pem);
    await user.type(pem, "certs/tls.pem");
    expect(screen.getByText(/Private key/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /Upload 2/ }));
    expect(await screen.findByText("added")).toBeInTheDocument();
    expect(await screen.findByText("already in the vault")).toBeInTheDocument();
    // .env: a dry run only; tls.pem: dry run, then the write.
    expect(calls.map((c) => [c.files[0].path, !!c.dry_run])).toEqual([
      [".env", true],
      ["certs/tls.pem", true],
      ["certs/tls.pem", false],
    ]);
    expect(calls.every((c) => c.repo_id === 12)).toBe(true);

    await user.click(screen.getByRole("button", { name: "Replace" }));
    expect(await screen.findByText("replaced")).toBeInTheDocument();
    expect(calls.at(-1)).toMatchObject({ files: [{ path: ".env" }] });
    expect(calls.at(-1)?.dry_run).toBeUndefined();
  });
});
