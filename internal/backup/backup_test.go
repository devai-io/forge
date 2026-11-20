package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A hand-made copy in backups/ must not count as a snapshot: its name sorts
// after every dated one, so it would be "the newest" for ever.
func TestOnlyDatedSnapshotsCount(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-72 * time.Hour)
	for _, n := range []string{"forge-2026-01-01.db", "forge-2026-01-02.db", "forge-20260101T120000-before-move.db", "pre-upgrade.db", "forge-notes.db"} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := os.Chtimes(filepath.Join(dir, "forge-2026-01-02.db"), now, now); err != nil {
		t.Fatal(err)
	}
	if got := list(dir); len(got) != 2 || got[1] != "forge-2026-01-02.db" {
		t.Fatalf("list = %v, want the two dated snapshots", got)
	}
	if age := time.Since(Latest(dir)); age > time.Minute {
		t.Fatalf("Latest is %v old: a stray file was taken for the newest snapshot", age)
	}
	if err := prune(dir, 1); err != nil {
		t.Fatal(err)
	}
	for n, want := range map[string]bool{"forge-2026-01-01.db": false, "forge-2026-01-02.db": true,
		"forge-20260101T120000-before-move.db": true, "pre-upgrade.db": true} {
		_, err := os.Stat(filepath.Join(dir, n))
		if (err == nil) != want {
			t.Errorf("%s: exists = %v, want %v", n, err == nil, want)
		}
	}
}
