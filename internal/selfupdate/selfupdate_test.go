package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func archive(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, f := range []struct {
		name string
		body []byte
	}{{"forge_x/README.md", []byte("readme")}, {name, body}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(f.body)
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

// release serves a fake GitHub: /releases/latest redirects to the tag, and
// the tag's download folder holds the archive and checksums.txt.
func release(t *testing.T, tag string, bin []byte, corrupt bool) *httptest.Server {
	t.Helper()
	name := ArchiveName(runtime.GOOS, runtime.GOARCH)
	arc := archive(t, strings.TrimSuffix(name, ".tar.gz")+"/forge", bin)
	sum := sha256.Sum256(arc)
	if corrupt {
		arc = append(arc, 0)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/"+Repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/"+Repo+"/releases/tag/"+tag, http.StatusFound)
	})
	dl := "/" + Repo + "/releases/download/" + tag + "/"
	mux.HandleFunc(dl+"checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  install.sh\n%s  %s\n", strings.Repeat("0", 64), hex.EncodeToString(sum[:]), name)
	})
	mux.HandleFunc(dl+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(arc) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestLatestFetchInstall(t *testing.T) {
	bin := []byte("#!/bin/sh\necho v9.9.9\n")
	srv := release(t, "v9.9.9", bin, false)
	src := Source{GitHub: srv.URL}
	ctx := context.Background()

	tag, err := src.Latest(ctx)
	if err != nil || tag != "v9.9.9" {
		t.Fatalf("latest = %q, %v", tag, err)
	}
	got, err := src.Fetch(ctx, tag)
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("fetch = %q, %v", got, err)
	}

	path := filepath.Join(t.TempDir(), "forge")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := Stamp(path)
	if err := Install(path, got); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, bin) {
		t.Fatalf("installed %q", b)
	}
	if b, _ := os.ReadFile(path + ".prev"); string(b) != "old" {
		t.Fatalf("prev %q", b)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", st.Mode())
	}
	if Stamp(path) == before {
		t.Fatal("stamp did not change after the swap")
	}
	// A mirror (FORGE_DOWNLOAD_BASE) names no version and serves the assets directly.
	mirror := Source{Base: srv.URL + "/" + Repo + "/releases/download/v9.9.9"}
	if tag, err := mirror.Latest(ctx); err != nil || tag != "" {
		t.Fatalf("mirror latest = %q, %v", tag, err)
	}
	if got, err := mirror.Fetch(ctx, ""); err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("mirror fetch = %q, %v", got, err)
	}
}

func TestFetchRejectsAModifiedArchive(t *testing.T) {
	srv := release(t, "v9.9.9", []byte("bin"), true)
	if _, err := (Source{GitHub: srv.URL}).Fetch(context.Background(), "v9.9.9"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchMissingRelease(t *testing.T) {
	srv := release(t, "v9.9.9", []byte("bin"), false)
	if _, err := (Source{GitHub: srv.URL}).Fetch(context.Background(), "v0.0.1"); err == nil {
		t.Fatal("a missing release should fail")
	}
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v0.1.35", "v0.1.35", 0},
		{"v0.1.35", "v0.1.36", -1},
		{"v0.1.100", "v0.1.35", 1},
		{"v0.2.0", "v0.1.99", 1},
		{"v0.1.35-3-gabc1234", "v0.1.35", 1},
		{"v0.1.35-dirty", "v0.1.36", -1},
		{"dev", "v0.1.35", -1},
		{"v0.1.35", "dev", 1},
		{"0.1.35", "v0.1.35", 0},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
