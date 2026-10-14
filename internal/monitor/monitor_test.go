package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devai-io/forge/internal/db"
	"github.com/devai-io/forge/internal/seed"
	"github.com/devai-io/forge/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	st := store.New(d)
	if ok, err := st.SeedIfEmpty(ctx, seed.Demo); err != nil || !ok {
		t.Fatalf("seed: %v %v", ok, err)
	}
	return st
}

func waitStatus(t *testing.T, st *store.Store, id int64, want string) *store.Endpoint {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e, err := st.EndpointByID(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if e.LastStatus == want {
			return e
		}
		if time.Now().After(deadline) {
			t.Fatalf("endpoint stayed %s (streak %d), want %s", e.LastStatus, e.FailStreak, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A failing endpoint is rechecked on its own, quickly, and turns down only
// after the retries fail too; a success clears it and stops the rechecks.
func TestFailingEndpointIsRecheckedBeforeDown(t *testing.T) {
	st := openStore(t)
	var failing atomic.Bool
	failing.Store(true)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if failing.Load() {
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(st, time.Hour, "test")
	m.base = ctx
	m.delay = func(context.Context, int) time.Duration { return 20 * time.Millisecond }

	e, err := st.CreateEndpoint(ctx, "SHOP", store.EndpointInput{Name: "flaky", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Check(ctx, *e); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.EndpointByID(ctx, e.ID); got.LastStatus != "warning" || got.FailStreak != 1 {
		t.Fatalf("after first failure = %s/%d", got.LastStatus, got.FailStreak)
	}
	down := waitStatus(t, st, e.ID, "down")
	if down.FailStreak != store.EndpointRetries+1 || int(hits.Load()) != store.EndpointRetries+1 {
		t.Fatalf("down after %d checks (streak %d)", hits.Load(), down.FailStreak)
	}
	time.Sleep(60 * time.Millisecond)
	if m.recheckPending(e.ID) || int(hits.Load()) != store.EndpointRetries+1 {
		t.Fatalf("a down endpoint is still being rechecked (%d hits)", hits.Load())
	}

	// Manual check: a success brings it straight back up.
	failing.Store(false)
	if err := m.Check(ctx, *down); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, e.ID, "up")

	// A single blip clears on its recheck without ever going down.
	failing.Store(true)
	if err := m.Check(ctx, *e); err != nil {
		t.Fatal(err)
	}
	failing.Store(false)
	waitStatus(t, st, e.ID, "up")
	if m.recheckPending(e.ID) {
		t.Fatal("recheck still pending after recovery")
	}
}

// Without Run (no base context) nothing is scheduled, and the sweep leaves
// an endpoint with a pending recheck to it.
func TestRecheckBookkeeping(t *testing.T) {
	st := openStore(t)
	m := New(st, time.Hour, "test")
	m.scheduleRecheck(1, time.Millisecond)
	if m.recheckPending(1) {
		t.Fatal("scheduled before Run")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.base = ctx
	m.scheduleRecheck(1, time.Hour)
	m.scheduleRecheck(1, time.Hour) // replaces, does not stack
	if !m.recheckPending(1) || len(m.rechecks) != 1 {
		t.Fatalf("pending = %v", m.rechecks)
	}
	m.cancelRecheck(1)
	if m.recheckPending(1) {
		t.Fatal("cancel left it pending")
	}
	m.scheduleRecheck(2, time.Hour)
	cancel()
	m.stopRechecks()
	if len(m.rechecks) != 0 {
		t.Fatal("stop left timers")
	}
}

// The settings decide how many rechecks run and how long they wait.
func TestRechecksFollowSettings(t *testing.T) {
	st := openStore(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := st.SetEndpointSettings(ctx, store.EndpointSettings{Retries: 4, RecheckSeconds: []int{20, 40}}); err != nil {
		t.Fatal(err)
	}
	m := New(st, time.Hour, "test")
	if d := m.delay(ctx, 2); d != 40*time.Second {
		t.Fatalf("default delay func = %v, want the setting's 40s", d)
	}
	m.base = ctx
	m.delay = func(context.Context, int) time.Duration { return 10 * time.Millisecond }
	e, err := st.CreateEndpoint(ctx, "SHOP", store.EndpointInput{Name: "four", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Check(ctx, *e); err != nil {
		t.Fatal(err)
	}
	down := waitStatus(t, st, e.ID, "down")
	if down.FailStreak != 5 || hits.Load() != 5 || down.RetryLimit != 4 {
		t.Fatalf("down after %d checks, streak %d, limit %d", hits.Load(), down.FailStreak, down.RetryLimit)
	}
}
