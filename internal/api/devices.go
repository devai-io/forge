package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/devai-io/forge/internal/auth"
	"github.com/devai-io/forge/internal/store"
)

// Trusted devices: a browser that passed two-factor once gets a long-lived
// forge_device cookie, and from then on signs in and confirms sensitive
// actions with the password alone. A new browser (or one unused for
// deviceIdle) is asked for the code again. Only the cookie's hash is stored.

const (
	deviceCookie = "forge_device"
	deviceIdle   = 180 * 24 * time.Hour
	// deviceMaxAge is the longest a browser keeps a cookie (Chrome caps 400 days).
	deviceMaxAge = 400 * 24 * time.Hour
)

func deviceHash(r *http.Request) []byte {
	c, err := r.Cookie(deviceCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	return hashToken(c.Value)
}

// deviceTrusted: this browser passed two-factor for u before.
func (s *Server) deviceTrusted(r *http.Request, u *store.User) bool {
	h := deviceHash(r)
	if h == nil {
		return false
	}
	ok, err := s.store.TrustedDevice(r.Context(), u.ID, h, deviceIdle)
	if err != nil {
		slog.Error("trusted device", "err", err)
	}
	return ok
}

// trustDevice remembers this browser after a correct two-factor code.
func (s *Server) trustDevice(w http.ResponseWriter, r *http.Request, u *store.User) {
	if s.deviceTrusted(r, u) {
		return
	}
	token, hash := auth.Token("")
	if err := s.store.TrustDevice(r.Context(), u.ID, hash, r.UserAgent(), s.clientIP(r)); err != nil {
		slog.Error("trust device", "err", err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: deviceCookie, Value: token, Path: "/api/auth",
		MaxAge: int(deviceMaxAge.Seconds()), HttpOnly: true,
		Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteStrictMode,
	})
}

// forgetDevices drops every trusted device but, when keepThis, this one.
func (s *Server) forgetDevices(r *http.Request, userID int64, keepThis bool) int {
	var keep []byte
	if keepThis {
		keep = deviceHash(r)
	}
	n, err := s.store.ForgetDevices(r.Context(), userID, keep)
	if err != nil {
		slog.Error("forget devices", "err", err)
	}
	return n
}

// forgetTrustedDevices: Settings → Security, "Ask for the code again
// everywhere" — this browser included.
func (s *Server) forgetTrustedDevices(w http.ResponseWriter, r *http.Request, u *store.User) {
	n := s.forgetDevices(r, u.ID, false)
	s.sec(r, "devices_forgotten", "")
	writeJSON(w, http.StatusOK, map[string]int{"forgotten": n})
}
