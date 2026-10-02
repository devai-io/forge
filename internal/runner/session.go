package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// An interactive run keeps Claude Code's session open, the way a terminal
// session is: the prompt goes in as a stream-json user message, and
// `--permission-prompt-tool stdio` turns everything that would ask the user
// (AskUserQuestion, a tool permission, ExitPlanMode) into a control request
// on stdout. The session relays those to Forge, long-polls the run's inbox
// for the answers and follow-up messages, and answers Claude Code itself —
// so what is allowed is decided here, by this machine's config:
//
//   - questions are always relayed (an answer is only information);
//   - permissions and plan approvals only with "approvals": true, otherwise
//     they are denied as in a plain -p run;
//   - "always allow" is kept to this session, and a mode switch only to a
//     mode this machine accepts.
//
// After a turn the session waits idle_minutes for a follow-up, then closes.

// interactiveArgs are what an interactive run adds to the claude command.
var interactiveArgs = []string{"--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--replay-user-messages"}

type pendingPrompt struct {
	kind        string
	input       json.RawMessage
	suggestions json.RawMessage
}

type session struct {
	r    *Runner
	run  claimedRun
	ship *shipper
	ctx  context.Context
	stop context.CancelFunc

	mu      sync.Mutex
	w       io.WriteCloser
	closed  bool
	pending map[string]pendingPrompt
	idle    *time.Timer
	waiting bool // a turn ended; nothing new since

	// The awaiting state goes to Forge from one goroutine, latest wins, so
	// "working again" can never land after the "reply" that followed it.
	awaitWant string
	awaitKick chan struct{}
}

func newSession(ctx context.Context, r *Runner, run claimedRun, ship *shipper, w io.WriteCloser) *session {
	ctx, stop := context.WithCancel(ctx)
	return &session{r: r, run: run, ship: ship, ctx: ctx, stop: stop, w: w, pending: map[string]pendingPrompt{},
		awaitKick: make(chan struct{}, 1)}
}

// start sends the prompt, then reads the inbox. In the background: a pipe
// only takes so much before claude reads it.
func (s *session) start() {
	go func() {
		_ = s.write(map[string]any{"type": "control_request", "request_id": "forge-init",
			"request": map[string]any{"subtype": "initialize"}})
		_ = s.write(userMessage(s.run.Prompt))
		s.inboxLoop() // after the prompt: a quick follow-up must not overtake it
	}()
	go s.awaitLoop()
}

func (s *session) setAwaiting(v string) {
	s.mu.Lock()
	s.awaitWant = v
	s.mu.Unlock()
	select {
	case s.awaitKick <- struct{}{}:
	default:
	}
}

func (s *session) awaitLoop() {
	sent := ""
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.awaitKick:
		}
		s.mu.Lock()
		want := s.awaitWant
		s.mu.Unlock()
		if want != sent && s.post("awaiting", map[string]string{"awaiting": want}, nil) == nil {
			sent = want
		}
	}
}

// end stops the inbox loop (the process has exited).
func (s *session) end() {
	s.stop()
	s.closeInput()
}

func userMessage(text string) map[string]any {
	return map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text},
		"parent_tool_use_id": nil, "session_id": ""}
}

func (s *session) write(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("session input is closed")
	}
	_, err = s.w.Write(append(line, '\n'))
	return err
}

// closeInput ends the session: claude finishes what it is doing and exits.
func (s *session) closeInput() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idle != nil {
		s.idle.Stop()
	}
	if !s.closed {
		s.closed = true
		_ = s.w.Close()
	}
}

// observe looks at one stdout line; false means it is protocol, not
// transcript (control requests and responses are not shipped).
func (s *session) observe(line []byte) bool {
	var m struct {
		Type      string          `json:"type"`
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request"`
	}
	if json.Unmarshal(line, &m) != nil {
		return true
	}
	switch m.Type {
	case "control_request":
		go s.onControl(m.RequestID, m.Request)
		return false
	case "control_response":
		return false
	case "control_cancel_request":
		s.mu.Lock()
		delete(s.pending, m.RequestID)
		s.mu.Unlock()
		go s.post("prompts/expire", map[string]string{"request_id": m.RequestID}, nil)
		return false
	case "result":
		s.turnDone()
	case "assistant", "user", "stream_event":
		s.working()
	}
	return true
}

// turnDone: Claude Code answered; wait for a follow-up, for a while.
func (s *session) turnDone() {
	minutes := s.r.cfg.IdleMinutes
	s.mu.Lock()
	s.waiting = true
	if s.idle != nil {
		s.idle.Stop()
	}
	s.idle = time.AfterFunc(time.Duration(minutes)*time.Minute, func() {
		s.ship.send(textEvent("system", fmt.Sprintf("no reply for %d minutes: ending the session", minutes)))
		s.closeInput()
	})
	s.mu.Unlock()
	s.setAwaiting("reply")
}

// working: the session is busy again (a follow-up was picked up).
func (s *session) working() {
	s.mu.Lock()
	was := s.waiting
	s.waiting = false
	if was && s.idle != nil {
		s.idle.Stop()
	}
	s.mu.Unlock()
	if was {
		s.setAwaiting("")
	}
}

func (s *session) post(what string, body, out any) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
		_, err = s.r.client.post(ctx, fmt.Sprintf("/api/runner/runs/%d/%s", s.run.ID, what), body, out)
		cancel()
		var ae *apiError
		if err == nil || s.ctx.Err() != nil || (errors.As(err, &ae) && ae.status < 500) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	if err != nil && s.ctx.Err() == nil {
		slog.Warn("session report failed", "run", s.run.ID, "what", what, "err", err)
	}
	return err
}

func (s *session) onControl(requestID string, raw json.RawMessage) {
	var req struct {
		Subtype     string          `json:"subtype"`
		ToolName    string          `json:"tool_name"`
		Input       json.RawMessage `json:"input"`
		Suggestions json.RawMessage `json:"permission_suggestions"`
		Description string          `json:"description"`
	}
	_ = json.Unmarshal(raw, &req)
	if req.Subtype != "can_use_tool" {
		s.respondError(requestID, "forge-agent does not handle "+req.Subtype)
		return
	}
	kind := promptKind(req.ToolName)
	if kind != "question" && !s.r.cfg.Approvals {
		s.ship.send(textEvent("system", fmt.Sprintf(
			"%s asked to use %s; this machine does not take approvals from Forge (\"approvals\": true in agent.json), so it was denied",
			"Claude Code", req.ToolName)))
		s.respond(requestID, deny("The user cannot approve this from here, so it was denied. Continue without it, or explain what you need and stop."))
		return
	}
	s.mu.Lock()
	s.pending[requestID] = pendingPrompt{kind: kind, input: req.Input, suggestions: req.Suggestions}
	s.mu.Unlock()
	body := map[string]any{"request_id": requestID, "tool_name": req.ToolName, "input": req.Input,
		"suggestions": req.Suggestions, "description": req.Description}
	if err := s.post("prompts", body, nil); err != nil {
		s.mu.Lock()
		delete(s.pending, requestID)
		s.mu.Unlock()
		s.respond(requestID, deny("Forge could not pass this on to the user ("+err.Error()+"), so it was denied."))
	}
}

func promptKind(tool string) string {
	switch tool {
	case "AskUserQuestion":
		return "question"
	case "ExitPlanMode":
		return "plan"
	}
	return "permission"
}

func deny(message string) map[string]any {
	return map[string]any{"behavior": "deny", "message": message}
}

func (s *session) respond(requestID string, response map[string]any) {
	_ = s.write(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": requestID, "response": response}})
}

func (s *session) respondError(requestID, msg string) {
	_ = s.write(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "error", "request_id": requestID, "error": msg}})
}

type inboxItem struct {
	ID   int64           `json:"id"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

type promptAnswer struct {
	Decision string            `json:"decision"`
	Answers  map[string]string `json:"answers"`
	Message  string            `json:"message"`
}

func (s *session) inboxLoop() {
	var after int64
	for s.ctx.Err() == nil {
		var resp struct {
			Items []inboxItem `json:"items"`
			Done  bool        `json:"done"`
		}
		ctx, cancel := context.WithTimeout(s.ctx, 40*time.Second)
		_, err := s.r.client.post(ctx, fmt.Sprintf("/api/runner/runs/%d/inbox", s.run.ID), map[string]int64{"after": after}, &resp)
		cancel()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}
		for _, it := range resp.Items {
			after = it.ID
			s.handle(it)
		}
		if resp.Done {
			return
		}
	}
}

func (s *session) handle(it inboxItem) {
	switch it.Kind {
	case "message":
		var m struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(it.Data, &m) == nil && m.Text != "" {
			s.working()
			if err := s.write(userMessage(m.Text)); err != nil {
				s.ship.send(textEvent("system", "could not pass the message on: "+err.Error()))
			}
		}
	case "end":
		s.ship.send(textEvent("system", "ending the session (from Forge)"))
		s.closeInput()
	case "answer":
		var a struct {
			RequestID string       `json:"request_id"`
			Answer    promptAnswer `json:"answer"`
		}
		if json.Unmarshal(it.Data, &a) != nil {
			return
		}
		s.mu.Lock()
		p, ok := s.pending[a.RequestID]
		delete(s.pending, a.RequestID)
		s.mu.Unlock()
		if ok { // only what this session asked, and only once
			s.respond(a.RequestID, s.response(p, a.Answer))
		}
	}
}

// response turns the user's answer into Claude Code's permission result,
// within this machine's rules.
func (s *session) response(p pendingPrompt, a promptAnswer) map[string]any {
	input := map[string]any{}
	_ = json.Unmarshal(p.input, &input)
	allow := func(perms []any) map[string]any {
		out := map[string]any{"behavior": "allow", "updatedInput": input}
		if len(perms) > 0 {
			out["updatedPermissions"] = perms
		}
		return out
	}
	declined := func(def string) map[string]any {
		if a.Message != "" {
			return deny(a.Message)
		}
		return deny(def)
	}
	if p.kind != "question" && !s.r.cfg.Approvals {
		return deny("This machine does not take approvals from Forge.")
	}
	switch p.kind {
	case "question":
		if a.Decision == "answer" && len(a.Answers) > 0 {
			input["answers"] = a.Answers
			return allow(nil)
		}
		return declined("The user chose not to answer. Continue with your best judgement.")
	case "plan":
		switch a.Decision {
		case "approve":
			return allow(nil)
		case "approve_edits":
			if contains(s.r.cfg.PermissionModes, "acceptEdits") {
				return allow([]any{map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "session"}})
			}
			return allow(nil)
		}
		return declined("The user wants to keep planning. Ask what to change.")
	default:
		switch a.Decision {
		case "allow":
			return allow(nil)
		case "allow_always":
			return allow(s.sessionRules(p.suggestions))
		}
		return declined("The user denied this.")
	}
}

// sessionRules keeps what "always allow" may change: rules and mode
// switches, for this session only (never written to a settings file), and a
// mode only if this machine accepts it.
func (s *session) sessionRules(raw json.RawMessage) []any {
	var suggestions []map[string]any
	_ = json.Unmarshal(raw, &suggestions)
	out := []any{}
	for _, sg := range suggestions {
		switch sg["type"] {
		case "addRules", "replaceRules":
		case "setMode":
			mode, _ := sg["mode"].(string)
			if !contains(s.r.cfg.PermissionModes, mode) {
				continue
			}
		default:
			continue
		}
		sg["destination"] = "session"
		out = append(out, sg)
	}
	return out
}
