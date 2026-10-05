package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	refs, memories := splitSeen(known)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var res struct {
		Markdown string `json:"markdown"`
		Matches  []struct {
			Ref string `json:"ref"`
		} `json:"matches"`
		Memories []struct {
			ID int64 `json:"id"`
		} `json:"memories"`
	}
	body := map[string]any{"cwd": h.Cwd, "prompt": h.Prompt, "known": refs, "known_memories": memories}
	if _, err := newClient(cfg.APIURL, cfg.Token).post(ctx, "/api/runner/match", body, &res); err != nil || res.Markdown == "" {
		return
	}
	_ = json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "UserPromptSubmit", "additionalContext": res.Markdown}})

	for _, m := range res.Matches {
		known = append(known, m.Ref)
	}
	for _, m := range res.Memories {
		known = append(known, fmt.Sprintf("m%d", m.ID))
	}
	saveSeen(seenPath, known)
}

// saveSeen keeps what a session was told (task refs, and memories as "m12").
func saveSeen(path string, known []string) {
	if path == "" {
		return
	}
	if b, err := json.Marshal(known); err == nil {
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		_ = os.WriteFile(path, b, 0o600)
		pruneOld(filepath.Dir(path), 7*24*time.Hour)
	}
}

// splitSeen separates the task refs from the memory ids ("m12").
func splitSeen(known []string) ([]string, []int64) {
	refs, memories := []string{}, []int64{}
	for _, k := range known {
		var id int64
		if strings.HasPrefix(k, "m") {
			if _, err := fmt.Sscanf(k, "m%d", &id); err == nil {
				memories = append(memories, id)
				continue
			}
		}
		refs = append(refs, k)
	}
	return refs, memories
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
