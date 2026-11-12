package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResolveUploads(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir()) // macOS: UserCacheDir is under HOME
	img := []byte("\x89PNG\r\n\x1a\nfake")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer frg_test" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/runner/uploads/AAAAAAAAAAAAAAAAAAAAAAAA":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(img)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	r := &Runner{client: newClient(srv.URL, "frg_test")}

	in := "Look at this:\n\n[image: shot.png](forge-upload:AAAAAAAAAAAAAAAAAAAAAAAA)\n[image: gone.png](forge-upload:BBBBBBBBBBBBBBBBBBBBBBBB)"
	out := r.resolveUploads(context.Background(), in)
	lines := strings.Split(out, "\n")
	if lines[0] != "Look at this:" || !strings.HasPrefix(lines[2], "[image: shot.png] /") || !strings.HasSuffix(lines[2], "AAAAAAAAAAAAAAAAAAAAAAAA.png") {
		t.Fatalf("out = %q", out)
	}
	if !strings.Contains(lines[3], "gone.png: could not be fetched") {
		t.Fatalf("missing image line = %q", lines[3])
	}
	path := strings.TrimPrefix(lines[2], "[image: shot.png] ")
	if got, err := os.ReadFile(path); err != nil || string(got) != string(img) {
		t.Fatalf("file %s: %v", path, err)
	}
	// Fetched once: the next mention reuses the file.
	before := hits.Load()
	_ = r.resolveUploads(context.Background(), "[image: shot.png](forge-upload:AAAAAAAAAAAAAAAAAAAAAAAA)")
	if hits.Load() != before {
		t.Fatal("fetched the same image twice")
	}
	if plain := "no images here"; r.resolveUploads(context.Background(), plain) != plain {
		t.Fatal("plain text changed")
	}
}
