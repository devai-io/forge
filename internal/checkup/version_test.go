package checkup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devai-io/forge/internal/store"
)

func TestOlderVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.1.40", "v0.1.42", true},
		{"0.1.40", "v0.1.42", true},
		{"v0.1.42", "v0.1.42", false},
		{"v0.2.0", "v0.1.42", false},
		{"v0.1.9", "v0.1.10", true},
		{"v0.1.42-3-gabc", "v0.1.42", false},
		{"dev", "v0.1.42", false},
		{"v0.1.40", "dev", false},
	}
	for _, c := range cases {
		if got := olderVersion(c.a, c.b); got != c.want {
			t.Errorf("olderVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestVersionChecks(t *testing.T) {
	r := &Runner{Version: "v0.1.42"}
	items := r.versionChecks([]store.Runner{{Name: "desk", Version: "v0.1.42"}, {Name: "laptop", Version: "v0.1.40"}, {Name: "new", Version: ""}})
	if len(items) != 1 || items[0].Key != "runner-version:laptop" || items[0].Severity != "warn" {
		t.Fatalf("items = %+v", items)
	}
	if (&Runner{Version: "dev"}).versionChecks([]store.Runner{{Name: "laptop", Version: "v0.1.1"}}) != nil {
		t.Fatal("a dev server compares nothing")
	}
}

func TestForgeBackupCheck(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{BackupDir: dir}
	got := r.forgeBackup()
	if got == nil || got.Severity != "fail" || got.Key != "backup:forge" {
		t.Fatalf("no snapshot: %+v", got)
	}
	path := filepath.Join(dir, "forge-2026-11-01.db")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if got := r.forgeBackup(); got == nil || got.Severity != "fail" {
		t.Fatalf("stale snapshot: %+v", got)
	}
	if err := os.Chtimes(path, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := r.forgeBackup(); got != nil {
		t.Fatalf("fresh snapshot: %+v", got)
	}
}
