package runner

import (
	"encoding/json"
	"testing"
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
