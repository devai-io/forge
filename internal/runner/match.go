package runner

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// PrintMatch is the UserPromptSubmit hook: it asks Forge which open tasks
// the request typed into Claude Code is about and adds them to the
// conversation. A session is told about each task once (the refs are kept
// per session in the user cache dir). Like the context hook it never fails
// the prompt: any error prints nothing.
func PrintMatch(cfg *Config, in io.Reader, out io.Writer) {
	var h struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
		Prompt    string `json:"prompt"`
	}
	raw, _ := io.ReadAll(io.LimitReader(in, 4<<20))
	if json.Unmarshal(raw, &h) != nil || h.Prompt == "" {
		return
	}
	if h.Cwd == "" {
		h.Cwd, _ = os.Getwd()
	}
	seenPath := matchSeenPath(h.SessionID)
	var known []string
	if b, err := os.ReadFile(seenPath); err == nil {
		_ = json.Unmarshal(b, &known)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var res struct {
		Markdown string `json:"markdown"`
		Matches  []struct {
			Ref string `json:"ref"`
		} `json:"matches"`
	}
	body := map[string]any{"cwd": h.Cwd, "prompt": h.Prompt, "known": known}
	if _, err := newClient(cfg.APIURL, cfg.Token).post(ctx, "/api/runner/match", body, &res); err != nil || res.Markdown == "" {
		return
	}
	_ = json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "UserPromptSubmit", "additionalContext": res.Markdown}})

	if seenPath == "" {
		return
	}
	for _, m := range res.Matches {
		known = append(known, m.Ref)
	}
	if b, err := json.Marshal(known); err == nil {
		_ = os.MkdirAll(filepath.Dir(seenPath), 0o700)
		_ = os.WriteFile(seenPath, b, 0o600)
		pruneOld(filepath.Dir(seenPath), 7*24*time.Hour)
	}
}

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`)

func matchSeenPath(sessionID string) string {
	dir, err := os.UserCacheDir()
	if err != nil || !sessionIDRe.MatchString(sessionID) {
		return ""
	}
	return filepath.Join(dir, "forge", "prompt-matches", sessionID+".json")
}

func pruneOld(dir string, age time.Duration) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > age {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
