package api

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/store"
)

// VS Code in the browser. The master machine runs VS Code's own web server
// (`code serve-web`) on localhost; its runner exposes it on the tailnet behind
// a gateway that only answers requests carrying this runner's token hash; and
// this proxy serves it to the browser at /code/, same origin as Forge so it
// embeds in the app. Opening needs elevation and sets a short-lived forge_code
// cookie bound to the session; the editor's own requests (assets, its
// WebSockets) then pass on that cookie.

const (
	codeCookie = "forge_code"
	codeTTL    = 12 * time.Hour
)

type codeSession struct {
	sessionID int64
	expires   time.Time
}

type codeSessions struct {
	mu sync.Mutex
	m  map[string]codeSession
}

func newCodeSessions() *codeSessions { return &codeSessions{m: map[string]codeSession{}} }

func (c *codeSessions) add(token string, sessionID int64) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.m { // prune while here
		if now.After(v.expires) {
			delete(c.m, k)
		}
	}
	exp := now.Add(codeTTL)
	c.m[token] = codeSession{sessionID: sessionID, expires: exp}
	return exp
}

func (c *codeSessions) valid(token string, sessionID int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[token]
	return ok && v.sessionID == sessionID && time.Now().Before(v.expires)
}

func (c *codeSessions) dropSession(sessionID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.m {
		if v.sessionID == sessionID {
			delete(c.m, k)
		}
	}
}

// codeRunner is the master, when it serves VS Code and is connected.
func (s *Server) codeRunner(r *http.Request) (*store.Runner, string) {
	rn, err := s.store.MasterRunner(r.Context())
	if err != nil {
		return nil, "no master runner is elected"
	}
	switch {
	case !rn.Online:
		return rn, rn.Name + " is offline"
	case !rn.Capabilities.Code || rn.Capabilities.CodeGateway == "":
		return rn, "VS Code is not enabled on " + rn.Name + ` ("code" in its runner.json)`
	}
	return rn, ""
}

func (s *Server) codeStatus(w http.ResponseWriter, r *http.Request, u *store.User) {
	rn, reason := s.codeRunner(r)
	var name *string
	if rn != nil {
		name = &rn.Name
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": reason == "", "runner_name": name, "reason": reason})
}

func (s *Server) codeOpen(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		ProjectKey string  `json:"project_key"`
		RepoIDs    []int64 `json:"repo_ids"`
		Folder     string  `json:"folder"`
	}
	if !decode(w, r, &in) {
		return
	}
	rn, reason := s.codeRunner(r)
	if reason != "" {
		writeError(w, http.StatusConflict, "runner_offline", reason)
		return
	}
	cc := s.terminals.control(rn.ID)
	if cc == nil {
		writeError(w, http.StatusConflict, "runner_offline", rn.Name+" is not connected")
		return
	}

	// What to open: a folder as-is; one repo as a folder; a project (or
	// several of its repos) as a multi-root workspace the runner writes.
	var target string
	switch {
	case in.Folder != "":
		res, err := cc.call(r.Context(), map[string]any{"type": "code_folder", "folder": in.Folder}, 10*time.Second)
		if err != nil {
			writeError(w, http.StatusBadGateway, "runner_error", err.Error())
			return
		}
		target = "folder=" + url.QueryEscape(res.Text)
	default:
		if in.ProjectKey == "" {
			writeErr(w, r, &store.ValidationError{Field: "project_key", Message: "name a project or a folder"})
			return
		}
		p, err := s.store.ProjectByKey(r.Context(), store.TodayFor(u.Timezone), in.ProjectKey)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		want := map[int64]bool{}
		for _, id := range in.RepoIDs {
			want[id] = true
		}
		var folders []map[string]string
		for _, repo := range p.Repos {
			if repo.Path == "" || (len(want) > 0 && !want[repo.ID]) {
				continue
			}
			folders = append(folders, map[string]string{"name": repo.Name, "path": repo.Path})
		}
		if len(folders) == 0 {
			writeErr(w, r, &store.ValidationError{Field: "repo_ids", Message: "no repos with a local path to open"})
			return
		}
		msg := map[string]any{"type": "code_workspace", "name": p.Key, "folders": folders}
		if len(folders) == 1 {
			msg = map[string]any{"type": "code_folder", "folder": folders[0]["path"]}
		} else if len(want) > 0 {
			msg["name"] = fmt.Sprintf("%s-%d", p.Key, len(folders))
		}
		res, err := cc.call(r.Context(), msg, 10*time.Second)
		if err != nil {
			writeError(w, http.StatusBadGateway, "runner_error", err.Error())
			return
		}
		if msg["type"] == "code_folder" {
			target = "folder=" + url.QueryEscape(res.Text)
		} else {
			target = "workspace=" + url.QueryEscape(res.Text)
		}
	}

	token := randomID() + randomID()
	exp := s.codes.add(token, sessionID(r))
	http.SetCookie(w, &http.Cookie{Name: codeCookie, Value: token, Path: "/code", MaxAge: int(codeTTL.Seconds()),
		HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode})
	slog.Info("vscode opened", "target", target, "ip", s.clientIP(r))
	if shown, err := url.QueryUnescape(target); err == nil {
		s.sec(r, "code_open", shown)
	} else {
		s.sec(r, "code_open", target)
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": "/code/?" + target, "workspace": target, "expires_at": exp})
}

// codeProxy forwards everything under /code/ to the master's VS Code gateway.
func (s *Server) codeProxy(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "sign in to Forge first")
		return
	}
	_, info, err := s.store.SessionUser(r.Context(), hashToken(c.Value), sessionTTL)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "your session has ended")
		return
	}
	cc, err := r.Cookie(codeCookie)
	if err != nil || !s.codes.valid(cc.Value, info.ID) {
		writeError(w, http.StatusForbidden, "elevation_required", "open the editor from Forge again")
		return
	}
	// The editor runs same-origin with Forge; refuse writes from elsewhere.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if o := r.Header.Get("Origin"); o != "" && o != s.cfg.PublicURL {
			writeError(w, http.StatusForbidden, "csrf", "cross-origin request refused")
			return
		}
	}
	rn, reason := s.codeRunner(r)
	if reason != "" {
		writeError(w, http.StatusBadGateway, "runner_offline", reason)
		return
	}
	target, err := url.Parse(rn.Capabilities.CodeGateway)
	if err != nil || target.Host == "" {
		writeError(w, http.StatusBadGateway, "runner_error", "the master's VS Code gateway address is invalid")
		return
	}
	hash, err := s.store.RunnerTokenHash(r.Context(), rn.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	secret := hex.EncodeToString(hash)

	noDeadlines(w) // editor WebSockets and large assets outlive the server's 60 s
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// Keep the browser's Host: VS Code derives the address its
			// WebSockets dial (remoteAuthority) from it.
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Set("X-Forge-Gateway", secret)
			// Forge's own cookies stay here; VS Code gets only its own.
			var keep []string
			for _, ck := range pr.In.Cookies() {
				if ck.Name != sessionCookie && ck.Name != codeCookie {
					keep = append(keep, ck.Name+"="+ck.Value)
				}
			}
			pr.Out.Header.Del("Cookie")
			if len(keep) > 0 {
				pr.Out.Header.Set("Cookie", strings.Join(keep, "; "))
			}
			pr.SetXForwarded()
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, http.ErrAbortHandler) {
				return
			}
			slog.Warn("vscode proxy", "err", err)
			writeError(w, http.StatusBadGateway, "runner_error", "could not reach VS Code on "+rn.Name)
		},
	}
	proxy.ServeHTTP(w, r)
}
