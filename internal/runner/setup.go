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
		"terminal": false,
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
	// Linux names a binary replaced under a running process (forge update)
	// "<path> (deleted)"; the new one is at the path itself.
	exe = strings.TrimSuffix(exe, " (deleted)")
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
		// bootout returns before the old agent has gone; bootstrapping over
		// it fails with "5: Input/output error". Wait for it, then retry.
		for i := 0; i < 20 && exec.Command("launchctl", "print", domain+"/"+launchdLabel).Run() == nil; i++ {
			time.Sleep(250 * time.Millisecond)
		}
		var err error
		for attempt := 0; attempt < 5; attempt++ {
			var o []byte
			if o, err = exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err == nil {
				break
			}
			err = fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(o)))
			time.Sleep(time.Second)
		}
		if err != nil {
			return err
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
// and adds the SessionStart (project context) and UserPromptSubmit (task
// matching) hooks to ~/.claude/settings.json, replacing earlier Forge
// entries so running it again is harmless.
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

	path := claudeSettingsPath()
	if err := installClaudeHooks(path, exe, configPath); err != nil {
		return err
	}
	fmt.Fprintf(out, "Added the SessionStart (context), UserPromptSubmit (task matching) and session-report hooks to %s.\n", path)
	return nil
}

// reportEvents are the Claude Code events `forge agent hook --report` handles.
var reportEvents = []string{"SessionStart", "UserPromptSubmit", "Stop", "Notification", "SessionEnd"}

// claudeHook is one Forge hook Claude Code needs: the event, and the marker
// (the agent subcommand) that tells a Forge entry from everything else.
type claudeHook struct{ event, marker string }

// claudeHooks are every Forge hook setup-claude installs: the project context
// at SessionStart, task matching at UserPromptSubmit, and the session report
// (RunHook) on every event it handles.
func claudeHooks() []claudeHook {
	hooks := []claudeHook{{"SessionStart", "context --hook"}, {"UserPromptSubmit", "match --hook"}}
	for _, event := range reportEvents {
		hooks = append(hooks, claudeHook{event, "hook --report"})
	}
	return hooks
}

func claudeSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// installClaudeHooks puts every Forge hook into the Claude Code settings file
// at path, replacing earlier Forge entries and leaving everything else.
func installClaudeHooks(path, exe, configPath string) error {
	for _, h := range claudeHooks() {
		sub := strings.Fields(h.marker) // "context --hook" → context --hook
		cmd := shellJoin(append([]string{exe}, agentArgs(configPath, sub...)...))
		if err := addHook(path, h.event, cmd, h.marker); err != nil {
			return err
		}
	}
	return nil
}

// missingClaudeHooks lists the Forge hooks the settings file at path lacks
// ("Stop: hook --report"). A file that cannot be read or parsed lacks them
// all; one that does not exist too.
func missingClaudeHooks(path string) []string {
	settings := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &settings)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	var missing []string
	for _, want := range claudeHooks() {
		found := false
		groups, _ := hooks[want.event].([]any)
		for _, group := range groups {
			g, _ := group.(map[string]any)
			inner, _ := g["hooks"].([]any)
			for _, h := range inner {
				hm, _ := h.(map[string]any)
				if cmd, _ := hm["command"].(string); isForgeHook(cmd, want.marker) {
					found = true
				}
			}
		}
		if !found {
			missing = append(missing, want.event+": "+want.marker)
		}
	}
	return missing
}

// EnsureClaudeHooks puts the Forge hooks back into ~/.claude/settings.json
// when something removed them — a config manager that rewrites the file (a
// Home Manager rebuild did, and every tmux agent stopped reporting its turns
// while Forge showed it working). It returns what it re-added (nil when
// nothing was missing).
func EnsureClaudeHooks(configPath string) ([]string, error) {
	path := claudeSettingsPath()
	missing := missingClaudeHooks(path)
	if len(missing) == 0 {
		return nil, nil
	}
	exe, err := executable()
	if err != nil {
		return nil, err
	}
	if err := installClaudeHooks(path, exe, configPath); err != nil {
		return nil, err
	}
	return missing, nil
}

// addHook puts one Forge hook for event into a Claude Code settings file,
// dropping earlier Forge ones (commands containing marker) and leaving
// everything else.
func addHook(path, event, command, marker string) error {
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
	existing, _ := hooks[event].([]any)
	for _, group := range existing {
		g, _ := group.(map[string]any)
		inner, _ := g["hooks"].([]any)
		var keep []any
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			cmd, _ := hm["command"].(string)
			if isForgeHook(cmd, marker) {
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
	hooks[event] = kept
	settings["hooks"] = hooks
	if raw != nil { // the first time only: the file as it was before Forge touched it
		if _, err := os.Stat(path + ".before-forge"); errors.Is(err, os.ErrNotExist) {
			_ = os.WriteFile(path+".before-forge", raw, 0o600)
		}
	}
	return writePrivateJSON(path, settings)
}

func isForgeHook(cmd, marker string) bool {
	return strings.Contains(cmd, marker) &&
		(strings.Contains(cmd, "forge") || strings.Contains(cmd, "runner"))
}
