package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/devai-io/forge/internal/runner"
	"github.com/devai-io/forge/internal/store"
)

func TestSetupCreatesTheFirstAccountOnce(t *testing.T) {
	h := setupWith(t, false)
	var status struct{ Needed bool }
	expect(t, "status", h.do("GET", "/api/setup", nil, &status), 200)
	if !status.Needed {
		t.Fatal("a server without accounts needs setup")
	}
	token, err := EnsureSetupToken(h.home)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"token": "wrong", "username": "ada", "email": "ada@example.com",
		"password": testPassword, "timezone": "Europe/Lisbon", "demo_data": false}
	expect(t, "wrong token", h.do("POST", "/api/setup", body, nil), 403)

	body["token"] = token
	body["password"] = "short"
	expect(t, "weak password", h.do("POST", "/api/setup", body, nil), 422)
	body["password"] = testPassword
	body["username"] = "a b"
	expect(t, "bad username", h.do("POST", "/api/setup", body, nil), 422)
	body["username"] = "ada"

	var me struct{ User store.User }
	expect(t, "setup", h.do("POST", "/api/setup", body, &me), 201)
	if me.User.Username != "ada" || me.User.Timezone != "Europe/Lisbon" {
		t.Fatalf("user = %+v", me.User)
	}
	expect(t, "signed in by setup", h.do("GET", "/api/auth/me", nil, nil), 200)
	expect(t, "again", h.do("POST", "/api/setup", body, nil), 409)
	expect(t, "status after", h.do("GET", "/api/setup", nil, &status), 200)
	if status.Needed {
		t.Fatal("setup still needed after it ran")
	}
	// A password the owner chose is not the "unchanged setup password".
	if unchanged, _ := h.store.InitialPasswordUnchanged(t.Context(), me.User.ID); unchanged {
		t.Fatal("a chosen password is flagged as generated")
	}
}

func TestPairingIssuesATokenOnce(t *testing.T) {
	h := setup(t)
	h.login()
	var created struct {
		Runner  store.Runner
		Pairing struct{ Code string }
	}
	expect(t, "add machine", h.do("POST", "/api/runners", map[string]string{"name": "desk", "role": "master"}, &created), 201)
	if created.Runner.Role != "master" || created.Runner.PairExpiresAt == nil || len(created.Pairing.Code) != 9 {
		t.Fatalf("created = %+v", created)
	}

	// The agent side, for real: runner.Pair against this server.
	cfgPath := t.TempDir() + "/agent.json"
	res, err := runner.Pair(t.Context(), h.srv.URL, strings.ToLower(created.Pairing.Code), cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "desk" || !strings.HasPrefix(res.Token, "frg_") {
		t.Fatalf("pair = %+v", res)
	}
	cfg, err := runner.LoadConfig(cfgPath)
	if err != nil || cfg.Token != res.Token || cfg.APIURL != h.srv.URL {
		t.Fatalf("written config: %+v %v", cfg, err)
	}
	if _, err := runner.Pair(t.Context(), h.srv.URL, created.Pairing.Code, cfgPath); err == nil {
		t.Fatal("a pairing code worked twice")
	}

	// The token works on the runner API.
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/runner/heartbeat", strings.NewReader(`{"hostname":"desk"}`))
	req.Header.Set("Authorization", "Bearer "+res.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("heartbeat with the paired token: %v %v", err, resp.Status)
	}
	resp.Body.Close()

	// A new code for the same machine; the old token lasts until it is used.
	var again struct{ Pairing struct{ Code string } }
	expect(t, "pair again", h.do("POST", "/api/runners/"+itoa(created.Runner.ID)+"/pair", nil, &again), 200)
	if again.Pairing.Code == created.Pairing.Code {
		t.Fatal("the same code twice")
	}
}

func TestProjectFiles(t *testing.T) {
	h := setup(t)
	h.login()
	upload := func(name, content string) int {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", name)
		_, _ = io.WriteString(fw, content)
		_ = mw.Close()
		req, _ := http.NewRequest("POST", h.srv.URL+"/api/projects/SHOP/files", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set(csrfHeader, "web")
		resp, err := h.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	expect(t, "upload", upload("spec.md", "# spec"), 201)
	expect(t, "hidden name", upload(".env", "x"), 422)
	expect(t, "unknown project", h.do("GET", "/api/projects/NOPE/files", nil, nil), 404)

	var list struct {
		Files []struct {
			Name string
			Size int64
		}
	}
	expect(t, "list", h.do("GET", "/api/projects/SHOP/files", nil, &list), 200)
	if len(list.Files) != 1 || list.Files[0].Name != "spec.md" || list.Files[0].Size != 6 {
		t.Fatalf("files = %+v", list.Files)
	}
	resp, err := h.client.Get(h.srv.URL + "/api/projects/SHOP/files/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != "# spec" || resp.Header.Get("Content-Type") != "application/octet-stream" ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %q %v", got, resp.Header)
	}
	expect(t, "traversal", h.do("GET", "/api/projects/SHOP/files/..%2Fforge.db", nil, nil), 404)
	expect(t, "delete", h.do("DELETE", "/api/projects/SHOP/files/spec.md", nil, nil), 204)
	expect(t, "gone", h.do("DELETE", "/api/projects/SHOP/files/spec.md", nil, nil), 404)
}

func TestAccent(t *testing.T) {
	h := setup(t)
	h.login()
	var me struct{ User store.User }
	expect(t, "preset", h.do("PATCH", "/api/auth/me", map[string]string{"accent": "teal"}, &me), 200)
	expect(t, "custom", h.do("PATCH", "/api/auth/me", map[string]string{"accent": "#0D9488"}, &me), 200)
	if me.User.Accent != "#0d9488" {
		t.Fatalf("accent = %q", me.User.Accent)
	}
	expect(t, "bad", h.do("PATCH", "/api/auth/me", map[string]string{"accent": "chartreuse"}, nil), 422)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
