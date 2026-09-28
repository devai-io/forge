package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

type Runner struct {
	cfg      *Config
	client   *client
	hostname string

	mu      sync.Mutex
	running map[int64]context.CancelFunc
	repos   []repoRef
	slots   chan struct{}
	rescan  chan struct{}

	ciMu    sync.Mutex
	ciCache map[int64]ciEntry

	// busy counts the runs working in each repo directory: no sync there.
	busy     map[string]int
	lastSync time.Time
	syncs    map[int64]*syncResult // the latest sync per repo, reported with every scan

	scanOK bool
	role   string
}

func New(cfg *Config) *Runner {
	host, _ := os.Hostname()
	return &Runner{
		cfg:      cfg,
		client:   newClient(cfg.APIURL, cfg.Token),
		hostname: host,
		running:  map[int64]context.CancelFunc{},
		slots:    make(chan struct{}, cfg.MaxConcurrent),
		rescan:   make(chan struct{}, 1),
		ciCache:  map[int64]ciEntry{},
		busy:     map[string]int{},
		syncs:    map[int64]*syncResult{},
	}
}

// Run blocks until ctx is cancelled. Three loops: heartbeat (liveness,
// capabilities, cancellations, the repo list), claim (take work while there
// is a free slot), and scan (report git state).
func (r *Runner) Run(ctx context.Context) error {
	_, claudeErr := exec.LookPath(r.cfg.ClaudePath)
	slog.Info("forge runner starting", "api", r.cfg.APIURL, "host", r.hostname, "version", Version,
		"claude", claudeErr == nil, "terminal", r.terminalEnabled(), "code", r.codeEnabled(), "modes", r.cfg.PermissionModes, "commands", r.cfg.CommandNames(),
		"roots", r.cfg.AllowedRoots)
	if err := r.heartbeat(ctx); err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.status == 401 {
			return fmt.Errorf("the API rejected this runner's token: %w", err)
		}
		slog.Warn("first heartbeat failed; retrying in the background", "err", err)
	}

	var wg sync.WaitGroup
	wg.Add(3)
	// The control socket carries terminals and VS Code workspace requests.
	if r.terminalEnabled() || r.codeEnabled() {
		wg.Add(1)
		go func() { defer wg.Done(); r.controlLoop(ctx) }()
	}
	if r.codeEnabled() {
		if !portFree(r.cfg.Code.Port) {
			slog.Warn("vscode port already in use; is another serve-web running?", "port", r.cfg.Code.Port)
		}
		wg.Add(1)
		go func() { defer wg.Done(); r.runCode(ctx) }()
	}
	go func() { defer wg.Done(); r.heartbeatLoop(ctx) }()
	go func() { defer wg.Done(); r.claimLoop(ctx) }()
	go func() { defer wg.Done(); r.scanLoop(ctx) }()
	wg.Wait()

	// Let in-flight runs report before exiting (their contexts are already
	// cancelled, so this is the kill + final report, not the whole run).
	for i := 0; i < cap(r.slots); i++ {
		r.slots <- struct{}{}
	}
	return nil
}

func (r *Runner) capabilities() map[string]any {
	_, err := exec.LookPath(r.cfg.ClaudePath)
	details := []map[string]any{}
	for _, name := range r.cfg.CommandNames() {
		c := r.cfg.Commands[name]
		details = append(details, map[string]any{"name": name, "description": c.Description,
			"repos": c.Repos, "confirm": c.Confirm})
	}
	return map[string]any{
		"claude":           err == nil,
		"permission_modes": r.cfg.PermissionModes,
		"commands":         r.cfg.CommandNames(),
		"command_details":  details,
		"max_concurrent":   r.cfg.MaxConcurrent,
		"ci":               r.ciEnabled(),
		"terminal":         r.terminalEnabled(),
		"code":             r.codeEnabled(),
		"code_gateway":     r.codeGateway(),
	}
}

func (r *Runner) codeGateway() string {
	if !r.codeEnabled() {
		return ""
	}
	return "http://" + r.cfg.Code.Listen
}

func (r *Runner) terminalEnabled() bool {
	if !r.cfg.Terminal {
		return false
	}
	_, err := exec.LookPath(r.cfg.TmuxPath)
	return err == nil
}

func (r *Runner) heartbeat(ctx context.Context) error {
	r.mu.Lock()
	ids := make([]int64, 0, len(r.running))
	for id := range r.running {
		ids = append(ids, id)
	}
	r.mu.Unlock()

	var resp struct {
		Cancel []int64   `json:"cancel"`
		Repos  []repoRef `json:"repos"`
		Role   string    `json:"role"`
		Scan   *bool     `json:"scan"`
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	body := map[string]any{
		"hostname":     r.hostname,
		"os":           runtime.GOOS + "/" + runtime.GOARCH,
		"version":      Version,
		"capabilities": r.capabilities(),
		"running":      ids,
	}
	if r.terminalEnabled() {
		body["tmux"] = r.listTmux(ctx)
	}
	if _, err := r.client.post(ctx, "/api/runner/heartbeat", body, &resp); err != nil {
		return err
	}

	r.mu.Lock()
	first := r.repos == nil
	r.repos = resp.Repos
	// Only the master scans; an older API that does not say, means "yes".
	r.scanOK = resp.Scan == nil || *resp.Scan
	r.role = resp.Role
	if r.repos == nil {
		r.repos = []repoRef{}
	}
	for _, id := range resp.Cancel {
		if stop, ok := r.running[id]; ok {
			slog.Info("cancelling run", "run", id)
			stop()
		}
	}
	r.mu.Unlock()
	if first {
		select {
		case r.rescan <- struct{}{}:
		default:
		}
	}
	return nil
}

func (r *Runner) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.heartbeat(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("heartbeat failed", "err", err)
			}
		}
	}
}

func (r *Runner) claimLoop(ctx context.Context) {
	backoff := time.Second
	for {
		// Wait for a free slot before asking for work.
		select {
		case <-ctx.Done():
			return
		case r.slots <- struct{}{}:
		}
		var resp struct {
			Run claimedRun `json:"run"`
		}
		got, err := r.client.post(ctx, "/api/runner/claim", nil, &resp)
		if err != nil || !got {
			<-r.slots
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				slog.Warn("claim failed", "err", err)
				sleep(ctx, backoff)
				backoff = min(backoff*2, time.Minute)
			}
			continue
		}
		backoff = time.Second
		go r.handle(ctx, resp.Run)
	}
}

func (r *Runner) handle(parent context.Context, run claimedRun) {
	defer func() { <-r.slots }()
	ctx, cancel := context.WithCancel(parent)
	busyDir, _ := r.cfg.Allowed(run.RepoPath)
	r.mu.Lock()
	r.running[run.ID] = cancel
	r.busy[busyDir]++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.running, run.ID)
		if r.busy[busyDir]--; r.busy[busyDir] <= 0 {
			delete(r.busy, busyDir)
		}
		r.mu.Unlock()
		cancel()
	}()

	slog.Info("run started", "run", run.ID, "kind", run.Kind, "repo", run.RepoName)
	rep := r.execute(ctx, run)
	if parent.Err() != nil {
		rep.Status, rep.Error = "failed", "the runner was stopped mid-run"
	}
	slog.Info("run finished", "run", run.ID, "status", rep.Status)

	// Report even while shutting down: a fresh context, a few tries.
	for attempt := 0; attempt < 5; attempt++ {
		fctx, fcancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, err := r.client.post(fctx, fmt.Sprintf("/api/runner/runs/%d/finish", run.ID), rep, nil)
		fcancel()
		if err == nil {
			break
		}
		var ae *apiError
		if errors.As(err, &ae) && ae.status < 500 {
			slog.Error("finish rejected", "run", run.ID, "err", err)
			break
		}
		time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
	}
	// A finished run may have committed; refresh the repo states soon.
	select {
	case r.rescan <- struct{}{}:
	default:
	}
}

func (r *Runner) scanLoop(ctx context.Context) {
	t := time.NewTicker(r.cfg.scanEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.rescan:
		}
		r.scan(ctx)
	}
}

func (r *Runner) scan(ctx context.Context) {
	r.mu.Lock()
	repos, ok := r.repos, r.scanOK
	r.mu.Unlock()
	if !ok {
		return // not the master: its view of the repos is the one Forge keeps
	}
	doSync := r.cfg.pullEvery > 0 && time.Since(r.lastSync) >= r.cfg.pullEvery
	if doSync {
		r.lastSync = time.Now()
	}
	scans := []repoScan{}
	pulled := 0
	for _, repo := range repos {
		dir, ok := r.cfg.Allowed(repo.Path)
		if !ok {
			continue
		}
		if doSync {
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				r.mu.Lock()
				busy := r.busy[dir] > 0
				r.mu.Unlock()
				res := syncRepo(ctx, dir, busy)
				r.syncs[repo.ID] = &res
				pulled += res.Pulled
				if res.Result == "pulled" || res.Result == "error" {
					slog.Info("repo sync", "repo", repo.Name, "result", res.Result, "detail", res.Detail)
				}
			}
		}
		if s, ok := scanRepo(ctx, dir, repo.ID); ok {
			s.CI = r.ciStatus(ctx, dir, repo)
			s.Sync = r.syncs[repo.ID]
			scans = append(scans, s)
		}
	}
	if doSync {
		slog.Info("repos synced", "repos", len(repos), "commits_pulled", pulled)
	}
	if len(scans) == 0 {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := r.client.post(pctx, "/api/runner/repos", map[string]any{"repos": scans}, nil); err != nil {
		slog.Warn("repo report failed", "err", err)
		return
	}
	slog.Info("repos scanned", "count", len(scans))
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
