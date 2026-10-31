package runner

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/devai-io/forge/internal/selfupdate"
)

// ErrRestart is what Run returns when a new forge binary was installed over
// this one (`forge update`, install.sh) and the agent stopped, idle, so its
// caller can exec the new binary in place: same PID, same arguments, so
// systemd, launchd or a terminal all carry on with the new version.
var ErrRestart = errors.New("restart onto the new forge binary")

// StateFile is where a running agent says what it is doing, for
// `forge update` on the same machine.
func StateFile() string { return filepath.Join(Home(), "agent-state.json") }

// AgentState is StateFile's content.
type AgentState struct {
	PID     int    `json:"pid"`
	Version string `json:"version"`
	Exe     string `json:"exe"`
	Running int    `json:"running"` // runs holding a slot
	// SelfRestart: this agent picks up a new binary by itself when idle
	// (agents before it need a service restart).
	SelfRestart bool      `json:"self_restart"`
	At          time.Time `json:"at"`
}

// ReadAgentState returns the state of the agent running on this machine, or
// nil when there is none (no file, stale, or its process is gone).
func ReadAgentState() *AgentState {
	b, err := os.ReadFile(StateFile())
	if err != nil {
		return nil
	}
	var st AgentState
	if json.Unmarshal(b, &st) != nil || time.Since(st.At) > 2*time.Minute || !processAlive(st.PID) {
		return nil
	}
	return &st
}

// watchBinary remembers the binary this agent started from.
func (r *Runner) watchBinary() {
	if exe, err := selfupdate.Executable(); err == nil {
		r.exe, r.exeStamp = exe, selfupdate.Stamp(exe)
	}
}

// runsInProgress is the slots held by runs, not by a claim long-polling for one.
func (r *Runner) runsInProgress() int {
	return max(r.slots.inUse()-int(r.claiming.Load()), 0)
}

// selfCheck runs with every heartbeat tick: it records the agent's state,
// and when the binary on disk changed it moves the agent onto it without
// interrupting anything. With no run in progress it stops claiming new work
// and, once the claim that is long-polling has come back empty, ends Run
// with ErrRestart. If that claim brought a run, claiming reopens and the
// check repeats after the run.
func (r *Runner) selfCheck() {
	r.writeState()
	if r.exe == "" || r.restart.Load() {
		return
	}
	if stamp := selfupdate.Stamp(r.exe); stamp == "" || stamp == r.exeStamp {
		return
	}
	runs := r.runsInProgress()
	if !r.draining {
		if runs > 0 {
			if !r.updateSeen {
				r.updateSeen = true
				slog.Info("a new forge binary is installed; restarting onto it once the runs in progress finish", "running", runs)
			}
			return
		}
		r.draining = true
		r.slots.close()
	}
	switch {
	case r.slots.inUse() == 0:
		slog.Info("a new forge binary is installed; restarting onto it", "exe", r.exe)
		r.restart.Store(true)
		r.stop()
	case runs > 0:
		// The last claim brought work: let it run, then try again.
		r.draining = false
		r.slots.reopen()
	}
}

func (r *Runner) writeState() {
	b, _ := json.Marshal(AgentState{PID: os.Getpid(), Version: Version, Exe: r.exe, Running: r.runsInProgress(),
		SelfRestart: true, At: time.Now().UTC()})
	tmp := StateFile() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, StateFile())
	}
}

// processAlive: signal 0 checks a PID without touching it.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
