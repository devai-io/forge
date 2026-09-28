package api

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/store"
)

// Project files: each project has a folder in the workspace,
// <FORGE_HOME>/projects/<KEY>/, holding whatever the owner keeps with it
// (specs, exports, screenshots). Flat — no sub-folders — so a name is all
// there is to validate.

const maxProjectFile = 100 << 20

type projectFile struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

// projectDir resolves the project (404 if there is none) and its folder.
func (s *Server) projectDir(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.ToUpper(r.PathValue("key"))
	if _, err := s.store.ProjectIDByKey(r.Context(), key); err != nil {
		writeErr(w, r, err)
		return "", false
	}
	return filepath.Join(s.cfg.Home, "projects", key), true
}

// cleanFileName accepts a plain file name: no path, no leading dot, no
// control characters.
func cleanFileName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return "", false
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f {
			return "", false
		}
	}
	return name, true
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request, _ *store.User) {
	dir, ok := s.projectDir(w, r)
	if !ok {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		writeErr(w, r, err)
		return
	}
	files := []projectFile{}
	for _, e := range entries {
		if !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, projectFile{Name: e.Name(), Size: info.Size(), ModifiedAt: info.ModTime().UTC()})
	}
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request, _ *store.User) {
	dir, ok := s.projectDir(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxProjectFile+1<<20)
	file, header, err := r.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "files are at most 100 MB")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", "send one file in the multipart field \"file\"")
		return
	}
	defer file.Close()
	name, ok := cleanFileName(filepath.Base(header.Filename))
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "validation", "that file name cannot be stored")
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, r, err)
		return
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(file, maxProjectFile+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if n > maxProjectFile {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "files are at most 100 MB")
		return
	}
	dst := filepath.Join(dir, name)
	if err := os.Rename(tmp.Name(), dst); err != nil {
		writeErr(w, r, err)
		return
	}
	info, err := os.Stat(dst)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"file": projectFile{Name: name, Size: info.Size(), ModifiedAt: info.ModTime().UTC()}})
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request, _ *store.User) {
	dir, ok := s.projectDir(w, r)
	if !ok {
		return
	}
	name, ok := cleanFileName(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	// Always a download, never rendered: an uploaded .html or .svg must not
	// run as this origin.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request, _ *store.User) {
	dir, ok := s.projectDir(w, r)
	if !ok {
		return
	}
	name, ok := cleanFileName(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// retireProjectFiles moves a deleted project's folder aside rather than
// deleting it: files are the owner's, and a new project may reuse the key.
func (s *Server) retireProjectFiles(key string) {
	dir := filepath.Join(s.cfg.Home, "projects", key)
	if _, err := os.Stat(dir); err != nil {
		return
	}
	trash := filepath.Join(s.cfg.Home, "projects", ".deleted")
	if err := os.MkdirAll(trash, 0o700); err == nil {
		err = os.Rename(dir, filepath.Join(trash, fmt.Sprintf("%s-%s", key, time.Now().UTC().Format("20060102T150405"))))
		if err != nil {
			slog.Error("retire project files", "project", key, "err", err)
		}
	}
}
