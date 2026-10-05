package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

func TestMemoryLifecycle(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()

	// The web app saves, finds (stemmed: "deploying" finds "deploys"), edits
	// and forgets.
	var saved store.MemorySaved
	expect(t, "create", h.do("POST", "/api/memories", map[string]any{"project_key": "shop", "kind": "gotcha",
		"content": "Checkout deploys need the payments feature flag flipped first"}, &saved), 201)
	if saved.Memory.ProjectKey == nil || *saved.Memory.ProjectKey != "SHOP" || saved.Memory.Source != "user" || saved.Duplicate {
		t.Fatalf("saved = %+v", saved.Memory)
	}
	expect(t, "same words again", h.do("POST", "/api/memories", map[string]any{"project_key": "SHOP",
		"content": "  checkout deploys need the payments feature flag flipped first"}, &saved), 201)
	if !saved.Duplicate {
		t.Fatal("duplicate not detected")
	}
	expect(t, "bad kind", h.do("POST", "/api/memories", map[string]any{"project_key": "SHOP", "kind": "rumour", "content": "x y z"}, nil), 422)
	expect(t, "no such project", h.do("POST", "/api/memories", map[string]any{"project_key": "NOPE", "content": "x y z"}, nil), 422)
	expect(t, "global", h.do("POST", "/api/memories", map[string]any{"kind": "preference",
		"content": "Prefers small commits with conventional prefixes"}, nil), 201)

	var near store.MemorySaved
	expect(t, "near copy", h.do("POST", "/api/memories", map[string]any{"project_key": "SHOP",
		"content": "Flip the payments flag before deploying checkout"}, &near), 201)
	if len(near.Similar) != 1 || near.Similar[0].ID != saved.Memory.ID {
		t.Fatalf("similar = %+v", near.Similar)
	}

	var list struct {
		Memories []store.Memory
		Counts   map[string]int
	}
	expect(t, "search", h.do("GET", "/api/memories?project=SHOP&q=deploying", nil, &list), 200)
	if len(list.Memories) != 2 || list.Counts["SHOP"] != 2 || list.Counts[""] != 1 {
		t.Fatalf("search = %+v", list)
	}
	expect(t, "global only", h.do("GET", "/api/memories?project=global", nil, &list), 200)
	if len(list.Memories) != 1 || list.Memories[0].ProjectKey != nil {
		t.Fatalf("global = %+v", list.Memories)
	}

	var m store.Memory
	expect(t, "edit", h.do("PATCH", fmt.Sprintf("/api/memories/%d", near.Memory.ID),
		map[string]any{"content": "Stripe webhooks retry for three days", "pinned": true}, &m), 200)
	expect(t, "old words gone", h.do("GET", "/api/memories?project=SHOP&q=deploying", nil, &list), 200)
	if len(list.Memories) != 1 {
		t.Fatalf("after edit = %+v", list.Memories)
	}
	expect(t, "new words found", h.do("GET", "/api/memories?q=webhooks", nil, &list), 200)
	if len(list.Memories) != 1 || !list.Memories[0].Pinned {
		t.Fatalf("webhooks = %+v", list.Memories)
	}

	// Sessions start with them: the project's and the user's.
	c, err := h.store.ContextForPath(ctx, "/home/demo/dev/shop/shop_ui", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Memory: what earlier sessions learned here", "(gotcha) Checkout deploys",
		"(fact, pinned) Stripe webhooks", "## Memory: about the user", "Prefers small commits", "forge_remember"} {
		if !strings.Contains(c.Markdown, want) {
			t.Fatalf("context lacks %q:\n%s", want, c.Markdown)
		}
	}
	if len(c.MemoryIDs) != 3 {
		t.Fatalf("memory ids = %v", c.MemoryIDs)
	}
	// Pinned first.
	if strings.Index(c.Markdown, "Stripe webhooks") > strings.Index(c.Markdown, "Checkout deploys") {
		t.Fatalf("pinned is not first:\n%s", c.Markdown)
	}

	expect(t, "forget", h.do("DELETE", fmt.Sprintf("/api/memories/%d", m.ID), nil, nil), 204)
	expect(t, "forget twice", h.do("DELETE", fmt.Sprintf("/api/memories/%d", m.ID), nil, nil), 404)
	expect(t, "gone from search", h.do("GET", "/api/memories?q=webhooks", nil, &list), 200)
	if len(list.Memories) != 0 {
		t.Fatalf("still found: %+v", list.Memories)
	}

	// Deleting a project takes its memories along, index included.
	expect(t, "delete project", h.do("DELETE", "/api/projects/SHOP", nil, nil), 204)
	expect(t, "nothing left", h.do("GET", "/api/memories?q=checkout", nil, &list), 200)
	if len(list.Memories) != 0 {
		t.Fatalf("after project delete: %+v", list.Memories)
	}
	var orphans int
	_ = h.store.DB.QueryRow(ctx, `SELECT count(*) FROM memories_fts WHERE rowid NOT IN (SELECT id FROM memories)`).Scan(&orphans)
	if orphans != 0 {
		t.Fatalf("%d orphaned index rows", orphans)
	}
}

func TestRunnerMemoryAndRecall(t *testing.T) {
	h := setup(t)
	h.login()
	var rn store.Runner
	var token string
	expect(t, "machine", h.newRunner("desk", &rn, &token), 201)
	machine := &harness{t: t, srv: h.srv, client: &http.Client{}}
	auth := []string{"Authorization", "Bearer " + token}

	// Claude saves from a repo directory: the project comes from the path.
	var saved store.MemorySaved
	expect(t, "remember", machine.do("POST", "/api/runner/memories", map[string]any{"cwd": "/home/demo/dev/shop/shop_api",
		"kind": "convention", "content": "Migrations live in db/migrations and run at startup"}, &saved, auth...), 201)
	if saved.Memory.ProjectKey == nil || *saved.Memory.ProjectKey != "SHOP" || saved.Memory.Source != "claude" ||
		saved.Memory.Origin != "Claude on desk" {
		t.Fatalf("saved = %+v", saved.Memory)
	}
	expect(t, "outside any project", machine.do("POST", "/api/runner/memories", map[string]any{"cwd": "/tmp",
		"content": "something"}, nil, auth...), 422)
	expect(t, "global", machine.do("POST", "/api/runner/memories", map[string]any{"cwd": "/tmp", "global": true,
		"kind": "preference", "content": "Answers in Portuguese when asked in Portuguese"}, nil, auth...), 201)

	var res struct {
		ProjectKey string `json:"project_key"`
		Memories   []store.Memory
	}
	expect(t, "recall", machine.do("GET", "/api/runner/memories?cwd=/home/demo/dev/shop/shop_api&q=migration", nil, &res, auth...), 200)
	if res.ProjectKey != "SHOP" || len(res.Memories) != 1 {
		t.Fatalf("recall = %+v", res)
	}
	m, _ := h.store.MemoryByID(context.Background(), saved.Memory.ID)
	if m.Recalls != 1 {
		t.Fatalf("recalls = %d", m.Recalls)
	}

	// The prompt hook recalls a memory sharing words with the request —
	// only for agents that say what they know, and only once.
	var match struct {
		Memories []store.Memory
		Markdown string
	}
	prompt := map[string]any{"cwd": "/home/demo/dev/shop/shop_api", "prompt": "add a migration for the orders table; it must apply at startup"}
	expect(t, "old agent", machine.do("POST", "/api/runner/match", prompt, &match, auth...), 200)
	if len(match.Memories) != 0 {
		t.Fatalf("old agent got memories: %+v", match.Memories)
	}
	prompt["known_memories"] = []int64{}
	expect(t, "match", machine.do("POST", "/api/runner/match", prompt, &match, auth...), 200)
	if len(match.Memories) != 1 || !strings.Contains(match.Markdown, "Migrations live in db/migrations") {
		t.Fatalf("match = %+v", match)
	}
	prompt["known_memories"] = []int64{saved.Memory.ID}
	expect(t, "known", machine.do("POST", "/api/runner/match", prompt, &match, auth...), 200)
	if len(match.Memories) != 0 {
		t.Fatalf("recalled again: %+v", match.Memories)
	}
	prompt["prompt"], prompt["known_memories"] = "what is the weather like on startup day", []int64{}
	expect(t, "unrelated", machine.do("POST", "/api/runner/match", prompt, &match, auth...), 200)
	if len(match.Memories) != 0 {
		t.Fatalf("unrelated request recalled: %+v", match.Memories)
	}

	var upd store.Memory
	expect(t, "update", machine.do("PATCH", fmt.Sprintf("/api/runner/memories/%d", saved.Memory.ID),
		map[string]any{"kind": "howto"}, &upd, auth...), 200)
	if upd.Kind != "howto" {
		t.Fatalf("kind = %s", upd.Kind)
	}
	expect(t, "forget", machine.do("DELETE", fmt.Sprintf("/api/runner/memories/%d", saved.Memory.ID), nil, nil, auth...), 204)
}
