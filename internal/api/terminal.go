package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/devai-io/forge/internal/store"
)

// The terminal relay. Each terminal-enabled runner holds one control
// WebSocket open to the API. To attach a browser to a tmux session the API
// asks the runner (over control) to open a terminal on a fresh channel; the
// runner dials /api/runner/tty/{channel}, the API pairs that socket with the
// browser's, and pipes frames both ways. Nothing on the runner's machine ever
// listens, so it works from behind NAT, on a laptop, on a sleeping Mac.

var sessionName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,40}$`)

type controlConn struct {
	runnerID int64
	conn     *websocket.Conn
	writeMu  sync.Mutex

	mu      sync.Mutex
	pending map[string]chan controlResult
}

type controlResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Text  string `json:"text"`
}

type terminalHub struct {
	mu       sync.Mutex
	controls map[int64]*controlConn
	channels map[string]chan ttyHandoff // channel -> where the runner's tty socket is handed over
}

// ttyHandoff passes the runner's socket to the waiting browser request; done
// is closed by the relay so the runner's handler can return. (A hijacked
// request's context is never cancelled, so it cannot be used for that.)
type ttyHandoff struct {
	conn *websocket.Conn
	done chan struct{}
}

func newTerminalHub() *terminalHub {
	return &terminalHub{controls: map[int64]*controlConn{}, channels: map[string]chan ttyHandoff{}}
}

func (h *terminalHub) control(runnerID int64) *controlConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.controls[runnerID]
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (c *controlConn) send(ctx context.Context, msg any) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageText, raw)
}

// call sends a request carrying an id and waits for the runner's result.
func (c *controlConn) call(ctx context.Context, msg map[string]any, timeout time.Duration) (controlResult, error) {
	id := randomID()
	msg["id"] = id
	ch := make(chan controlResult, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.send(ctx, msg); err != nil {
		return controlResult{}, err
	}
	select {
	case r := <-ch:
		if !r.OK {
			return r, errors.New(r.Error)
		}
		return r, nil
	case <-time.After(timeout):
		return controlResult{}, errors.New("the runner did not answer in time")
	case <-ctx.Done():
		return controlResult{}, ctx.Err()
	}
}

// noDeadlines clears the server's read/write timeouts for a request that is
// about to become a long-lived WebSocket: a hijacked connection keeps whatever
// deadlines were set on it, which would cut every terminal at 60 s.
func noDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
}

// keepalive pings so idle sockets survive Cloudflare's ~100 s idle cut.
func keepalive(ctx context.Context, c *websocket.Conn) {
	t := time.NewTicker(25 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// ── Runner side ───────────────────────────────────────────────────────────

func (s *Server) runnerControl(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	noDeadlines(w)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(4 << 20)
	cc := &controlConn{runnerID: rn.ID, conn: conn, pending: map[string]chan controlResult{}}

	s.terminals.mu.Lock()
	if old := s.terminals.controls[rn.ID]; old != nil {
		_ = old.conn.Close(websocket.StatusPolicyViolation, "replaced by a newer connection")
	}
	s.terminals.controls[rn.ID] = cc
	s.terminals.mu.Unlock()
	slog.Info("runner control connected", "runner", rn.Name)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go keepalive(ctx, conn)
	defer func() {
		s.terminals.mu.Lock()
		if s.terminals.controls[rn.ID] == cc {
			delete(s.terminals.controls, rn.ID)
		}
		s.terminals.mu.Unlock()
		_ = conn.CloseNow()
		slog.Info("runner control disconnected", "runner", rn.Name)
	}()

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var res controlResult
		if json.Unmarshal(data, &res) != nil || res.ID == "" {
			continue
		}
		cc.mu.Lock()
		ch := cc.pending[res.ID]
		cc.mu.Unlock()
		if ch != nil {
			ch <- res
		}
	}
}

// runnerTTY is the runner's end of one terminal. It hands the socket to the
// waiting browser request and stays open until the relay is done with it.
func (s *Server) runnerTTY(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	channel := r.PathValue("channel")
	s.terminals.mu.Lock()
	wait := s.terminals.channels[channel]
	delete(s.terminals.channels, channel)
	s.terminals.mu.Unlock()
	if wait == nil {
		writeError(w, http.StatusNotFound, "not_found", "no terminal is waiting on that channel")
		return
	}
	noDeadlines(w)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(4 << 20)
	h := ttyHandoff{conn: conn, done: make(chan struct{})}
	select {
	case wait <- h:
		<-h.done
	default: // the browser request gave up already
		_ = conn.Close(websocket.StatusGoingAway, "nobody is waiting")
	}
}

// ── Browser side ──────────────────────────────────────────────────────────

func (s *Server) terminalRunner(w http.ResponseWriter, r *http.Request) (*store.Runner, *controlConn, bool) {
	id, err := strconv.ParseInt(r.PathValue("runner"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such runner")
		return nil, nil, false
	}
	rn, err := s.store.RunnerByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return nil, nil, false
	}
	if !rn.Capabilities.Terminal {
		writeError(w, http.StatusConflict, "terminal_disabled", "terminals are off on "+rn.Name+" (\"terminal\": true in its agent.json)")
		return nil, nil, false
	}
	cc := s.terminals.control(rn.ID)
	if cc == nil {
		writeError(w, http.StatusConflict, "runner_offline", rn.Name+" is not connected")
		return nil, nil, false
	}
	return rn, cc, true
}

func (s *Server) terminalHosts(w http.ResponseWriter, r *http.Request, u *store.User) {
	hosts, err := s.store.TerminalHosts(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var def *int64
	for i := range hosts {
		// "online" for a terminal means the control socket is up, not just a
		// recent heartbeat.
		hosts[i].Online = hosts[i].Online && s.terminals.control(hosts[i].RunnerID) != nil
		if hosts[i].Role == "master" {
			id := hosts[i].RunnerID
			def = &id
		}
	}
	if def == nil && len(hosts) > 0 {
		id := hosts[0].RunnerID
		def = &id
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts, "default_runner_id": def})
}

func (s *Server) terminalAttach(w http.ResponseWriter, r *http.Request, u *store.User) {
	// A WebSocket cannot carry the custom CSRF header, so the Origin check is
	// the whole cross-site defence here — and it is mandatory, not "if sent".
	if r.Header.Get("Origin") != s.cfg.PublicURL {
		writeError(w, http.StatusForbidden, "csrf", "cross-origin terminal refused")
		return
	}
	rn, cc, ok := s.terminalRunner(w, r)
	if !ok {
		return
	}
	session := r.URL.Query().Get("session")
	if !sessionName.MatchString(session) {
		writeErr(w, r, &store.ValidationError{Field: "session", Message: "invalid session name"})
		return
	}
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	if cols < 20 || cols > 500 {
		cols = 120
	}
	if rows < 5 || rows > 200 {
		rows = 32
	}

	channel := randomID()
	handoff := make(chan ttyHandoff, 1)
	s.terminals.mu.Lock()
	s.terminals.channels[channel] = handoff
	s.terminals.mu.Unlock()
	defer func() {
		s.terminals.mu.Lock()
		delete(s.terminals.channels, channel)
		s.terminals.mu.Unlock()
	}()
	open := map[string]any{"type": "open", "channel": channel, "session": session, "cols": cols, "rows": rows}
	if wv := r.URL.Query().Get("window"); wv != "" {
		if n, err := strconv.Atoi(wv); err == nil && n >= 0 && n < 1000 {
			open["window"] = n
		}
	}
	if err := cc.send(r.Context(), open); err != nil {
		writeError(w, http.StatusBadGateway, "runner_offline", "could not reach "+rn.Name)
		return
	}
	var h ttyHandoff
	select {
	case h = <-handoff:
	case <-time.After(15 * time.Second):
		writeError(w, http.StatusGatewayTimeout, "runner_offline", rn.Name+" did not open the terminal")
		return
	case <-r.Context().Done():
		return
	}
	tty := h.conn
	defer close(h.done)
	defer tty.CloseNow()

	noDeadlines(w)
	browser, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer browser.CloseNow()
	browser.SetReadLimit(1 << 20)
	slog.Info("terminal attached", "runner", rn.Name, "session", session, "ip", s.clientIP(r))
	s.sec(r, "terminal_attach", rn.Name+" · "+session)
	relay(browser, tty)
	slog.Info("terminal detached", "runner", rn.Name, "session", session)
}

// relay copies frames both ways until either side goes away, then tells the
// browser why.
func relay(browser, tty *websocket.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go keepalive(ctx, browser)
	go keepalive(ctx, tty)
	pipe := func(dst, src *websocket.Conn) {
		defer cancel()
		for {
			typ, data, err := src.Read(ctx)
			if err != nil {
				return
			}
			wctx, wcancel := context.WithTimeout(ctx, 30*time.Second)
			err = dst.Write(wctx, typ, data)
			wcancel()
			if err != nil {
				return
			}
		}
	}
	go pipe(tty, browser)
	pipe(browser, tty)
	_ = browser.Close(websocket.StatusNormalClosure, "")
	_ = tty.Close(websocket.StatusNormalClosure, "")
}

func (s *Server) terminalCall(w http.ResponseWriter, r *http.Request, msg map[string]any, timeout time.Duration) (controlResult, bool) {
	_, cc, ok := s.terminalRunner(w, r)
	if !ok {
		return controlResult{}, false
	}
	res, err := cc.call(r.Context(), msg, timeout)
	if err != nil {
		writeError(w, http.StatusBadGateway, "runner_error", err.Error())
		return res, false
	}
	return res, true
}

func pathSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if !sessionName.MatchString(name) {
		writeErr(w, r, &store.ValidationError{Field: "session", Message: "invalid session name"})
		return "", false
	}
	return name, true
}

func (s *Server) terminalCapture(w http.ResponseWriter, r *http.Request, u *store.User) {
	name, ok := pathSession(w, r)
	if !ok {
		return
	}
	lines := queryInt(r, "lines", 60)
	if lines < 1 || lines > 400 {
		lines = 60
	}
	msg := map[string]any{"type": "capture", "session": name, "lines": lines}
	if wv := r.URL.Query().Get("window"); wv != "" {
		if n, err := strconv.Atoi(wv); err == nil && n >= 0 && n < 1000 {
			msg["window"] = n
		}
	}
	res, ok := s.terminalCall(w, r, msg, 10*time.Second)
	if ok {
		writeJSON(w, http.StatusOK, map[string]string{"text": res.Text})
	}
}

func (s *Server) terminalKeys(w http.ResponseWriter, r *http.Request, u *store.User) {
	name, ok := pathSession(w, r)
	if !ok {
		return
	}
	var in struct {
		Text   string `json:"text"`
		Enter  bool   `json:"enter"`
		Window *int   `json:"window"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Text) > 10000 {
		writeErr(w, r, &store.ValidationError{Field: "text", Message: "at most 10000 characters"})
		return
	}
	msg := map[string]any{"type": "keys", "session": name, "text": in.Text, "enter": in.Enter}
	if in.Window != nil && *in.Window >= 0 && *in.Window < 1000 {
		msg["window"] = *in.Window
	}
	if _, ok := s.terminalCall(w, r, msg, 10*time.Second); ok {
		slog.Info("terminal keys sent", "session", name, "ip", s.clientIP(r))
		s.sec(r, "terminal_keys", name)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) terminalKill(w http.ResponseWriter, r *http.Request, u *store.User) {
	name, ok := pathSession(w, r)
	if !ok {
		return
	}
	if _, ok := s.terminalCall(w, r, map[string]any{"type": "kill", "session": name}, 10*time.Second); ok {
		w.WriteHeader(http.StatusNoContent)
	}
}

// terminalCreate starts a detached session in a repo's directory, running
// Claude Code (which picks up the Forge context through its SessionStart
// hook) or a plain shell.
func (s *Server) terminalCreate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Name       string `json:"name"`
		RepoID     *int64 `json:"repo_id"`
		ProjectKey string `json:"project_key"`
		Start      string `json:"start"`
		Prompt     string `json:"prompt"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Start == "" {
		in.Start = "claude"
	}
	if in.Start != "claude" && in.Start != "shell" {
		writeErr(w, r, &store.ValidationError{Field: "start", Message: "must be claude or shell"})
		return
	}
	cwd, name := "", in.Name
	if in.RepoID != nil {
		repo, err := s.store.RepoByID(r.Context(), *in.RepoID)
		if err != nil {
			writeErr(w, r, &store.ValidationError{Field: "repo_id", Message: "no such repo"})
			return
		}
		cwd = repo.Path
		if name == "" {
			key := in.ProjectKey
			if key == "" {
				if p, err := s.store.ProjectKeyByID(r.Context(), repo.ProjectID); err == nil {
					key = p
				}
			}
			name = key + "-" + repo.Name
		}
	}
	if name == "" && in.ProjectKey != "" {
		name = in.ProjectKey
	}
	if name == "" {
		name = "forge-" + time.Now().Format("0102-1504")
	}
	if len(name) > 40 {
		name = name[:40]
	}
	if !sessionName.MatchString(name) {
		writeErr(w, r, &store.ValidationError{Field: "name", Message: "letters, digits, _ . - only (max 40)"})
		return
	}
	if len(in.Prompt) > 20000 {
		writeErr(w, r, &store.ValidationError{Field: "prompt", Message: "too long"})
		return
	}
	if _, ok := s.terminalCall(w, r, map[string]any{"type": "create", "session": name, "cwd": cwd,
		"start": in.Start, "prompt": in.Prompt}, 20*time.Second); ok {
		slog.Info("terminal session created", "session", name, "start", in.Start, "ip", s.clientIP(r))
		s.sec(r, "terminal_create", name+" ("+in.Start+")")
		writeJSON(w, http.StatusCreated, map[string]string{"session": name})
	}
}

// ── Runner-side context and task access (backs `forge agent mcp`) ─────────

func (s *Server) runnerContext(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	cwd := r.URL.Query().Get("cwd")
	ctxInfo, err := s.store.ContextForPath(r.Context(), cwd)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ctxInfo)
}

func (s *Server) runnerProjects(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	ps, err := s.store.ListProjects(r.Context(), store.TodayFor("UTC"), false)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		out = append(out, map[string]any{"key": p.Key, "name": p.Name, "status": p.Status, "summary": p.Summary,
			"priority": p.Priority, "open": p.Stats.Total - p.Stats.Done})
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

func (s *Server) runnerTasks(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	q := r.URL.Query()
	tasks, err := s.store.ListTasks(r.Context(), store.TaskFilter{ProjectKey: q.Get("project"), Query: q.Get("q"),
		Open: queryBool(r, "open"), Limit: queryInt(r, "limit", 100), Today: store.TodayFor("UTC")})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) runnerCreateTask(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	var in store.TaskInput
	if !decode(w, r, &in) {
		return
	}
	t, err := s.store.CreateTask(r.Context(), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// runnerTaskID accepts a numeric id or a ref like ALPHA-12.
func (s *Server) runnerTaskID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("task")
	if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return id, true
	}
	t, err := s.store.TaskByRef(r.Context(), raw)
	if err != nil {
		writeErr(w, r, err)
		return 0, false
	}
	return t.ID, true
}

func (s *Server) runnerUpdateTask(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	id, ok := s.runnerTaskID(w, r)
	if !ok {
		return
	}
	p, ok := decodePatch(w, r)
	if !ok {
		return
	}
	for k := range p {
		if !store.OneOf(k, []string{"status", "title", "description", "priority", "focus", "labels", "due_date", "type"}) {
			writeErr(w, r, &store.ValidationError{Field: k, Message: "not editable from a runner"})
			return
		}
	}
	t, err := s.store.UpdateTask(r.Context(), id, p)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) runnerComment(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	id, ok := s.runnerTaskID(w, r)
	if !ok {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	c, err := s.store.CreateComment(r.Context(), id, fmt.Sprintf("%s\n\n_— via Claude on %s_", in.Body, rn.Name))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) runnerCheckup(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	c, err := s.store.LatestCheckup(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
