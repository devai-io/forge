package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Keeping checkouts current. Every pull_interval (default 30m, "0" = off) the
// scanning machine fetches each registered repo and fast-forwards it when
// that cannot lose or tangle anything:
//
//   - never while a run is working in the repo, or a merge/rebase is in progress;
//   - never with tracked local changes (untracked files are fine — a
//     fast-forward refuses to overwrite them anyway);
//   - never when the branch has local commits (ahead): that is someone's
//     unpushed work, and --ff-only would fail on divergence regardless;
//   - only the checked-out branch, only from its upstream, only --ff-only.
//
// The fetch always happens, so "behind" in Forge is measured against the
// remote as of the last sync rather than whenever someone last fetched.

// syncResult is reported with the repo's scan until the next sync.
type syncResult struct {
	At     string `json:"at"`
	Result string `json:"result"` // up_to_date | pulled | skipped | error
	Detail string `json:"detail"`
	Pulled int    `json:"pulled"` // commits fast-forwarded in this sync
}

func syncRepo(ctx context.Context, dir string, busy bool) syncResult {
	res := syncResult{At: time.Now().UTC().Format(time.RFC3339)}
	skip := func(why string) syncResult { res.Result, res.Detail = "skipped", why; return res }
	if busy {
		return skip("a run is working in this repo")
	}
	if op := gitOperation(ctx, dir); op != "" {
		return skip(op + " in progress")
	}

	fctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if _, err := git(fctx, dir, "fetch", "--prune", "--quiet"); err != nil {
		res.Result, res.Detail = "error", "fetch: "+firstLine(err.Error())
		return res
	}

	status, err := git(ctx, dir, "status", "--porcelain=v1", "--branch")
	if err != nil {
		res.Result, res.Detail = "error", firstLine(err.Error())
		return res
	}
	header, _, _ := strings.Cut(status, "\n")
	branch, ahead, behind, dirty, _ := parseStatus(status)
	switch {
	case strings.HasPrefix(header, "## HEAD (no branch)"):
		return skip("detached HEAD")
	case !strings.Contains(header, "..."):
		return skip("branch " + branch + " has no upstream")
	case behind == 0:
		res.Result = "up_to_date"
		if ahead > 0 {
			res.Detail = fmt.Sprintf("%d local commit%s not pushed", ahead, plural(ahead))
		}
		return res
	case dirty > 0:
		return skip(fmt.Sprintf("%d local change%s; %d commit%s to pull", dirty, plural(dirty), behind, plural(behind)))
	case ahead > 0:
		return skip(fmt.Sprintf("diverged: %d local and %d remote commit%s", ahead, behind, plural(behind)))
	}
	if _, err := git(ctx, dir, "merge", "--ff-only", "--quiet", "@{upstream}"); err != nil {
		res.Result, res.Detail = "error", "fast-forward: "+firstLine(err.Error())
		return res
	}
	res.Result, res.Pulled = "pulled", behind
	res.Detail = fmt.Sprintf("fast-forwarded %d commit%s", behind, plural(behind))
	return res
}

// gitOperation names an unfinished merge/rebase/cherry-pick/revert, if any.
func gitOperation(ctx context.Context, dir string) string {
	gitDir, err := git(ctx, dir, "rev-parse", "--git-dir")
	if err != nil {
		return ""
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	for file, op := range map[string]string{"MERGE_HEAD": "a merge", "rebase-merge": "a rebase", "rebase-apply": "a rebase",
		"CHERRY_PICK_HEAD": "a cherry-pick", "REVERT_HEAD": "a revert"} {
		if _, err := os.Stat(filepath.Join(gitDir, file)); err == nil {
			return op
		}
	}
	return ""
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
