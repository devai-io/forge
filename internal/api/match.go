package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/devai-io/forge/internal/jev"
	"github.com/devai-io/forge/internal/store"
)

// Matching a request typed into Claude Code against the open tasks, so the
// session knows which task it is working on and keeps it current. Refs named
// in the request always count; otherwise Jev scores the candidates (one
// call), or, with Jev off or failing, shared keywords do. Nothing is
// returned for an unrelated request.

type taskMatch struct {
	Ref      string  `json:"ref"`
	Title    string  `json:"title"`
	Status   string  `json:"status"`
	Priority string  `json:"priority"`
	Score    float64 `json:"score"`
	Why      string  `json:"why"` // "named" | "jev" | "keywords"
}

var taskRefRe = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9]{1,9})-(\d{1,6})\b`)

const (
	matchMinPrompt = 12  // shorter ("yes", "go on") matches nothing but refs
	matchMaxTasks  = 60  // candidates per request
	matchJevMin    = 0.6 // Jev probability to report a task
	matchMax       = 3
)

func (s *Server) runnerMatch(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	var in struct {
		Cwd    string   `json:"cwd"`
		Prompt string   `json:"prompt"`
		Known  []string `json:"known"` // refs this session was already told about
	}
	if !decode(w, r, &in) {
		return
	}
	all, key := s.matchTasks(r.Context(), in.Cwd, in.Prompt)
	known := map[string]bool{}
	for _, ref := range in.Known {
		known[strings.ToUpper(ref)] = true
	}
	matches := []taskMatch{}
	for _, m := range all {
		if !known[m.Ref] {
			matches = append(matches, m)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"project_key": key, "matches": matches, "markdown": matchMarkdown(matches)})
}

func (s *Server) matchTasks(ctx context.Context, cwd, prompt string) ([]taskMatch, string) {
	prompt = strings.TrimSpace(prompt)
	out := []taskMatch{}
	seen := map[string]bool{}
	for _, m := range taskRefRe.FindAllStringSubmatch(prompt, 5) {
		ref := strings.ToUpper(m[0])
		if seen[ref] {
			continue
		}
		if t, err := s.store.TaskByRef(ctx, ref); err == nil {
			seen[ref] = true
			out = append(out, taskMatch{Ref: t.Ref, Title: t.Title, Status: t.Status, Priority: t.Priority, Score: 1, Why: "named"})
		}
	}
	key, _, _ := s.store.ProjectForPath(ctx, cwd)
	if len([]rune(prompt)) < matchMinPrompt || strings.HasPrefix(prompt, "/") {
		return out, key
	}

	tasks, err := s.store.ListTasks(ctx, store.TaskFilter{ProjectKey: key, Open: true, Limit: matchMaxTasks, Today: store.TodayFor("UTC")})
	if err != nil {
		return out, key
	}
	cands := tasks[:0:0]
	for _, t := range tasks {
		if !seen[t.Ref] {
			cands = append(cands, t)
		}
	}
	if len(cands) == 0 {
		return out, key
	}

	var found []taskMatch
	if set, on := s.jevOn(ctx); on && set.Match {
		found, err = s.jevMatch(ctx, prompt, cands)
		if err != nil {
			slog.Warn("jev task matching fell back to keywords", "err", err)
			found = keywordMatch(prompt, cands)
		}
	} else {
		found = keywordMatch(prompt, cands)
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].Score > found[j].Score })
	for _, m := range found {
		if len(out) >= matchMax {
			break
		}
		out = append(out, m)
	}
	return out, key
}

func (s *Server) jevMatch(ctx context.Context, prompt string, tasks []store.Task) ([]taskMatch, error) {
	if len(prompt) > 2000 {
		prompt = prompt[:2000] + " …"
	}
	list := make([]string, len(tasks))
	questions := map[string]jev.Question{}
	for i, t := range tasks {
		desc := t.Description
		if len(desc) > 160 {
			desc = desc[:160] + "…"
		}
		list[i] = fmt.Sprintf("T%d: %s [%s, %s] %s", i, t.Title, t.Status, t.Priority, desc)
		questions[fmt.Sprintf("t%d", i)] = jev.Noul(fmt.Sprintf(
			"Is the developer's request work on task T%d — doing it, continuing it, or directly part of it?", i))
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second) // the prompt waits for this
	defer cancel()
	ans, err := s.jev.Ask(ctx, map[string]any{"developer_request": prompt, "open_tasks": list}, questions)
	if err != nil {
		return nil, err
	}
	var out []taskMatch
	for i, t := range tasks {
		if p := ans[fmt.Sprintf("t%d", i)].Noul; p >= matchJevMin {
			out = append(out, taskMatch{Ref: t.Ref, Title: t.Title, Status: t.Status, Priority: t.Priority, Score: p, Why: "jev"})
		}
	}
	return out, nil
}

// keywordMatch reports tasks whose title shares at least two significant
// words with the request, covering at least a third of the title.
func keywordMatch(prompt string, tasks []store.Task) []taskMatch {
	words := keywords(prompt)
	var out []taskMatch
	for _, t := range tasks {
		title := keywords(t.Title)
		if len(title) == 0 {
			continue
		}
		shared := 0
		for w := range title {
			if words[w] {
				shared++
			}
		}
		if score := float64(shared) / float64(len(title)); shared >= 2 && score >= 0.34 {
			out = append(out, taskMatch{Ref: t.Ref, Title: t.Title, Status: t.Status, Priority: t.Priority, Score: score, Why: "keywords"})
		}
	}
	return out
}

var stopwords = map[string]bool{
	"this": true, "that": true, "with": true, "from": true, "into": true, "have": true, "make": true, "sure": true,
	"please": true, "should": true, "would": true, "could": true, "about": true, "there": true, "their": true,
	"what": true, "when": true, "where": true, "which": true, "will": true, "also": true, "them": true, "then": true,
	"than": true, "some": true, "more": true, "only": true, "just": true, "like": true, "want": true, "need": true,
	"help": true, "task": true, "tasks": true, "work": true, "does": true, "done": true, "each": true, "they": true,
}

func keywords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 4 && !stopwords[w] {
			out[strings.TrimSuffix(w, "s")] = true
		}
	}
	return out
}

func matchMarkdown(ms []taskMatch) string {
	if len(ms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Forge: tasks this request may be about\n")
	for _, m := range ms {
		how := map[string]string{"named": "named in the request", "jev": fmt.Sprintf("Jev %.0f%%", m.Score*100), "keywords": "shared keywords"}[m.Why]
		fmt.Fprintf(&b, "- %s (%s/%s): %s — %s\n", m.Ref, m.Status, m.Priority, m.Title, how)
	}
	b.WriteString("If the work is one of these: read it with forge_task, move it to in_progress (forge_update_task) when you start, " +
		"forge_comment what you did (commits, decisions) and set it done when finished. Mention the ref in your reply. " +
		"If none fits, ignore this note.\n")
	return b.String()
}
