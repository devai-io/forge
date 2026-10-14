package runner

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// `forge agent secrets scan|push`: collect a checkout's git-ignored secret
// files (.env, keys, keystores) into the Forge vault, so another machine can
// get them back after a fresh clone (Vault → Set up a machine).

const maxSecretFile = 1 << 20

// skipDirs never hold the files worth keeping, and can be huge.
var skipDirs = map[string]bool{"node_modules": true, "vendor": true, ".venv": true, "venv": true, "build": true,
	"dist": true, ".dart_tool": true, ".gradle": true, "Pods": true, "target": true, ".next": true, ".git": true,
	"__pycache__": true, ".terraform": true}

// IsSecretCandidate says whether a repo-relative path looks like a secret
// file a fresh clone would miss.
func IsSecretCandidate(p string) bool {
	for _, seg := range strings.Split(path.Dir(p), "/") {
		if skipDirs[seg] {
			return false
		}
	}
	base := strings.ToLower(path.Base(p))
	ext := path.Ext(base)
	switch {
	case base == "local.properties":
		return false
	case base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env"):
		for _, s := range []string{".example", ".sample", ".template", ".dist", ".defaults"} {
			if strings.HasSuffix(base, s) {
				return false
			}
		}
		return true
	case ext == ".pem" || ext == ".key" || ext == ".p8" || ext == ".p12" || ext == ".pfx" || ext == ".jks" ||
		ext == ".keystore" || ext == ".mobileprovision" || ext == ".tfvars":
		return true
	case base == "key.properties" || base == "google-services.json" || base == "googleservice-info.plist":
		return true
	case ext == ".json" && (strings.HasPrefix(base, "service-account") || strings.HasPrefix(base, "service_account") ||
		strings.HasPrefix(base, "credentials")):
		return true
	case strings.HasPrefix(base, "secrets."):
		return true
	}
	return false
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// scanSecrets lists the checkout's ignored files that look like secrets,
// relative to its root.
func scanSecrets(root string) ([]string, error) {
	out, err := gitOut(root, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || !IsSecretCandidate(p) {
			continue
		}
		if st, err := os.Stat(filepath.Join(root, p)); err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > maxSecretFile {
			continue
		}
		files = append(files, p)
	}
	return files, nil
}

func repoRoot(dir string) (string, error) {
	out, err := gitOut(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a git checkout: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// SecretsScan prints what `secrets push` would collect here.
func SecretsScan(dir string, out io.Writer) error {
	root, err := repoRoot(dir)
	if err != nil {
		return err
	}
	files, err := scanSecrets(root)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintln(out, "No git-ignored secret files found in", root)
		return nil
	}
	fmt.Fprintf(out, "Git-ignored secret files in %s:\n", root)
	for _, f := range files {
		fmt.Fprintln(out, "  "+f)
	}
	fmt.Fprintln(out, "\nforge agent secrets push   stores them in the Forge vault")
	return nil
}

type pushFile struct {
	Path          string `json:"path"`
	ContentBase64 string `json:"content_base64"`
}

type pushResult struct {
	ProjectKey string `json:"project_key"`
	Repo       string `json:"repo"`
	Files      []struct {
		Path   string `json:"path"`
		Action string `json:"action"`
	} `json:"files"`
}

// SecretsPush sends the checkout's secret files (or the given ones) to the
// vault: a dry run first to show what would change, then, confirmed, the
// upload.
func SecretsPush(cfg *Config, dir string, paths []string, yes bool, in io.Reader, out io.Writer) error {
	root, err := repoRoot(dir)
	if err != nil {
		return err
	}
	var rel []string
	if len(paths) == 0 {
		if rel, err = scanSecrets(root); err != nil {
			return err
		}
	} else {
		for _, p := range paths {
			abs := filepath.Clean(p)
			if !filepath.IsAbs(p) {
				abs = filepath.Join(dir, p)
			}
			r, err := filepath.Rel(root, abs)
			if err != nil || r == ".." || strings.HasPrefix(r, "../") {
				return fmt.Errorf("%s is outside the checkout %s", p, root)
			}
			rel = append(rel, filepath.ToSlash(r))
		}
	}
	if len(rel) == 0 {
		fmt.Fprintln(out, "Nothing to push: no git-ignored secret files found (name files to push them anyway).")
		return nil
	}
	var files []pushFile
	for _, p := range rel {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return err
		}
		if len(data) == 0 || len(data) > maxSecretFile {
			return fmt.Errorf("%s: must be 1 byte to 1 MB", p)
		}
		files = append(files, pushFile{Path: p, ContentBase64: base64.StdEncoding.EncodeToString(data)})
	}
	remote, _ := gitOut(root, "remote", "get-url", "origin")
	c := newClient(cfg.APIURL, cfg.Token)
	send := func(dry bool) (pushResult, error) {
		var all pushResult
		for _, batch := range batchFiles(files, 8<<20) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			var res pushResult
			_, err := c.post(ctx, "/api/runner/vault/files", map[string]any{"cwd": root, "remote_url": strings.TrimSpace(remote),
				"files": batch, "dry_run": dry}, &res)
			cancel()
			if err != nil {
				return all, err
			}
			all.ProjectKey, all.Repo = res.ProjectKey, res.Repo
			all.Files = append(all.Files, res.Files...)
		}
		return all, nil
	}
	plan, err := send(true)
	if err != nil {
		return err
	}
	changes := 0
	fmt.Fprintf(out, "%s · %s\n", plan.ProjectKey, plan.Repo)
	for _, f := range plan.Files {
		fmt.Fprintf(out, "  %-7s %s\n", f.Action, f.Path)
		if f.Action != "same" {
			changes++
		}
	}
	if changes == 0 {
		fmt.Fprintln(out, "The vault already has all of them.")
		return nil
	}
	if !yes {
		fmt.Fprintf(out, "Store %d file(s) in the vault? [y/N] ", changes)
		line, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "Nothing stored.")
			return nil
		}
	}
	if _, err := send(false); err != nil {
		return err
	}
	fmt.Fprintf(out, "Stored %d file(s). Get them on another machine: Vault → %s → Set up a machine.\n", changes, plan.ProjectKey)
	return nil
}

// batchFiles keeps each request under the server's body cap.
func batchFiles(files []pushFile, limit int) [][]pushFile {
	var out [][]pushFile
	var cur []pushFile
	size := 0
	for _, f := range files {
		n := len(f.ContentBase64) + len(f.Path)
		if len(cur) > 0 && (size+n > limit || len(cur) >= 100) {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, f)
		size += n
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}
