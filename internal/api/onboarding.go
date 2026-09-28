package api

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/auth"
	"github.com/devai-io/forge/internal/seed"
	"github.com/devai-io/forge/internal/store"
)

// First-run setup and machine pairing: the two moments where someone who
// has no session yet is let in, each behind a one-time secret.

// ── Setup ─────────────────────────────────────────────────────────────────

// SetupTokenFile is where the first-run token lives while there is no
// account: the server prints it, `forge setup-token` reads it back, and
// completing setup deletes it.
func SetupTokenFile(home string) string { return filepath.Join(home, "setup-token") }

// EnsureSetupToken returns the current first-run token, creating one if
// there is none yet.
func EnsureSetupToken(home string) (string, error) {
	path := SetupTokenFile(home)
	if raw, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(raw))) >= 16 {
		return strings.TrimSpace(string(raw)), nil
	}
	token, _ := auth.Token("")
	token = token[:24]
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CountUsers(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needed": n == 0})
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{1,31}$`)

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token       string `json:"token"`
		Username    string `json:"username"`
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		Timezone    string `json:"timezone"`
		DemoData    bool   `json:"demo_data"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	ip := s.clientIP(r)
	if !s.logins.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts — try again in a few minutes")
		return
	}
	if n, err := s.store.CountUsers(ctx); err != nil {
		writeErr(w, r, err)
		return
	} else if n > 0 {
		writeError(w, http.StatusConflict, "setup_done", "this Forge already has an account — sign in")
		return
	}
	want, _ := os.ReadFile(SetupTokenFile(s.cfg.Home))
	got := strings.TrimSpace(in.Token)
	if len(want) == 0 || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(string(want))), []byte(got)) != 1 {
		s.logins.Record(ip)
		slog.Warn("setup: wrong token", "ip", ip)
		writeError(w, http.StatusForbidden, "bad_setup_token", "wrong setup token — see the server log or run `forge setup-token`")
		return
	}

	in.Username = strings.TrimSpace(in.Username)
	in.Email = strings.TrimSpace(in.Email)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	invalid := func(field, msg string) {
		writeErr(w, r, &store.ValidationError{Field: field, Message: msg})
	}
	if !usernameRe.MatchString(in.Username) {
		invalid("username", "2-32 letters, digits, dots, dashes or underscores")
		return
	}
	if a, err := mail.ParseAddress(in.Email); err != nil || a.Address != in.Email || len(in.Email) > 254 {
		invalid("email", "must be an e-mail address")
		return
	}
	if err := auth.ValidatePassword(in.Password); err != nil {
		invalid("password", err.Error())
		return
	}
	if in.Timezone == "" {
		in.Timezone = s.cfg.DefaultTimezone
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "Local" {
		invalid("timezone", "unknown time zone")
		return
	}
	if in.DisplayName == "" {
		in.DisplayName = in.Username
	}
	if len(in.DisplayName) > 100 {
		invalid("display_name", "at most 100 characters")
		return
	}

	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	u, err := s.store.CreateFirstUser(ctx, in.Username, in.Email, in.DisplayName, hash, in.Timezone)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "setup_done", "this Forge already has an account — sign in")
		return
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	_ = os.Remove(SetupTokenFile(s.cfg.Home))
	if in.DemoData {
		if _, err := s.store.SeedIfEmpty(ctx, seed.Demo); err != nil {
			slog.Error("setup: demo data", "err", err)
		}
	}

	token, tokenHash := auth.Token("")
	sid, err := s.store.CreateSession(ctx, u.ID, tokenHash, r.UserAgent(), ip, sessionTTL)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.setCookie(w, token)
	_, _ = s.store.SeenDevice(ctx, ip, r.UserAgent())
	_ = s.store.LogSecurity(ctx, "setup", "created the account "+u.Username, ip, r.UserAgent(), sid)
	slog.Info("setup complete", "user", u.Username, "ip", ip)
	writeJSON(w, http.StatusCreated, map[string]any{"user": u, "elevated_until": nil})
}

// ── Pairing ───────────────────────────────────────────────────────────────

// pairTTL is how long a pairing code works: long enough to install Forge on
// the machine, short enough that a code in a screenshot is soon worthless.
const pairTTL = 15 * time.Minute

func (s *Server) issuePairCode(r *http.Request, id int64) (map[string]any, error) {
	code := auth.PairCode()
	until, err := s.store.SetPairCode(r.Context(), id, auth.HashToken(auth.NormalizePairCode(code)), pairTTL)
	if err != nil {
		return nil, err
	}
	return map[string]any{"code": code, "expires_at": until}, nil
}

// createRunner registers a machine and hands back a pairing code (not a
// token): the machine gets its token by running `forge agent pair`.
func (s *Server) createRunner(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Role != "" && !store.OneOf(in.Role, store.RunnerRoles) {
		writeErr(w, r, &store.ValidationError{Field: "role", Message: "must be master, ios or worker"})
		return
	}
	_, unused := auth.Token("frg_") // replaced when the machine pairs
	rn, err := s.store.CreateRunner(r.Context(), in.Name, unused)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if in.Role != "" && in.Role != rn.Role {
		if rn, err = s.store.SetRunnerRole(r.Context(), rn.ID, in.Role); err != nil {
			_ = s.store.DeleteRunner(r.Context(), rn.ID)
			writeErr(w, r, err)
			return
		}
	}
	pairing, err := s.issuePairCode(r, rn.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if rn, err = s.store.RunnerByID(r.Context(), rn.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "runner_created", rn.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"runner": rn, "pairing": pairing})
}

// pairRunner issues a fresh code for an existing machine (reinstalled, or
// the first code expired). Its current token keeps working until the code
// is used.
func (s *Server) pairRunner(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	pairing, err := s.issuePairCode(r, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	rn, err := s.store.RunnerByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "runner_pair_code", rn.Name)
	writeJSON(w, http.StatusOK, map[string]any{"runner": rn, "pairing": pairing})
}

// runnerPair is the machine's side: a live code buys the runner token. No
// session and no CSRF header (it is not a browser); the code is the
// credential, so failures are rate-limited like passwords.
func (s *Server) runnerPair(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code     string `json:"code"`
		Hostname string `json:"hostname"`
		OS       string `json:"os"`
		Version  string `json:"version"`
	}
	if !decode(w, r, &in) {
		return
	}
	ip := s.clientIP(r)
	if !s.pairs.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts — try again in a few minutes")
		return
	}
	code := auth.NormalizePairCode(in.Code)
	token, tokenHash := auth.Token("frg_")
	var rn *store.Runner
	var err error
	if len(code) == 8 {
		rn, err = s.store.ClaimPairCode(r.Context(), auth.HashToken(code), tokenHash, in.Hostname, in.OS)
	} else {
		err = store.ErrNotFound
	}
	if errors.Is(err, store.ErrNotFound) {
		s.pairs.Record(ip)
		slog.Warn("pairing failed", "ip", ip)
		writeError(w, http.StatusNotFound, "bad_code", "unknown or expired pairing code — make a new one on the Agents page")
		return
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	_ = s.store.LogSecurity(r.Context(), "runner_paired", rn.Name+" ("+truncate(in.Hostname, 80)+")", ip, r.UserAgent(), 0)
	slog.Info("machine paired", "runner", rn.Name, "hostname", in.Hostname, "ip", ip)
	writeJSON(w, http.StatusOK, map[string]any{"runner_id": rn.ID, "name": rn.Name, "role": rn.Role,
		"token": token, "api_url": s.cfg.PublicURL})
}
