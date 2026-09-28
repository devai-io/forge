// Package api is the HTTP surface: routing, authentication, and handlers that
// decode, call the store, and encode. docs/API.md is the contract.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/checkup"
	"github.com/devai-io/forge/internal/config"
	"github.com/devai-io/forge/internal/mail"
	"github.com/devai-io/forge/internal/monitor"
	"github.com/devai-io/forge/internal/monitoring"
	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/vault"
	"github.com/devai-io/forge/internal/webui"
)

const (
	sessionCookie = "forge_session"
	sessionTTL    = 30 * 24 * time.Hour
	csrfHeader    = "X-Forge-Client"
)

type Server struct {
	cfg      config.Config
	store    *store.Store
	mailer   *mail.Mailer
	monitor  *monitor.Monitor
	box      *vault.Box
	mon      *monitoring.Client
	checkups *checkup.Runner

	terminals *terminalHub
	codes     *codeSessions

	logins  *limiter // failed logins / password checks
	resets  *limiter // reset e-mails requested
	wakeups *notifier

	// claimWait is how long a runner's claim long-poll is held open.
	claimWait time.Duration
}

// Deps are the optional collaborators; a zero value turns the feature off
// (the vault answers vault_unavailable, monitoring is empty).
type Deps struct {
	Box      *vault.Box
	Mon      *monitoring.Client
	Checkups *checkup.Runner
}

func New(cfg config.Config, st *store.Store, m *mail.Mailer, mon *monitor.Monitor, d Deps) *Server {
	return &Server{
		cfg: cfg, store: st, mailer: m, monitor: mon, box: d.Box, mon: d.Mon, checkups: d.Checkups,
		// Eight wrong passwords per address and thirty overall per quarter
		// hour. The global cap is what actually protects a single-account
		// site from a distributed guesser; the per-address one keeps a
		// fat-fingered phone from locking out the laptop.
		logins:    newLimiter(8, 30, 15*time.Minute),
		resets:    newLimiter(5, 20, time.Hour),
		wakeups:   newNotifier(),
		terminals: newTerminalHub(),
		codes:     newCodeSessions(),
		claimWait: 25 * time.Second,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn http.HandlerFunc) { mux.HandleFunc(pattern, fn) }

	h("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	h("GET /api/status", s.status)

	// Public auth routes (still CSRF-guarded: login CSRF is a thing).
	h("POST /api/auth/login", s.web(s.login))
	h("POST /api/auth/forgot", s.web(s.forgot))
	h("POST /api/auth/reset", s.web(s.reset))

	// Session routes.
	h("POST /api/auth/logout", s.web(s.logout))
	h("GET /api/auth/me", s.authed(s.me))
	h("PATCH /api/auth/me", s.authed(s.updateMe))
	h("POST /api/auth/password", s.authed(s.changePassword))
	h("GET /api/auth/sessions", s.authed(s.listSessions))
	h("POST /api/auth/sessions/revoke-others", s.authed(s.revokeOtherSessions))
	h("GET /api/auth/security", s.authed(s.securityLog))
	h("GET /api/system", s.authed(s.systemFacts))
	h("DELETE /api/auth/sessions/{id}", s.authed(s.deleteSession))

	h("POST /api/auth/elevate", s.authed(s.elevate))
	h("POST /api/auth/totp/setup", s.authed(s.totpSetup))
	h("POST /api/auth/totp/enable", s.authed(s.totpEnable))
	h("POST /api/auth/totp/disable", s.authed(s.totpDisable))

	h("GET /api/dashboard", s.authed(s.dashboard))

	h("GET /api/vault", s.authed(s.listVault))
	h("POST /api/vault", s.authed(s.createVault))
	h("GET /api/vault/audit", s.authed(s.vaultAudit))
	h("GET /api/vault/{id}", s.authed(s.getVault))
	h("PATCH /api/vault/{id}", s.authed(s.updateVault))
	h("DELETE /api/vault/{id}", s.authed(s.elevated(s.deleteVault)))
	h("POST /api/vault/{id}/reveal", s.authed(s.elevated(s.revealVault)))
	h("GET /api/vault/{id}/file", s.authed(s.elevated(s.vaultFile)))

	h("GET /api/monitoring", s.authed(s.monitoring))

	h("GET /api/checkups", s.authed(s.listCheckups))
	h("POST /api/checkups", s.authed(s.runCheckup))
	h("GET /api/checkups/latest", s.authed(s.latestCheckup))
	h("GET /api/checkups/{id}", s.authed(s.getCheckup))
	h("PATCH /api/checkups/{id}/items/{key}", s.authed(s.updateCheckupItem))
	h("POST /api/checkups/{id}/items/{key}/task", s.authed(s.checkupItemTask))

	h("GET /api/projects", s.authed(s.listProjects))
	h("POST /api/projects", s.authed(s.createProject))
	h("GET /api/projects/{key}", s.authed(s.getProject))
	h("PATCH /api/projects/{key}", s.authed(s.updateProject))
	h("DELETE /api/projects/{key}", s.authed(s.deleteProject))
	h("GET /api/projects/{key}/activity", s.authed(s.projectActivity))
	h("PUT /api/projects/{key}/servers", s.authed(s.setProjectServers))
	h("POST /api/projects/{key}/repos", s.authed(s.createRepo))
	h("POST /api/projects/{key}/endpoints", s.authed(s.createEndpoint))
	h("PATCH /api/repos/{id}", s.authed(s.updateRepo))
	h("DELETE /api/repos/{id}", s.authed(s.deleteRepo))

	h("GET /api/endpoints", s.authed(s.listEndpoints))
	h("PATCH /api/endpoints/{id}", s.authed(s.updateEndpoint))
	h("DELETE /api/endpoints/{id}", s.authed(s.deleteEndpoint))
	h("POST /api/endpoints/{id}/check", s.authed(s.checkEndpoint))
	h("GET /api/endpoints/{id}/checks", s.authed(s.endpointChecks))

	h("GET /api/servers", s.authed(s.listServers))
	h("POST /api/servers", s.authed(s.createServer))
	h("GET /api/servers/{id}", s.authed(s.getServer))
	h("PATCH /api/servers/{id}", s.authed(s.updateServer))
	h("DELETE /api/servers/{id}", s.authed(s.deleteServer))

	h("GET /api/tasks", s.authed(s.listTasks))
	h("POST /api/tasks", s.authed(s.createTask))
	h("GET /api/tasks/{id}", s.authed(s.getTask))
	h("PATCH /api/tasks/{id}", s.authed(s.updateTask))
	h("DELETE /api/tasks/{id}", s.authed(s.deleteTask))
	h("POST /api/tasks/{id}/comments", s.authed(s.createComment))
	h("DELETE /api/comments/{id}", s.authed(s.deleteComment))

	h("GET /api/runners", s.authed(s.listRunners))
	h("POST /api/runners", s.authed(s.createRunner))
	h("POST /api/runners/{id}/rotate", s.authed(s.rotateRunner))
	h("PATCH /api/runners/{id}", s.authed(s.updateRunner))
	h("DELETE /api/runners/{id}", s.authed(s.deleteRunner))
	h("GET /api/runs", s.authed(s.listRuns))
	h("POST /api/runs", s.authed(s.createRun))
	h("GET /api/runs/{id}", s.authed(s.getRun))
	h("GET /api/runs/{id}/events", s.authed(s.runEvents))
	h("POST /api/runs/{id}/cancel", s.authed(s.cancelRun))

	h("POST /api/runner/heartbeat", s.runnerAuth(s.runnerHeartbeat))
	h("POST /api/runner/claim", s.runnerAuth(s.runnerClaim))
	h("POST /api/runner/runs/{id}/events", s.runnerAuth(s.runnerEvents))
	h("POST /api/runner/runs/{id}/finish", s.runnerAuth(s.runnerFinish))
	h("POST /api/runner/repos", s.runnerAuth(s.runnerRepos))
	h("GET /api/runner/control", s.runnerAuth(s.runnerControl))
	h("GET /api/runner/tty/{channel}", s.runnerAuth(s.runnerTTY))
	h("GET /api/runner/context", s.runnerAuth(s.runnerContext))
	h("GET /api/runner/projects", s.runnerAuth(s.runnerProjects))
	h("GET /api/runner/tasks", s.runnerAuth(s.runnerTasks))
	h("POST /api/runner/tasks", s.runnerAuth(s.runnerCreateTask))
	h("PATCH /api/runner/tasks/{task}", s.runnerAuth(s.runnerUpdateTask))
	h("POST /api/runner/tasks/{task}/comments", s.runnerAuth(s.runnerComment))
	h("GET /api/runner/checkup", s.runnerAuth(s.runnerCheckup))

	h("GET /api/terminal/hosts", s.authed(s.terminalHosts))
	h("GET /api/terminal/{runner}/attach", s.authed(s.elevated(s.terminalAttach)))
	h("POST /api/terminal/{runner}/sessions", s.authed(s.elevated(s.terminalCreate)))
	h("DELETE /api/terminal/{runner}/sessions/{name}", s.authed(s.elevated(s.terminalKill)))
	h("GET /api/terminal/{runner}/sessions/{name}/capture", s.authed(s.elevated(s.terminalCapture)))
	h("POST /api/terminal/{runner}/sessions/{name}/keys", s.authed(s.elevated(s.terminalKeys)))

	h("GET /api/code/status", s.authed(s.codeStatus))
	h("POST /api/code/open", s.authed(s.elevated(s.codeOpen)))
	h("/code/", s.codeProxy)

	// Everything that is not /api, /code or /health is the web app.
	h("/", webui.Handler().ServeHTTP)

	h("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such route")
	})

	return recoverer(logRequests(mux))
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	dbStatus := "ok"
	code := http.StatusOK
	if err := s.store.DB.Ping(ctx); err != nil {
		dbStatus, code = "down", http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]string{"status": "ok", "db": dbStatus, "version": s.cfg.Version})
}

// ── Authentication wrappers ───────────────────────────────────────────────

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxSession
)

type userHandler func(w http.ResponseWriter, r *http.Request, u *store.User)

// web guards a browser route that may run without a session: non-GET
// requests must carry the CSRF header and, when the browser says where it
// came from, come from the SPA's own origin.
func (s *Server) web(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get(csrfHeader) != "web" {
				writeError(w, http.StatusForbidden, "csrf", "missing "+csrfHeader+" header")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != s.cfg.PublicURL {
				writeError(w, http.StatusForbidden, "csrf", "cross-origin request refused")
				return
			}
		}
		next(w, r)
	}
}

// authed requires a live session.
func (s *Server) authed(next userHandler) http.HandlerFunc {
	return s.web(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "sign in first")
			return
		}
		u, info, err := s.store.SessionUser(r.Context(), hashToken(c.Value), sessionTTL)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				slog.Error("session lookup", "err", err)
			}
			s.clearCookie(w)
			writeError(w, http.StatusUnauthorized, "unauthorized", "your session has ended")
			return
		}
		ctx := context.WithValue(r.Context(), ctxSession, info)
		ctx = context.WithValue(ctx, ctxUser, u)
		next(w, r.WithContext(ctx), u)
	})
}

// elevated additionally requires a recent password re-confirmation on this
// session (POST /api/auth/elevate).
func (s *Server) elevated(next userHandler) userHandler {
	return func(w http.ResponseWriter, r *http.Request, u *store.User) {
		if !sessionInfo(r).Elevated() {
			writeError(w, http.StatusForbidden, "elevation_required", "confirm your password to continue")
			return
		}
		next(w, r, u)
	}
}

func sessionInfo(r *http.Request) *store.SessionInfo {
	info, _ := r.Context().Value(ctxSession).(*store.SessionInfo)
	return info
}

func sessionID(r *http.Request) int64 {
	if info := sessionInfo(r); info != nil {
		return info.ID
	}
	return 0
}

type runnerHandler func(w http.ResponseWriter, r *http.Request, rn *store.Runner)

// runnerAuth accepts only a runner token, only on runner routes. Runner
// bodies can be large (a batch of log lines), so the cap is higher here.
func (s *Server) runnerAuth(next runnerHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !strings.HasPrefix(token, "frg_") {
			writeError(w, http.StatusUnauthorized, "unauthorized", "runner token required")
			return
		}
		rn, err := s.store.RunnerByToken(r.Context(), hashToken(token))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "unknown runner token")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
		next(w, r, rn)
	}
}

func (s *Server) setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		MaxAge: int(sessionTTL.Seconds()), HttpOnly: true,
		Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
}

// clientIP is the browser's address. The API sits behind Caddy on the same
// host — which reaches it on the node's own (public) address, not loopback —
// so X-Forwarded-For is trusted exactly when the peer is loopback or one of
// TRUSTED_PROXIES. Caddy sets that header to its own peer; when that peer is a
// Cloudflare edge (when the site is proxied by Cloudflare), the real client is in
// CF-Connecting-IP.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil || !(peer.IsLoopback() || s.trustedProxy(peer)) {
		return host
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return host
	}
	first, _, _ := strings.Cut(xff, ",")
	first = strings.TrimSpace(first)
	if ip := net.ParseIP(first); ip != nil && isCloudflare(ip) {
		if cf := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); cf != nil {
			return cf.String()
		}
	}
	return first
}

func (s *Server) trustedProxy(ip net.IP) bool {
	for _, t := range s.cfg.TrustedProxies {
		if t.Equal(ip) {
			return true
		}
	}
	return false
}

// Cloudflare's published edge ranges (https://www.cloudflare.com/ips/).
var cloudflareNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
		"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
		"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
		"2a06:98c0::/29", "2c0f:f248::/32",
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

func isCloudflare(ip net.IP) bool {
	for _, n := range cloudflareNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// sec records a security event for the request's session (best effort).
func (s *Server) sec(r *http.Request, kind, detail string) {
	if err := s.store.LogSecurity(r.Context(), kind, detail, s.clientIP(r), r.UserAgent(), sessionID(r)); err != nil {
		slog.Error("security log", "kind", kind, "err", err)
	}
}

// ── JSON helpers ──────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// writeErr maps store errors onto the contract's error codes.
func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var verr *store.ValidationError
	switch {
	case errors.As(err, &verr):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]string{
			"code": "validation", "field": verr.Field, "message": verr.Message}})
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, store.ErrConflict):
		msg := strings.TrimPrefix(err.Error(), store.ErrConflict.Error()+": ")
		writeError(w, http.StatusConflict, "conflict", msg)
	case errors.Is(err, vault.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "vault_unavailable", "the vault has no key configured on this server")
	case errors.Is(err, context.Canceled):
		// The client went away; nobody is listening for an answer.
	default:
		slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "something went wrong")
	}
}

// decode reads a JSON body of at most 2 MB.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func decodePatch(w http.ResponseWriter, r *http.Request) (store.Patch, bool) {
	var p store.Patch
	if !decode(w, r, &p) {
		return nil, false
	}
	if p == nil {
		p = store.Patch{}
	}
	return p, true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return 0, false
	}
	return id, true
}

func queryInt(r *http.Request, name string, fallback int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil {
		return v
	}
	return fallback
}

func queryBool(r *http.Request, name string) bool {
	v := r.URL.Query().Get(name)
	return v == "1" || v == "true"
}

// ── Middleware ────────────────────────────────────────────────────────────

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController and the WebSocket upgrade reach the
// underlying writer (Hijack, deadlines).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// Health checks and the runner's polling are noise at info level.
		if r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/api/runner/") && rec.status < 400 {
			return
		}
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"ms", time.Since(start).Milliseconds())
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				slog.Error("panic", "value", v, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal", "something went wrong")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ── Rate limiting ─────────────────────────────────────────────────────────

// limiter counts failures in a sliding window, per key and overall.
type limiter struct {
	mu        sync.Mutex
	perKey    int
	global    int
	window    time.Duration
	byKey     map[string][]time.Time
	allEvents []time.Time
}

func newLimiter(perKey, global int, window time.Duration) *limiter {
	return &limiter{perKey: perKey, global: global, window: window, byKey: map[string][]time.Time{}}
}

func prune(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	return ts[i:]
}

// Allowed reports whether key may try again now.
func (l *limiter) Allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-l.window)
	l.allEvents = prune(l.allEvents, cutoff)
	l.byKey[key] = prune(l.byKey[key], cutoff)
	if len(l.byKey[key]) == 0 {
		delete(l.byKey, key)
	}
	return len(l.byKey[key]) < l.perKey && len(l.allEvents) < l.global
}

func (l *limiter) Record(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.byKey[key] = append(l.byKey[key], now)
	l.allEvents = append(l.allEvents, now)
}

func (l *limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.byKey, key)
}

// ── Runner wake-ups ───────────────────────────────────────────────────────

// notifier wakes a runner's pending long-poll the moment a run is queued
// for it, instead of on its next poll.
type notifier struct {
	mu    sync.Mutex
	chans map[int64]chan struct{}
}

func newNotifier() *notifier { return &notifier{chans: map[int64]chan struct{}{}} }

func (n *notifier) wait(runnerID int64) <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	ch, ok := n.chans[runnerID]
	if !ok {
		ch = make(chan struct{}, 1)
		n.chans[runnerID] = ch
	}
	return ch
}

func (n *notifier) notify(runnerID int64) {
	n.mu.Lock()
	ch, ok := n.chans[runnerID]
	n.mu.Unlock()
	if ok {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
