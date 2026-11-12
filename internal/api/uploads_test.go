package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/devai-io/forge/internal/auth"
	"github.com/devai-io/forge/internal/store"
)

func pngBytes(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// uploadImage posts one file the way the composer does and decodes the answer.
func (h *harness) uploadImage(name string, data []byte, out any) int {
	h.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/uploads", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(csrfHeader, "web")
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		_ = json.Unmarshal(raw, out)
	}
	return resp.StatusCode
}

func TestImageUploads(t *testing.T) {
	h := setup(t)
	h.login()
	img := pngBytes(t)

	var up store.Upload
	expect(t, "upload", h.uploadImage("screen [1].png", img, &up), 201)
	if up.Token == "" || up.ContentType != "image/png" || up.Size != len(img) || up.Name != "screen _1_.png" ||
		up.Marker != "[image: screen _1_.png](forge-upload:"+up.Token+")" {
		t.Fatalf("upload = %+v", up)
	}
	expect(t, "not an image", h.uploadImage("notes.txt", []byte("hello, plain text"), nil), 415)
	expect(t, "too large", h.uploadImage("big.png", append(img, make([]byte, maxUpload)...), nil), 413)

	// The uploader sees it back (thumbnails)…
	resp, err := h.client.Get(h.srv.URL + "/api/uploads/" + up.Token)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(got, img) || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("get: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	expect(t, "unknown token", h.do("GET", "/api/uploads/nope-nope-nope-nope", nil, nil), 404)

	// …a member who did not upload it does not.
	ctx := context.Background()
	hash, _ := auth.HashPassword(testPassword)
	bob, _ := h.store.CreateUser(ctx, "bob", "bob@example.com", "Bob", hash, "UTC", false)
	_ = h.store.SetUserRole(ctx, bob.ID, store.RoleMember)
	jar, _ := cookiejar.New(nil)
	member := &harness{t: t, srv: h.srv, api: h.api, store: h.store, client: &http.Client{Jar: jar}}
	expect(t, "member login", member.signIn("bob", testPassword), 200)
	expect(t, "someone else's image", member.do("GET", "/api/uploads/"+up.Token, nil, nil), 404)
	var mine store.Upload
	expect(t, "member uploads", member.uploadImage("m.png", img, &mine), 201)
	expect(t, "member sees its own", member.do("GET", "/api/uploads/"+mine.Token, nil, nil), 200)

	// The machine fetches it by token.
	var rn store.Runner
	var token string
	expect(t, "machine", h.newRunner("box", &rn, &token), 201)
	fetch := func(tok, bearer string) int {
		req, _ := http.NewRequest("GET", h.srv.URL+"/api/runner/uploads/"+tok, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	expect(t, "runner fetch", fetch(up.Token, token), 200)
	expect(t, "no runner token", fetch(up.Token, ""), 401)
	expect(t, "unknown", fetch("nope-nope-nope-nope", token), 404)

	if n, err := h.store.PruneUploads(ctx, 0); err != nil || n != 2 {
		t.Fatalf("prune: %d %v", n, err)
	}
}
