package runner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// GitHub Actions status per repo, read with the gh CLI the machine is already
// logged into — so Forge itself never holds a GitHub token. Cached per repo
// for ci_interval; one `gh run list` per repo per interval is well inside the
// API's rate limit.

type ciStatus struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Workflow   string `json:"workflow"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	SHA        string `json:"sha"`
	At         string `json:"at"`
}

type ciEntry struct {
	at     time.Time
	status *ciStatus
}

func (r *Runner) ciEnabled() bool {
	if r.cfg.ciEvery == 0 {
		return false
	}
	_, err := exec.LookPath("gh")
	return err == nil
}

var githubRemote = regexp.MustCompile(`github\.com[:/]([^/]+)/([^/]+?)(\.git)?/?$`)

// githubSlug turns a remote URL into owner/repo, or "" if it is not GitHub.
func githubSlug(remote string) string {
	m := githubRemote.FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil {
		return ""
	}
	return m[1] + "/" + m[2]
}

func (r *Runner) ciStatus(ctx context.Context, dir string, repo repoRef) *ciStatus {
	if !r.ciEnabled() {
		return nil
	}
	r.ciMu.Lock()
	cached, ok := r.ciCache[repo.ID]
	r.ciMu.Unlock()
	if ok && time.Since(cached.at) < r.cfg.ciEvery {
		return cached.status
	}

	status := r.fetchCI(ctx, dir, repo.DefaultBranch)
	r.ciMu.Lock()
	r.ciCache[repo.ID] = ciEntry{at: time.Now(), status: status}
	r.ciMu.Unlock()
	return status
}

func (r *Runner) fetchCI(ctx context.Context, dir, branch string) *ciStatus {
	remote, err := git(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return nil
	}
	slug := githubSlug(remote)
	if slug == "" {
		return nil
	}
	if branch == "" {
		branch = "main"
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "run", "list", "-R", slug, "-b", branch, "-L", "1",
		"--json", "status,conclusion,workflowName,displayTitle,url,headSha,createdAt")
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	out, err := cmd.Output()
	if err != nil {
		return nil // no Actions, no access, or offline: say nothing rather than "failing"
	}
	var runs []struct {
		Status       string `json:"status"`
		Conclusion   string `json:"conclusion"`
		WorkflowName string `json:"workflowName"`
		DisplayTitle string `json:"displayTitle"`
		URL          string `json:"url"`
		HeadSha      string `json:"headSha"`
		CreatedAt    string `json:"createdAt"`
	}
	if json.Unmarshal(out, &runs) != nil || len(runs) == 0 {
		return nil
	}
	x := runs[0]
	sha := x.HeadSha
	if len(sha) > 7 {
		sha = sha[:7]
	}
	return &ciStatus{Status: x.Status, Conclusion: x.Conclusion, Workflow: x.WorkflowName, Title: x.DisplayTitle,
		URL: x.URL, SHA: sha, At: x.CreatedAt}
}
