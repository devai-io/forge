package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Jev compaction in this machine's Claude Code, as the server's Settings say
// (Jev on + "compaction"). When on, the agent:
//
//   - checks out the compaction plugin at the commit the server pins, into
//     <Home>/claude-plugins/fast-jev-compaction, and installs it from there
//     (a local marketplace: nothing is fetched from the plugin's repo later);
//   - sets TYPESAFE_API_KEY and CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1 in
//     ~/.claude/settings.json "env", marking them as Forge's.
//
// When off it disables the plugin and removes only the env keys it added.
// The heartbeat carries a revision, so this runs at start and on change.
// "claude_jev": false in agent.json keeps Forge out of Claude Code entirely.

const (
	jevPlugin      = "fast-jev-compaction"
	jevPluginID    = jevPlugin + "@" + jevPlugin
	jevMarkerEnv   = "FORGE_MANAGES_JEV"
	jevKeyEnv      = "TYPESAFE_API_KEY"
	jevFunctionEnv = "CLAUDE_CODE_ENABLE_FUNCTION_HOOKS"
)

type jevConfig struct {
	Compaction   bool   `json:"compaction"`
	APIKey       string `json:"api_key"`
	PluginRepo   string `json:"plugin_repo"`
	PluginCommit string `json:"plugin_commit"`
	Rev          string `json:"rev"`
}

// syncJev applies the server's Jev settings when its revision changed.
func (r *Runner) syncJev(ctx context.Context, rev string) {
	if rev == "" || !r.cfg.claudeJev() {
		return
	}
	r.mu.Lock()
	same := rev == r.jevRev
	r.mu.Unlock()
	if same {
		return
	}
	var cfg jevConfig
	gctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := r.client.get(gctx, "/api/runner/jev", &cfg); err != nil {
		slog.Warn("jev: reading settings", "err", err)
		r.jevBackoff()
		return
	}
	actx, cancel2 := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel2()
	if err := applyJev(actx, r.cfg.ClaudePath, cfg); err != nil {
		slog.Warn("jev: applying settings to Claude Code", "err", err)
		r.jevBackoff()
		return
	}
	r.mu.Lock()
	r.jevRev = rev
	r.mu.Unlock()
	slog.Info("jev: Claude Code compaction " + map[bool]string{true: "on", false: "off"}[cfg.Compaction])
}

func (r *Runner) jevBackoff() {
	r.mu.Lock()
	r.jevRetryAt = time.Now().Add(10 * time.Minute)
	r.mu.Unlock()
}

func applyJev(ctx context.Context, claude string, cfg jevConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if !cfg.Compaction {
		if _, err := exec.LookPath(claude); err == nil {
			_ = exec.CommandContext(ctx, claude, "plugin", "disable", jevPluginID).Run() // not installed is fine
		}
		return setClaudeEnv(settings, nil)
	}
	if cfg.APIKey == "" || cfg.PluginRepo == "" || cfg.PluginCommit == "" {
		return errors.New("incomplete settings from the server")
	}
	if _, err := exec.LookPath(claude); err != nil {
		return fmt.Errorf("claude not found: %w", err)
	}
	dir := filepath.Join(Home(), "claude-plugins", jevPlugin)
	if err := checkoutPinned(ctx, cfg.PluginRepo, cfg.PluginCommit, dir); err != nil {
		return err
	}
	// Idempotent enough: "already added/installed" is an error we can ignore;
	// whether the plugin ends up enabled is checked below.
	_ = exec.CommandContext(ctx, claude, "plugin", "marketplace", "add", dir).Run()
	_ = exec.CommandContext(ctx, claude, "plugin", "marketplace", "update", jevPlugin).Run()
	_ = exec.CommandContext(ctx, claude, "plugin", "install", jevPluginID).Run()
	_ = exec.CommandContext(ctx, claude, "plugin", "enable", jevPluginID).Run()
	if out, err := exec.CommandContext(ctx, claude, "plugin", "list").CombinedOutput(); err == nil &&
		!strings.Contains(string(out), jevPlugin) {
		return fmt.Errorf("%s did not install: %s", jevPlugin, firstLine(string(out)))
	}
	return setClaudeEnv(settings, map[string]string{jevKeyEnv: cfg.APIKey, jevFunctionEnv: "1"})
}

// checkoutPinned makes dir a checkout of repo at exactly commit.
func checkoutPinned(ctx context.Context, repo, commit, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return err
		}
		if _, err := git(ctx, filepath.Dir(dir), "clone", "--quiet", repo, dir); err != nil {
			return fmt.Errorf("cloning %s: %w", repo, err)
		}
	}
	if head, _ := git(ctx, dir, "rev-parse", "HEAD"); strings.TrimSpace(head) == commit {
		return nil
	}
	if _, err := git(ctx, dir, "cat-file", "-e", commit+"^{commit}"); err != nil {
		if _, err := git(ctx, dir, "fetch", "--quiet", "origin"); err != nil {
			return fmt.Errorf("fetching %s: %w", repo, err)
		}
	}
	if _, err := git(ctx, dir, "checkout", "--quiet", "--detach", commit); err != nil {
		return fmt.Errorf("checking out %s: %w", commit, err)
	}
	return nil
}

// setClaudeEnv puts vars into settings.json "env" (marking them Forge's), or
// with nil removes the ones Forge put there. Everything else is left alone.
func setClaudeEnv(path string, vars map[string]string) error {
	settings := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		if vars == nil {
			return nil
		}
	default:
		return err
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	if vars == nil {
		if env[jevMarkerEnv] == nil {
			return nil // Forge never set anything here
		}
		delete(env, jevKeyEnv)
		delete(env, jevMarkerEnv)
		if v, _ := env[jevFunctionEnv].(string); v == "1" {
			delete(env, jevFunctionEnv)
		}
	} else {
		for k, v := range vars {
			env[k] = v
		}
		env[jevMarkerEnv] = "1"
	}
	if len(env) == 0 {
		delete(settings, "env")
	} else {
		settings["env"] = env
	}
	if raw != nil {
		if _, err := os.Stat(path + ".before-forge"); errors.Is(err, os.ErrNotExist) {
			_ = os.WriteFile(path+".before-forge", raw, 0o600)
		}
	}
	return writePrivateJSON(path, settings)
}
