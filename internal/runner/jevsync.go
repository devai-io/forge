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
	"time"
)

// Jev compaction in this machine's Claude Code. Forge used to install
// TypeSafe's fast-jev-compaction plugin and put TYPESAFE_API_KEY and
// CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1 into ~/.claude/settings.json "env".
// The plugin calls TypeSafe's own API with its own key; Forge now reaches
// Jev through OpenRouter, so the server always answers "off" and the agent
// undoes what it set up: it disables the plugin and removes only the env
// keys it added (marked FORGE_MANAGES_JEV). The heartbeat carries a
// revision, so this runs at start and on change. "claude_jev": false in
// agent.json keeps Forge out of Claude Code entirely.

const (
	jevPlugin      = "fast-jev-compaction"
	jevPluginID    = jevPlugin + "@" + jevPlugin
	jevMarkerEnv   = "FORGE_MANAGES_JEV"
	jevKeyEnv      = "TYPESAFE_API_KEY"
	jevFunctionEnv = "CLAUDE_CODE_ENABLE_FUNCTION_HOOKS"
)

type jevConfig struct {
	Compaction bool   `json:"compaction"`
	Rev        string `json:"rev"`
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
	slog.Info("jev: Claude Code compaction off")
}

func (r *Runner) jevBackoff() {
	r.mu.Lock()
	r.jevRetryAt = time.Now().Add(10 * time.Minute)
	r.mu.Unlock()
}

// applyJev removes Forge's Jev compaction from Claude Code (the only state
// the server asks for now; see the top of this file).
func applyJev(ctx context.Context, claude string, cfg jevConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	env, err := readClaudeEnv(settings)
	if err != nil {
		return err
	}
	if env[jevMarkerEnv] == nil {
		return nil // Forge never set it up here
	}
	if _, err := exec.LookPath(claude); err == nil {
		_ = exec.CommandContext(ctx, claude, "plugin", "disable", jevPluginID).Run() // not installed is fine
	}
	return setClaudeEnv(settings, nil)
}

// readClaudeEnv is settings.json's "env" (empty when there is none).
func readClaudeEnv(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var settings struct {
		Env map[string]any `json:"env"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if settings.Env == nil {
		settings.Env = map[string]any{}
	}
	return settings.Env, nil
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
