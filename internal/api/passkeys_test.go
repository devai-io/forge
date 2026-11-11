package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"github.com/devai-io/forge/internal/store"
)

// softKey is a software passkey: an ES256 key the way a platform
// authenticator answers navigator.credentials.create / get.
type softKey struct {
	t      *testing.T
	key    *ecdsa.PrivateKey
	id     []byte
	user   []byte // the user handle it was registered for
	count  uint32
	origin string
}

func newSoftKey(t *testing.T, origin string) *softKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &softKey{t: t, key: k, id: id, origin: origin}
}

var b64 = base64.RawURLEncoding

type pkOptions struct {
	Ceremony  string
	PublicKey struct {
		Challenge string
		RpID      string `json:"rpId"`
		Rp        struct{ ID string }
		User      struct{ ID string }
	}
}

func (k *softKey) clientData(kind, challenge string) []byte {
	raw, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": k.origin, "crossOrigin": false})
	return raw
}

func (k *softKey) authData(rpID string, flags byte, attested []byte) []byte {
	rp := sha256.Sum256([]byte(rpID))
	out := append([]byte{}, rp[:]...)
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, k.count)
	return append(out, attested...)
}

// create answers a registration's options.
func (k *softKey) create(o pkOptions) map[string]any {
	k.user, _ = b64.DecodeString(o.PublicKey.User.ID)
	x, y := k.key.X.FillBytes(make([]byte, 32)), k.key.Y.FillBytes(make([]byte, 32))
	cose, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	attested := make([]byte, 16) // AAGUID: none
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(k.id)))
	attested = append(attested, k.id...)
	attested = append(attested, cose...)
	// UP | UV | AT
	ad := k.authData(o.PublicKey.Rp.ID, 0x01|0x04|0x40, attested)
	att, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": ad})
	return map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(k.clientData("webauthn.create", o.PublicKey.Challenge)),
			"attestationObject": b64.EncodeToString(att),
			"transports":        []string{"internal"},
		},
		"clientExtensionResults": map[string]any{},
	}
}

// get answers a sign-in's options, its counter one up.
func (k *softKey) get(o pkOptions) map[string]any {
	k.count++
	ad := k.authData(o.PublicKey.RpID, 0x01|0x04, nil)
	cd := k.clientData("webauthn.get", o.PublicKey.Challenge)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.key, digest[:])
	if err != nil {
		k.t.Fatal(err)
	}
	return map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cd),
			"authenticatorData": b64.EncodeToString(ad),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(k.user),
		},
		"clientExtensionResults": map[string]any{},
	}
}

func (h *harness) passkeySignIn(k *softKey) int {
	h.t.Helper()
	var o pkOptions
	expect(h.t, "passkey begin", h.do("POST", "/api/auth/passkey/begin", nil, &o), 200)
	return h.do("POST", "/api/auth/passkey/finish", map[string]any{"ceremony": o.Ceremony, "credential": k.get(o)}, nil)
}

func TestPasskeys(t *testing.T) {
	h := setup(t)
	// A WebAuthn RP ID is a domain, never an IP address: the test server
	// listens on 127.0.0.1, so it plays localhost (the CSRF origin check only
	// looks at an Origin header, which these requests do not send).
	origin := strings.Replace(h.srv.URL, "127.0.0.1", "localhost", 1)
	wa, err := newWebAuthn(origin)
	if err != nil {
		t.Fatal(err)
	}
	h.api.wa = wa
	h.login()
	var list struct {
		Passkeys  []store.Passkey
		Available bool
	}
	expect(t, "list", h.do("GET", "/api/auth/passkeys", nil, &list), 200)
	if !list.Available || len(list.Passkeys) != 0 {
		t.Fatalf("list = %+v", list)
	}

	// Adding one needs the password again.
	var o pkOptions
	expect(t, "add needs elevation", h.do("POST", "/api/auth/passkeys/begin", nil, nil), 403)
	expect(t, "elevate", h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, nil), 200)
	expect(t, "add: begin", h.do("POST", "/api/auth/passkeys/begin", nil, &o), 200)
	if o.Ceremony == "" || o.PublicKey.Challenge == "" || len(o.PublicKey.User.ID) == 0 {
		t.Fatalf("options = %+v", o)
	}
	key := newSoftKey(t, origin)
	var pk store.Passkey
	expect(t, "add: finish", h.do("POST", "/api/auth/passkeys/finish",
		map[string]any{"ceremony": o.Ceremony, "name": "  Laptop  ", "credential": key.create(o)}, &pk), 201)
	if pk.Name != "Laptop" || pk.LastUsedAt != nil {
		t.Fatalf("passkey = %+v", pk)
	}
	expect(t, "a ceremony is used once", h.do("POST", "/api/auth/passkeys/finish",
		map[string]any{"ceremony": o.Ceremony, "credential": key.create(o)}, nil), 400)

	// A forged registration (signed for another site) is refused.
	expect(t, "begin again", h.do("POST", "/api/auth/passkeys/begin", nil, &o), 200)
	evil := newSoftKey(t, "https://evil.example")
	expect(t, "wrong origin", h.do("POST", "/api/auth/passkeys/finish",
		map[string]any{"ceremony": o.Ceremony, "credential": evil.create(o)}, nil), 422)

	// A passkey signs in on its own: no username, no password, no code.
	fresh := &harness{t: t, srv: h.srv, api: h.api, store: h.store, client: &http.Client{Jar: mustJar()}}
	expect(t, "passkey sign-in", fresh.passkeySignIn(key), 200)
	var me struct {
		User     store.User
		Passkeys int
	}
	expect(t, "me", fresh.do("GET", "/api/auth/me", nil, &me), 200)
	if me.User.Username != "ada" || me.Passkeys != 1 {
		t.Fatalf("me = %+v", me)
	}

	// An answer is good for its own challenge only.
	var first, second pkOptions
	expect(t, "begin", fresh.do("POST", "/api/auth/passkey/begin", nil, &first), 200)
	answer := key.get(first)
	expect(t, "begin", fresh.do("POST", "/api/auth/passkey/begin", nil, &second), 200)
	expect(t, "answer to another challenge", fresh.do("POST", "/api/auth/passkey/finish",
		map[string]any{"ceremony": second.Ceremony, "credential": answer}, nil), 401)
	expect(t, "unknown ceremony", fresh.do("POST", "/api/auth/passkey/finish",
		map[string]any{"ceremony": "nope", "credential": answer}, nil), 400)

	// It confirms sensitive actions too.
	var until struct {
		ElevatedUntil *string `json:"elevated_until"`
	}
	expect(t, "elevate: begin", fresh.do("POST", "/api/auth/elevate/passkey/begin", nil, &o), 200)
	expect(t, "elevate: finish", fresh.do("POST", "/api/auth/elevate/passkey/finish",
		map[string]any{"ceremony": o.Ceremony, "credential": key.get(o)}, &until), 200)
	if until.ElevatedUntil == nil {
		t.Fatal("not elevated")
	}
	expect(t, "an elevate ceremony is not a sign-in", fresh.do("POST", "/api/auth/elevate/passkey/begin", nil, &o), 200)
	expect(t, "used for sign-in", fresh.do("POST", "/api/auth/passkey/finish",
		map[string]any{"ceremony": o.Ceremony, "credential": key.get(o)}, nil), 400)

	// A key whose counter goes backwards is a copy: refused.
	saved := key.count
	key.count = 0
	expect(t, "cloned key", (&harness{t: t, srv: h.srv, client: &http.Client{Jar: mustJar()}}).passkeySignIn(key), 401)
	key.count = saved + 1

	// Rename and remove (removing needs elevation; fresh just confirmed).
	expect(t, "list", fresh.do("GET", "/api/auth/passkeys", nil, &list), 200)
	if len(list.Passkeys) != 1 || list.Passkeys[0].LastUsedAt == nil {
		t.Fatalf("list = %+v", list)
	}
	id := list.Passkeys[0].ID
	expect(t, "rename", fresh.do("PATCH", fmt.Sprintf("/api/auth/passkeys/%d", id), map[string]string{"name": "Phone"}, &pk), 200)
	if pk.Name != "Phone" {
		t.Fatalf("renamed = %+v", pk)
	}
	var admin struct{ Users []store.AdminUser }
	expect(t, "admin view", h.do("GET", "/api/admin/users", nil, &admin), 200)
	if len(admin.Users) == 0 || admin.Users[0].Passkeys != 1 {
		t.Fatalf("admin users = %+v", admin.Users)
	}
	expect(t, "remove", fresh.do("DELETE", fmt.Sprintf("/api/auth/passkeys/%d", id), nil, nil), 204)
	expect(t, "removed passkey no longer signs in",
		(&harness{t: t, srv: h.srv, client: &http.Client{Jar: mustJar()}}).passkeySignIn(key), 401)

	var log struct{ Events []store.SecurityEvent }
	expect(t, "security log", h.do("GET", "/api/auth/security", nil, &log), 200)
	seen := map[string]bool{}
	for _, e := range log.Events {
		seen[e.Kind] = true
	}
	for _, k := range []string{"passkey_added", "passkey_removed", "passkey_refused", "login_failed"} {
		if !seen[k] {
			t.Errorf("security log has no %s: %+v", k, seen)
		}
	}
}
