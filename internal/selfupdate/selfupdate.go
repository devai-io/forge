// Package selfupdate replaces the running forge binary with a release from
// GitHub (or a mirror): find the version, download the archive for this
// platform, check it against the release's checksums.txt, and swap it in
// atomically next to the old one, which is kept as forge.prev.
package selfupdate

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Repo is where releases come from.
const Repo = "devai-io/forge"

// maxArchive bounds a download; a release archive is ~15 MB.
const maxArchive = 200 << 20

// Source says where release assets live.
type Source struct {
	// Base, when set, is a mirror that holds the assets of the wanted
	// release (FORGE_DOWNLOAD_BASE, as install.sh reads it).
	Base string
	// GitHub is the github.com root (tests point it elsewhere).
	GitHub string
	Client *http.Client
}

// DefaultSource reads FORGE_DOWNLOAD_BASE like install.sh does.
func DefaultSource() Source {
	return Source{Base: strings.TrimRight(os.Getenv("FORGE_DOWNLOAD_BASE"), "/"), GitHub: "https://github.com",
		Client: &http.Client{Timeout: 5 * time.Minute}}
}

func (s Source) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

// Latest is the newest release tag, read from where github.com redirects
// /releases/latest (no API token, no rate limit). Empty with a mirror: the
// mirror holds one release and its version shows after download.
func (s Source) Latest(ctx context.Context) (string, error) {
	if s.Base != "" {
		return "", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.GitHub+"/"+Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	c := *s.client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("checking the latest release: %w", err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/")
	if resp.StatusCode/100 != 3 || i < 0 {
		return "", fmt.Errorf("checking the latest release: unexpected answer %s", resp.Status)
	}
	return loc[i+len("/tag/"):], nil
}

// assetBase is the URL the release's assets live under.
func (s Source) assetBase(version string) string {
	if s.Base != "" {
		return s.Base
	}
	return s.GitHub + "/" + Repo + "/releases/download/" + version
}

// ArchiveName is this platform's release archive.
func ArchiveName(goos, goarch string) string {
	return fmt.Sprintf("forge_%s_%s.tar.gz", goos, goarch)
}

func (s Source) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("download %s: larger than %d bytes", url, limit)
	}
	return b, nil
}

// Fetch downloads the release's binary for this platform and checks it
// against checksums.txt. It returns the binary's bytes.
func (s Source) Fetch(ctx context.Context, version string) ([]byte, error) {
	name := ArchiveName(runtime.GOOS, runtime.GOARCH)
	base := s.assetBase(version)
	sums, err := s.get(ctx, base+"/checksums.txt", 1<<20)
	if err != nil {
		return nil, err
	}
	want := checksumFor(sums, name)
	if want == "" {
		return nil, fmt.Errorf("%s is not in the release's checksums.txt", name)
	}
	archive, err := s.get(ctx, base+"/"+name, maxArchive)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("checksum mismatch for %s: the download is not the release", name)
	}
	return extractBinary(archive)
}

// checksumFor finds name's sha256 in a `sha256sum` listing.
func checksumFor(sums []byte, name string) string {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

// extractBinary pulls the `forge` executable out of a release archive
// (forge_<os>_<arch>/forge, or forge at the top).
func extractBinary(archive []byte) ([]byte, error) {
	zr, err := gzip.NewReader(strings.NewReader(string(archive)))
	if err != nil {
		return nil, fmt.Errorf("reading the archive: %w", err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no forge binary in the archive")
		}
		if err != nil {
			return nil, fmt.Errorf("reading the archive: %w", err)
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "forge" {
			return io.ReadAll(io.LimitReader(tr, maxArchive))
		}
	}
}

// Install writes bin over the executable at path: the new file is written
// next to it and renamed into place (a running process keeps its old copy),
// and the old one stays as <path>.prev for a manual rollback.
func Install(path string, bin []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".forge-update-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s (%w): rerun with the rights to that folder, or reinstall with install.sh", dir, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	prev := path + ".prev"
	_ = os.Remove(prev)
	if err := os.Link(path, prev); err != nil {
		// No hard links here (another filesystem, an odd mount): copy instead.
		if old, rerr := os.ReadFile(path); rerr == nil {
			_ = os.WriteFile(prev, old, 0o755)
		}
	}
	return os.Rename(tmp.Name(), path)
}

// Executable is the running binary's real path (symlinks resolved).
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

// Stamp identifies a file's contents cheaply: size and modification time.
// The agent compares it with the one it started from to notice an update.
func Stamp(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UnixNano())
}

// Compare orders two versions like v1.2.3: -1, 0 or 1 for a older than,
// equal to or newer than b. A suffix after the numbers (git describe's
// v1.2.3-4-gabc, -dirty, a test tag) means a build after that release.
// An unparsable version ("dev") is older than anything.
func Compare(a, b string) int {
	if a == b {
		return 0
	}
	na, sa, oka := parseVersion(a)
	nb, sb, okb := parseVersion(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := range na {
		if na[i] != nb[i] {
			if na[i] < nb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case sa == sb:
		return 0
	case sa == "":
		return -1
	case sb == "":
		return 1
	}
	return strings.Compare(sa, sb)
}

func parseVersion(v string) (nums [3]int, suffix string, ok bool) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v, suffix = v[:i], v[i:]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nums, "", false
	}
	for i, p := range parts {
		n := 0
		if p == "" {
			return nums, "", false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return nums, "", false
			}
			n = n*10 + int(c-'0')
		}
		nums[i] = n
	}
	return nums, suffix, true
}
