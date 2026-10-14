package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/devai-io/forge/internal/store"
)

// untar reads a bundle into path → content.
func untar(t *testing.T, data []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Mode != 0o600 {
			t.Fatalf("%s mode %o", hdr.Name, hdr.Mode)
		}
		b, _ := io.ReadAll(tr)
		out[hdr.Name] = string(b)
	}
}

func (h *harness) raw(method, path string, client *http.Client) (int, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, nil)
	if method != http.MethodGet {
		req.Header.Set(csrfHeader, "web")
	}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestVaultRepoFilesAndMachineSetup(t *testing.T) {
	h := setup(t)
	h.login()

	// A machine collects a checkout's .env (write-only).
	var rn store.Runner
	var token string
	expect(t, "add machine", h.newRunner("laptop", &rn, &token), 201)
	machine := &harness{t: t, srv: h.srv, client: &http.Client{}}
	auth := []string{"Authorization", "Bearer " + token}
	body := map[string]any{"cwd": "/home/demo/dev/shop/shop_api", "dry_run": true,
		"files": []map[string]string{{"path": ".env", "content_base64": base64.StdEncoding.EncodeToString([]byte("KEY=s3cret\n"))}}}
	var res struct {
		ProjectKey string `json:"project_key"`
		Repo       string
		Files      []store.RepoFileResult
	}
	expect(t, "dry run", machine.do("POST", "/api/runner/vault/files", body, &res, auth...), 200)
	if res.ProjectKey != "SHOP" || res.Repo != "shop_api" || res.Files[0].Action != "create" {
		t.Fatalf("dry run = %+v", res)
	}
	body["dry_run"] = false
	expect(t, "push", machine.do("POST", "/api/runner/vault/files", body, &res, auth...), 200)
	expect(t, "push again", machine.do("POST", "/api/runner/vault/files", body, &res, auth...), 200)
	if res.Files[0].Action != "same" {
		t.Fatalf("again = %+v", res)
	}
	expect(t, "unknown checkout", machine.do("POST", "/api/runner/vault/files", map[string]any{"cwd": "/tmp/elsewhere",
		"files": body["files"]}, nil, auth...), 422)
	expect(t, "push without a token", machine.do("POST", "/api/runner/vault/files", body, nil), 401)

	// The browser bundle needs elevation.
	code, _ := h.raw("POST", "/api/projects/SHOP/vault/bundle", h.client)
	expect(t, "bundle without elevation", code, 403)
	expect(t, "setup code without elevation", h.do("POST", "/api/projects/SHOP/vault/setup-code", nil, nil), 403)
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)
	code, data := h.raw("POST", "/api/projects/SHOP/vault/bundle", h.client)
	expect(t, "bundle", code, 200)
	files := untar(t, data)
	if files["shop_api/.env"] != "KEY=s3cret\n" || !strings.Contains(files["FORGE-SECRETS.txt"], "wrote    shop_api/.env") {
		t.Fatalf("bundle = %v", files)
	}

	// A one-time code works once, with no session at all.
	var sc struct {
		Code, URL, Command string
		ExpiresAt          time.Time `json:"expires_at"`
	}
	expect(t, "setup code", h.do("POST", "/api/projects/SHOP/vault/setup-code", nil, &sc), 201)
	if len(sc.Code) != 64 || !strings.Contains(sc.Command, sc.URL) || !strings.HasPrefix(sc.Command, "curl ") || time.Until(sc.ExpiresAt) < 9*time.Minute {
		t.Fatalf("setup code = %+v", sc)
	}
	path := strings.TrimPrefix(sc.URL, h.srv.URL)
	code, data = h.raw("GET", path, &http.Client{})
	expect(t, "setup download", code, 200)
	if untar(t, data)["shop_api/.env"] != "KEY=s3cret\n" {
		t.Fatal("setup bundle content")
	}
	code, _ = h.raw("GET", path, &http.Client{})
	expect(t, "setup code reused", code, 404)

	// Expired codes are refused.
	c, _ := h.api.vaultSetups.issue("SHOP", 0)
	h.api.vaultSetups.mu.Lock()
	for k, v := range h.api.vaultSetups.codes {
		v.expires = time.Now().Add(-time.Second)
		h.api.vaultSetups.codes[k] = v
	}
	h.api.vaultSetups.mu.Unlock()
	code, _ = h.raw("GET", "/api/vault-setup/"+c, &http.Client{})
	expect(t, "expired code", code, 404)

	// A repo of another project is refused.
	var e map[string]map[string]string
	expect(t, "foreign repo", h.do("POST", "/api/projects/SHOP/vault/setup-code", map[string]any{"repo_id": 999999}, &e), 422)

	// Downloads are audited.
	var audit struct{ Events []store.VaultAudit }
	expect(t, "audit", h.do("GET", "/api/vault/audit", nil, &audit), 200)
	downloads := 0
	for _, ev := range audit.Events {
		if ev.Action == "download" {
			downloads++
		}
	}
	if downloads != 2 {
		t.Fatalf("downloads audited = %d (%+v)", downloads, audit.Events)
	}
}

// "Add files" in the vault: the browser's twin of `forge agent secrets push`.
func TestVaultFilesFromTheBrowser(t *testing.T) {
	h := setup(t)
	h.login()
	var p store.ProjectDetail
	expect(t, "project", h.do("GET", "/api/projects/SHOP", nil, &p), 200)
	var repo, other int64
	for _, r := range p.Repos {
		if r.Name == "shop_ui" {
			repo = r.ID
		}
	}
	var game store.ProjectDetail
	expect(t, "other project", h.do("GET", "/api/projects/GAME", nil, &game), 200)
	other = game.Repos[0].ID

	files := func(s string) []map[string]string {
		return []map[string]string{{"path": "config/key.pem", "content_base64": base64.StdEncoding.EncodeToString([]byte(s))}}
	}
	var res struct{ Files []store.RepoFileResult }
	expect(t, "dry run", h.do("POST", "/api/projects/SHOP/vault/files", map[string]any{"repo_id": repo, "dry_run": true, "files": files("k1")}, &res), 200)
	if res.Files[0].Action != "create" || res.Files[0].ItemID != nil {
		t.Fatalf("dry run = %+v", res)
	}
	expect(t, "upload", h.do("POST", "/api/projects/SHOP/vault/files", map[string]any{"repo_id": repo, "files": files("k1")}, &res), 200)
	var item store.VaultItem
	expect(t, "item", h.do("GET", "/api/vault/"+itoa(*res.Files[0].ItemID), nil, &item), 200)
	if item.Kind != "private_key" || item.Path != "config/key.pem" || !item.HasFile {
		t.Fatalf("item = %+v", item)
	}
	expect(t, "changed", h.do("POST", "/api/projects/SHOP/vault/files", map[string]any{"repo_id": repo, "dry_run": true, "files": files("k2")}, &res), 200)
	if res.Files[0].Action != "update" {
		t.Fatalf("changed = %+v", res)
	}
	expect(t, "repo of another project", h.do("POST", "/api/projects/SHOP/vault/files", map[string]any{"repo_id": other, "files": files("k1")}, nil), 422)
	expect(t, "bad path", h.do("POST", "/api/projects/SHOP/vault/files", map[string]any{"repo_id": repo,
		"files": []map[string]string{{"path": "../x", "content_base64": "eA=="}}}, nil), 422)
}
