package api

import (
	"net"
	"testing"
)

func nets(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	var out []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

// With password_login_nets set, a password is only good from those networks:
// outside them sign-in, confirming and the reset link all answer
// passkey_required, before the password is even looked at.
func TestPasswordOnlyFromItsNetworks(t *testing.T) {
	h := setup(t)
	h.api.cfg.PasswordNets = nets(t, "10.0.0.0/8") // the test client is 127.0.0.1: outside

	var e struct{ Error struct{ Code string } }
	if st := h.do("POST", "/api/auth/login", map[string]string{"username": "ada", "password": testPassword}, &e); st != 403 || e.Error.Code != "passkey_required" {
		t.Fatalf("sign-in from outside = %d %q, want 403 passkey_required", st, e.Error.Code)
	}
	if st := h.do("POST", "/api/auth/login", map[string]string{"username": "nobody", "password": "x"}, &e); st != 403 {
		t.Fatalf("unknown account from outside = %d, want the same 403", st)
	}
	expect(t, "reset from outside", h.do("POST", "/api/auth/reset", map[string]string{"token": "t", "new_password": "a-long-new-password-1"}, nil), 403)
	expect(t, "forgot still answers 204", h.do("POST", "/api/auth/forgot", map[string]string{"login": "ada"}, nil), 204)

	h.api.cfg.PasswordNets = nets(t, "10.0.0.0/8", "127.0.0.0/8", "::1/128")
	expect(t, "sign-in from inside", h.signIn("ada", testPassword), 200)

	h.api.cfg.PasswordNets = nets(t, "10.0.0.0/8")
	if st := h.do("POST", "/api/auth/elevate", map[string]string{"password": testPassword}, &e); st != 403 || e.Error.Code != "passkey_required" {
		t.Fatalf("confirming from outside = %d %q, want 403 passkey_required", st, e.Error.Code)
	}
	expect(t, "the session itself still works", h.do("GET", "/api/auth/me", nil, nil), 200)
}
