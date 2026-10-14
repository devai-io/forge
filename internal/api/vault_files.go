package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/store"
)

// Repo files: the git-ignored files (.env, keys, keystores) a fresh clone of
// a project's repos needs, kept in the vault and handed to a machine as one
// tar.gz — downloaded in the browser, or fetched with a one-time setup code
// (`curl … | tar -xz`) on a machine with no session. Machines add them with
// `forge agent secrets push` (runnerVaultFiles: write-only).

const setupCodeTTL = 10 * time.Minute

type setupCode struct {
	projectKey string
	repoID     int64
	expires    time.Time
}

// setupCodes holds live codes by their SHA-256, so a lookup never compares
// the secret itself. In memory: a restart simply voids them.
type setupCodes struct {
	mu    sync.Mutex
	codes map[string]setupCode
	fails *limiter // wrong codes, per address
}

func newSetupCodes() *setupCodes {
	return &setupCodes{codes: map[string]setupCode{}, fails: newLimiter(10, 60, 15*time.Minute)}
}

func codeHash(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

func (c *setupCodes) issue(projectKey string, repoID int64) (string, time.Time) {
	code := randomID() + randomID() // 32 random bytes
	exp := time.Now().Add(setupCodeTTL)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep()
	c.codes[codeHash(code)] = setupCode{projectKey: projectKey, repoID: repoID, expires: exp}
	return code, exp
}

// take consumes a code: it works once.
func (c *setupCodes) take(code string) (setupCode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep()
	h := codeHash(code)
	sc, ok := c.codes[h]
	delete(c.codes, h)
	return sc, ok
}

func (c *setupCodes) sweep() {
	now := time.Now()
	for h, sc := range c.codes {
		if now.After(sc.expires) {
			delete(c.codes, h)
		}
	}
}

// bundleRepoID reads an optional repo filter (?repo= or a body's repo_id).
func bundleRepoID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.URL.Query().Get("repo"), 10, 64)
	return id
}

func (s *Server) vaultBundle(w http.ResponseWriter, r *http.Request, u *store.User) {
	key := strings.ToUpper(r.PathValue("key"))
	s.writeBundle(w, r, key, bundleRepoID(r), "vault_bundle")
}

func (s *Server) vaultSetupCode(w http.ResponseWriter, r *http.Request, u *store.User) {
	key := strings.ToUpper(r.PathValue("key"))
	var in struct {
		RepoID *int64 `json:"repo_id"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	pid, err := s.store.ProjectIDByKey(r.Context(), key)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var repoID int64
	if in.RepoID != nil {
		repo, err := s.store.RepoByID(r.Context(), *in.RepoID)
		if err != nil || repo.ProjectID != pid {
			writeErr(w, r, &store.ValidationError{Field: "repo_id", Message: "must be a repo of this project"})
			return
		}
		repoID = repo.ID
	}
	code, exp := s.vaultSetups.issue(key, repoID)
	base := strings.TrimRight(s.cfg.PublicURL, "/")
	if base == "" {
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		base = scheme + "://" + r.Host
	}
	url := base + "/api/vault-setup/" + code
	s.sec(r, "vault_setup_code", key)
	writeJSON(w, http.StatusCreated, map[string]any{
		"code": code, "url": url, "expires_at": exp.UTC(),
		"command": "curl -fsSL '" + url + "' | tar -xzvf -",
	})
}

// vaultSetup answers a one-time setup code with the bundle. No session: the
// code is the credential, and it works once.
func (s *Server) vaultSetup(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.vaultSetups.fails.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts — try again in a few minutes")
		return
	}
	sc, ok := s.vaultSetups.take(r.PathValue("code"))
	if !ok {
		s.vaultSetups.fails.Record(ip)
		slog.Warn("vault setup code refused", "ip", ip)
		writeError(w, http.StatusNotFound, "bad_code", "unknown, used or expired setup code — make a new one in the vault")
		return
	}
	s.writeBundle(w, r, sc.projectKey, sc.repoID, "vault_setup_used")
}

func (s *Server) writeBundle(w http.ResponseWriter, r *http.Request, key string, repoID int64, event string) {
	files, err := s.store.RepoFiles(r.Context(), s.box, key, repoID, s.clientIP(r))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	data, written, err := bundleTarGz(key, files, time.Now())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("vault bundle", "project", key, "files", written, "ip", s.clientIP(r))
	s.sec(r, event, fmt.Sprintf("%s: %d file(s)", key, written))
	name := strings.ToLower(key) + "-secrets.tar.gz"
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// bundleTarGz lays the files out as <repo dir>/<path> (mode 0600) plus a
// FORGE-SECRETS.txt manifest. No directory entries: tar creates the missing
// ones and leaves existing checkouts' permissions alone.
func bundleTarGz(key string, files []store.RepoFile, now time.Time) ([]byte, int, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	var manifest strings.Builder
	fmt.Fprintf(&manifest, "Forge vault — repo files of %s, %s\n", key, now.UTC().Format(time.RFC3339))
	manifest.WriteString("Extract in the folder that holds the project's repo checkouts.\n\n")
	written := 0
	for _, f := range files {
		name := f.RepoDir + "/" + f.Path
		if f.Content == nil {
			fmt.Fprintf(&manifest, "skipped  %s  (%s: %s)\n", name, f.Name, f.Skipped)
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(f.Content)), ModTime: now, Typeflag: tar.TypeReg}); err != nil {
			return nil, 0, err
		}
		if _, err := tw.Write(f.Content); err != nil {
			return nil, 0, err
		}
		fmt.Fprintf(&manifest, "wrote    %s\n", name)
		written++
	}
	if len(files) == 0 {
		manifest.WriteString("No repo files in the vault for this project yet.\n")
	}
	m := []byte(manifest.String())
	if err := tw.WriteHeader(&tar.Header{Name: "FORGE-SECRETS.txt", Mode: 0o600, Size: int64(len(m)), ModTime: now, Typeflag: tar.TypeReg}); err != nil {
		return nil, 0, err
	}
	if _, err := tw.Write(m); err != nil {
		return nil, 0, err
	}
	if err := tw.Close(); err != nil {
		return nil, 0, err
	}
	if err := gz.Close(); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), written, nil
}

// runnerVaultFiles stores files a machine collected in a repo checkout
// (`forge agent secrets push`). Write-only: the answer says what happened to
// each path, never what is stored.
func (s *Server) runnerVaultFiles(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	var in struct {
		Cwd       string                `json:"cwd"`
		RemoteURL string                `json:"remote_url"`
		Files     []store.RepoFileInput `json:"files"`
		DryRun    bool                  `json:"dry_run"`
	}
	if !decode16(w, r, &in) {
		return
	}
	repo, err := s.store.RepoForCheckout(r.Context(), in.Cwd, in.RemoteURL)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	actor := "machine " + rn.Name
	results, err := s.store.PushRepoFiles(r.Context(), s.box, repo, in.Files, in.DryRun, actor)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if !in.DryRun {
		changed := 0
		for _, res := range results {
			if res.Action != "same" {
				changed++
			}
		}
		if changed > 0 {
			_ = s.store.LogSecurity(r.Context(), "vault_push", fmt.Sprintf("%s pushed %d file(s) to %s/%s", rn.Name, changed, repo.ProjectKey, repo.Name),
				s.clientIP(r), r.UserAgent(), 0)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"project_key": repo.ProjectKey, "repo": repo.Name, "repo_id": repo.ID, "files": results})
}
