package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devai-io/forge/internal/runner"
	"github.com/devai-io/forge/internal/store"
)

// TestFakeClaude is not a test: it is the claude binary the interactive run
// test starts (through a wrapper script), speaking Claude Code's stream-json
// control protocol. Turn one asks a question and a Bash permission; turn two
// echoes the follow-up. It exits when stdin closes.
func TestFakeClaude(t *testing.T) {
	if os.Getenv("FORGE_FAKE_CLAUDE") != "1" {
		t.Skip("helper process")
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	out := json.NewEncoder(os.Stdout)
	next := func() map[string]any { // the next line, answering initialize on the way
		for in.Scan() {
			var m map[string]any
			if json.Unmarshal(in.Bytes(), &m) != nil {
				continue
			}
			if m["type"] == "control_request" {
				_ = out.Encode(map[string]any{"type": "control_response", "response": map[string]any{
					"subtype": "success", "request_id": m["request_id"], "response": map[string]any{}}})
				continue
			}
			return m
		}
		return nil
	}
	ask := func(id, tool string, input, suggestions any) map[string]any {
		_ = out.Encode(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{
			"subtype": "can_use_tool", "tool_name": tool, "input": input, "permission_suggestions": suggestions}})
		m := next()
		resp, _ := m["response"].(map[string]any)
		inner, _ := resp["response"].(map[string]any)
		return inner
	}
	say := func(text string) {
		_ = out.Encode(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant",
			"content": []any{map[string]any{"type": "text", "text": text}}}})
	}
	result := func(text string, cost float64) {
		_ = out.Encode(map[string]any{"type": "result", "subtype": "success", "result": text, "session_id": "sess-1",
			"total_cost_usd": cost, "num_turns": 1, "duration_ms": 10})
	}

	first := next()
	_ = out.Encode(map[string]any{"type": "user", "message": first["message"], "isReplay": true})
	_ = out.Encode(map[string]any{"type": "system", "subtype": "init", "session_id": "sess-1"})
	q := ask("q1", "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Color?", "header": "Color",
		"options": []any{map[string]any{"label": "Red"}, map[string]any{"label": "Blue"}}, "multiSelect": false}}}, nil)
	answers, _ := q["updatedInput"].(map[string]any)["answers"].(map[string]any)
	say(fmt.Sprintf("color=%v", answers["Color?"]))
	p := ask("p1", "Bash", map[string]any{"command": "make clean"}, []any{
		map[string]any{"type": "addRules", "rules": []any{map[string]any{"toolName": "Bash"}}, "behavior": "allow", "destination": "localSettings"},
		map[string]any{"type": "setMode", "mode": "bypassPermissions", "destination": "session"}})
	perms, _ := json.Marshal(p["updatedPermissions"])
	say(fmt.Sprintf("bash=%v perms=%s", p["behavior"], perms))
	result("turn one", 0.01)

	for m := next(); m != nil; m = next() {
		_ = out.Encode(map[string]any{"type": "user", "message": m["message"], "isReplay": true})
		msg, _ := m["message"].(map[string]any)
		say(fmt.Sprintf("got %v", msg["content"]))
		result("turn two", 0.03)
	}
}

func TestInteractiveRun(t *testing.T) {
	h := setup(t)
	h.login()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var rn store.Runner
	var token string
	expect(t, "machine", h.newRunner("desk", &rn, &token), 201)

	// A repo that exists on this "machine".
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "shop_api")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p, _ := h.store.ProjectByKey(ctx, "2026-01-01", "SHOP")
	repoID := p.Repos[0].ID
	expect(t, "repo path", h.do("PATCH", fmt.Sprintf("/api/repos/%d", repoID), map[string]any{"path": repoDir}, nil), 200)

	// claude = this test binary, as TestFakeClaude.
	self, _ := os.Executable()
	wrapper := filepath.Join(dir, "claude")
	script := fmt.Sprintf("#!/bin/sh\nFORGE_FAKE_CLAUDE=1 exec %q -test.run='^TestFakeClaude$' -- \"$@\"\n", self)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"api_url": h.srv.URL, "token": token, "allowed_roots": []string{dir},
		"permission_modes": []string{"plan", "acceptEdits"}, "claude_path": wrapper, "approvals": true,
		"idle_minutes": 1, "pull_interval": "0", "ci_interval": "0"}
	raw, _ := json.Marshal(cfg)
	cfgPath := filepath.Join(dir, "agent.json")
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORGE_AGENT_HOME", dir)
	rc, err := runner.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = runner.New(rc).Run(ctx) }()

	var run store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": rn.ID, "repo_id": repoID, "kind": "agent",
		"prompt": "Pick a color", "engine": "claude", "interactive": true}, &run), 201)
	if !run.Interactive {
		t.Fatalf("run = %+v", run)
	}

	type view struct {
		Run     store.Run
		Prompts []store.RunPrompt
		Events  []store.RunEvent
	}
	poll := func(what string, ok func(v view) bool) view {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			var v view
			expect(t, "events", h.do("GET", fmt.Sprintf("/api/runs/%d/events", run.ID), nil, &v), 200)
			if ok(v) {
				return v
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s: run %+v, prompts %+v", what, v.Run, v.Prompts)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	pending := func(v view) *store.RunPrompt {
		for i := range v.Prompts {
			if v.Prompts[i].Status == "pending" {
				return &v.Prompts[i]
			}
		}
		return nil
	}
	transcript := func(v view) string {
		var b strings.Builder
		for _, e := range v.Events {
			b.Write(e.Data)
		}
		return b.String()
	}

	// The question arrives; the run waits for an answer.
	v := poll("the question", func(v view) bool { return pending(v) != nil })
	q := pending(v)
	if q.Kind != "question" || v.Run.Awaiting != "answer" {
		t.Fatalf("question = %+v, awaiting %q", q, v.Run.Awaiting)
	}
	answer := func(id int64, body map[string]any) int {
		return h.do("POST", fmt.Sprintf("/api/runs/%d/prompts/%d/answer", run.ID, id), body, nil)
	}
	expect(t, "bad decision", answer(q.ID, map[string]any{"decision": "allow"}), 422)
	expect(t, "answer", answer(q.ID, map[string]any{"decision": "answer", "answers": map[string]string{"Color?": "Blue"}}), 200)
	expect(t, "answered twice", answer(q.ID, map[string]any{"decision": "answer", "answers": map[string]string{"Color?": "Red"}}), 409)

	// Then the Bash permission: "always allow" stays in this session, and
	// a mode the machine does not accept is dropped.
	v = poll("the permission", func(v view) bool { p := pending(v); return p != nil && p.Kind == "permission" })
	expect(t, "allow", answer(pending(v).ID, map[string]any{"decision": "allow_always"}), 200)

	v = poll("the end of turn one", func(v view) bool {
		return v.Run.Awaiting == "reply" && strings.Contains(transcript(v), "turn one") // events come in batches
	})
	got := transcript(v)
	for _, want := range []string{"color=Blue", "bash=allow", `\"destination\":\"session\"`, "Pick a color"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcript lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "bypassPermissions") {
		t.Fatalf("bypassPermissions was passed on:\n%s", got)
	}
	if strings.Contains(got, "control_request") {
		t.Fatalf("protocol lines in the transcript:\n%s", got)
	}

	// A follow-up in the same session, then end it.
	expect(t, "empty message", h.do("POST", fmt.Sprintf("/api/runs/%d/messages", run.ID), map[string]any{"text": " "}, nil), 422)
	expect(t, "message", h.do("POST", fmt.Sprintf("/api/runs/%d/messages", run.ID), map[string]any{"text": "and now?"}, nil), 200)
	v = poll("turn two", func(v view) bool { return strings.Contains(transcript(v), "got and now?") && v.Run.Awaiting == "reply" })
	expect(t, "end", h.do("POST", fmt.Sprintf("/api/runs/%d/end", run.ID), nil, nil), 200)
	v = poll("the finish", func(v view) bool { return v.Run.Status != "running" })
	if v.Run.Status != "succeeded" || v.Run.Result != "turn two" || v.Run.SessionID != "sess-1" ||
		v.Run.NumTurns == nil || *v.Run.NumTurns != 2 || v.Run.Awaiting != "" {
		t.Fatalf("finished run = %+v", v.Run)
	}
	expect(t, "message after the end", h.do("POST", fmt.Sprintf("/api/runs/%d/messages", run.ID), map[string]any{"text": "hi"}, nil), 422)
}
