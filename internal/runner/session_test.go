package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestSessionResponseFollowsTheMachine(t *testing.T) {
	s := &session{r: &Runner{cfg: &Config{PermissionModes: []string{"plan", "acceptEdits"}}}}
	bash := pendingPrompt{kind: "permission", input: json.RawMessage(`{"command":"make"}`),
		suggestions: json.RawMessage(`[{"type":"addRules","rules":[{"toolName":"Bash"}],"behavior":"allow","destination":"userSettings"},
			{"type":"addDirectories","directories":["/"],"destination":"session"},
			{"type":"setMode","mode":"acceptEdits","destination":"localSettings"}]`)}
	plan := pendingPrompt{kind: "plan", input: json.RawMessage(`{"plan":"x"}`)}
	question := pendingPrompt{kind: "question", input: json.RawMessage(`{"questions":[]}`)}

	// Approvals off: permissions and plans are denied whatever the answer;
	// questions still go through.
	if got := s.response(bash, promptAnswer{Decision: "allow"}); got["behavior"] != "deny" {
		t.Fatalf("approvals off, bash = %v", got)
	}
	if got := s.response(plan, promptAnswer{Decision: "approve"}); got["behavior"] != "deny" {
		t.Fatalf("approvals off, plan = %v", got)
	}
	got := s.response(question, promptAnswer{Decision: "answer", Answers: map[string]string{"Q?": "A"}})
	if got["behavior"] != "allow" || got["updatedInput"].(map[string]any)["answers"].(map[string]string)["Q?"] != "A" {
		t.Fatalf("question = %v", got)
	}

	s.r.cfg.Approvals = true
	got = s.response(bash, promptAnswer{Decision: "allow_always"})
	perms, _ := json.Marshal(got["updatedPermissions"])
	if got["behavior"] != "allow" ||
		string(perms) != `[{"behavior":"allow","destination":"session","rules":[{"toolName":"Bash"}],"type":"addRules"},{"destination":"session","mode":"acceptEdits","type":"setMode"}]` {
		t.Fatalf("allow_always = %v %s", got, perms)
	}
	if got := s.response(bash, promptAnswer{Decision: "deny", Message: "not now"}); got["behavior"] != "deny" || got["message"] != "not now" {
		t.Fatalf("deny = %v", got)
	}
	if got := s.response(plan, promptAnswer{Decision: "approve_edits"}); got["updatedPermissions"] == nil {
		t.Fatalf("approve_edits = %v", got)
	}
	s.r.cfg.PermissionModes = []string{"plan"}
	if got := s.response(plan, promptAnswer{Decision: "approve_edits"}); got["behavior"] != "allow" || got["updatedPermissions"] != nil {
		t.Fatalf("approve_edits without acceptEdits = %v", got)
	}
}

// A follow-up turn that ends before the first "reply" report has gone out
// moves awaiting "reply" → "" → "reply" in one go: the second "reply" must
// still reach Forge, which cleared awaiting when the follow-up arrived.
func TestAwaitingReplyAfterAQuickFollowUpIsReported(t *testing.T) {
	var mu sync.Mutex
	var got []string
	first := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Awaiting string `json:"awaiting"`
		}
		_ = json.NewDecoder(r.Body).Decode(&v)
		mu.Lock()
		got = append(got, v.Awaiting)
		n := len(got)
		mu.Unlock()
		if n == 1 {
			close(first)
			<-release // hold the first report while the follow-up turn comes and goes
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := New(&Config{APIURL: srv.URL, Token: "frg_test"})
	s := newSession(ctx, r, claimedRun{ID: 7}, nil, nil)
	go s.awaitLoop()

	s.setAwaiting("reply") // turn 1 ended
	<-first
	s.setAwaiting("")      // the follow-up was picked up…
	s.setAwaiting("reply") // …and its turn ended, all while the report was in flight
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "reply" || got[1] != "reply" {
		t.Fatalf("awaiting reports = %q, want the second reply too", got)
	}
}

// closeRecorder is a session input that remembers being closed.
type closeRecorder struct{ closed chan struct{} }

func (c *closeRecorder) Write(p []byte) (int, error) { return len(p), nil }
func (c *closeRecorder) Close() error                { close(c.closed); return nil }

// A one-turn session ends when its turn does; a plain one waits for a reply.
func TestOneTurnSessionClosesAfterTheTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := New(&Config{APIURL: srv.URL, Token: "frg_test", IdleMinutes: 15})
	ship := newShipper(r.client, 7)
	defer ship.close()

	w := &closeRecorder{closed: make(chan struct{})}
	s := newSession(ctx, r, claimedRun{ID: 7, OneTurn: true}, ship, w)
	s.turnDone()
	select {
	case <-w.closed:
	default:
		t.Fatal("a one-turn session must close its input when the turn ends")
	}
	if s.isParked() {
		t.Fatal("finished, not parked")
	}

	w2 := &closeRecorder{closed: make(chan struct{})}
	s2 := newSession(ctx, r, claimedRun{ID: 8}, ship, w2)
	s2.turnDone()
	select {
	case <-w2.closed:
		t.Fatal("a plain session waits for a reply after its turn")
	default:
	}
	s2.closeInput() // stops the idle timer
}
