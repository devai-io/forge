package api

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/devai-io/forge/internal/store"
)

// Images for agents: the browser uploads one (drag and drop, paste, the
// paperclip) and puts its marker, [image: NAME](forge-upload:TOKEN), in the
// prompt or message. The machine running the agent fetches the bytes by the
// token (GET /api/runner/uploads/{token}), saves them and hands Claude the
// local file instead of the marker.

const maxUpload = 10 << 20

// uploadTypes are the images Claude reads, by sniffed content type.
var uploadTypes = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

func (s *Server) createUpload(w http.ResponseWriter, r *http.Request, u *store.User) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<20)
	file, header, err := r.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "images are at most 10 MB")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", "send one image in the multipart field \"file\"")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if len(data) > maxUpload {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "images are at most 10 MB")
		return
	}
	ct := http.DetectContentType(data)
	ext, ok := uploadTypes[ct]
	if !ok {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_type", "only PNG, JPEG, GIF or WebP images")
		return
	}
	up, err := s.store.CreateUpload(r.Context(), realUser(r).ID, uploadName(header.Filename, ext), ct, data)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, up)
}

// uploadName keeps a file name that cannot break the marker around it.
func uploadName(name, ext string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '[' || r == ']' || r == '(' || r == ')':
			return '_'
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if len(name) > 80 {
		name = name[len(name)-80:]
	}
	if name == "" || name == "." || name == "/" {
		name = "image" + ext
	}
	return name
}

// getUpload shows an attached image back to the person who attached it (and
// to owners): thumbnails in the conversation.
func (s *Server) getUpload(w http.ResponseWriter, r *http.Request, u *store.User) {
	up, err := s.store.UploadByToken(r.Context(), r.PathValue("token"))
	if err != nil || (up.UserID != realUser(r).ID && realUser(r).Role != store.RoleOwner) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	serveUpload(w, up)
}

func (s *Server) runnerUpload(w http.ResponseWriter, r *http.Request, _ *store.Runner) {
	up, err := s.store.UploadByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	serveUpload(w, up)
}

func serveUpload(w http.ResponseWriter, up *store.Upload) {
	h := w.Header()
	h.Set("Content-Type", up.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(up.Data)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'")
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("X-Upload-Name", up.Name)
	_, _ = w.Write(up.Data)
}
