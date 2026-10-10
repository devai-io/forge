package runner

import (
	"slices"
	"testing"
)

func TestPermissionArgs(t *testing.T) {
	if got := permissionArgs("acceptEdits"); !slices.Equal(got, []string{"--permission-mode", "acceptEdits"}) {
		t.Fatalf("acceptEdits = %v", got)
	}
	want := []string{"--permission-mode", "bypassPermissions", "--dangerously-skip-permissions"}
	if got := permissionArgs("bypassPermissions"); !slices.Equal(got, want) {
		t.Fatalf("bypassPermissions = %v", got)
	}
}
