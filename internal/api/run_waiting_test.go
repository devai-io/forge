package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

// A run the user started stays open until they finish it: it answers and
// waits; its session parks; a reply queues the same run to resume in place
// (no prompt again, only new inbox items); Finish closes it; and `forge
// resume` lists it from its folder and takes it over.
func TestRunStaysOpenUntilFinished(t *testing.T) {
	h := setup(t)
	h.login()
	var rn store.Runner
	var token string
	expect(t, "create runner", h.newRunner("desktop", &rn, &token), 201)
	runner := &harness{t: t, srv: h.srv, api: h.api, store: h.store, client: &http.Client{}}
	bearer := []string{"Authorization", "Bearer " + token}
	var hb struct{ Repos []store.RunnerRepo }
	beat := func(running []int64, resumeInPlace bool) {
		t.Helper()
		expect(t, "heartbeat", runner.do("POST", "/api/runner/heartbeat", map[string]any{
			"hostname": "desk", "os": "linux/amd64", "version": "test",
			"capabilities": map[string]any{"claude": true, "permission_modes": []string{"plan"}, "interactive": true,
				"resume_in_place": resumeInPlace, "terminal_resume": true, "max_concurrent": 2},
			"running": running,
		}, &hb, bearer...), 200)
	}
	beat(nil, true)
	var repoPath string
	var repoID int64
	for _, r := range hb.Repos {
		if r.Path != "" {
			repoID, repoPath = r.ID, r.Path
			break
		}
	}
	if repoID == 0 {
		t.Fatal("no repo with a path in the seed")
	}

	var run store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": rn.ID, "repo_id": repoID, "kind": "agent",
		"prompt": "Look into it", "interactive": true}, &run), 201)
	var claim struct{ Run store.Run }
	expect(t, "claim", runner.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	if claim.Run.Resumes != 0 {
		t.Fatalf("fresh run resumes = %d", claim.Run.Resumes)
	}
	// One follow-up during the session, read by the machine (inbox id 1).
	expect(t, "follow-up", h.do("POST", fmt.Sprintf("/api/runs/%d/messages", run.ID), map[string]string{"text": "also this"}, nil), 200)
	// The session parks after a quiet spell, having read the inbox.
	expect(t, "park", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", run.ID), map[string]any{
		"status": "waiting", "session_id": "sess-0042-aaaa", "result": "Found it.", "inbox_after": 1}, nil, bearer...), 204)
	expect(t, "run", h.do("GET", fmt.Sprintf("/api/runs/%d", run.ID), nil, &run), 200)
	if run.Status != "waiting" || run.InboxAfter != 1 {
		t.Fatalf("parked = %+v", run)
	}
	var list struct{ Runs []store.Run }
	expect(t, "open runs", h.do("GET", "/api/runs?status=queued,running,waiting", nil, &list), 200)
	if len(list.Runs) != 1 || list.Runs[0].ID != run.ID {
		t.Fatalf("open runs = %+v", list.Runs)
	}

	// A reply queues the same run again, to resume in place.
	expect(t, "reply", h.do("POST", fmt.Sprintf("/api/runs/%d/messages", run.ID), map[string]string{"text": "go on"}, &run), 200)
	if run.Status != "queued" || run.Resumes != 1 {
		t.Fatalf("after the reply = %+v", run)
	}
	expect(t, "claim the resume", runner.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	if claim.Run.ID != run.ID || claim.Run.Resumes != 1 || claim.Run.ResumeSession != "sess-0042-aaaa" || claim.Run.InboxAfter != 1 {
		t.Fatalf("resumed claim = %+v", claim.Run)
	}
	var inbox struct {
		Items []store.InboxItem
	}
	expect(t, "inbox after", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/inbox", run.ID), map[string]int64{"after": claim.Run.InboxAfter}, &inbox, bearer...), 200)
	if len(inbox.Items) != 1 || inbox.Items[0].Kind != "message" {
		t.Fatalf("only the new message: %+v", inbox.Items)
	}

	// A message the session never read before parking resumes it at once.
	expect(t, "late message", h.do("POST", fmt.Sprintf("/api/runs/%d/messages", run.ID), map[string]string{"text": "one more"}, nil), 200)
	expect(t, "park with one unread", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", run.ID), map[string]any{
		"status": "waiting", "inbox_after": inbox.Items[0].ID}, nil, bearer...), 204)
	expect(t, "run", h.do("GET", fmt.Sprintf("/api/runs/%d", run.ID), nil, &run), 200)
	if run.Status != "queued" || run.Resumes != 2 {
		t.Fatalf("unread message should requeue: %+v", run)
	}
	expect(t, "claim again", runner.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)

	// Finish while live: the session is told to end, then the run succeeds.
	expect(t, "finish live", h.do("POST", fmt.Sprintf("/api/runs/%d/finish", run.ID), nil, &run), 200)
	if run.Status != "running" || !run.FinishRequested {
		t.Fatalf("finish requested = %+v", run)
	}
	expect(t, "machine ends", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", run.ID), map[string]any{"status": "succeeded"}, nil, bearer...), 204)
	expect(t, "run", h.do("GET", fmt.Sprintf("/api/runs/%d", run.ID), nil, &run), 200)
	if run.Status != "succeeded" {
		t.Fatalf("finished = %+v", run)
	}

	// `forge resume`: the machine lists the folder's runs and takes one over.
	var resumable struct {
		Runs []map[string]any
	}
	expect(t, "runs for the folder", runner.do("GET", "/api/runner/runs?path="+repoPath, nil, &resumable, bearer...), 200)
	if len(resumable.Runs) != 1 || resumable.Runs[0]["session_id"] != "sess-0042-aaaa" || resumable.Runs[0]["dir"] != repoPath {
		t.Fatalf("resumable = %+v", resumable.Runs)
	}
	var taken struct{ Run map[string]any }
	expect(t, "hand off", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/handoff", run.ID), map[string]any{}, &taken, bearer...), 200)
	if taken.Run["handoff"] != "desktop · shell" {
		t.Fatalf("handed off = %+v", taken.Run)
	}

	// An agent that cannot resume in place: a reply to a waiting run is refused
	// with what to do instead.
	var old store.Run
	expect(t, "queue 2", h.do("POST", "/api/runs", map[string]any{"runner_id": rn.ID, "repo_id": repoID, "kind": "agent", "prompt": "Second"}, &old), 201)
	expect(t, "claim 2", runner.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	beat([]int64{old.ID}, false)
	expect(t, "one-shot answers", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", old.ID), map[string]any{
		"status": "succeeded", "session_id": "sess-0043-bbbb"}, nil, bearer...), 204)
	expect(t, "reply to an old agent", h.do("POST", fmt.Sprintf("/api/runs/%d/messages", old.ID), map[string]string{"text": "and?"}, nil), 422)
	expect(t, "cancel a waiting run", h.do("POST", fmt.Sprintf("/api/runs/%d/cancel", old.ID), nil, &old), 200)
	if old.Status != "cancelled" {
		t.Fatalf("cancelled = %+v", old)
	}
}

// A question still open when the session parks (the time limit caught it)
// stays answerable; the answer resumes the session in place as the reply.
func TestAnswerAfterPark(t *testing.T) {
	h := setup(t)
	h.login()
	var rn store.Runner
	var token string
	expect(t, "create runner", h.newRunner("desktop", &rn, &token), 201)
	runner := &harness{t: t, srv: h.srv, api: h.api, store: h.store, client: &http.Client{}}
	bearer := []string{"Authorization", "Bearer " + token}
	var hb struct{ Repos []store.RunnerRepo }
	expect(t, "heartbeat", runner.do("POST", "/api/runner/heartbeat", map[string]any{
		"hostname": "desk", "os": "linux/amd64", "version": "test",
		"capabilities": map[string]any{"claude": true, "permission_modes": []string{"plan"}, "interactive": true, "resume_in_place": true, "max_concurrent": 2},
		"running":      []int64{},
	}, &hb, bearer...), 200)
	var repoID int64
	for _, r := range hb.Repos {
		if r.Path != "" {
			repoID = r.ID
			break
		}
	}
	var run store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": rn.ID, "repo_id": repoID, "kind": "agent",
		"prompt": "Deploy?", "interactive": true}, &run), 201)
	var claim struct{ Run store.Run }
	expect(t, "claim", runner.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	var asked struct{ Prompt store.RunPrompt }
	expect(t, "question", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/prompts", run.ID), map[string]any{
		"request_id": "q1", "tool_name": "AskUserQuestion",
		"input": map[string]any{"questions": []any{map[string]any{"question": "Merge the PRs?", "options": []any{map[string]any{"label": "Yes"}, map[string]any{"label": "Hold"}}}}},
	}, &asked, bearer...), 200)
	// The time limit parks it with the question still open.
	expect(t, "park", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", run.ID), map[string]any{
		"status": "waiting", "session_id": "sess-0077-cccc", "exit_code": 143}, nil, bearer...), 204)
	var ev struct {
		Run     store.Run
		Prompts []store.RunPrompt
	}
	expect(t, "events", h.do("GET", fmt.Sprintf("/api/runs/%d/events", run.ID), nil, &ev), 200)
	if ev.Run.Status != "waiting" || len(ev.Prompts) != 1 || ev.Prompts[0].Status != "pending" {
		t.Fatalf("parked with the question open: run %+v prompts %+v", ev.Run, ev.Prompts)
	}
	expect(t, "answer", h.do("POST", fmt.Sprintf("/api/runs/%d/prompts/%d/answer", run.ID, asked.Prompt.ID),
		map[string]any{"decision": "answer", "answers": map[string]string{"Merge the PRs?": "Hold"}}, nil), 200)
	expect(t, "run", h.do("GET", fmt.Sprintf("/api/runs/%d", run.ID), nil, &run), 200)
	if run.Status != "queued" || run.Resumes != 1 {
		t.Fatalf("the answer resumes it: %+v", run)
	}
	expect(t, "claim the resume", runner.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	var inbox struct{ Items []store.InboxItem }
	expect(t, "inbox", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/inbox", run.ID), map[string]int64{"after": claim.Run.InboxAfter}, &inbox, bearer...), 200)
	if len(inbox.Items) != 1 || inbox.Items[0].Kind != "message" || !strings.Contains(string(inbox.Items[0].Data), "Merge the PRs? → Hold") {
		t.Fatalf("the answer as the reply: %+v", inbox.Items)
	}
}

// A one-turn run (the dashboard's Refresh) finishes once its session has
// answered instead of waiting for a reply; so does a parked run bumped with a
// one-turn message. A question left open when the session parked still keeps
// the run waiting until it is answered.
func TestOneTurnRunFinishesAfterItsTurn(t *testing.T) {
	h := setup(t)
	h.login()
	var rn store.Runner
	var token string
	expect(t, "create runner", h.newRunner("desktop", &rn, &token), 201)
	runner := &harness{t: t, srv: h.srv, api: h.api, store: h.store, client: &http.Client{}}
	bearer := []string{"Authorization", "Bearer " + token}
	var hb struct{ Repos []store.RunnerRepo }
	expect(t, "heartbeat", runner.do("POST", "/api/runner/heartbeat", map[string]any{
		"hostname": "desk", "os": "linux/amd64", "version": "test",
		"capabilities": map[string]any{"claude": true, "permission_modes": []string{"plan"}, "interactive": true, "resume_in_place": true, "max_concurrent": 2},
		"running":      []int64{},
	}, &hb, bearer...), 200)
	var repoID int64
	for _, r := range hb.Repos {
		if r.Path != "" {
			repoID = r.ID
			break
		}
	}
	inboxLast := func(id, after int64) int64 {
		t.Helper()
		var inbox struct{ Items []store.InboxItem }
		expect(t, "inbox", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/inbox", id), map[string]int64{"after": after}, &inbox, bearer...), 200)
		if len(inbox.Items) == 0 {
			t.Fatalf("run #%d: nothing in the inbox after %d", id, after)
		}
		return inbox.Items[len(inbox.Items)-1].ID
	}

	// Created as one turn: the machine reports the session parked, the run is over.
	var run store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": rn.ID, "repo_id": repoID, "kind": "agent",
		"prompt": "Status?", "interactive": true, "one_turn": true}, &run), 201)
	if !run.OneTurn {
		t.Fatalf("one turn = %+v", run)
	}
	var claim struct{ Run store.Run }
	expect(t, "claim", runner.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	if claim.Run.ID != run.ID || !claim.Run.OneTurn {
		t.Fatalf("the machine must see one_turn: %+v", claim.Run)
	}
	expect(t, "answered", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", run.ID), map[string]any{
		"status": "waiting", "session_id": "sess-0101-dddd", "result": "Already finished."}, nil, bearer...), 204)
	expect(t, "run", h.do("GET", fmt.Sprintf("/api/runs/%d", run.ID), nil, &run), 200)
	if run.Status != "succeeded" || run.FinishRequested {
		t.Fatalf("a one-turn run ends when it has answered: %+v", run)
	}

	// A parked run bumped with a one-turn message: finishes after that turn. A
	// live session is not changed by the flag.
	var parked store.Run
	expect(t, "queue 2", h.do("POST", "/api/runs", map[string]any{"runner_id": rn.ID, "repo_id": repoID, "kind": "agent",
		"prompt": "Look into it", "interactive": true}, &parked), 201)
	expect(t, "claim 2", runner.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	expect(t, "message while live", h.do("POST", fmt.Sprintf("/api/runs/%d/messages", parked.ID), map[string]any{"text": "also this", "one_turn": true}, &parked), 200)
	if parked.OneTurn {
		t.Fatalf("a live session keeps its lifecycle: %+v", parked)
	}
	expect(t, "park", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", parked.ID), map[string]any{
		"status": "waiting", "session_id": "sess-0102-eeee", "inbox_after": inboxLast(parked.ID, 0)}, nil, bearer...), 204)
	expect(t, "status check", h.do("POST", fmt.Sprintf("/api/runs/%d/messages", parked.ID), map[string]any{"text": "Status?", "one_turn": true}, &parked), 200)
	if parked.Status != "queued" || parked.Resumes != 1 || !parked.OneTurn {
		t.Fatalf("resumed for one turn: %+v", parked)
	}
	expect(t, "claim the resume", runner.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	if claim.Run.ID != parked.ID || !claim.Run.OneTurn {
		t.Fatalf("resumed claim = %+v", claim.Run)
	}
	expect(t, "answered 2", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", parked.ID), map[string]any{
		"status": "waiting", "inbox_after": inboxLast(parked.ID, claim.Run.InboxAfter)}, nil, bearer...), 204)
	expect(t, "run 2", h.do("GET", fmt.Sprintf("/api/runs/%d", parked.ID), nil, &parked), 200)
	if parked.Status != "succeeded" {
		t.Fatalf("the status check's turn ends the run: %+v", parked)
	}

	// A one-turn run parked on an open question stays answerable; the answer's
	// turn is the one that ends it.
	var asking store.Run
	expect(t, "queue 3", h.do("POST", "/api/runs", map[string]any{"runner_id": rn.ID, "repo_id": repoID, "kind": "agent",
		"prompt": "Deploy?", "interactive": true, "one_turn": true}, &asking), 201)
	expect(t, "claim 3", runner.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	var asked struct{ Prompt store.RunPrompt }
	expect(t, "question", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/prompts", asking.ID), map[string]any{
		"request_id": "q1", "tool_name": "AskUserQuestion",
		"input": map[string]any{"questions": []any{map[string]any{"question": "Merge?", "options": []any{map[string]any{"label": "Yes"}}}}},
	}, &asked, bearer...), 200)
	expect(t, "park with the question", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", asking.ID), map[string]any{
		"status": "waiting", "session_id": "sess-0103-ffff", "exit_code": 143}, nil, bearer...), 204)
	expect(t, "run 3", h.do("GET", fmt.Sprintf("/api/runs/%d", asking.ID), nil, &asking), 200)
	if asking.Status != "waiting" {
		t.Fatalf("an open question keeps a one-turn run waiting: %+v", asking)
	}
	expect(t, "answer", h.do("POST", fmt.Sprintf("/api/runs/%d/prompts/%d/answer", asking.ID, asked.Prompt.ID),
		map[string]any{"decision": "answer", "answers": map[string]string{"Merge?": "Yes"}}, nil), 200)
	expect(t, "claim the answer", runner.do("POST", "/api/runner/claim", nil, &claim, bearer...), 200)
	if claim.Run.ID != asking.ID || claim.Run.Resumes != 1 {
		t.Fatalf("the answer resumes it: %+v", claim.Run)
	}
	expect(t, "answered 3", runner.do("POST", fmt.Sprintf("/api/runner/runs/%d/finish", asking.ID), map[string]any{
		"status": "waiting", "inbox_after": inboxLast(asking.ID, claim.Run.InboxAfter)}, nil, bearer...), 204)
	expect(t, "run 3 again", h.do("GET", fmt.Sprintf("/api/runs/%d", asking.ID), nil, &asking), 200)
	if asking.Status != "succeeded" {
		t.Fatalf("answered, so over: %+v", asking)
	}
}
