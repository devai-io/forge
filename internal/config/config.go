// Package config reads the API's environment once, at boot, and fails fast on
// a bad value rather than letting it surface as a confusing error later.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr  string
	DatabaseURL string
	// PublicURL is the origin the SPA is served from. It is the one allowed
	// Origin for cookie-authenticated writes, the base of password-reset
	// links, and decides whether the session cookie is Secure.
	PublicURL string

	SMTP SMTP

	SeedOnEmpty     bool // FORGE_DEMO_DATA: load example projects into an empty database
	MonitorInterval time.Duration
	Version         string

	// DataDir holds files the server creates for itself (the vault key when
	// none is provided). Empty = none.
	DataDir string
	// DefaultTimezone is the timezone new accounts start in.
	DefaultTimezone string

	// Fleet observability (optional). Empty turns a source off.
	VictoriaMetricsURL string
	GrafanaURL         string
	GrafanaPublicURL   string

	// TrustedProxies may set X-Forwarded-For (Caddy's addresses on the host).
	TrustedProxies []net.IP
}

type SMTP struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
}

// Enabled reports whether reset e-mails can be sent at all.
func (s SMTP) Enabled() bool { return s.Host != "" && s.From != "" }

func Load() (Config, error) {
	c := Config{
		ListenAddr:      env("LISTEN_ADDR", ""),
		PublicURL:       strings.TrimRight(env("PUBLIC_URL", "http://localhost:8080"), "/"),
		SeedOnEmpty:     env("FORGE_DEMO_DATA", "false") == "true",
		DataDir:         os.Getenv("FORGE_DATA_DIR"),
		DefaultTimezone: env("FORGE_TIMEZONE", "UTC"),
		MonitorInterval: 2 * time.Minute,
		Version:         env("FORGE_VERSION", "dev"),

		VictoriaMetricsURL: strings.TrimRight(os.Getenv("VICTORIAMETRICS_URL"), "/"),
		GrafanaURL:         strings.TrimRight(os.Getenv("GRAFANA_URL"), "/"),
		GrafanaPublicURL:   strings.TrimRight(env("GRAFANA_PUBLIC_URL", os.Getenv("GRAFANA_URL")), "/"),
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "0.0.0.0:" + env("PORT", "8080")
	}

	dsn, err := databaseURL()
	if err != nil {
		return c, err
	}
	c.DatabaseURL = dsn

	if _, err := url.Parse(c.PublicURL); err != nil {
		return c, fmt.Errorf("PUBLIC_URL: %w", err)
	}

	if v := os.Getenv("MONITOR_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 10*time.Second {
			return c, fmt.Errorf("MONITOR_INTERVAL %q: want a duration >= 10s", v)
		}
		c.MonitorInterval = d
	}

	for _, raw := range strings.Split(os.Getenv("TRUSTED_PROXIES"), ",") {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}
		ip := net.ParseIP(raw)
		if ip == nil {
			return c, fmt.Errorf("TRUSTED_PROXIES: %q is not an IP", raw)
		}
		c.TrustedProxies = append(c.TrustedProxies, ip)
	}

	c.SMTP = SMTP{
		Host: os.Getenv("SMTP_HOST"),
		User: os.Getenv("SMTP_USER"),
		From: os.Getenv("SMTP_FROM"),
		Port: 587,
	}
	if v := os.Getenv("SMTP_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return c, fmt.Errorf("SMTP_PORT %q: %w", v, err)
		}
		c.SMTP.Port = p
	}
	c.SMTP.Password, err = secret("SMTP_PASSWORD", "SMTP_PASSWORD_FILE")
	if err != nil {
		return c, err
	}
	return c, nil
}

// SecureCookies is true whenever the SPA is served over TLS.
func (c Config) SecureCookies() bool { return strings.HasPrefix(c.PublicURL, "https://") }

// databaseURL returns DATABASE_URL with the password from
// DATABASE_PASSWORD_FILE spliced in when that is set — the same shape as
// the password can live in a secret file mounted into the container rather
// than in the job definition or the environment.
func databaseURL() (string, error) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		return "", errors.New("DATABASE_URL is not set")
	}
	password, err := secret("", "DATABASE_PASSWORD_FILE")
	if err != nil || password == "" {
		return base, err
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("DATABASE_URL is not a URL: %w", err)
	}
	if u.User == nil {
		return "", errors.New("DATABASE_URL has no user to attach the password to")
	}
	u.User = url.UserPassword(u.User.Username(), password)
	return u.String(), nil
}

// secret reads a value from the environment, or from the file named by the
// *_FILE variable when that is set (the file wins). Trailing whitespace is
// trimmed: every editor and most secret managers add a newline.
func secret(name, fileVar string) (string, error) {
	if path := os.Getenv(fileVar); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", fileVar, err)
		}
		v := strings.TrimSpace(string(raw))
		if v == "" {
			return "", fmt.Errorf("%s (%s) is empty", fileVar, path)
		}
		return v, nil
	}
	if name == "" {
		return "", nil
	}
	return os.Getenv(name), nil
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
