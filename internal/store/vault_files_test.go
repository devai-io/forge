package store_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/vault"
)

func repoID(t *testing.T, st *store.Store, project, name string) int64 {
	t.Helper()
	var id int64
	if err := st.DB.QueryRow(context.Background(), `SELECT r.id FROM repos r JOIN projects p ON p.id = r.project_id
		WHERE p.key = $1 AND r.name = $2`, project, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCleanRepoPath(t *testing.T) {
	for in, want := range map[string]string{"": "", ".env": ".env", " config/./app.yml ": "config/app.yml", "android/app/key.properties": "android/app/key.properties"} {
		if got, err := store.CleanRepoPath(in); err != nil || got != want {
			t.Errorf("CleanRepoPath(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"/etc/passwd", "../x", "a/../../x", `a\b`, "a\x00b", ".", "./"} {
		if _, err := store.CleanRepoPath(bad); err == nil {
			t.Errorf("CleanRepoPath(%q) accepted", bad)
		}
	}
}

func TestRenderDotenv(t *testing.T) {
	got := store.RenderDotenv(map[string]string{"B": "plain", "A": "has space", "C": "it's", "D": "multi\nline", "E": "", "F": "$HOME"})
	want := "A='has space'\nB=plain\nC=\"it's\"\nD=\"multi\\nline\"\nE=\nF='$HOME'\n"
	if got != want {
		t.Fatalf("dotenv =\n%s\nwant\n%s", got, want)
	}
}

func TestInferRepoFileKind(t *testing.T) {
	for p, want := range map[string]string{".env": "env_file", "api/.env.production": "env_file", "certs/server.pem": "private_key",
		"android/app/upload.jks": "keystore", "ios/dist.mobileprovision": "provisioning_profile", "android/app/google-services.json": "service_account",
		"ca.crt": "certificate", "config/settings.yml": "file", "service-account.json": "service_account"} {
		if got := store.InferRepoFileKind(p); got != want {
			t.Errorf("InferRepoFileKind(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestRepoFiles(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	box := must(vault.New(bytes.Repeat([]byte{1}, 32)))
	shop, game := "SHOP", "GAME"
	api := repoID(t, st, "SHOP", "shop_api")
	gameAPI := repoID(t, st, "GAME", "game_api")

	// Repo and path rules.
	if _, err := st.CreateVault(ctx, box, store.VaultInput{Name: "x", Kind: "file", Path: ".env"}, ""); err == nil {
		t.Fatal("a path without a repo was accepted")
	}
	if _, err := st.CreateVault(ctx, box, store.VaultInput{Name: "x", Kind: "file", ProjectKey: &shop, RepoID: &gameAPI, Path: ".env"}, ""); err == nil {
		t.Fatal("another project's repo was accepted")
	}
	env := must(st.CreateVault(ctx, box, store.VaultInput{Name: "api env", Kind: "env_file", ProjectKey: &shop, RepoID: &api, Path: ".env",
		Secret: map[string]string{"DB_URL": "postgres://u:p@db.example.com/x", "MODE": "dev"}}, ""))
	if env.RepoID == nil || *env.RepoID != api || env.RepoName == nil || *env.RepoName != "shop_api" || env.Path != ".env" {
		t.Fatalf("item = %+v", env)
	}
	if _, err := st.CreateVault(ctx, box, store.VaultInput{Name: "dup", Kind: "file", ProjectKey: &shop, RepoID: &api, Path: ".env"}, ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate path: %v", err)
	}
	must(st.CreateVault(ctx, box, store.VaultInput{Name: "keystore", Kind: "keystore", ProjectKey: &shop, RepoID: &api, Path: "android/upload.jks",
		File: &store.VaultFile{Name: "upload.jks", ContentBase64: base64.StdEncoding.EncodeToString([]byte{9, 8, 7})}}, ""))
	must(st.CreateVault(ctx, box, store.VaultInput{Name: "two fields", Kind: "password", ProjectKey: &shop, RepoID: &api, Path: "creds.txt",
		Secret: map[string]string{"a": "1", "b": "2"}}, ""))
	if list := must(st.ListVault(ctx, store.VaultFilter{RepoID: api})); len(list) != 3 {
		t.Fatalf("repo filter = %d", len(list))
	}

	files := must(st.RepoFiles(ctx, box, "shop", 0, "192.0.2.1"))
	if len(files) != 3 {
		t.Fatalf("files = %+v", files)
	}
	byPath := map[string]store.RepoFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	if string(byPath[".env"].Content) != "DB_URL=postgres://u:p@db.example.com/x\nMODE=dev\n" || byPath[".env"].RepoDir != "shop_api" {
		t.Fatalf(".env = %q in %q", byPath[".env"].Content, byPath[".env"].RepoDir)
	}
	if !bytes.Equal(byPath["android/upload.jks"].Content, []byte{9, 8, 7}) {
		t.Fatal("keystore content")
	}
	if byPath["creds.txt"].Content != nil || byPath["creds.txt"].Skipped == "" {
		t.Fatal("an item with several fields and no file should be skipped")
	}

	// Moving the item to another project drops its repo and path.
	moved := must(st.UpdateVault(ctx, box, env.ID, store.Patch{"project_key": json.RawMessage(`"GAME"`)}, ""))
	if moved.RepoID != nil || moved.Path != "" {
		t.Fatalf("moved = %+v", moved)
	}
	if _, err := st.UpdateVault(ctx, box, env.ID, store.Patch{"path": json.RawMessage(`".env"`)}, ""); err == nil {
		t.Fatal("a path without a repo was accepted on update")
	}
	upd := must(st.UpdateVault(ctx, box, env.ID, store.Patch{"repo_id": json.RawMessage([]byte(itoa(gameAPI))), "path": json.RawMessage(`"cfg/.env"`)}, ""))
	if upd.RepoID == nil || *upd.RepoID != gameAPI || upd.Path != "cfg/.env" {
		t.Fatalf("updated = %+v", upd)
	}
	_ = game

	// A machine's push: create, then same, then update.
	repo := must(st.RepoForCheckout(ctx, "/somewhere/else", "https://github.com/example/shop_ui"))
	if repo.Name != "shop_ui" {
		t.Fatalf("repo by remote = %s", repo.Name)
	}
	in := []store.RepoFileInput{{Path: ".env.local", ContentBase64: base64.StdEncoding.EncodeToString([]byte("A=1\n"))}}
	if res := must(st.PushRepoFiles(ctx, box, repo, in, true, "machine m", "note")); res[0].Action != "create" || res[0].ItemID != nil {
		t.Fatalf("dry run = %+v", res)
	}
	if res := must(st.PushRepoFiles(ctx, box, repo, in, false, "machine m", "note")); res[0].Action != "create" || res[0].ItemID == nil {
		t.Fatalf("push = %+v", res)
	}
	if res := must(st.PushRepoFiles(ctx, box, repo, in, false, "machine m", "note")); res[0].Action != "same" {
		t.Fatalf("again = %+v", res)
	}
	in[0].ContentBase64 = base64.StdEncoding.EncodeToString([]byte("A=2\n"))
	if res := must(st.PushRepoFiles(ctx, box, repo, in, false, "machine m", "note")); res[0].Action != "update" {
		t.Fatalf("changed = %+v", res)
	}
	got := must(st.RepoFiles(ctx, box, "SHOP", repo.ID, ""))
	if len(got) != 1 || string(got[0].Content) != "A=2\n" {
		t.Fatalf("pushed file = %+v", got)
	}
	item := must(st.VaultByID(ctx, *must(st.PushRepoFiles(ctx, box, repo, in, true, "m", "note"))[0].ItemID))
	if item.Kind != "env_file" || item.Name != "shop_ui · .env.local" {
		t.Fatalf("pushed item = %+v", item)
	}
	if _, err := st.RepoForCheckout(ctx, "/nowhere", ""); err == nil {
		t.Fatal("an unknown checkout was accepted")
	}
}

func itoa(n int64) string { return string(must(json.Marshal(n))) }

// `forge import` places vault items in a repo by the repo's name.
func TestImportRepoFiles(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	box := must(vault.New(bytes.Repeat([]byte{1}, 32)))
	content := base64.StdEncoding.EncodeToString([]byte("API_KEY=demo\n"))
	rep := must(st.Import(ctx, box, []byte(`{"vault":[{"name":"shop ui env","kind":"env_file","project_key":"SHOP",
		"repo":"shop_ui","path":".env","file":{"name":".env","content_base64":"`+content+`"}}]}`), "cli"))
	if len(rep.VaultAdded) != 1 {
		t.Fatalf("import = %+v", rep)
	}
	items := must(st.ListVault(ctx, store.VaultFilter{ProjectKey: "SHOP", Query: "shop ui env"}))
	if len(items) != 1 || items[0].RepoID == nil || *items[0].RepoID != repoID(t, st, "SHOP", "shop_ui") || items[0].Path != ".env" {
		t.Fatalf("imported = %+v", items)
	}
	for _, bad := range []string{
		`{"vault":[{"name":"x1","kind":"file","project_key":"SHOP","repo":"nope","path":"a"}]}`,
		`{"vault":[{"name":"x2","kind":"file","repo":"shop_ui","path":"a"}]}`,
		`{"vault":[{"name":"x3","kind":"file","project_key":"SHOP","repo":"shop_ui","path":"../a"}]}`,
		`{"vault":[{"name":"x4","kind":"file","project_key":"SHOP","path":"a"}]}`,
	} {
		if _, err := st.Import(ctx, box, []byte(bad), "cli"); err == nil {
			t.Errorf("import accepted %s", bad)
		}
	}

	// The web app's "Add files" goes through RepoInProject.
	if _, err := st.RepoInProject(ctx, "GAME", repoID(t, st, "SHOP", "shop_ui")); err == nil {
		t.Error("a repo of another project was accepted")
	}
	if r := must(st.RepoInProject(ctx, "shop", repoID(t, st, "SHOP", "shop_ui"))); r.Name != "shop_ui" {
		t.Errorf("repo = %+v", r)
	}
}
