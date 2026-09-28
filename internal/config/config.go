// Package config reads the server's settings once, at boot, and fails fast on
// a bad value rather than letting it surface as a confusing error later.
//
// Everything the server keeps lives in one workspace folder, FORGE_HOME
// (default ~/.forge; /data in the container image):
//
//	config.json   settings (this package; environment variables override it)
//	forge.db      the SQLite database (+ -wal/-shm while running)
//	vault.key     the vault master key — back it up on its own
//	backups/      nightly database snapshots
//	projects/KEY/ each project's files
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Home is the workspace folder (FORGE_HOME).
	Home string

	ListenAddr string
	// PublicURL is the origin the web app is served from. It is the one
	// allowed Origin for cookie-authenticated writes, the base of links in
	// e-mails, and decides whether the session cookie is Secure.
	PublicURL string

	SMTP SMTP

	SeedOnEmpty     bool // demo_data / FORGE_DEMO_DATA: load example projects into an empty database
	MonitorInterval time.Duration
	Version         string

	// DefaultTimezone is the timezone new accounts start in.
	DefaultTimezone string

	// BackupKeep is how many nightly database snapshots stay in backups/
	// (0 turns backups off).
	BackupKeep int

	// Fleet observability (optional). Empty turns a source off.
	VictoriaMetricsURL string
	GrafanaURL         string
	GrafanaPublicURL   string

	// TrustedProxies may set X-Forwarded-For (the reverse proxy's addresses).
	TrustedProxies []net.IP
}

type SMTP struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
}

// Enabled reports whether e-mails can be sent at all.
func (s SMTP) Enabled() bool { return s.Host != "" && s.From != "" }

// File is config.json. Every field is optional; environment variables win.
type File struct {
	PublicURL       string   `json:"public_url,omitempty"`
	ListenAddr      string   `json:"listen_addr,omitempty"`
	Timezone        string   `json:"timezone,omitempty"`
	DemoData        bool     `json:"demo_data,omitempty"`
	MonitorInterval string   `json:"monitor_interval,omitempty"`
	BackupKeep      *int     `json:"backup_keep,omitempty"`
	TrustedProxies  []string `json:"trusted_proxies,omitempty"`
	SMTP            struct {
		Host         string `json:"host,omitempty"`
		Port         int    `json:"port,omitempty"`
		User         string `json:"user,omitempty"`
		Password     string `json:"password,omitempty"`
		PasswordFile string `json:"password_file,omitempty"`
		From         string `json:"from,omitempty"`
	} `json:"smtp"`
	Monitoring struct {
		VictoriaMetricsURL string `json:"victoriametrics_url,omitempty"`
		GrafanaURL         string `json:"grafana_url,omitempty"`
		GrafanaPublicURL   string `json:"grafana_public_url,omitempty"`
	} `json:"monitoring"`
}

// DefaultHome is FORGE_HOME, else ~/.forge.
func DefaultHome() string {
	if h := os.Getenv("FORGE_HOME"); h != "" {
		return h
	}
	if h := os.Getenv("FORGE_DATA_DIR"); h != "" { // the name before FORGE_HOME
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".forge"
	}
	return filepath.Join(home, ".forge")
}

func (c Config) ConfigPath() string  { return filepath.Join(c.Home, "config.json") }
func (c Config) DBPath() string      { return filepath.Join(c.Home, "forge.db") }
func (c Config) BackupDir() string   { return filepath.Join(c.Home, "backups") }
func (c Config) ProjectsDir() string { return filepath.Join(c.Home, "projects") }

// Load reads FORGE_HOME/config.json (writing a starter one when there is
// none) and applies the environment on top.
func Load() (Config, error) {
	c := Config{
		Home:            DefaultHome(),
		PublicURL:       "http://localhost:8080",
		DefaultTimezone: "UTC",
		MonitorInterval: 2 * time.Minute,
		Version:         env("FORGE_VERSION", "dev"),
		BackupKeep:      14,
	}
	abs, err := filepath.Abs(c.Home)
	if err != nil {
		return c, err
	}
	c.Home = abs
	for _, dir := range []string{c.Home, c.BackupDir(), c.ProjectsDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return c, fmt.Errorf("workspace %s: %w", dir, err)
		}
	}

	f, err := readFile(c.ConfigPath())
	if err != nil {
		return c, err
	}
	smtpPasswordFile := f.SMTP.PasswordFile
	c.SMTP = SMTP{Host: f.SMTP.Host, Port: f.SMTP.Port, User: f.SMTP.User, Password: f.SMTP.Password, From: f.SMTP.From}
	pick := func(dst *string, fileVal, envName string) {
		if fileVal != "" {
			*dst = fileVal
		}
		if v := os.Getenv(envName); v != "" {
			*dst = v
		}
	}
	pick(&c.PublicURL, f.PublicURL, "PUBLIC_URL")
	pick(&c.ListenAddr, f.ListenAddr, "LISTEN_ADDR")
	pick(&c.DefaultTimezone, f.Timezone, "FORGE_TIMEZONE")
	pick(&c.VictoriaMetricsURL, f.Monitoring.VictoriaMetricsURL, "VICTORIAMETRICS_URL")
	pick(&c.GrafanaURL, f.Monitoring.GrafanaURL, "GRAFANA_URL")
	c.GrafanaPublicURL = c.GrafanaURL
	pick(&c.GrafanaPublicURL, f.Monitoring.GrafanaPublicURL, "GRAFANA_PUBLIC_URL")
	pick(&c.SMTP.Host, "", "SMTP_HOST")
	pick(&c.SMTP.User, "", "SMTP_USER")
	pick(&c.SMTP.From, "", "SMTP_FROM")
	pick(&smtpPasswordFile, "", "SMTP_PASSWORD_FILE")
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	c.VictoriaMetricsURL = strings.TrimRight(c.VictoriaMetricsURL, "/")
	c.GrafanaURL = strings.TrimRight(c.GrafanaURL, "/")
	c.GrafanaPublicURL = strings.TrimRight(c.GrafanaPublicURL, "/")

	c.SeedOnEmpty = f.DemoData
	if v := os.Getenv("FORGE_DEMO_DATA"); v != "" {
		c.SeedOnEmpty = v == "true"
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "0.0.0.0:" + env("PORT", "8080")
	}
	if u, err := url.Parse(c.PublicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return c, fmt.Errorf("public_url %q: want http(s)://host[:port]", c.PublicURL)
	}
	if _, err := time.LoadLocation(c.DefaultTimezone); err != nil {
		return c, fmt.Errorf("timezone %q: %w", c.DefaultTimezone, err)
	}

	interval := f.MonitorInterval
	pick(&interval, "", "MONITOR_INTERVAL")
	if interval != "" {
		d, err := time.ParseDuration(interval)
		if err != nil || d < 10*time.Second {
			return c, fmt.Errorf("monitor_interval %q: want a duration >= 10s", interval)
		}
		c.MonitorInterval = d
	}
	if f.BackupKeep != nil {
		c.BackupKeep = *f.BackupKeep
	}
	if v := os.Getenv("FORGE_BACKUP_KEEP"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return c, fmt.Errorf("FORGE_BACKUP_KEEP %q: want a number >= 0", v)
		}
		c.BackupKeep = n
	}

	proxies := f.TrustedProxies
	if v := os.Getenv("TRUSTED_PROXIES"); v != "" {
		proxies = strings.Split(v, ",")
	}
	for _, raw := range proxies {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}
		ip := net.ParseIP(raw)
		if ip == nil {
			return c, fmt.Errorf("trusted_proxies: %q is not an IP", raw)
		}
		c.TrustedProxies = append(c.TrustedProxies, ip)
	}

	if c.SMTP.Port == 0 {
		c.SMTP.Port = 587
	}
	if v := os.Getenv("SMTP_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return c, fmt.Errorf("SMTP_PORT %q: %w", v, err)
		}
		c.SMTP.Port = p
	}
	if v := os.Getenv("SMTP_PASSWORD"); v != "" {
		c.SMTP.Password = v
	}
	if smtpPasswordFile != "" {
		raw, err := os.ReadFile(smtpPasswordFile)
		if err != nil {
			return c, fmt.Errorf("smtp password file: %w", err)
		}
		c.SMTP.Password = strings.TrimSpace(string(raw))
	}
	return c, nil
}

// readFile reads config.json; a missing one is replaced by a starter file
// holding the defaults, so the workspace documents itself.
func readFile(path string) (File, error) {
	var f File
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		starter := File{PublicURL: env("PUBLIC_URL", "http://localhost:8080"), Timezone: env("FORGE_TIMEZONE", "UTC")}
		out, _ := json.MarshalIndent(starter, "", "  ")
		_ = os.WriteFile(path, append(out, '\n'), 0o600)
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return f, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// SecureCookies is true whenever the app is served over TLS.
func (c Config) SecureCookies() bool { return strings.HasPrefix(c.PublicURL, "https://") }

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
