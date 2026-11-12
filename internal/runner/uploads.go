package runner

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Images attached in Forge travel in a prompt or message as a marker,
// [image: NAME](forge-upload:TOKEN). Before the text reaches Claude Code (or
// Codex) — a run's prompt, a follow-up, a paste into a tmux session — the
// agent fetches each image, keeps it in its cache folder and puts the local
// path where the marker was: Claude reads the file (an image, to its Read
// tool) like one dropped into its terminal.

var uploadMarker = regexp.MustCompile(`\[image: ([^\]\n]{0,200})\]\(forge-upload:([A-Za-z0-9_-]{16,64})\)`)

const uploadKeep = 30 * 24 * time.Hour

var uploadExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

// resolveUploads swaps every image marker in text for the image's local path.
func (r *Runner) resolveUploads(ctx context.Context, text string) string {
	if !strings.Contains(text, "forge-upload:") {
		return text
	}
	return uploadMarker.ReplaceAllStringFunc(text, func(m string) string {
		sub := uploadMarker.FindStringSubmatch(m)
		name, token := sub[1], sub[2]
		path, err := r.fetchUpload(ctx, token)
		if err != nil {
			return fmt.Sprintf("[image %s: could not be fetched from Forge (%v)]", name, err)
		}
		return fmt.Sprintf("[image: %s] %s", name, path)
	})
}

func uploadsDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "forge", "uploads")
	return dir, os.MkdirAll(dir, 0o700)
}

// fetchUpload downloads an image once (later markers for it reuse the file)
// and returns where it is.
func (r *Runner) fetchUpload(ctx context.Context, token string) (string, error) {
	dir, err := uploadsDir()
	if err != nil {
		return "", err
	}
	if found, _ := filepath.Glob(filepath.Join(dir, token+".*")); len(found) > 0 {
		return found[0], nil
	}
	pruneUploads(dir)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.client.base+"/api/runner/uploads/"+token, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+r.client.token)
	req.Header.Set("User-Agent", "forge-agent/"+Version)
	resp, err := r.client.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	ext, ok := uploadExt[strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])]
	if !ok {
		return "", fmt.Errorf("not an image (%s)", resp.Header.Get("Content-Type"))
	}
	tmp, err := os.CreateTemp(dir, ".fetch-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 16<<20)); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(dir, token+ext)
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

// pruneUploads forgets images fetched more than uploadKeep ago.
func pruneUploads(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > uploadKeep {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
