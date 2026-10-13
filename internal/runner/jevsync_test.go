package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	orig := `{"model":"opus","env":{"FOO":"bar"},"hooks":{"SessionStart":[]}}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setClaudeEnv(path, map[string]string{jevKeyEnv: "k", jevFunctionEnv: "1"}); err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	raw, _ := os.ReadFile(path)
	_ = json.Unmarshal(raw, &s)
	env := s["env"].(map[string]any)
	if env[jevKeyEnv] != "k" || env[jevFunctionEnv] != "1" || env[jevMarkerEnv] != "1" || env["FOO"] != "bar" || s["model"] != "opus" {
		t.Fatalf("on: %s", raw)
	}
	if err := setClaudeEnv(path, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	s = nil
	_ = json.Unmarshal(raw, &s)
	env = s["env"].(map[string]any)
	if len(env) != 1 || env["FOO"] != "bar" || s["hooks"] == nil {
		t.Fatalf("off must restore exactly what was there: %s", raw)
	}
	// Off on a file Forge never touched changes nothing.
	other := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(other, []byte(`{"env":{"TYPESAFE_API_KEY":"theirs"}}`), 0o600)
	_ = setClaudeEnv(other, nil)
	if raw, _ := os.ReadFile(other); !strings.Contains(string(raw), "theirs") {
		t.Fatalf("removed a key Forge did not set: %s", raw)
	}
}
