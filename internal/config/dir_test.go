package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultLocations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("FORGE_HOME", "")
	t.Setenv("FORGE_DATA_DIR", "")

	want := filepath.Join(home, ".config", "forge")
	if got := DefaultHome(); got != want {
		t.Fatalf("default home %s, want %s", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	if got := Dir(); got != filepath.Join(home, "xdg", "forge") {
		t.Fatalf("XDG dir %s", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")

	// A v0.1 install keeps its ~/.forge until the database is moved.
	old := filepath.Join(home, ".forge")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "forge.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DefaultHome(); got != old {
		t.Fatalf("legacy home %s, want %s", got, old)
	}
	if err := os.MkdirAll(want, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(want, "forge.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DefaultHome(); got != want {
		t.Fatalf("once moved, home %s, want %s", got, want)
	}
	t.Setenv("FORGE_HOME", "/srv/forge")
	if got := DefaultHome(); got != "/srv/forge" {
		t.Fatalf("FORGE_HOME ignored: %s", got)
	}
}
