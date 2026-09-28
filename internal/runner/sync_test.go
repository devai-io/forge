package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitFixture is a bare "origin", the checkout Forge keeps current, and a
// second clone that pushes new commits to origin.
type gitFixture struct {
	t               *testing.T
	checkout, other string
}

func (f *gitFixture) git(dir string, args ...string) {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// commit adds a file named after msg in dir and commits it.
func (f *gitFixture) commit(dir, msg string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, msg+".txt"), []byte(msg), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git(dir, "add", ".")
	f.git(dir, "commit", "-qm", msg)
}

// push lands a new commit on origin from the other clone.
func (f *gitFixture) push(msg string) {
	f.t.Helper()
	f.commit(f.other, msg)
	f.git(f.other, "push", "-q")
}

func newGitFixture(t *testing.T) *gitFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_AUTHOR_NAME": "t",
		"GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com"} {
		t.Setenv(k, v)
	}
	root := t.TempDir()
	f := &gitFixture{t: t, checkout: filepath.Join(root, "checkout"), other: filepath.Join(root, "other")}
	origin := filepath.Join(root, "origin.git")
	f.git(root, "init", "-q", "--bare", "-b", "main", origin)
	f.git(root, "clone", "-q", origin, f.checkout)
	f.git(f.checkout, "checkout", "-q", "-b", "main")
	f.commit(f.checkout, "first")
	f.git(f.checkout, "push", "-q", "-u", "origin", "main")
	f.git(root, "clone", "-q", origin, f.other)
	return f
}

func TestSyncRepo(t *testing.T) {
	ctx := context.Background()
	f := newGitFixture(t)
	dir := f.checkout

	if r := syncRepo(ctx, dir, false); r.Result != "up_to_date" {
		t.Fatalf("fresh: %+v", r)
	}
	f.push("second")
	f.push("third")
	if r := syncRepo(ctx, dir, true); r.Result != "skipped" || !strings.Contains(r.Detail, "run") {
		t.Fatalf("busy: %+v", r)
	}
	// Tracked local change: fetched, not pulled.
	os.WriteFile(filepath.Join(dir, "first.txt"), []byte("edited\n"), 0o644)
	if r := syncRepo(ctx, dir, false); r.Result != "skipped" || !strings.Contains(r.Detail, "2 commits to pull") {
		t.Fatalf("dirty: %+v", r)
	}
	os.WriteFile(filepath.Join(dir, "first.txt"), []byte("first"), 0o644)
	// Untracked files do not block a fast-forward.
	os.WriteFile(filepath.Join(dir, "notes.tmp"), []byte("x"), 0o644)
	if r := syncRepo(ctx, dir, false); r.Result != "pulled" || r.Pulled != 2 {
		t.Fatalf("pull: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "third.txt")); err != nil {
		t.Fatal("the pulled commit is not in the working tree")
	}
	// Local unpushed work plus new remote commits: diverged, left alone.
	f.commit(dir, "local")
	f.push("fourth")
	if r := syncRepo(ctx, dir, false); r.Result != "skipped" || !strings.Contains(r.Detail, "diverged") {
		t.Fatalf("diverged: %+v", r)
	}
	// No upstream.
	f.git(dir, "checkout", "-q", "-b", "feature")
	if r := syncRepo(ctx, dir, false); r.Result != "skipped" || !strings.Contains(r.Detail, "no upstream") {
		t.Fatalf("no upstream: %+v", r)
	}
	// A merge in progress.
	os.WriteFile(filepath.Join(dir, ".git", "MERGE_HEAD"), []byte("x"), 0o644)
	if r := syncRepo(ctx, dir, false); r.Result != "skipped" || !strings.Contains(r.Detail, "merge") {
		t.Fatalf("merge: %+v", r)
	}
}
