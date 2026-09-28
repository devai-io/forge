package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestStreaks(t *testing.T) {
	today := day("2026-09-27")
	cases := []struct {
		name          string
		days          []string
		current, best int
	}{
		{"none", nil, 0, 0},
		{"today only", []string{"2026-09-27"}, 1, 1},
		{"yesterday keeps the streak alive", []string{"2026-09-25", "2026-09-26"}, 2, 2},
		{"two days ago breaks it", []string{"2026-09-24", "2026-09-25"}, 0, 2},
		{"gap resets the run", []string{"2026-09-01", "2026-09-02", "2026-09-03", "2026-09-26", "2026-09-27"}, 2, 3},
	}
	for _, c := range cases {
		var days []time.Time
		for _, d := range c.days {
			days = append(days, day(d))
		}
		cur, best := Streaks(days, today)
		if cur != c.current || best != c.best {
			t.Errorf("%s: got (%d, %d), want (%d, %d)", c.name, cur, best, c.current, c.best)
		}
	}
}

func TestTodayUsesTheViewersCalendar(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata")
	}
	// 23:30 UTC on a Sunday is already Monday in Berlin.
	now := time.Date(2026, 9, 27, 23, 30, 0, 0, time.UTC)
	today, week := Today(now, berlin)
	if got := dateStr(today); got != "2026-09-28" {
		t.Errorf("today = %s, want 2026-09-28", got)
	}
	if got := dateStr(week); got != "2026-09-28" {
		t.Errorf("week start = %s, want the same Monday", got)
	}
	_, week = Today(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC), berlin)
	if got := dateStr(week); got != "2026-09-21" {
		t.Errorf("week start for a Sunday = %s, want the Monday before", got)
	}
}

func TestValidators(t *testing.T) {
	for _, k := range []string{"AB", "ALPHA", "SRV", "X1", "ABCDEFGHIJ"} {
		if !IsProjectKey(k) {
			t.Errorf("%q should be a valid key", k)
		}
	}
	for _, k := range []string{"", "A", "1AB", "alpha", "AB-C", "ABCDEFGHIJK"} {
		if IsProjectKey(k) {
			t.Errorf("%q should be rejected", k)
		}
	}
	if !IsColor("#aBc123") || IsColor("abc123") || IsColor("#abc") || IsColor("#gggggg") {
		t.Error("IsColor")
	}
	if !IsDate("2026-10-06") || IsDate("2026-1-6") || IsDate("06/10/2026") {
		t.Error("IsDate")
	}
}

func TestCleanStrings(t *testing.T) {
	got, err := CleanStrings("labels", []string{" launch ", "launch", "", "ops"}, 5, 10)
	if err != nil || len(got) != 2 || got[0] != "launch" || got[1] != "ops" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := CleanStrings("labels", []string{"this-one-is-too-long"}, 5, 10); err == nil {
		t.Error("an over-long entry should be rejected")
	}
}

func TestUpdateSetRejectsUnknownFields(t *testing.T) {
	_, _, err := updateSet(Patch{"titel": json.RawMessage(`"x"`)}, taskFields, taskReadOnly, 2)
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Field != "titel" {
		t.Fatalf("want a validation error naming the field, got %v", err)
	}
	// Read-only fields the UI echoes back are ignored, not errors.
	set, args, err := updateSet(Patch{"ref": json.RawMessage(`"X-1"`), "focus": json.RawMessage(`true`)},
		taskFields, taskReadOnly, 2)
	if err != nil || set != "focus = $2" || len(args) != 1 {
		t.Fatalf("got %q %v %v", set, args, err)
	}
	if _, _, err := updateSet(Patch{"due_date": json.RawMessage(`"tomorrow"`)}, taskFields, taskReadOnly, 2); err == nil {
		t.Error("a malformed date should be rejected")
	}
	set, args, err = updateSet(Patch{"due_date": json.RawMessage(`null`)}, taskFields, taskReadOnly, 2)
	if err != nil || set != "due_date = $2" || args[0] != nil {
		t.Fatalf("null should clear: %q %v %v", set, args, err)
	}
}

func TestExcerpt(t *testing.T) {
	if got := excerpt("short", 10); got != "short" {
		t.Error(got)
	}
	if got := excerpt("one two three four five six", 12); got != "one two…" {
		t.Errorf("got %q", got)
	}
}
