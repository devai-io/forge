package api

import (
	"net"
	"net/http/httptest"
	"testing"

	"github.com/devai-io/forge/internal/config"
)

func TestClientIP(t *testing.T) {
	s := &Server{cfg: config.Config{TrustedProxies: []net.IP{net.ParseIP("203.0.113.50")}}}
	cases := []struct{ remote, xff, cf, want string }{
		{"203.0.113.9:4000", "203.0.113.4", "", "203.0.113.9"},                // untrusted peer: header ignored
		{"203.0.113.50:5000", "198.51.100.7", "", "198.51.100.7"},             // reverse proxy via the public address
		{"127.0.0.1:5000", "198.51.100.7", "", "198.51.100.7"},                // Caddy via loopback
		{"203.0.113.50:5000", "172.70.1.2", "198.51.100.4", "198.51.100.4"},   // Cloudflare edge → CF-Connecting-IP
		{"203.0.113.50:5000", "198.51.100.7", "198.51.100.4", "198.51.100.7"}, // CF header from a non-CF hop is not trusted
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.cf != "" {
			r.Header.Set("CF-Connecting-IP", c.cf)
		}
		if got := s.clientIP(r); got != c.want {
			t.Errorf("%+v: got %s", c, got)
		}
	}
}
