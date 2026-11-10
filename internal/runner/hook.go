package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// RunHook is `forge agent hook --report`, Claude Code's hook for
// SessionStart, UserPromptSubmit, Stop, Notification and SessionEnd. It tells
// Forge what a Claude Code session in tmux is doing, so that session and its
// run are one thing: a session Forge started reports its turns (with the
// transcript since the last report), one started by hand registers itself
// as a run. Sessions outside tmux, and headless runs (their process reports
// them), say nothing. Like every Forge hook it never fails or slows the
// session down: errors are dropped, the report gives up after a few seconds,
// and nothing is printed.
func RunHook(cfg *Config, in io.Reader) {
	if os.Getenv("FORGE_HEADLESS") == "1" {
		return
	}
	var h struct {
		Event            string `json:"hook_event_name"`
		SessionID        string `json:"session_id"`
		TranscriptPath   string `json:"transcript_path"`
		Cwd              string `json:"cwd"`
		Source           string `json:"source"`
		Prompt           string `json:"prompt"`
		Message          string `json:"message"`
		NotificationType string `json:"notification_type"`
		Reason           string `json:"reason"`
		LastMessage      string `json:"last_assistant_message"`
	}
	raw, _ := io.ReadAll(io.LimitReader(in, 4<<20))
	if json.Unmarshal(raw, &h) != nil || h.SessionID == "" || !resumeID.MatchString(h.SessionID) {
		return
	}
	runID, _ := strconv.ParseInt(os.Getenv("FORGE_RUN_ID"), 10, 64)
	tmuxSession := currentTmuxSession(cfg)
	if tmuxSession == "" && runID == 0 {
		return
	}
	body := map[string]any{"event": h.Event, "session_id": h.SessionID, "cwd": h.Cwd, "tmux_session": tmuxSession,
		"run_id": runID, "source": h.Source, "prompt": clip(h.Prompt, 100000), "message": clip(h.Message, 2000),
		"notification_type": h.NotificationType, "reason": h.Reason}
	off := hookOffset{session: h.SessionID}
	switch h.Event {
	case "SessionStart":
		// Only what happens from here on is this run's: a resumed session's
		// history is already in Forge (or is not this run's business).
		off.set(fileSize(h.TranscriptPath))
	case "Stop", "SessionEnd", "Notification":
		events, last, next := readTranscript(h.TranscriptPath, off.get())
		if h.LastMessage != "" { // Stop says it outright (Claude Code 2.1+)
			last = clip(h.LastMessage, 100000)
		}
		body["events"], body["last_message"] = events, last
		defer off.set(next) // after the report: a failed one is sent again with the next
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := newClient(cfg.APIURL, cfg.Token).post(ctx, "/api/runner/hook", body, nil); err != nil {
		off.keep() // not reported: send it again next time
	}
}

// currentTmuxSession is the name of the tmux session this hook's Claude Code
// runs in ("" outside tmux).
func currentTmuxSession(cfg *Config) string {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" || os.Getenv("TMUX") == "" {
		return ""
	}
	tmux := cfg.TmuxPath
	if tmux == "" {
		tmux = "tmux"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tmux, "display-message", "-p", "-t", pane, "#S").Output()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	if !tmuxName.MatchString(name) {
		return ""
	}
	return name
}

// hookOffset is how far into a session's transcript Forge has been told,
// kept per session in the user cache dir.
type hookOffset struct {
	session string
	skip    bool
}

func (o *hookOffset) path() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "forge", "hooks", o.session+".offset")
}

func (o *hookOffset) get() int64 {
	b, err := os.ReadFile(o.path())
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func (o *hookOffset) keep() { o.skip = true }

func (o *hookOffset) set(n int64) {
	p := o.path()
	if o.skip || p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte(strconv.FormatInt(n, 10)), 0o600)
}

func fileSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

const (
	hookMaxEvents = 400
	hookMaxBytes  = 1 << 20
)

// readTranscript reads a Claude Code transcript (JSON lines) from offset:
// the user and assistant entries as run events (the shape the run log reads,
// large ones shrunk), the text of the last assistant message, and the offset
// to start from next time. Only whole lines count; a line still being written
// is left for the next read.
func readTranscript(path string, offset int64) (events []event, last string, next int64) {
	events, next = []event{}, offset
	f, err := os.Open(path)
	if err != nil {
		return events, "", offset
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() < offset {
		offset, next = 0, 0 // rewritten (compacted): start over
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return events, "", offset
	}
	r := bufio.NewReaderSize(f, 1<<16)
	size := 0
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			break // EOF, or a partial last line: next time
		}
		next += int64(len(line))
		line = bytes.TrimSpace(line)
		var e struct {
			Type    string `json:"type"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil || (e.Type != "user" && e.Type != "assistant") {
			continue
		}
		if e.Type == "assistant" {
			if t := contentText(e.Message.Content); t != "" {
				last = t
			}
		}
		if len(events) < hookMaxEvents && size < hookMaxBytes {
			data := shrink(line)
			size += len(data)
			events = append(events, event{Kind: "claude", Data: data})
		}
	}
	return events, clip(last, 100000), next
}

// contentText is the text blocks of a message's content (a string or a list
// of blocks).
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimRight(cut, " \t\n") + "…"
}
