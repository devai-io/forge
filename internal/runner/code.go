package runner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// VS Code on the master: `code serve-web` on localhost (with its own
// connection token), and a gateway on the tailnet address that forge-api
// proxies /code/ to. The gateway answers only requests carrying
// hex(sha256(runner token)) — the hash forge-api stores for this runner — and
// adds VS Code's token itself, so neither secret reaches the browser.

type CodeConfig struct {
	Enabled bool   `json:"enabled"`
	Listen  string `json:"listen"`   // private address the server can reach, e.g. "10.0.0.5:7422"
	Port    int    `json:"port"`     // local port for serve-web (default 18765)
	Command string `json:"command"`  // the VS Code CLI (default "code")
	DataDir string `json:"data_dir"` // server data: extensions, settings (default ~/.config/forge/vscode)
}

func (r *Runner) codeEnabled() bool {
	if !r.cfg.Code.Enabled || r.cfg.Code.Listen == "" {
		return false
	}
	_, err := exec.LookPath(r.cfg.Code.Command)
	return err == nil
}

type codeServer struct {
	cfg      CodeConfig
	secret   string // what forge-api must present
	vsToken  string // serve-web's own connection token
	tokenDir string

	mu  sync.Mutex
	cmd *exec.Cmd
}

func (r *Runner) runCode(ctx context.Context) {
	c := r.cfg.Code
	sum := sha256.Sum256([]byte(r.cfg.Token))
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	cs := &codeServer{cfg: c, secret: hex.EncodeToString(sum[:]), vsToken: hex.EncodeToString(b),
		tokenDir: Home()}
	if err := os.MkdirAll(cs.tokenDir, 0o700); err != nil {
		slog.Error("vscode: token dir", "err", err)
		return
	}
	go cs.supervise(ctx)

	upstream, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", c.Port))
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			// Keep the Host forge-api passed on (the public host): VS Code
			// builds the address its WebSockets dial from it.
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Del("X-Forge-Gateway")
			// The theme hint is ours; and the root document must arrive
			// uncompressed so it can be rewritten (see codetheme.go).
			if theme := stripThemeParam(pr.Out); theme != "" {
				pr.Out.Header.Del("Accept-Encoding")
				pr.Out.Header.Set("X-Forge-Theme", theme)
			}
			cookie := pr.In.Header.Get("Cookie")
			if cookie != "" {
				cookie += "; "
			}
			pr.Out.Header.Set("Cookie", cookie+"vscode-tkn="+cs.vsToken)
		},
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			return rewriteTheme(resp, resp.Request.Header.Get("X-Forge-Theme"))
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprintf(w, "VS Code is starting on this machine — reload in a few seconds (%v)", err)
		},
	}
	srv := &http.Server{
		Addr:              c.Listen,
		ReadHeaderTimeout: 15 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			got := req.Header.Get("X-Forge-Gateway")
			if subtle.ConstantTimeCompare([]byte(got), []byte(cs.secret)) != 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			proxy.ServeHTTP(w, req)
		}),
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("vscode gateway listening", "addr", c.Listen)
	for ctx.Err() == nil {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// The private address may not exist yet right after boot.
			slog.Warn("vscode gateway", "err", err)
			sleep(ctx, 15*time.Second)
			continue
		}
		return
	}
}

// supervise keeps `code serve-web` running for as long as the runner runs.
func (cs *codeServer) supervise(ctx context.Context) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		tokenFile := filepath.Join(cs.tokenDir, "vscode-token")
		if err := os.WriteFile(tokenFile, []byte(cs.vsToken), 0o600); err != nil {
			slog.Error("vscode: token file", "err", err)
			return
		}
		cmd := exec.Command(cs.cfg.Command, "serve-web",
			"--host", "127.0.0.1", "--port", strconv.Itoa(cs.cfg.Port),
			"--connection-token-file", tokenFile,
			"--server-base-path", "/code",
			"--server-data-dir", cs.cfg.DataDir,
			"--accept-server-license-terms", "--disable-telemetry")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Env = os.Environ()
		start := time.Now()
		if err := cmd.Start(); err != nil {
			slog.Error("vscode: start", "err", err)
			sleep(ctx, backoff)
			continue
		}
		cs.mu.Lock()
		cs.cmd = cmd
		cs.mu.Unlock()
		slog.Info("vscode serve-web started", "port", cs.cfg.Port)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			return
		case err := <-done:
			slog.Warn("vscode serve-web exited", "err", err)
			if time.Since(start) > time.Minute {
				backoff = 5 * time.Second
			}
			sleep(ctx, backoff)
			backoff = min(backoff*2, 2*time.Minute)
		}
	}
}

// ── Workspaces ────────────────────────────────────────────────────────────

var workspaceName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,60}$`)

// codeFolder checks a folder is inside allowed_roots (after path_map) and
// returns this machine's path for it.
func (r *Runner) codeFolder(folder string) (string, error) {
	p, ok := r.cfg.Allowed(folder)
	if !ok {
		return "", fmt.Errorf("%s is not inside this runner's allowed_roots or does not exist", folder)
	}
	return p, nil
}

// codeWorkspace writes a multi-root .code-workspace for a project and returns
// its path. Regenerated on every open, so it follows the project's repos.
func (r *Runner) codeWorkspace(name string, raw json.RawMessage) (string, error) {
	if !workspaceName.MatchString(name) {
		return "", errors.New("invalid workspace name")
	}
	var folders []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &folders); err != nil {
		return "", err
	}
	type wsFolder struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	var out []wsFolder
	for _, f := range folders {
		if p, ok := r.cfg.Allowed(f.Path); ok {
			out = append(out, wsFolder{Name: f.Name, Path: p})
		}
	}
	if len(out) == 0 {
		return "", errors.New("none of the project's repos exist on this machine")
	}
	dir := filepath.Join(Home(), "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	doc, _ := json.MarshalIndent(map[string]any{"folders": out, "settings": map[string]any{}}, "", "  ")
	path := filepath.Join(dir, name+".code-workspace")
	return path, os.WriteFile(path, doc, 0o600)
}

// portFree reports whether nothing listens on the local port yet — used to
// warn when another serve-web (a manual one) already holds it.
func portFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}
