package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devai-io/forge/internal/selfupdate"
)

func TestSelfRestartWaitsForIdle(t *testing.T) {
	t.Setenv("FORGE_AGENT_HOME", t.TempDir())
	exe := filepath.Join(t.TempDir(), "forge")
	if err := os.WriteFile(exe, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	stopped := false
	r := &Runner{slots: newSlots(2), exe: exe, exeStamp: selfupdate.Stamp(exe), stop: func() { stopped = true }}
	bg := context.Background()
	claimInFlight := func() {
		if !r.slots.acquire(bg) {
			t.Fatal("acquire")
		}
		r.claiming.Add(1)
	}

	// Same binary: nothing happens, but the state is written for `forge update`.
	claimInFlight()
	r.selfCheck()
	if r.restart.Load() || stopped {
		t.Fatal("restarted without a new binary")
	}
	if st := ReadAgentState(); st == nil || !st.SelfRestart || st.PID != os.Getpid() || st.Running != 0 {
		t.Fatalf("state = %+v (a claim long-polling is not a run)", st)
	}

	// A new binary while a run is in progress (a second slot): wait, keep claiming.
	if !r.slots.acquire(bg) {
		t.Fatal("acquire run")
	}
	if err := os.WriteFile(exe, []byte("v2, longer"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.selfCheck()
	if r.restart.Load() || stopped || r.draining {
		t.Fatal("restarted or stopped claiming while a run was in progress")
	}
	if st := ReadAgentState(); st == nil || st.Running != 1 {
		t.Fatalf("state while busy = %+v", st)
	}

	// The run ends. Claiming stops, but the long-poll still holds its slot.
	r.slots.release()
	r.selfCheck()
	if r.restart.Load() || !r.draining {
		t.Fatalf("restart=%v draining=%v: should stop claiming and wait for the claim", r.restart.Load(), r.draining)
	}
	ctx, cancel := context.WithTimeout(bg, 50*time.Millisecond)
	defer cancel()
	if r.slots.acquire(ctx) {
		t.Fatal("a new claim started after the agent decided to restart")
	}

	// The claim comes back with a run: claiming reopens and nothing restarts.
	r.claiming.Add(-1)
	r.selfCheck()
	if r.restart.Load() || r.draining {
		t.Fatal("restarted although the last claim brought a run")
	}
	// That run ends and no claim is out: the next check restarts.
	r.slots.release()
	r.selfCheck() // stops claiming
	r.selfCheck()
	if !r.restart.Load() || !stopped {
		t.Fatal("did not restart once idle")
	}
}

func TestReadAgentStateIgnoresADeadAgent(t *testing.T) {
	t.Setenv("FORGE_AGENT_HOME", t.TempDir())
	if err := os.WriteFile(StateFile(), []byte(`{"pid":2147483000,"self_restart":true,"at":"`+time.Now().UTC().Format(time.RFC3339)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := ReadAgentState(); st != nil {
		t.Fatalf("a dead agent's state was read: %+v", st)
	}
}
