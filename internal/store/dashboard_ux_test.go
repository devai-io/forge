package store_test

import (
	"context"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

func TestProjectStatsCountFailingCI(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	var projectID, repoID int64
	if err := st.DB.QueryRow(ctx, `SELECT project_id, id FROM repos ORDER BY id LIMIT 1`).Scan(&projectID, &repoID); err != nil {
		t.Fatal(err)
	}
	set := func(git string) {
		t.Helper()
		if _, err := st.DB.Exec(ctx, `UPDATE repos SET git = $1 WHERE id = $2`, git, repoID); err != nil {
			t.Fatal(err)
		}
	}
	failing := func() int {
		t.Helper()
		ps, err := st.ListProjects(ctx, "2026-11-08", true)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			if p.ID == projectID {
				return p.Stats.CIFailing
			}
		}
		t.Fatal("project not listed")
		return 0
	}
	set(`{"ci":{"status":"completed","conclusion":"failure","workflow":"build"}}`)
	if n := failing(); n != 1 {
		t.Fatalf("failed run: ci_failing = %d, want 1", n)
	}
	set(`{"ci":{"status":"in_progress","conclusion":""}}`)
	if n := failing(); n != 0 {
		t.Fatalf("running: ci_failing = %d, want 0", n)
	}
	set(`{"ci":{"status":"completed","conclusion":"skipped"}}`)
	if n := failing(); n != 0 {
		t.Fatalf("skipped: ci_failing = %d, want 0", n)
	}
}

func TestDashboardCheckupHighlightsSkipServices(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	id, err := st.StartCheckup(ctx, "2026-11-08", "manual")
	if err != nil {
		t.Fatal(err)
	}
	items := []store.CheckItem{
		{Key: "host:web1", Category: "hosts", Severity: "fail", Title: "web1 is not reporting metrics"},
		{Key: "endpoint:1", Category: "endpoints", Severity: "fail", Title: "shop is down"},
		{Key: "repo:1:ci", Category: "ci", Severity: "fail", Title: "shop_api: CI failure (build)"},
		{Key: "alert:x", Category: "alerts", Severity: "warn", Title: "DiskFilling on web1"},
		{Key: "backup:forge", Category: "backups", Severity: "warn", Title: "No Forge backup"},
	}
	if _, err := st.FinishCheckup(ctx, id, "fail", items); err != nil {
		t.Fatal(err)
	}
	d, err := st.Dashboard(ctx, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range d.CheckupOpen {
		got = append(got, it.Key)
	}
	if len(got) != 2 || got[0] != "repo:1:ci" || got[1] != "backup:forge" {
		t.Fatalf("checkup_open = %v, want [repo:1:ci backup:forge]", got)
	}
}
