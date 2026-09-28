package runner

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// repoRef is what the server tells the runner to look at.
type repoRef struct {
	ID            int64  `json:"id"`
	ProjectKey    string `json:"project_key"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	DefaultBranch string `json:"default_branch"`
}

type gitHead struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	At      string `json:"at"`
}

type repoScan struct {
	RepoID       int64       `json:"repo_id"`
	Branch       string      `json:"branch"`
	Dirty        int         `json:"dirty"`
	Untracked    int         `json:"untracked"`
	Ahead        int         `json:"ahead"`
	Behind       int         `json:"behind"`
	Head         *gitHead    `json:"head"`
	Commits7d    int         `json:"commits_7d"`
	LastCommitAt *string     `json:"last_commit_at"`
	Error        string      `json:"error"`
	CI           *ciStatus   `json:"ci"`
	Sync         *syncResult `json:"sync"`
}

// scanRepo reads a repo's state with plain git, without touching the network:
// ahead/behind are against whatever was last fetched. ok is false when the
// path is not on this machine — another runner owns it, so nothing is
// reported rather than overwriting a good scan with "not found".
func scanRepo(ctx context.Context, path string, id int64) (repoScan, bool) {
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		return repoScan{}, false
	}
	s := repoScan{RepoID: id}
	status, err := git(ctx, path, "status", "--porcelain=v1", "--branch")
	if err != nil {
		s.Error = firstLine(err.Error())
		return s, true
	}
	s.Branch, s.Ahead, s.Behind, s.Dirty, s.Untracked = parseStatus(status)

	if out, err := git(ctx, path, "log", "-1", "--format=%H%x1f%s%x1f%an%x1f%cI"); err == nil {
		if parts := strings.Split(strings.TrimSpace(out), "\x1f"); len(parts) == 4 {
			s.Head = &gitHead{Hash: parts[0], Subject: parts[1], Author: parts[2], At: parts[3]}
			s.LastCommitAt = &parts[3]
		}
	}
	since := time.Now().Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	if out, err := git(ctx, path, "rev-list", "--count", "--since="+since, "HEAD"); err == nil {
		s.Commits7d, _ = strconv.Atoi(strings.TrimSpace(out))
	}
	return s, true
}

var aheadRe = regexp.MustCompile(`ahead (\d+)`)
var behindRe = regexp.MustCompile(`behind (\d+)`)

// parseStatus reads `git status --porcelain=v1 --branch`: the "## " header
// names the branch and its divergence; "??" lines are untracked files, every
// other line a tracked change (modified, staged, deleted, renamed).
func parseStatus(out string) (branch string, ahead, behind, dirty, untracked int) {
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "?? ") {
			untracked++
			continue
		}
		if !strings.HasPrefix(line, "## ") {
			dirty++
			continue
		}
		head := strings.TrimPrefix(line, "## ")
		if b, ok := strings.CutPrefix(head, "No commits yet on "); ok {
			head = b
		}
		branch, _, _ = strings.Cut(head, "...")
		branch, _, _ = strings.Cut(branch, " ")
		if m := aheadRe.FindStringSubmatch(head); m != nil {
			ahead, _ = strconv.Atoi(m[1])
		}
		if m := behindRe.FindStringSubmatch(head); m != nil {
			behind, _ = strconv.Atoi(m[1])
		}
	}
	return
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok { // local commands; network ones bring their own
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "--no-optional-locks"}, args...)...)
	// Never prompt for credentials or open a pager from a daemon; SSH fails
	// instead of waiting for a passphrase or a host-key question.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "LC_ALL=C")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=15")
	}
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", &gitError{msg: strings.TrimSpace(string(ee.Stderr))}
		}
		return "", err
	}
	return string(out), nil
}

type gitError struct{ msg string }

func (e *gitError) Error() string { return e.msg }

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
