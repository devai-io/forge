package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Setting a machine up, in three commands (install.sh runs all three):
//
//	forge agent pair <url> <code>   trade a pairing code for this machine's token
//	forge agent install             run the agent in the background, at login
//	forge agent setup-claude        give Claude Code the Forge MCP server + hook

// PairResult is what the server answers a good pairing code with.
type PairResult struct {
	RunnerID int64  `json:"runner_id"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Token    string `json:"token"`
	APIURL   string `json:"api_url"`
}

// Pair trades a pairing code for this machine's token and writes it into the
// agent settings at configPath — keeping every other setting already there,
// or starting from safe defaults.
func Pair(ctx context.Context, serverURL, code, configPath string) (*PairResult, error) {
	serverURL = strings.TrimRight(strings.TrimSpace(serverURL), "/")
	u, err := url.Parse(serverURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%q is not a Forge address (want https://forge.example.com)", serverURL)
	}
	hostname, _ := os.Hostname()
	body, _ := json.Marshal(map[string]string{"code": code, "hostname": hostname,
		"os": runtime.GOOS + "/" + runtime.GOARCH, "version": Version})
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/runner/pair", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "forge-agent/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reaching %s: %w", serverURL, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			return nil, errors.New(e.Error.Message)
		}
		return nil, fmt.Errorf("%s answered %s", serverURL, resp.Status)
	}
	var res PairResult
	if err := json.Unmarshal(raw, &res); err != nil || !strings.HasPrefix(res.Token, "frg_") {
		return nil, fmt.Errorf("%s did not answer like a Forge server", serverURL)
	}

	settings := map[string]any{}
	if existing, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(existing, &settings); err != nil {
			return nil, fmt.Errorf("%s: %w (fix or move it, then pair again)", configPath, err)
		}
	} else {
		settings = DefaultSettings()
	}
	// The address that just worked is the one to keep, even when the server
	// calls itself something else (a VPN name, say).
	settings["api_url"] = serverURL
	settings["token"] = res.Token
	delete(settings, "token_file")
	if err := writePrivateJSON(configPath, settings); err != nil {
		return nil, err
	}
	return &res, nil
}

// DefaultSettings is a new machine's agent.json: plan and edit modes only,
// terminals and VS Code off, a few read-mostly git commands, and the usual
// source folders that exist here (else ~/dev) as the roots runs may use.
func DefaultSettings() map[string]any {
	var roots []string
	home, _ := os.UserHomeDir()
	for _, d := range []string{"dev", "code", "src", "projects", "work"} {
		if st, err := os.Stat(filepath.Join(home, d)); err == nil && st.IsDir() {
			roots = append(roots, "~/"+d)
		}
	}
	if len(roots) == 0 {
		roots = []string{"~/dev"}
	}
	return map[string]any{
		"allowed_roots":    roots,
		"permission_modes": []string{"plan", "acceptEdits"},
		"commands": map[string]string{
			"git-status": "git status -sb",
			"git-log":    "git log --oneline --decorate -15",
			"git-pull":   "git pull --ff-only",
			"git-fetch":  "git fetch --prune",
		},
		"max_concurrent": 2,
		"terminal":       false,
	}
}

func writePrivateJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ── Background service ────────────────────────────────────────────────────

const (
	systemdUnit  = "forge-agent.service"
	launchdLabel = "dev.forge.agent"
)

// executable is this binary's real path, for service files and hooks.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

// agentArgs is how the service and hooks start this agent: the config flag
// only when it is not the default.
func agentArgs(configPath string, sub ...string) []string {
	args := []string{"agent"}
	if configPath != "" && configPath != DefaultConfigPath() {
		args = append(args, "-config", configPath)
	}
	return append(args, sub...)
}

// InstallService runs the agent in the background from now on: a systemd
// user unit on Linux, a launchd agent on macOS. It carries this shell's PATH,
// so claude, git, gh and tmux resolve as they do for you.
func InstallService(configPath string, out io.Writer) error {
	if _, err := LoadConfig(configPath); err != nil {
		return fmt.Errorf("%w — pair this machine first: forge agent pair <url> <code>", err)
	}
	exe, err := executable()
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "linux":
		unit := fmt.Sprintf(`[Unit]
Description=Forge agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s
Environment=PATH=%s
Restart=always
RestartSec=5
TimeoutStopSec=30
KillMode=mixed

[Install]
WantedBy=default.target
`, shellJoin(append([]string{exe}, agentArgs(configPath)...)), os.Getenv("PATH"))
		path := filepath.Join(home, ".config", "systemd", "user", systemdUnit)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
			return err
		}
		for _, args := range [][]string{{"daemon-reload"}, {"enable", systemdUnit}, {"restart", systemdUnit}} {
			if o, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput(); err != nil {
				return fmt.Errorf("systemctl --user %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(o)))
			}
		}
		fmt.Fprintf(out, "Installed %s and started it.\n  status: systemctl --user status forge-agent\n  logs:   journalctl --user -u forge-agent -f\n", path)
		fmt.Fprintf(out, "To keep it running while you are logged out: sudo loginctl enable-linger %s\n", os.Getenv("USER"))
		return nil
	case "darwin":
		logs := filepath.Join(Home(), "logs")
		if err := os.MkdirAll(logs, 0o700); err != nil {
			return err
		}
		var argXML strings.Builder
		for _, a := range append([]string{exe}, agentArgs(configPath)...) {
			fmt.Fprintf(&argXML, "\t\t<string>%s</string>\n", xmlEscape(a))
		}
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, launchdLabel, argXML.String(), xmlEscape(os.Getenv("PATH")), filepath.Join(logs, "agent.log"), filepath.Join(logs, "agent.log"))
		path := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
			return err
		}
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		_ = exec.Command("launchctl", "bootout", domain+"/"+launchdLabel).Run() // not loaded yet is fine
		if o, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(o)))
		}
		fmt.Fprintf(out, "Installed %s and started it.\n  logs: tail -f %s\n", path, filepath.Join(logs, "agent.log"))
		return nil
	}
	return fmt.Errorf("no background service support on %s: run `forge agent` from your own supervisor", runtime.GOOS)
}

// UninstallService stops and removes what InstallService set up.
func UninstallService(out io.Writer) error {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "linux":
		_ = exec.Command("systemctl", "--user", "disable", "--now", systemdUnit).Run()
		path := filepath.Join(home, ".config", "systemd", "user", systemdUnit)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		fmt.Fprintln(out, "Stopped and removed", path)
	case "darwin":
		_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel)).Run()
		path := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Fprintln(out, "Stopped and removed", path)
	default:
		return fmt.Errorf("no background service support on %s", runtime.GOOS)
	}
	return nil
}

func shellJoin(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'\\$") {
			a = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(a) + `"`
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// ── Claude Code ───────────────────────────────────────────────────────────

// SetupClaude registers the Forge MCP server with Claude Code (user scope)
// and adds the SessionStart hook to ~/.claude/settings.json, replacing
// earlier Forge entries so running it again is harmless.
func SetupClaude(configPath, claudePath string, out io.Writer) error {
	if _, err := LoadConfig(configPath); err != nil {
		return fmt.Errorf("%w — pair this machine first: forge agent pair <url> <code>", err)
	}
	exe, err := executable()
	if err != nil {
		return err
	}
	if claudePath == "" {
		claudePath = "claude"
	}
	if _, err := exec.LookPath(claudePath); err != nil {
		fmt.Fprintf(out, "Claude Code (%s) is not on PATH; skipping the MCP server. Once it is installed:\n  claude mcp add --scope user forge -- %s\n",
			claudePath, shellJoin(append([]string{exe}, agentArgs(configPath, "mcp")...)))
	} else {
		_ = exec.Command(claudePath, "mcp", "remove", "--scope", "user", "forge").Run() // absent is fine
		args := append([]string{"mcp", "add", "--scope", "user", "forge", "--", exe}, agentArgs(configPath, "mcp")...)
		if o, err := exec.Command(claudePath, args...).CombinedOutput(); err != nil {
			return fmt.Errorf("claude mcp add: %v: %s", err, strings.TrimSpace(string(o)))
		}
		fmt.Fprintln(out, "Added the forge MCP server to Claude Code (user scope).")
	}

	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".claude", "settings.json")
	hook := shellJoin(append([]string{exe}, agentArgs(configPath, "context", "--hook")...))
	if err := addSessionHook(path, hook); err != nil {
		return err
	}
	fmt.Fprintf(out, "Added the SessionStart hook to %s.\n", path)
	return nil
}

// addSessionHook puts one Forge SessionStart hook into a Claude Code
// settings file, dropping earlier Forge ones and leaving everything else.
func addSessionHook(path, command string) error {
	settings := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	var kept []any
	existing, _ := hooks["SessionStart"].([]any)
	for _, group := range existing {
		g, _ := group.(map[string]any)
		inner, _ := g["hooks"].([]any)
		var keep []any
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			cmd, _ := hm["command"].(string)
			if isForgeHook(cmd) {
				continue
			}
			keep = append(keep, h)
		}
		if len(keep) > 0 {
			g["hooks"] = keep
			kept = append(kept, g)
		}
	}
	kept = append(kept, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}})
	hooks["SessionStart"] = kept
	settings["hooks"] = hooks
	if raw != nil { // the first time only: the file as it was before Forge touched it
		if _, err := os.Stat(path + ".before-forge"); errors.Is(err, os.ErrNotExist) {
			_ = os.WriteFile(path+".before-forge", raw, 0o600)
		}
	}
	return writePrivateJSON(path, settings)
}

func isForgeHook(cmd string) bool {
	return strings.Contains(cmd, "context --hook") &&
		(strings.Contains(cmd, "forge") || strings.Contains(cmd, "runner"))
}
