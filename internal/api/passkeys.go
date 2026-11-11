package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/devai-io/forge/internal/auth"
	"github.com/devai-io/forge/internal/store"
)

// Passkeys (WebAuthn) are the main way into Forge: a passkey signs in on its
// own (it is something you have, unlocked by something you are or know), and
// it also confirms sensitive actions. The password stays as the fallback, and
// a password sign-in always needs the authenticator code as well.
//
// Every ceremony is two calls: …/begin answers the options for
// navigator.credentials and a ceremony id; …/finish takes the id and the
// browser's answer. The challenge lives in memory for ceremonyTTL, keyed by
// that id, and is used once.

const (
	ceremonyTTL   = 5 * time.Minute
	maxCeremonies = 1000
)

type ceremony struct {
	kind      string // "login", "register", "elevate"
	userID    int64  // 0 for a sign-in: nobody is known yet
	sessionID int64
	data      webauthn.SessionData
	expires   time.Time
}

type ceremonies struct {
	mu sync.Mutex
	m  map[string]ceremony
}

func newCeremonies() *ceremonies { return &ceremonies{m: map[string]ceremony{}} }

// put stores a ceremony and returns its id; false when too many are open.
func (c *ceremonies) put(x ceremony) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for id, old := range c.m {
		if now.After(old.expires) {
			delete(c.m, id)
		}
	}
	if len(c.m) >= maxCeremonies {
		return "", false
	}
	id, _ := auth.Token("")
	x.expires = now.Add(ceremonyTTL)
	c.m[id] = x
	return id, true
}

// take hands a ceremony out once.
func (c *ceremonies) take(id, kind string) (ceremony, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	x, ok := c.m[id]
	if !ok {
		return ceremony{}, false
	}
	delete(c.m, id)
	return x, x.kind == kind && time.Now().Before(x.expires)
}

// newWebAuthn configures the relying party from the public URL: its host is
// the RP ID, its origin the only one accepted.
func newWebAuthn(publicURL string) (*webauthn.WebAuthn, error) {
	u, err := url.Parse(publicURL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("passkeys need a public URL with a host, got %q", publicURL)
	}
	return webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: "Forge",
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
	})
}

// passkeyUser adapts an account and its passkeys to go-webauthn.
type passkeyUser struct {
	u      *store.User
	handle []byte
	rows   []store.Passkey
	creds  []webauthn.Credential
}

func (p *passkeyUser) WebAuthnID() []byte   { return p.handle }
func (p *passkeyUser) WebAuthnName() string { return p.u.Username }
func (p *passkeyUser) WebAuthnDisplayName() string {
	if p.u.DisplayName != "" {
		return p.u.DisplayName
	}
	return p.u.Username
}
func (p *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return p.creds }

// row is the stored passkey a verified credential belongs to.
func (p *passkeyUser) row(credentialID []byte) *store.Passkey {
	for i := range p.rows {
		if bytes.Equal(p.rows[i].CredentialID, credentialID) {
			return &p.rows[i]
		}
	}
	return nil
}

func (s *Server) loadPasskeyUser(ctx context.Context, u *store.User) (*passkeyUser, error) {
	handle, err := s.store.WebAuthnID(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Passkeys(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	p := &passkeyUser{u: u, handle: handle, rows: rows}
	for _, row := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(row.Credential), &c); err != nil {
			slog.Error("passkey record", "id", row.ID, "err", err)
			continue
		}
		p.creds = append(p.creds, c)
	}
	return p, nil
}

func passkeyView(p store.Passkey) store.Passkey {
	var c webauthn.Credential
	if json.Unmarshal([]byte(p.Credential), &c) == nil {
		p.Synced = c.Flags.BackupEligible
	}
	return p
}

// passkeysOn answers 503 when this server cannot do WebAuthn (a public URL
// without a host).
func (s *Server) passkeysOn(w http.ResponseWriter) bool {
	if s.wa == nil {
		writeError(w, http.StatusServiceUnavailable, "passkeys_unavailable", "passkeys need the server's public URL to be set")
		return false
	}
	return true
}

// recordPasskeyUse saves the credential's moved-on sign count; false (and a
// security event) when the count went backwards, the mark of a cloned key.
func (s *Server) recordPasskeyUse(r *http.Request, p *passkeyUser, cred *webauthn.Credential) bool {
	row := p.row(cred.ID)
	if row == nil {
		return false
	}
	if cred.Authenticator.CloneWarning {
		slog.Warn("passkey sign count went backwards", "user", p.u.Username, "passkey", row.ID)
		_ = s.store.LogSecurity(r.Context(), "passkey_refused", fmt.Sprintf("%q: its counter went backwards (a copied key?)", row.Name),
			s.clientIP(r), r.UserAgent(), sessionID(r))
		return false
	}
	raw, err := json.Marshal(cred)
	if err == nil {
		err = s.store.UsePasskey(r.Context(), row.ID, string(raw))
	}
	if err != nil {
		slog.Error("record passkey use", "err", err)
	}
	return true
}

// ── Sign-in ───────────────────────────────────────────────────────────────

func (s *Server) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) {
		return
	}
	if !s.logins.Allowed(s.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts — try again in a few minutes")
		return
	}
	// Discoverable: the browser offers the passkeys it has for this site,
	// no username needed.
	assertion, data, err := s.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	id, ok := s.ceremonies.put(ceremony{kind: "login", data: *data})
	if !ok {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many sign-ins in progress — try again in a minute")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ceremony": id, "publicKey": assertion.Response})
}

type passkeyAnswer struct {
	Ceremony   string          `json:"ceremony"`
	Credential json.RawMessage `json:"credential"`
	Name       string          `json:"name"`
}

func (s *Server) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) {
		return
	}
	var in passkeyAnswer
	if !decode(w, r, &in) {
		return
	}
	ip := s.clientIP(r)
	if !s.logins.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts — try again in a few minutes")
		return
	}
	c, ok := s.ceremonies.take(in.Ceremony, "login")
	if !ok {
		writeError(w, http.StatusBadRequest, "ceremony_expired", "that took too long — try the passkey again")
		return
	}
	fail := func(why string, err error) {
		s.logins.Record(ip)
		slog.Warn("passkey sign-in refused", "ip", ip, "why", why, "err", err)
		s.sec(r, "login_failed", "passkey: "+why)
		writeError(w, http.StatusUnauthorized, "unauthorized", "that passkey was not accepted")
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(in.Credential)
	if err != nil {
		fail("unreadable answer", err)
		return
	}
	var owner *passkeyUser
	cred, err := s.wa.ValidateDiscoverableLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
		row, err := s.store.PasskeyByCredentialID(r.Context(), rawID)
		if err != nil {
			return nil, err
		}
		u, err := s.store.UserByID(r.Context(), row.UserID)
		if err != nil {
			return nil, err
		}
		p, err := s.loadPasskeyUser(r.Context(), u)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(p.handle, userHandle) {
			return nil, errors.New("user handle does not match the passkey's account")
		}
		owner = p
		return p, nil
	}, c.data, parsed)
	if err != nil || owner == nil {
		fail("unknown passkey or bad signature", err)
		return
	}
	if !s.recordPasskeyUse(r, owner, cred) {
		fail("refused", nil)
		return
	}
	s.logins.Reset(ip)
	s.startSession(w, r, owner.u, fmt.Sprintf("passkey %q", owner.row(cred.ID).Name), s.deviceTrusted(r, owner.u))
}

// ── Confirming a sensitive action ────────────────────────────────────────

func (s *Server) passkeyElevateBegin(w http.ResponseWriter, r *http.Request, _ *store.User) {
	if !s.passkeysOn(w) {
		return
	}
	u := realUser(r) // the person at the keyboard confirms, as with the password
	p, err := s.loadPasskeyUser(r.Context(), u)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if len(p.creds) == 0 {
		writeError(w, http.StatusBadRequest, "no_passkeys", "this account has no passkey yet")
		return
	}
	assertion, data, err := s.wa.BeginLogin(p, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	id, ok := s.ceremonies.put(ceremony{kind: "elevate", userID: u.ID, sessionID: sessionID(r), data: *data})
	if !ok {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "try again in a minute")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ceremony": id, "publicKey": assertion.Response})
}

func (s *Server) passkeyElevateFinish(w http.ResponseWriter, r *http.Request, _ *store.User) {
	if !s.passkeysOn(w) {
		return
	}
	u := realUser(r)
	var in passkeyAnswer
	if !decode(w, r, &in) {
		return
	}
	ip := s.clientIP(r)
	if !s.logins.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts — try again in a few minutes")
		return
	}
	c, ok := s.ceremonies.take(in.Ceremony, "elevate")
	if !ok || c.userID != u.ID || c.sessionID != sessionID(r) {
		writeError(w, http.StatusBadRequest, "ceremony_expired", "that took too long — try the passkey again")
		return
	}
	p, err := s.loadPasskeyUser(r.Context(), u)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(in.Credential)
	var cred *webauthn.Credential
	if err == nil {
		cred, err = s.wa.ValidateLogin(p, c.data, parsed)
	}
	if err != nil || !s.recordPasskeyUse(r, p, cred) {
		s.logins.Record(ip)
		slog.Warn("passkey confirmation refused", "user", u.Username, "err", err)
		s.sec(r, "elevate_failed", "passkey not accepted")
		writeErr(w, r, &store.ValidationError{Field: "passkey", Message: "was not accepted"})
		return
	}
	until, err := s.store.ElevateSession(r.Context(), sessionID(r), elevationTTL)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("session elevated", "user", u.Username, "ip", ip, "with", "passkey")
	s.sec(r, "elevate", "with a passkey")
	writeJSON(w, http.StatusOK, map[string]any{"elevated_until": until})
}

// ── Managing passkeys (Settings → Passkeys) ──────────────────────────────

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request, u *store.User) {
	rows, err := s.store.Passkeys(r.Context(), u.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	for i := range rows {
		rows[i] = passkeyView(rows[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": rows, "available": s.wa != nil})
}

// passkeyRegisterBegin needs elevation: a session that walked away from an
// unlocked laptop must not be able to add a way in.
func (s *Server) passkeyRegisterBegin(w http.ResponseWriter, r *http.Request, u *store.User) {
	if !s.passkeysOn(w) {
		return
	}
	p, err := s.loadPasskeyUser(r.Context(), u)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	required := true
	creation, data, err := s.wa.BeginRegistration(p,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: &required,
			UserVerification:   protocol.VerificationRequired,
		}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		webauthn.WithExclusions(webauthn.Credentials(p.creds).CredentialDescriptors()),
	)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	id, ok := s.ceremonies.put(ceremony{kind: "register", userID: u.ID, sessionID: sessionID(r), data: *data})
	if !ok {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "try again in a minute")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ceremony": id, "publicKey": creation.Response})
}

func (s *Server) passkeyRegisterFinish(w http.ResponseWriter, r *http.Request, u *store.User) {
	if !s.passkeysOn(w) {
		return
	}
	var in passkeyAnswer
	if !decode(w, r, &in) {
		return
	}
	c, ok := s.ceremonies.take(in.Ceremony, "register")
	if !ok || c.userID != u.ID || c.sessionID != sessionID(r) {
		writeError(w, http.StatusBadRequest, "ceremony_expired", "that took too long — add the passkey again")
		return
	}
	p, err := s.loadPasskeyUser(r.Context(), u)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(in.Credential)
	var cred *webauthn.Credential
	if err == nil {
		cred, err = s.wa.CreateCredential(p, c.data, parsed)
	}
	if err != nil {
		slog.Warn("passkey registration refused", "user", u.Username, "err", err)
		writeErr(w, r, &store.ValidationError{Field: "credential", Message: "the passkey could not be verified"})
		return
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	row, err := s.store.CreatePasskey(r.Context(), u.ID, cred.ID, in.Name, string(raw))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("passkey added", "user", u.Username, "passkey", row.ID)
	s.sec(r, "passkey_added", fmt.Sprintf("%q", row.Name))
	writeJSON(w, http.StatusCreated, passkeyView(*row))
}

func (s *Server) renamePasskey(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	row, err := s.store.RenamePasskey(r.Context(), u.ID, id, in.Name)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, passkeyView(*row))
}

func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeletePasskey(r.Context(), u.ID, id); err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("passkey removed", "user", u.Username, "passkey", id)
	s.sec(r, "passkey_removed", fmt.Sprintf("passkey %d", id))
	w.WriteHeader(http.StatusNoContent)
}
