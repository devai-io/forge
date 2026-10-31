package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/runner"
	"github.com/devai-io/forge/internal/selfupdate"
)

const updateUsage = `usage: forge update [--check] [--version vX.Y.Z] [--force] [--no-restart]

Installs the latest release on this machine and brings everything that uses
it along: the binary (the old one stays as forge.prev), Claude Code's and
Codex's Forge hooks, and the background agent, which switches to the new
binary as soon as no run is in progress.

  --check        only say whether a newer release exists
  --version V    install release V instead of the latest (also to go back)
  --force        reinstall even when this version is already installed
  --no-restart   leave a running agent or server on the old binary
`

// runUpdate is `forge update`.
func runUpdate(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	check := fs.Bool("check", false, "")
	want := fs.String("version", "", "")
	force := fs.Bool("force", false, "")
	noRestart := fs.Bool("no-restart", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprint(out, updateUsage)
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("bad arguments")
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return errors.New("this is a container: pull the new image instead (ghcr.io/devai-io/forge:<version>)")
	}
	exe, err := selfupdate.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	src := selfupdate.DefaultSource()

	target := strings.TrimSpace(*want)
	if target != "" && !strings.HasPrefix(target, "v") {
		target = "v" + target
	}
	latest := target == ""
	if latest {
		if target, err = src.Latest(ctx); err != nil {
			return err
		}
	}
	cmp := selfupdate.Compare(version, target)
	switch {
	case *check && target == "":
		fmt.Fprintf(out, "forge %s; FORGE_DOWNLOAD_BASE is set, so the newest version shows only after a download\n", version)
		return nil
	case *check && cmp == 0:
		fmt.Fprintf(out, "forge %s is the latest release\n", version)
		return nil
	case *check && cmp > 0:
		fmt.Fprintf(out, "forge %s is newer than the latest release (%s)\n", version, target)
		return nil
	case *check:
		fmt.Fprintf(out, "forge %s is installed; %s is available: forge update\n", version, target)
		return nil
	case target != "" && cmp == 0 && !*force:
		fmt.Fprintf(out, "forge %s is already installed (forge update --force reinstalls it)\n", version)
		return nil
	case latest && target != "" && cmp > 0 && !*force:
		// A build from after the latest tag: "updating" would go back.
		fmt.Fprintf(out, "forge %s is newer than the latest release (%s); nothing to do (--version %s goes back to it)\n", version, target, target)
		return nil
	}

	label := target
	if label == "" {
		label = "the mirror's release"
	}
	fmt.Fprintf(out, "Downloading forge %s for %s/%s…\n", label, runtime.GOOS, runtime.GOARCH)
	bin, err := src.Fetch(ctx, target)
	if err != nil {
		return err
	}
	if err := selfupdate.Install(exe, bin); err != nil {
		return err
	}
	got, err := exec.Command(exe, "version").Output()
	if err != nil {
		// A binary that cannot even print its version must not stay.
		if rerr := os.Rename(exe+".prev", exe); rerr != nil {
			return fmt.Errorf("the new binary does not run (%v), and putting the old one back failed: %v", err, rerr)
		}
		return fmt.Errorf("the new binary does not run on this machine (%v); kept %s", err, version)
	}
	newVersion := strings.TrimSpace(string(got))
	fmt.Fprintf(out, "Installed forge %s at %s (was %s; the old binary is %s.prev)\n", newVersion, exe, version, exe)

	refreshIntegrations(exe, out)
	restartAgent(newVersion, *noRestart, out)
	restartServer(exe, *noRestart, out)
	return nil
}

// refreshIntegrations re-runs setup-claude / setup-codex with the NEW binary,
// so hooks and the MCP server match it. Only on a paired machine, and only
// for what is set up already.
func refreshIntegrations(exe string, out io.Writer) {
	cfg, err := runner.LoadConfig(runner.DefaultConfigPath())
	if err != nil {
		return // not a machine (a server, or not paired yet)
	}
	home, _ := os.UserHomeDir()
	if _, err := exec.LookPath(cfg.ClaudePath); err == nil || exists(filepath.Join(home, ".claude", "settings.json")) {
		run(out, exe, "agent", "setup-claude")
	}
	if b, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json")); err == nil && strings.Contains(string(b), "context --hook") {
		run(out, exe, "agent", "setup-codex")
	}
}

// restartAgent moves the background agent to the new binary. An agent that
// restarts itself (it writes self_restart in its state file) does it alone
// once idle; an older one is restarted through its service manager.
func restartAgent(newVersion string, noRestart bool, out io.Writer) {
	st := runner.ReadAgentState()
	unit := agentServicePath()
	if st == nil && unit == "" {
		return
	}
	switch {
	case st != nil && st.SelfRestart && st.Running == 0:
		fmt.Fprintf(out, "The agent is idle: it switches to %s within about half a minute.\n", newVersion)
	case st != nil && st.SelfRestart:
		fmt.Fprintf(out, "The agent switches to %s when its %d run(s) in progress finish; nothing is interrupted.\n", newVersion, st.Running)
	case noRestart:
		fmt.Fprintf(out, "The agent still runs the old binary. Restart it when convenient:\n  %s\n", strings.Join(agentRestartCmd(), " "))
	case unit == "":
		fmt.Fprintln(out, "The agent runs outside a service: restart `forge agent` yourself.")
	default:
		// Agents before self-restart: restarting stops a run in progress
		// (an interactive session parks and can be resumed).
		cmd := agentRestartCmd()
		if o, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput(); err != nil {
			fmt.Fprintf(out, "Could not restart the agent (%v: %s). Run:\n  %s\n", err, strings.TrimSpace(string(o)), strings.Join(cmd, " "))
			return
		}
		fmt.Fprintf(out, "Restarted the agent on %s. From now on it switches to new releases by itself when idle.\n", newVersion)
	}
}

func agentServicePath() string {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".config", "systemd", "user", "forge-agent.service")
	if runtime.GOOS == "darwin" {
		p = filepath.Join(home, "Library", "LaunchAgents", "dev.forge.agent.plist")
	}
	if exists(p) {
		return p
	}
	return ""
}

func agentRestartCmd() []string {
	if runtime.GOOS == "darwin" {
		return []string{"launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/dev.forge.agent", os.Getuid())}
	}
	return []string{"systemctl", "--user", "restart", "forge-agent"}
}

// restartServer handles a `forge server` run by a system unit named forge
// (INSTALL.md's layout) from this very binary.
func restartServer(exe string, noRestart bool, out io.Writer) {
	b, err := os.ReadFile("/etc/systemd/system/forge.service")
	if err != nil || !strings.Contains(string(b), "ExecStart="+exe+" server") {
		return
	}
	if noRestart || os.Geteuid() != 0 {
		fmt.Fprintln(out, "The server still runs the old binary. Restart it: sudo systemctl restart forge")
		return
	}
	if o, err := exec.Command("systemctl", "restart", "forge").CombinedOutput(); err != nil {
		fmt.Fprintf(out, "Could not restart the server (%v: %s): systemctl restart forge\n", err, strings.TrimSpace(string(o)))
		return
	}
	fmt.Fprintln(out, "Restarted the server (systemctl restart forge).")
}

// run shows a step's output indented under the update's own lines.
func run(out io.Writer, name string, args ...string) {
	o, err := exec.Command(name, args...).CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(o)), "\n") {
		if line != "" {
			fmt.Fprintln(out, "  "+line)
		}
	}
	if err != nil {
		fmt.Fprintf(out, "  (%s %s failed: %v)\n", filepath.Base(name), strings.Join(args, " "), err)
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
