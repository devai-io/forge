package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeSessionLaunchKeepsProjectDirectory(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		for _, prompt := range []string{"", "Read the attached image"} {
			t.Run(strings.Join([]string{map[bool]string{false: "new", true: "reuse"}[reuse], map[bool]string{false: "empty", true: "prompt"}[prompt != ""]}, "/"), func(t *testing.T) {
				root := t.TempDir()
				root, _ = filepath.EvalSymlinks(root)
				project := filepath.Join(root, "project's repos")
				if err := os.Mkdir(project, 0700); err != nil {
					t.Fatal(err)
				}
				cache := t.TempDir()
				t.Setenv("XDG_CACHE_HOME", cache)
				capture := filepath.Join(root, "launch")
				pwd := filepath.Join(root, "pwd")
				tmux := filepath.Join(root, "tmux")
				claude := filepath.Join(root, "claude")
				exists := "1"
				if reuse {
					exists = "0"
				}
				script := "#!/bin/sh\ncase \"$1\" in\nhas-session) exit " + exists + ";;\nsend-keys) if [ \"$4\" = -l ]; then printf '%s' \"$5\" > " + shellQuote(capture) + "; fi;;\nesac\n"
				if err := os.WriteFile(tmux, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(claude, []byte("#!/bin/sh\npwd > "+shellQuote(pwd)+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
				r := &Runner{cfg: &Config{TmuxPath: tmux, ClaudePath: claude, AllowedRoots: []string{root}}}
				if err := r.createSession(context.Background(), controlMsg{Session: "F-Shop", Cwd: project, Start: "claude", Reuse: reuse, Prompt: prompt}); err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(capture)
				if err != nil {
					t.Fatal(err)
				}
				line := string(raw)
				if strings.Contains(line, "--add-dir") || !strings.Contains(line, "--allowedTools 'Read(/") {
					t.Fatalf("attachment became a workspace: %s", line)
				}
				// Simulate a shell rc file that changed directories after tmux started.
				cmd := exec.Command("sh", "-c", line)
				cmd.Dir = cache
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v", out, err)
				}
				got, err := os.ReadFile(pwd)
				if err != nil || strings.TrimSpace(string(got)) != project {
					t.Fatalf("cwd = %q, err %v", got, err)
				}
			})
		}
	}
}

func TestUploadRuleRejectsPermissionMetacharacters(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache[unsafe]"))
	// UserCacheDir follows HOME on macOS; the launch integration also exercises
	// the platform's real cache path.
	if dir, err := os.UserCacheDir(); err == nil && strings.Contains(dir, "[unsafe]") && uploadReadRule() != "" {
		t.Fatal("unsafe permission pattern accepted")
	}
}
