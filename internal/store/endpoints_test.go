package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/devai-io/forge/internal/db"
	"github.com/devai-io/forge/internal/store"
)

// A failure is a warning first; only EndpointRetries more failures in a row
// make it down, and only that (and the recovery) reaches the activity feed.
func TestEndpointRetriesBeforeDown(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	e := must(st.CreateEndpoint(ctx, "SHOP", store.EndpointInput{Name: "retry-probe", URL: "https://example.com/retry"}))
	code := 503
	fail := store.CheckResult{Code: &code, Error: "expected HTTP 200, got 503"}

	record := func(r store.CheckResult) (string, int) {
		t.Helper()
		status, streak, err := st.RecordCheck(ctx, *e, r)
		ok(t, err)
		return status, streak
	}
	feed := func() []string {
		t.Helper()
		var out []string
		for _, a := range must(st.ListActivity(ctx, "SHOP", 50)) {
			if strings.Contains(a.Summary, "retry-probe") {
				out = append(out, a.Kind)
			}
		}
		return out
	}

	if s, n := record(store.CheckResult{OK: true}); s != "up" || n != 0 {
		t.Fatalf("first success = %s/%d", s, n)
	}
	for i := 1; i <= store.EndpointRetries; i++ {
		if s, n := record(fail); s != "warning" || n != i {
			t.Fatalf("failure %d = %s/%d, want warning/%d", i, s, n, i)
		}
	}
	got := must(st.EndpointByID(ctx, e.ID))
	if got.LastStatus != "warning" || got.FailStreak != store.EndpointRetries || got.RetryLimit != store.EndpointRetries || got.NextCheckAt == nil {
		t.Fatalf("warning endpoint = %+v", got)
	}
	if f := feed(); len(f) != 0 {
		t.Fatalf("warning reached the feed: %v", f)
	}
	p := must(st.ProjectByKey(ctx, store.TodayFor("UTC"), "SHOP"))
	if p.Stats.EndpointsWarning < 1 {
		t.Fatalf("stats = %+v", p.Stats)
	}
	if w := must(st.WarningEndpoints(ctx)); !hasEndpoint(w, e.ID) {
		t.Fatalf("warning list misses it: %+v", w)
	}

	if s, n := record(fail); s != "down" || n != store.EndpointRetries+1 {
		t.Fatalf("last retry = %s/%d", s, n)
	}
	if s, _ := record(fail); s != "down" {
		t.Fatalf("down endpoint failing again = %s", s)
	}
	if f := feed(); len(f) != 1 || f[0] != "endpoint.down" {
		t.Fatalf("feed after down = %v", f)
	}
	if d := must(st.DownEndpoints(ctx)); !hasEndpoint(d, e.ID) {
		t.Fatalf("down list misses it")
	}

	if s, n := record(store.CheckResult{OK: true}); s != "up" || n != 0 {
		t.Fatalf("recovery = %s/%d", s, n)
	}
	if f := feed(); len(f) != 2 || f[0] != "endpoint.up" {
		t.Fatalf("feed after recovery = %v", f)
	}

	// A warning that clears is not news.
	record(fail)
	record(store.CheckResult{OK: true})
	if f := feed(); len(f) != 2 {
		t.Fatalf("cleared warning reached the feed: %v", f)
	}

	// A new URL resets the verdict and the streak.
	record(fail)
	must(st.UpdateEndpoint(ctx, e.ID, store.Patch{"url": []byte(`"https://example.com/other"`)}))
	got = must(st.EndpointByID(ctx, e.ID))
	if got.LastStatus != "unknown" || got.FailStreak != 0 || got.NextCheckAt != nil {
		t.Fatalf("after url change = %+v", got)
	}
}

func hasEndpoint(list []store.Endpoint, id int64) bool {
	for _, e := range list {
		if e.ID == id {
			return true
		}
	}
	return false
}

func TestRecheckDelay(t *testing.T) {
	if store.RecheckDelay(0) != store.RecheckDelay(1) || store.RecheckDelay(1) >= store.RecheckDelay(2) ||
		store.RecheckDelay(9) != store.RecheckDelay(2) {
		t.Fatalf("delays: %v %v %v", store.RecheckDelay(1), store.RecheckDelay(2), store.RecheckDelay(9))
	}
}

// Re-running the rebuild keeps every endpoint, its id and its check history,
// and gives an endpoint that is already down a used-up streak.
func TestEndpointRetriesMigrationKeepsRows(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	e := must(st.CreateEndpoint(ctx, "SHOP", store.EndpointInput{Name: "kept", URL: "https://example.com/kept"}))
	for i := 0; i <= store.EndpointRetries; i++ {
		_, _, err := st.RecordCheck(ctx, *e, store.CheckResult{Error: "timed out"})
		ok(t, err)
	}
	ok(t, exec(st, `UPDATE endpoints SET fail_streak = 0 WHERE id = $1`, e.ID))
	before := len(must(st.ListEndpoints(ctx, 0)))

	ok(t, exec(st, `DELETE FROM schema_migrations WHERE name = '0013_endpoint_retries.sql'`))
	ok(t, db.Migrate(ctx, st.DB))

	if after := len(must(st.ListEndpoints(ctx, 0))); after != before {
		t.Fatalf("endpoints %d → %d", before, after)
	}
	got := must(st.EndpointByID(ctx, e.ID))
	if got.LastStatus != "down" || got.FailStreak != store.EndpointRetries+1 || got.URL != e.URL {
		t.Fatalf("after migration = %+v", got)
	}
	if n := len(must(st.EndpointChecks(ctx, e.ID, 24))); n != store.EndpointRetries+1 {
		t.Fatalf("checks = %d", n)
	}
	// Still cascades: the rebuilt table is the one endpoint_checks points at.
	ok(t, st.DeleteEndpoint(ctx, e.ID))
	var orphans int
	ok(t, st.DB.QueryRow(ctx, `SELECT count(*) FROM endpoint_checks WHERE endpoint_id = $1`, e.ID).Scan(&orphans))
	if orphans != 0 {
		t.Fatalf("orphaned checks = %d", orphans)
	}
}

func exec(st *store.Store, q string, args ...any) error {
	_, err := st.DB.Exec(context.Background(), q, args...)
	return err
}
