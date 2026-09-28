package runner

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseStatus(t *testing.T) {
	cases := []struct {
		in                              string
		branch                          string
		ahead, behind, dirty, untracked int
	}{
		{"## main...origin/main\n", "main", 0, 0, 0, 0},
		{"## main...origin/main [ahead 2, behind 5]\n M a.go\n?? b.go\n", "main", 2, 5, 1, 1},
		{"## feat/x...origin/feat/x [behind 1]\n", "feat/x", 0, 1, 0, 0},
		{"## HEAD (no branch)\n", "HEAD", 0, 0, 0, 0},
		{"## No commits yet on main\n?? README.md\n", "main", 0, 0, 0, 1},
		{"## local-only\nA  new.go\nR  a -> b\n", "local-only", 0, 0, 2, 0},
	}
	for _, c := range cases {
		b, a, be, d, u := parseStatus(c.in)
		if b != c.branch || a != c.ahead || be != c.behind || d != c.dirty || u != c.untracked {
			t.Errorf("%q: got %s %d %d %d %d", c.in, b, a, be, d, u)
		}
	}
}

func TestAllowedRoots(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "repo")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	// A symlink inside the root that points out of it must not count as inside.
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	resolvedRoot, _ := filepath.EvalSymlinks(root)
	c := &Config{AllowedRoots: []string{resolvedRoot}}

	if _, ok := c.Allowed(inside); !ok {
		t.Error("a directory inside the root should be allowed")
	}
	if _, ok := c.Allowed(link); ok {
		t.Error("a symlink escaping the root should be refused")
	}
	if _, ok := c.Allowed(outside); ok {
		t.Error("a directory outside the root should be refused")
	}
	if _, ok := c.Allowed(resolvedRoot + "-sibling"); ok {
		t.Error("a sibling sharing the root's prefix is not inside it")
	}
	if _, ok := c.Allowed("relative/path"); ok {
		t.Error("relative paths are refused")
	}
}

func TestConfigRequiresTokenAndRoots(t *testing.T) {
	c := &Config{APIURL: "https://forge.example/", Token: "frg_x", AllowedRoots: []string{t.TempDir()}}
	if err := c.normalize(); err != nil {
		t.Fatal(err)
	}
	if c.APIURL != "https://forge.example" || c.MaxConcurrent != 2 || len(c.PermissionModes) != 2 {
		t.Errorf("defaults not applied: %+v", c)
	}
	for _, bad := range []*Config{
		{APIURL: "https://x", Token: "nope", AllowedRoots: []string{"/tmp"}},
		{APIURL: "https://x", Token: "frg_x"},
		{APIURL: "https://x", Token: "frg_x", AllowedRoots: []string{"/"}},
	} {
		t.Setenv("FORGE_RUNNER_TOKEN", "")
		if bad.normalize() == nil {
			t.Errorf("%+v should be rejected", bad)
		}
	}
}

func TestClaudeResultObservesTheStream(t *testing.T) {
	var r claudeResult
	for _, line := range []string{
		`{"type":"system","subtype":"init","session_id":"abc","model":"x"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]},"session_id":"abc"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"abc","total_cost_usd":0.5,"num_turns":3,"duration_ms":1200}`,
	} {
		r.observe([]byte(line))
	}
	if r.SessionID != "abc" || r.Result != "done" || r.IsError || r.Subtype != "" ||
		r.CostUSD == nil || *r.CostUSD != 0.5 || *r.NumTurns != 3 || *r.DurationMS != 1200 {
		t.Errorf("got %+v", r)
	}
	r.observe([]byte(`{"type":"result","subtype":"error_max_turns","is_error":true}`))
	if !r.IsError || r.Subtype != "error_max_turns" {
		t.Errorf("error result not captured: %+v", r)
	}
}

func TestShrinkKeepsEventsValidAndBounded(t *testing.T) {
	if got := string(shrink([]byte("plain text"))); got != `{"text":"plain text"}` {
		t.Errorf("non-JSON output should become a text event, got %s", got)
	}
	small := []byte(`{"type":"assistant"}`)
	if string(shrink(small)) != string(small) {
		t.Error("small events pass through untouched")
	}
	big, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{
		"content": []any{map[string]any{"type": "tool_result", "content": strings.Repeat("x", 500<<10)}}}})
	out := shrink(big)
	if !json.Valid(out) || len(out) > 20<<10 {
		t.Fatalf("shrunk event is %d bytes, valid=%v", len(out), json.Valid(out))
	}
	if !strings.Contains(string(out), "truncated by forge-agent") {
		t.Error("truncation should be visible")
	}
}

func TestGithubSlug(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:example/shop_api.git":       "example/shop_api",
		"https://github.com/example-org/shop_ui":    "example-org/shop_ui",
		"https://github.com/owner/repo.git\n":       "owner/repo",
		"git@gitlab.com:someone/thing.git":          "",
		"ssh://git@git.example.com/templates/x.git": "",
	} {
		if got := githubSlug(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestCommandsAcceptStringsAndObjects(t *testing.T) {
	c := &Config{APIURL: "https://x", Token: "frg_x", AllowedRoots: []string{t.TempDir()},
		RawCommands: map[string]json.RawMessage{
			"git-status":  json.RawMessage(`"git status -sb"`),
			"release-ios": json.RawMessage(`{"run": "./release.sh", "repos": ["shop_mobile"], "confirm": true, "description": "TestFlight"}`),
		}}
	if err := c.normalize(); err != nil {
		t.Fatal(err)
	}
	if c.Commands["git-status"].Run != "git status -sb" || len(c.Commands["git-status"].Repos) != 0 {
		t.Errorf("string form: %+v", c.Commands["git-status"])
	}
	rel := c.Commands["release-ios"]
	if rel.Run != "./release.sh" || !rel.Confirm || rel.Repos[0] != "shop_mobile" {
		t.Errorf("object form: %+v", rel)
	}
	bad := &Config{APIURL: "https://x", Token: "frg_x", AllowedRoots: []string{t.TempDir()},
		RawCommands: map[string]json.RawMessage{"x": json.RawMessage(`{"description": "no run"}`)}}
	if bad.normalize() == nil {
		t.Error("a command without run should be rejected")
	}
}

func TestPathMap(t *testing.T) {
	c := &Config{PathMap: map[string]string{"/home/me/": "/Users/me/", "/home/me/dev/shop/": "/Volumes/work/shop/"}}
	for in, want := range map[string]string{
		"/home/me/dev/game/game_app":    "/Users/me/dev/game/game_app",
		"/home/me/dev/shop/shop_mobile": "/Volumes/work/shop/shop_mobile",
		"/srv/other":                    "/srv/other",
	} {
		if got := c.MapPath(in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}

func TestInjectTheme(t *testing.T) {
	page := []byte(`<html><body><div id="vscode-workbench-web-configuration" data-settings="{&quot;remoteAuthority&quot;:&quot;forge.example.com&quot;,&quot;serverBasePath&quot;:&quot;/code&quot;,&quot;productConfiguration&quot;:{&quot;nameShort&quot;:&quot;Code &amp; co&quot;}}"></div></body></html>`)
	out, err := injectTheme(page, "dark")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"&quot;workbench.colorTheme&quot;:&quot;Dark 2026&quot;", "&quot;themeType&quot;:&quot;dark&quot;",
		"&quot;remoteAuthority&quot;:&quot;forge.example.com&quot;", "Code &amp; co"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	if _, err := injectTheme([]byte("<html>no config</html>"), "dark"); err == nil {
		t.Error("a page without the configuration element should be left alone")
	}
	r := httptest.NewRequest("GET", "/code/?folder=%2Fx&forge_theme=light", nil)
	if got := stripThemeParam(r); got != "light" || r.URL.Query().Has("forge_theme") || r.URL.Query().Get("folder") != "/x" {
		t.Errorf("strip: %q %s", got, r.URL.RawQuery)
	}
	r = httptest.NewRequest("GET", "/code/?forge_theme=dark&forge_accent=0D9488", nil)
	if got := stripThemeParam(r); got != "dark:0d9488" || r.URL.Query().Has("forge_accent") {
		t.Errorf("strip with accent: %q %s", got, r.URL.RawQuery)
	}
	out, err = injectTheme(page, "dark:0d9488")
	if err != nil || !strings.Contains(string(out), "&quot;focusBorder&quot;:&quot;#0d9488&quot;") {
		t.Errorf("accent not injected: %v %s", err, out)
	}
	r = httptest.NewRequest("GET", "/code/?forge_theme=purple", nil)
	if got := stripThemeParam(r); got != "" {
		t.Errorf("unknown theme should be ignored, got %q", got)
	}
}
