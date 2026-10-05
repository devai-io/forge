package telegram

import (
	"strings"
	"testing"
)

func TestHTML(t *testing.T) {
	got := HTML("## Done\n\n- fixed **auth** in `a<b>.go`\n- see [run](https://example.com/r?a=1&b=2)\n\n```\nif a < b {}\n```\nok_ish _really_")
	for _, want := range []string{
		"<b>Done</b>",
		"• fixed <b>auth</b> in <code>a&lt;b&gt;.go</code>",
		`<a href="https://example.com/r?a=1&amp;b=2">run</a>`,
		"<pre>if a &lt; b {}</pre>",
		"ok_ish <i>really</i>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Count(got, "<pre>") != strings.Count(got, "</pre>") {
		t.Fatalf("unbalanced pre: %s", got)
	}
	if open := HTML("```\nunterminated"); !strings.HasSuffix(open, "</pre>") {
		t.Fatalf("open fence not closed: %s", open)
	}
	if StripTags("<b>a &amp; b</b>") != "a & b" {
		t.Fatal("StripTags")
	}
}

func TestSplit(t *testing.T) {
	text := strings.Repeat("word ", 300) + "\n\n" + strings.Repeat("more ", 300)
	parts := Split(text, 2000)
	if len(parts) < 2 {
		t.Fatalf("parts = %d", len(parts))
	}
	for _, p := range parts {
		if len(p) > 2000 {
			t.Fatalf("part too long: %d", len(p))
		}
	}
	if Clip("héllo wörld", 5) != "héllo…" {
		t.Fatalf("clip = %q", Clip("héllo wörld", 5))
	}
}
