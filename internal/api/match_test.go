package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/devai-io/forge/internal/store"
)

func TestPromptMatchesTasks(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := context.Background()
	var created struct {
		Runner store.Runner
		Token  string
	}
	expect(t, "machine", h.newRunner("desk", &created.Runner, &created.Token), 201)
	runner := &harness{t: t, srv: h.srv, client: &http.Client{}}
	bearer := []string{"Authorization", "Bearer " + created.Token}
	refOf := func(title string) string {
		tasks, err := h.store.ListTasks(ctx, store.TaskFilter{ProjectKey: "SHOP", Query: title, Limit: 5})
		if err != nil || len(tasks) == 0 {
			t.Fatalf("no task %q: %v", title, err)
		}
		return tasks[0].Ref
	}
	rateLimit := refOf("rate limiting")
	checkout := refOf("checkout flow")

	type result struct {
		ProjectKey string `json:"project_key"`
		Matches    []taskMatch
		Markdown   string
	}
	match := func(cwd, prompt string, known ...string) result {
		t.Helper()
		var res result
		expect(t, "match", runner.do("POST", "/api/runner/match", map[string]any{"cwd": cwd, "prompt": prompt, "known": known}, &res, bearer...), 200)
		return res
	}
	refs := func(r result) string {
		var out []string
		for _, m := range r.Matches {
			out = append(out, m.Ref+":"+m.Why)
		}
		return strings.Join(out, ",")
	}
	cwd := "/home/demo/dev/shop/shop_api"

	// Without Jev: shared keywords with the title.
	r := match(cwd, "add rate limiting on the login endpoint, 5 tries a minute")
	if r.ProjectKey != "SHOP" || refs(r) != rateLimit+":keywords" || !strings.Contains(r.Markdown, "forge_update_task") {
		t.Fatalf("keywords: %+v", r)
	}
	// Unrelated requests and short follow-ups add nothing.
	if r := match(cwd, "explain how goroutines are scheduled in the runtime"); len(r.Matches) != 0 || r.Markdown != "" {
		t.Fatalf("unrelated: %+v", r)
	}
	if r := match(cwd, "yes, go on"); len(r.Matches) != 0 {
		t.Fatalf("short: %+v", r)
	}
	// A named ref always counts, even in a short prompt.
	if r := match(cwd, "do "+strings.ToLower(checkout)); refs(r) != checkout+":named" {
		t.Fatalf("named: %+v", r)
	}
	// Refs the session already knows are not repeated.
	if r := match(cwd, "add rate limiting on the login endpoint", rateLimit); len(r.Matches) != 0 || r.Markdown != "" {
		t.Fatalf("known: %+v", r)
	}

	// With Jev: its probabilities decide; everything but one task scores low.
	low := map[string]bool{}
	for i := 0; i < 60; i++ {
		low["t"+itoa(int64(i))] = true
	}
	tasks, _ := h.store.ListTasks(ctx, store.TaskFilter{ProjectKey: "SHOP", Open: true, Limit: matchMaxTasks, Today: store.TodayFor("UTC")})
	for i, tk := range tasks {
		if tk.Ref == checkout {
			delete(low, "t"+itoa(int64(i)))
		}
	}
	srv, calls := fakeJev(t, "", 0, 0.9, low)
	h.enableJev(srv.URL)
	before := calls.Load()
	if r := match(cwd, "the payment step loses the basket when I go back"); refs(r) != checkout+":jev" || calls.Load() != before+1 {
		t.Fatalf("jev: %+v", r)
	}
}
