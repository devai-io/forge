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
}

func New(st *store.Store, interval time.Duration, version string) *Monitor {
	return &Monitor{
		store:    st,
		interval: interval,
		version:  version,
		client: &http.Client{
			Timeout: 10 * time.Second,
			// A redirect is an answer, not something to follow: /health
			// answering 308 → https is exactly the misconfiguration to see.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (m *Monitor) Run(ctx context.Context) {
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
		wg.Add(1)
		sem <- struct{}{}
		go func(e store.Endpoint) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := m.store.RecordCheck(ctx, e, m.Probe(ctx, e)); err != nil {
				slog.Error("record check", "endpoint", e.URL, "err", err)
			}
		}(e)
	}
	wg.Wait()
}

func (m *Monitor) Check(ctx context.Context, e store.Endpoint) error {
	return m.store.RecordCheck(ctx, e, m.Probe(ctx, e))
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
