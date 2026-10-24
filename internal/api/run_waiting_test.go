package api

import (
	"fmt"
	"net/http"
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
	runner := &harness{t: t, srv: h.srv, client: &http.Client{}}
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
