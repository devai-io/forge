package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

func TestArchiveRun(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()
	var rn store.Runner
	var token string
	expect(t, "machine", h.newRunner("box", &rn, &token), 201)
	var shop store.ProjectDetail
	expect(t, "shop", h.do("GET", "/api/projects/SHOP", nil, &shop), 200)
	var run store.Run
	expect(t, "queue", h.do("POST", "/api/runs", map[string]any{"runner_id": rn.ID, "repo_id": shop.Repos[0].ID,
		"kind": "agent", "prompt": "Tidy the README", "engine": "claude"}, &run), 201)
	path := fmt.Sprintf("/api/runs/%d/archive", run.ID)

	listed := func(query string) bool {
		t.Helper()
		var out struct{ Runs []store.Run }
		expect(t, "list "+query, h.do("GET", "/api/runs"+query, nil, &out), 200)
		for _, r := range out.Runs {
			if r.ID == run.ID {
				return true
			}
		}
		return false
	}

	var got store.Run
	expect(t, "archive", h.do("POST", path, map[string]any{}, &got), 200)
	if got.ArchivedAt == nil || got.Status != "queued" {
		t.Fatalf("archived = %+v", got)
	}
	if listed("") || !listed("?archived=1") || !listed("?archived=all") {
		t.Fatal("an archived run is off the lists, and only there when asked for")
	}
	expect(t, "unarchive", h.do("POST", path, map[string]any{"archived": false}, &got), 200)
	if got.ArchivedAt != nil || !listed("") || listed("?archived=1") {
		t.Fatalf("unarchived = %+v", got)
	}
	expect(t, "unknown run", h.do("POST", "/api/runs/999999/archive", map[string]any{}, nil), 404)

	// It comes back by itself when it needs you: queued again (a reply
	// resumes it), or a question.
	unarchived := func() bool {
		r, err := h.store.RunByID(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		return r.ArchivedAt == nil
	}
	if _, err := h.store.DB.Exec(ctx, `UPDATE runs SET status = 'waiting' WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	expect(t, "archive waiting", h.do("POST", path, map[string]any{}, nil), 200)
	if unarchived() {
		t.Fatal("not archived")
	}
	if _, err := h.store.DB.Exec(ctx, `UPDATE runs SET status = 'queued' WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if !unarchived() {
		t.Fatal("queued again but still archived")
	}
	expect(t, "archive again", h.do("POST", path, map[string]any{}, nil), 200)
	if _, err := h.store.DB.Exec(ctx, `INSERT INTO run_prompts (run_id, request_id, kind) VALUES ($1, 'q1', 'question')`, run.ID); err != nil {
		t.Fatal(err)
	}
	if !unarchived() {
		t.Fatal("asked a question but still archived")
	}
}
