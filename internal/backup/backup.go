// Package backup keeps nightly snapshots of the database in the workspace's
// backups/ folder: one file per day (forge-YYYY-MM-DD.db), the newest `keep`
// kept. A snapshot is a complete SQLite database — restoring is copying it
// back to forge.db while the server is stopped.
package backup

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/db"
)

const prefix, suffix = "forge-", ".db"

// Now writes today's snapshot (replacing an earlier one from today) and
// prunes old ones.
func Now(ctx context.Context, d *db.DB, dir string, keep int) (string, error) {
	path := filepath.Join(dir, prefix+time.Now().UTC().Format("2006-01-02")+suffix)
	if err := d.Backup(ctx, path); err != nil {
		return "", err
	}
	return path, prune(dir, keep)
}

// Latest is the newest snapshot's time, or zero.
func Latest(dir string) time.Time {
	files := list(dir)
	if len(files) == 0 {
		return time.Time{}
	}
	info, err := os.Stat(filepath.Join(dir, files[len(files)-1]))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// Loop takes a snapshot whenever the newest one is more than a day old,
// checking hourly — so a server that restarts often still gets one a day.
func Loop(ctx context.Context, d *db.DB, dir string, keep int) {
	if keep <= 0 {
		return
	}
	for {
		if time.Since(Latest(dir)) > 24*time.Hour {
			if path, err := Now(ctx, d, dir, keep); err != nil {
				slog.Error("backup failed", "err", err)
			} else {
				slog.Info("backup written", "path", path)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

func list(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, prefix) && strings.HasSuffix(n, suffix) {
			out = append(out, n)
		}
	}
	sort.Strings(out) // the date in the name sorts
	return out
}

func prune(dir string, keep int) error {
	files := list(dir)
	for len(files) > keep {
		if err := os.Remove(filepath.Join(dir, files[0])); err != nil {
			return err
		}
		files = files[1:]
	}
	return nil
}
