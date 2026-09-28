package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/auth"
	"github.com/devai-io/forge/internal/store"
)

func hashToken(t string) []byte { return auth.HashToken(t) }

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	ip := s.clientIP(r)
	if !s.logins.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts — try again in a few minutes")
		return
	}
	u, err := s.store.UserByLogin(r.Context(), in.Username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeErr(w, r, err)
		return
	}
	if u == nil {
		auth.BurnPasswordCheck(in.Password)
	}
	if u == nil || !auth.CheckPassword(u.PasswordHash, in.Password) {
		s.logins.Record(ip)
		slog.Warn("login failed", "ip", ip, "login", in.Username)
		s.sec(r, "login_failed", "wrong password for "+truncate(in.Username, 60))
		writeError(w, http.StatusUnauthorized, "unauthorized", "wrong username or password")
		return
	}
	if u.TOTPEnabled {
		if strings.TrimSpace(in.Code) == "" {
			// The password was right; say what is missing. Not counted as a
			// failure, or the first half of every 2FA login would be one.
			writeError(w, http.StatusUnauthorized, "totp_required", "enter the 6-digit code from your authenticator")
			return
		}
		if !s.checkTOTP(r, u, in.Code) {
			s.logins.Record(ip)
			slog.Warn("login failed: bad TOTP code", "ip", ip, "login", in.Username)
			s.sec(r, "login_failed", "wrong two-factor code")
			writeError(w, http.StatusUnauthorized, "unauthorized", "wrong code")
			return
		}
	}
	s.logins.Reset(ip)

	token, hash := auth.Token("")
	sid, err := s.store.CreateSession(r.Context(), u.ID, hash, r.UserAgent(), ip, sessionTTL)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.setCookie(w, token)
	slog.Info("login", "user", u.Username, "ip", ip)
	known, err := s.store.SeenDevice(r.Context(), ip, r.UserAgent())
	if err != nil {
		slog.Error("known devices", "err", err)
	}
	kind := "login"
	if !known && err == nil {
		kind = "login_new_device"
	}
	_ = s.store.LogSecurity(r.Context(), kind, "", ip, r.UserAgent(), sid)
	if kind == "login_new_device" {
		s.notifyNewDevice(u, ip, r.UserAgent())
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "elevated_until": nil})
}

// notifyNewDevice e-mails the account about a sign-in from an address and
// browser Forge has not seen. The first sign-ins after this shipped will all
// be "new"; after that, only a stranger's would be.
func (s *Server) notifyNewDevice(u *store.User, ip, ua string) {
	if !s.mailer.Enabled() {
		return
	}
	body := fmt.Sprintf(`Forge was just signed into from a device it had not seen before.

Address:  %s
Browser:  %s
When:     %s

If that was you, nothing to do. If not: change your password now
(%s/settings) — that signs every other session out — and check
Settings → Security for what happened.
`, ip, ua, time.Now().UTC().Format(time.RFC1123), s.cfg.PublicURL)
	go func() {
		if err := s.mailer.Send(u.Email, "New sign-in to Forge from "+ip, body); err != nil {
			slog.Error("new-device mail", "err", err)
		}
	}()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// checkTOTP verifies a code against the user's enabled secret and burns its
// time step, so an observed code cannot be replayed.
func (s *Server) checkTOTP(r *http.Request, u *store.User, code string) bool {
	if len(u.TOTPSecret) == 0 {
		return false
	}
	secret, err := s.box.Open(u.TOTPSecret, totpAAD(u.ID))
	if err != nil {
		slog.Error("open TOTP secret", "err", err)
		return false
	}
	counter, ok := auth.VerifyTOTP(string(secret), code, time.Now(), u.TOTPLastCounter)
	if !ok {
		return false
	}
	used, err := s.store.UseTOTPCounter(r.Context(), u.ID, counter)
	return err == nil && used
}

func totpAAD(userID int64) []byte { return []byte(fmt.Sprintf("forge-totp:%d", userID)) }

const elevationTTL = 10 * time.Minute

// elevate re-confirms the password (and code) and opens a 10-minute window
// for sensitive actions on this session only.
func (s *Server) elevate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	ip := s.clientIP(r)
	if !s.logins.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts — try again in a few minutes")
		return
	}
	if !auth.CheckPassword(u.PasswordHash, in.Password) {
		s.logins.Record(ip)
		s.sec(r, "elevate_failed", "wrong password")
		writeErr(w, r, &store.ValidationError{Field: "password", Message: "is not your password"})
		return
	}
	if u.TOTPEnabled && !s.checkTOTP(r, u, in.Code) {
		s.logins.Record(ip)
		s.sec(r, "elevate_failed", "wrong two-factor code")
		writeErr(w, r, &store.ValidationError{Field: "code", Message: "wrong or reused code"})
		return
	}
	until, err := s.store.ElevateSession(r.Context(), sessionID(r), elevationTTL)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("session elevated", "user", u.Username, "ip", ip)
	s.sec(r, "elevate", "")
	writeJSON(w, http.StatusOK, map[string]any{"elevated_until": until})
}

func (s *Server) totpSetup(w http.ResponseWriter, r *http.Request, u *store.User) {
	if !sessionInfo(r).Elevated() {
		writeError(w, http.StatusForbidden, "elevation_required", "confirm your password to continue")
		return
	}
	if u.TOTPEnabled {
		writeError(w, http.StatusConflict, "conflict", "two-factor is already on — disable it first to re-enrol")
		return
	}
	secret := auth.NewTOTPSecret()
	sealed, err := s.box.Seal([]byte(secret), totpAAD(u.ID))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.store.SetTOTPPending(r.Context(), u.ID, sealed); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauth_url": auth.TOTPURL(secret, u.Username, "Forge")})
}

func (s *Server) totpEnable(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(u.TOTPPending) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "start the setup first")
		return
	}
	if !s.logins.Allowed(s.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts — try again in a few minutes")
		return
	}
	secret, err := s.box.Open(u.TOTPPending, totpAAD(u.ID))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	counter, ok := auth.VerifyTOTP(string(secret), in.Code, time.Now(), 0)
	if !ok {
		s.logins.Record(s.clientIP(r))
		writeErr(w, r, &store.ValidationError{Field: "code", Message: "wrong code — check the phone's clock"})
		return
	}
	if err := s.store.EnableTOTP(r.Context(), u.ID, counter); err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("two-factor enabled", "user", u.Username)
	s.sec(r, "totp_enabled", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	ip := s.clientIP(r)
	if !s.logins.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts — try again in a few minutes")
		return
	}
	if !auth.CheckPassword(u.PasswordHash, in.Password) || !s.checkTOTP(r, u, in.Code) {
		s.logins.Record(ip)
		writeErr(w, r, &store.ValidationError{Field: "code", Message: "wrong password or code"})
		return
	}
	if err := s.store.DisableTOTP(r.Context(), u.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Warn("two-factor disabled", "user", u.Username, "ip", ip)
	s.sec(r, "totp_disabled", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if _, info, err := s.store.SessionUser(r.Context(), hashToken(c.Value), sessionTTL); err == nil {
			s.codes.dropSession(info.ID)
			_ = s.store.LogSecurity(r.Context(), "logout", "", s.clientIP(r), r.UserAgent(), info.ID)
		}
		if err := s.store.DeleteSessionByToken(r.Context(), hashToken(c.Value)); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	s.clearCookie(w)
	http.SetCookie(w, &http.Cookie{Name: codeCookie, Value: "", Path: "/code", MaxAge: -1, HttpOnly: true,
		Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, u *store.User) {
	var until *time.Time
	if info := sessionInfo(r); info.Elevated() {
		until = info.ElevatedUntil
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "elevated_until": until})
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request, u *store.User) {
	p, ok := decodePatch(w, r)
	if !ok {
		return
	}
	var in struct {
		Email    *string `json:"email"`
		Timezone *string `json:"timezone"`
	}
	_ = decodeInto(p, &in)
	if in.Email != nil {
		if a, err := mail.ParseAddress(*in.Email); err != nil || a.Address != strings.TrimSpace(*in.Email) {
			writeErr(w, r, &store.ValidationError{Field: "email", Message: "must be a plain e-mail address"})
			return
		}
	}
	if in.Timezone != nil {
		if _, err := time.LoadLocation(*in.Timezone); err != nil || *in.Timezone == "" || *in.Timezone == "Local" {
			writeErr(w, r, &store.ValidationError{Field: "timezone", Message: "unknown time zone"})
			return
		}
	}
	nu, err := s.store.UpdateUser(r.Context(), u.ID, p)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": nu})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	ip := s.clientIP(r)
	if !s.logins.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts — try again in a few minutes")
		return
	}
	if !auth.CheckPassword(u.PasswordHash, in.Current) {
		s.logins.Record(ip)
		writeErr(w, r, &store.ValidationError{Field: "current_password", Message: "is not your current password"})
		return
	}
	if err := auth.ValidatePassword(in.New); err != nil {
		writeErr(w, r, &store.ValidationError{Field: "new_password", Message: err.Error()})
		return
	}
	hash, err := auth.HashPassword(in.New)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.store.SetPassword(r.Context(), u.ID, hash, sessionID(r)); err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("password changed", "user", u.Username)
	s.sec(r, "password_changed", "other sessions signed out")
	w.WriteHeader(http.StatusNoContent)
}

// forgot always answers 204, whether or not the account exists and whether
// or not mail could be sent: the response must not be an account oracle.
func (s *Server) forgot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Login string `json:"login"`
	}
	if !decode(w, r, &in) {
		return
	}
	ip := s.clientIP(r)
	if !s.resets.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many reset requests — try again later")
		return
	}
	s.resets.Record(ip)
	w.WriteHeader(http.StatusNoContent)

	u, err := s.store.UserByLogin(r.Context(), in.Login)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("forgot: lookup", "err", err)
		}
		return
	}
	if !s.mailer.Enabled() {
		slog.Warn("password reset requested but SMTP is not configured; reset it with `forge_api reset-password " +
			u.Username + "` instead")
		return
	}
	token, hash := auth.Token("")
	if err := s.store.CreatePasswordReset(r.Context(), u.ID, hash, time.Hour); err != nil {
		slog.Error("forgot: create token", "err", err)
		return
	}
	link := s.cfg.PublicURL + "/reset?token=" + token
	body := fmt.Sprintf(`Someone (hopefully you) asked to reset the Forge password for %s.

Open this link within an hour to choose a new one:

%s

If this was not you, ignore this e-mail — your password has not changed.
Request came from %s.
`, u.Username, link, ip)
	// Sent after the response so SMTP latency is not a timing oracle either.
	go func() {
		if err := s.mailer.Send(u.Email, "Reset your Forge password", body); err != nil {
			slog.Error("forgot: send mail", "err", err)
			return
		}
		slog.Info("password reset e-mail sent", "user", u.Username)
	}()
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
		New   string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := auth.ValidatePassword(in.New); err != nil {
		writeErr(w, r, &store.ValidationError{Field: "new_password", Message: err.Error()})
		return
	}
	hash, err := auth.HashPassword(in.New)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	userID, err := s.store.ConsumePasswordReset(r.Context(), hashToken(strings.TrimSpace(in.Token)))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "bad_request", "this reset link is invalid or has expired")
			return
		}
		writeErr(w, r, err)
		return
	}
	if err := s.store.SetPassword(r.Context(), userID, hash, 0); err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("password reset via e-mail link", "user_id", userID)
	_ = s.store.LogSecurity(r.Context(), "password_reset", "via e-mail link; all sessions signed out", s.clientIP(r), r.UserAgent(), 0)
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request, u *store.User) {
	sessions, err := s.store.ListSessions(r.Context(), u.ID, sessionID(r))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteSession(r.Context(), u.ID, id); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "session_revoked", fmt.Sprintf("session %d", id))
	if id == sessionID(r) {
		s.clearCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeOtherSessions(w http.ResponseWriter, r *http.Request, u *store.User) {
	n, err := s.store.RevokeOtherSessions(r.Context(), u.ID, sessionID(r))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "sessions_revoked_others", fmt.Sprintf("%d session(s)", n))
	writeJSON(w, http.StatusOK, map[string]int{"revoked": n})
}

func (s *Server) securityLog(w http.ResponseWriter, r *http.Request, u *store.User) {
	events, err := s.store.ListSecurity(r.Context(), r.URL.Query().Get("kind"), queryInt(r, "limit", 100))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
