// Package monitor probes every enabled endpoint on an interval and records
// the verdicts, plus the housekeeping that rides on the same clock.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/store"
)

type Monitor struct {
	store    *store.Store
	interval time.Duration
	client   *http.Client
	version  string

	// Rechecks of "warning" endpoints, one pending timer per endpoint. They
	// run on base, the context Run was given; before Run (and in tests that
	// never start it) nothing is scheduled.
	mu       sync.Mutex
	base     context.Context
	rechecks map[int64]*time.Timer
	// delay is how long to wait before rechecking after the streak-th
	// failure (store.RecheckDelay; tests shorten it).
	delay func(streak int) time.Duration
}

func New(st *store.Store, interval time.Duration, version string) *Monitor {
	return &Monitor{
		store:    st,
		interval: interval,
		version:  version,
		rechecks: map[int64]*time.Timer{},
		delay:    store.RecheckDelay,
		client: &http.Client{
			Timeout: 10 * time.Second,
			// A redirect is an answer, not something to follow: /health
			// answering 308 → https is exactly the misconfiguration to see.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (m *Monitor) Run(ctx context.Context) {
	m.mu.Lock()
	m.base = ctx
	m.mu.Unlock()
	defer m.stopRechecks()
	// Stagger the first sweep so a boot storm does not probe everything at once.
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	housekeeping := time.NewTicker(time.Hour)
	defer housekeeping.Stop()
	orphans := time.NewTicker(time.Minute)
	defer orphans.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			m.Sweep(ctx)
			timer.Reset(m.interval)
		case <-orphans.C:
			if err := m.store.FailOrphanedRuns(ctx); err != nil {
				slog.Error("fail orphaned runs", "err", err)
			}
		case <-housekeeping.C:
			if err := m.store.PruneChecks(ctx, 7*24*time.Hour); err != nil {
				slog.Error("prune checks", "err", err)
			}
			if err := m.store.PruneSessions(ctx); err != nil {
				slog.Error("prune sessions", "err", err)
			}
		}
	}
}

// Sweep checks every enabled endpoint, a few at a time.
func (m *Monitor) Sweep(ctx context.Context) {
	eps, err := m.store.EnabledEndpoints(ctx)
	if err != nil {
		slog.Error("list endpoints", "err", err)
		return
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, e := range eps {
		if m.recheckPending(e.ID) {
			continue // its quick recheck is due sooner than the next sweep
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(e store.Endpoint) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := m.Check(ctx, e); err != nil {
				slog.Error("record check", "endpoint", e.URL, "err", err)
			}
		}(e)
	}
	wg.Wait()
}

// Check probes one endpoint now and records the verdict. A failure that
// leaves it at "warning" schedules the next quick recheck (replacing any
// pending one); any other verdict cancels a pending recheck.
func (m *Monitor) Check(ctx context.Context, e store.Endpoint) error {
	status, streak, err := m.store.RecordCheck(ctx, e, m.Probe(ctx, e))
	if err != nil {
		return err
	}
	if status == "warning" {
		m.scheduleRecheck(e.ID, m.delay(streak))
	} else {
		m.cancelRecheck(e.ID)
	}
	return nil
}

func (m *Monitor) recheckPending(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.rechecks[id]
	return ok
}

func (m *Monitor) scheduleRecheck(id int64, after time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.base == nil || m.base.Err() != nil {
		return
	}
	if t, ok := m.rechecks[id]; ok {
		t.Stop()
	}
	var t *time.Timer
	t = time.AfterFunc(after, func() {
		m.mu.Lock()
		if m.rechecks[id] != t {
			m.mu.Unlock()
			return // replaced or cancelled meanwhile
		}
		delete(m.rechecks, id)
		ctx := m.base
		m.mu.Unlock()
		m.recheck(ctx, id)
	})
	m.rechecks[id] = t
}

func (m *Monitor) cancelRecheck(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.rechecks[id]; ok {
		t.Stop()
		delete(m.rechecks, id)
	}
}

func (m *Monitor) stopRechecks() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, t := range m.rechecks {
		t.Stop()
		delete(m.rechecks, id)
	}
}

// recheck re-reads the endpoint (it may have been edited, disabled or
// deleted since) and checks it again while it is still "warning".
func (m *Monitor) recheck(ctx context.Context, id int64) {
	if ctx.Err() != nil {
		return
	}
	e, err := m.store.EndpointByID(ctx, id)
	if err != nil || !e.Enabled || e.LastStatus != "warning" {
		return
	}
	if err := m.Check(ctx, *e); err != nil {
		slog.Error("recheck endpoint", "endpoint", e.URL, "err", err)
	}
}

func (m *Monitor) Probe(ctx context.Context, e store.Endpoint) store.CheckResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return store.CheckResult{Error: err.Error()}
	}
	req.Header.Set("User-Agent", "forge-monitor/"+m.version)
	start := time.Now()
	resp, err := m.client.Do(req)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return store.CheckResult{Error: describe(err), LatencyMS: &latency}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	code := resp.StatusCode
	r := store.CheckResult{OK: code == e.ExpectStatus, Code: &code, LatencyMS: &latency}
	if !r.OK {
		r.Error = fmt.Sprintf("expected HTTP %d, got %d", e.ExpectStatus, code)
	}
	return r
}

// describe shortens transport errors to the part a person needs.
func describe(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return "timed out"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "DNS: " + dnsErr.Err
	}
	return err.Error()
}
