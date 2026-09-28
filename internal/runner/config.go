// Package runner is forge_runner: the process on the user's own machines
// that polls Forge for work, runs it (Claude Code headless, or a named shell
// command) inside a registered repo, streams the output back, and reports the
// git state of every repo it can see.
//
// Everything it will do is decided here, locally: the permission modes it
// accepts, the commands it knows, the directories it will run in. The web UI
// can only choose among what this machine's config allows.
package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Version is the build version (set by cmd/forge).
var Version = "dev"

type Config struct {
	APIURL    string `json:"api_url"`
	Token     string `json:"token"`
	TokenFile string `json:"token_file"`

	// ClaudePath is the claude binary; "claude" resolves through PATH.
	ClaudePath string `json:"claude_path"`
	// PermissionModes this runner accepts. bypassPermissions is never implied:
	// it has to be listed here by hand on the machine itself.
	PermissionModes []string `json:"permission_modes"`
	// AllowedRoots bounds where anything runs; a repo outside them is refused.
	AllowedRoots []string `json:"allowed_roots"`
	// ExtraArgs are appended to every claude invocation, e.g.
	// ["--allowedTools", "Bash(git status:*)"].
	ExtraArgs []string `json:"extra_args"`
	// Commands are the named shell commands the UI may run, `sh -c` in the
	// repo directory. A value is either the command line, or an object:
	//   "release-ios": {"run": "./scripts/release-ios.sh", "description": "…",
	//                   "repos": ["shop_mobile"], "confirm": true}
	// `repos` limits where it may run; `confirm` makes the UI ask and the API
	// demand an explicit confirmation (store uploads, deploys).
	RawCommands map[string]json.RawMessage `json:"commands"`
	Env         map[string]string          `json:"env"`

	MaxConcurrent int    `json:"max_concurrent"`
	MaxRunMinutes int    `json:"max_run_minutes"`
	ScanInterval  string `json:"scan_interval"`
	// CIInterval is how often GitHub Actions status is read per repo with
	// the gh CLI ("0" turns it off). Default 15m.
	CIInterval string `json:"ci_interval"`

	// Terminal lets Forge attach to this machine's tmux sessions (a shell as
	// this user, from the browser). Off unless set.
	Terminal   bool   `json:"terminal"`
	TmuxPath   string `json:"tmux_path"`
	TmuxTmpdir string `json:"tmux_tmpdir"`

	// PathMap rewrites repo paths registered in Forge (the master's) to where the
	// same repos live on this machine, longest prefix first:
	//   {"/home/me/": "/Users/me/"}
	PathMap map[string]string `json:"path_map"`

	// Code serves VS Code (web) from this machine — the master only.
	Code CodeConfig `json:"code"`

	Commands  map[string]Command `json:"-"`
	scanEvery time.Duration
	ciEvery   time.Duration
}

type Command struct {
	Run         string   `json:"run"`
	Description string   `json:"description"`
	Repos       []string `json:"repos"`
	Confirm     bool     `json:"confirm"`
}

// DefaultConfigPath is ~/.config/forge/runner.json on every OS — also on
// macOS, where os.UserConfigDir would say ~/Library/Application Support; one
// path to document beats following each platform's convention.
func DefaultConfigPath() string {
	if p := os.Getenv("FORGE_RUNNER_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".config", "forge", "runner.json")
}

func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, c.normalize()
}

func (c *Config) normalize() error {
	c.APIURL = strings.TrimRight(c.APIURL, "/")
	if c.APIURL == "" {
		return errors.New("api_url is required")
	}
	if t := os.Getenv("FORGE_RUNNER_TOKEN"); t != "" {
		c.Token = t
	}
	if c.Token == "" && c.TokenFile != "" {
		raw, err := os.ReadFile(expandHome(c.TokenFile))
		if err != nil {
			return fmt.Errorf("token_file: %w", err)
		}
		c.Token = strings.TrimSpace(string(raw))
	}
	if !strings.HasPrefix(c.Token, "frg_") {
		return errors.New("token is missing or not a Forge runner token (frg_…)")
	}
	if c.ClaudePath == "" {
		c.ClaudePath = "claude"
	}
	if c.TmuxPath == "" {
		c.TmuxPath = "tmux"
	}
	if c.Code.Port == 0 {
		c.Code.Port = 18765
	}
	if c.Code.Command == "" {
		c.Code.Command = "code"
	}
	if c.Code.DataDir == "" {
		c.Code.DataDir = filepath.Join(expandHome("~/.local/share/forge"), "vscode")
	}
	c.Code.DataDir = expandHome(c.Code.DataDir)
	if len(c.PermissionModes) == 0 {
		c.PermissionModes = []string{"plan", "acceptEdits"}
	}
	if len(c.AllowedRoots) == 0 {
		return errors.New("allowed_roots is required (e.g. [\"/home/you/dev\"])")
	}
	for i, r := range c.AllowedRoots {
		r = filepath.Clean(expandHome(r))
		if !filepath.IsAbs(r) || r == "/" {
			return fmt.Errorf("allowed_roots[%d]: must be an absolute directory other than /", i)
		}
		if resolved, err := filepath.EvalSymlinks(r); err == nil {
			r = resolved
		}
		c.AllowedRoots[i] = r
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 2
	}
	if c.MaxRunMinutes <= 0 {
		c.MaxRunMinutes = 120
	}
	c.Commands = map[string]Command{}
	for name, raw := range c.RawCommands {
		var cmd Command
		var line string
		if err := json.Unmarshal(raw, &line); err == nil {
			cmd.Run = line
		} else if err := json.Unmarshal(raw, &cmd); err != nil {
			return fmt.Errorf("commands.%s: want a string or {run, description, repos, confirm}", name)
		}
		if strings.TrimSpace(cmd.Run) == "" {
			return fmt.Errorf("commands.%s: empty command", name)
		}
		if cmd.Repos == nil {
			cmd.Repos = []string{}
		}
		c.Commands[name] = cmd
	}
	c.ciEvery = 15 * time.Minute
	if c.CIInterval != "" {
		d, err := time.ParseDuration(c.CIInterval)
		if err != nil || (d != 0 && d < time.Minute) {
			return fmt.Errorf("ci_interval %q: want 0 or a duration >= 1m", c.CIInterval)
		}
		c.ciEvery = d
	}
	c.scanEvery = 5 * time.Minute
	if c.ScanInterval != "" {
		d, err := time.ParseDuration(c.ScanInterval)
		if err != nil || d < 30*time.Second {
			return fmt.Errorf("scan_interval %q: want a duration >= 30s", c.ScanInterval)
		}
		c.scanEvery = d
	}
	return nil
}

func (c *Config) CommandNames() []string {
	names := make([]string, 0, len(c.Commands))
	for n := range c.Commands {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Allowed resolves path and reports whether it sits inside an allowed root.
// Symlinks are resolved first so a link inside a root cannot point out of it.
func (c *Config) Allowed(path string) (string, bool) {
	path = c.MapPath(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", false
	}
	return resolved, withinRoots(resolved, c.AllowedRoots)
}

func withinRoots(path string, roots []string) bool {
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// MapPath applies the longest matching path_map prefix.
func (c *Config) MapPath(p string) string {
	best := ""
	for from := range c.PathMap {
		if strings.HasPrefix(p, from) && len(from) > len(best) {
			best = from
		}
	}
	if best == "" {
		return p
	}
	return c.PathMap[best] + strings.TrimPrefix(p, best)
}
