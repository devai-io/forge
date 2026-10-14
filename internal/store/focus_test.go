package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/devai-io/forge/internal/store"
)

func TestFocusDirtyMarksAndSave(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	pid := must(st.ProjectIDByKey(ctx, "SHOP"))

	if _, err := st.FocusByProject(ctx, pid); err != store.ErrNotFound {
		t.Fatalf("no focus yet: %v", err)
	}
	due := func(settle time.Duration) []store.DirtyFocus {
		t.Helper()
		return must(st.DirtyFocuses(ctx, settle, 10))
	}
	if len(due(0)) != 0 {
		t.Fatal("nothing changed yet")
	}

	task := must(st.CreateTask(ctx, store.TaskInput{ProjectKey: "SHOP", Title: "Add a status page"}))
	got := due(0)
	if len(got) != 1 || got[0].ProjectID != pid || !strings.Contains(got[0].Reason, task.Ref) {
		t.Fatalf("create marks dirty: %+v", got)
	}
	if len(due(time.Hour)) != 0 {
		t.Fatal("a fresh change has not settled")
	}

	// Saving clears the mark; a change after the computation started keeps it.
	started := time.Now()
	ok(t, st.SaveFocus(ctx, pid, store.Focus{Headline: "Ship it"}, "rules", "", "first look", "", started))
	f := must(st.FocusByProject(ctx, pid))
	if f.Dirty || f.Focus.Headline != "Ship it" || f.Source != "rules" || f.GeneratedAt == nil {
		t.Fatalf("saved: %+v", f)
	}
	if f.Focus.Now == nil || f.Focus.Roadmap == nil {
		t.Fatal("lists are never null")
	}

	started = time.Now().Add(-time.Second)
	must(st.UpdateTask(ctx, task.ID, store.Patch{"status": json.RawMessage(`"in_progress"`)}))
	ok(t, st.SaveFocus(ctx, pid, store.Focus{Headline: "Again"}, "ai", "fake", "after x", "", started))
	if f := must(st.FocusByProject(ctx, pid)); !f.Dirty {
		t.Fatal("a change during the computation keeps it dirty")
	}
	ok(t, st.SaveFocus(ctx, pid, store.Focus{Headline: "Again"}, "ai", "fake", "after x", "", time.Now().Add(time.Second)))

	// Comments, deletes and check-up item changes mark it too.
	must(st.CreateComment(ctx, task.ID, "started"))
	if len(due(0)) != 1 {
		t.Fatal("comment marks dirty")
	}
	ok(t, st.SaveFocus(ctx, pid, store.Focus{}, "rules", "", "", "", time.Now().Add(time.Second)))
	ok(t, st.DeleteTask(ctx, task.ID))
	if len(due(0)) != 1 {
		t.Fatal("delete marks dirty")
	}
}

func TestFocusRulesAndInputs(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	pid := must(st.ProjectIDByKey(ctx, "SHOP"))
	in := must(st.FocusInputs(ctx, pid, "2030-01-01"))
	if in.Key != "SHOP" || len(in.Open) == 0 || len(in.Repos) == 0 {
		t.Fatalf("inputs: %+v", in)
	}
	f := store.FocusRules(in)
	if f.Headline == "" || len(f.Now) == 0 || len(f.Now) > 5 {
		t.Fatalf("now: %+v", f)
	}
	// In progress comes first; the blocked task is a blocker, not an action.
	if f.Now[0].Kind != "task" || !strings.HasPrefix(f.Now[0].Title, "Finish ") {
		t.Fatalf("first action: %+v", f.Now[0])
	}
	if len(f.Blockers) == 0 || f.Blockers[0].Ref == nil {
		t.Fatalf("blockers: %+v", f.Blockers)
	}
	seen := map[string]bool{}
	for _, list := range [][]store.FocusItem{f.Now, f.Next, f.Blockers} {
		for _, it := range list {
			if it.Ref != nil {
				if seen[*it.Ref] {
					t.Fatalf("%s listed twice", *it.Ref)
				}
				seen[*it.Ref] = true
			}
		}
	}
	if len(f.Roadmap) == 0 {
		t.Fatal("roadmap from labels")
	}
	for _, s := range f.Roadmap {
		if !store.OneOf(s.Status, store.RoadmapStatuses) {
			t.Fatalf("status %q", s.Status)
		}
	}

	// Nothing open: a plain headline, empty lists.
	empty := store.FocusRules(&store.FocusInputs{Today: "2030-01-01"})
	if !strings.Contains(empty.Headline, "No open tasks") || empty.Now == nil || len(empty.Now) != 0 {
		t.Fatalf("empty: %+v", empty)
	}
}

func TestCleanFocus(t *testing.T) {
	ref := func(s string) *string { return &s }
	long := strings.Repeat("word ", 100)
	var now []store.FocusItem
	for i := 0; i < 9; i++ {
		now = append(now, store.FocusItem{Title: "Do thing", Ref: ref("shop-1"), Kind: "weird"})
	}
	now = append([]store.FocusItem{{Title: "  ", Why: "dropped: no title"}, {Title: long, Why: long, Ref: ref("SHOP-99")}}, now...)
	f := store.CleanFocus(store.Focus{
		Headline: long,
		Now:      now,
		Roadmap:  []store.RoadmapStep{{Title: "Launch", Status: "someday", Refs: []string{"SHOP-1", "NOPE-2"}}},
	}, map[string]bool{"SHOP-1": true})
	if len(f.Now) != 5 {
		t.Fatalf("now clipped to 5: %d", len(f.Now))
	}
	if f.Now[0].Ref != nil || len([]rune(f.Now[0].Title)) > 160 || f.Now[0].Kind != "other" {
		t.Fatalf("unknown ref dropped, title clipped: %+v", f.Now[0])
	}
	if f.Now[1].Ref == nil || *f.Now[1].Ref != "SHOP-1" || f.Now[1].Kind != "task" {
		t.Fatalf("known ref kept and upper-cased: %+v", f.Now[1])
	}
	if f.Roadmap[0].Status != "planned" || len(f.Roadmap[0].Refs) != 1 {
		t.Fatalf("roadmap: %+v", f.Roadmap[0])
	}
	if len([]rune(f.Headline)) > 200 || f.Next == nil || f.Blockers == nil {
		t.Fatalf("headline / lists: %+v", f)
	}
}

// A Claude Code session that stops (its window closes, or Claude exits in
// it) marks the focus of the project its directory belongs to.
func TestFocusDirtyWhenClaudeSessionEnds(t *testing.T) {
	cur = t
	st := open(t)
	ctx := context.Background()
	shop := must(st.ProjectIDByKey(ctx, "SHOP"))
	rn := must(st.CreateRunner(ctx, "desk", []byte("tok")))
	beat := func(sessions []store.TmuxSession) {
		t.Helper()
		must(st.Heartbeat(ctx, rn.ID, store.HeartbeatInput{Hostname: "desk", Running: []int64{}, Tmux: sessions}))
	}
	dirty := func() (bool, string) {
		t.Helper()
		for _, d := range must(st.DirtyFocuses(ctx, 0, 10)) {
			if d.ProjectID == shop {
				return true, d.Reason
			}
		}
		return false, ""
	}
	claude := []store.TmuxSession{{Name: "SHOP-api", Path: "/home/demo/dev/shop/shop_api", Claude: true,
		WindowList: []store.TmuxWindow{
			{Index: 0, Path: "/home/demo/dev/shop/shop_api", Claude: true},
			{Index: 1, Path: "/home/demo/dev/shop/shop_api", Command: "zsh"},
		}}}
	beat(claude)
	beat(claude) // still running: nothing to say
	if d, _ := dirty(); d {
		t.Fatal("a running session marked the focus")
	}
	// Claude exits; the shell window stays.
	beat([]store.TmuxSession{{Name: "SHOP-api", Path: "/home/demo/dev/shop/shop_api",
		WindowList: []store.TmuxWindow{{Index: 0, Path: "/home/demo/dev/shop/shop_api", Command: "zsh"}}}})
	if d, why := dirty(); !d || !strings.Contains(why, "SHOP-api ended") {
		t.Fatalf("after Claude exited: dirty=%v %q", d, why)
	}
}
