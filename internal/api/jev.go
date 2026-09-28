package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/jev"
	"github.com/devai-io/forge/internal/store"
)

// Jev (TypeSafe's decision model) where Forge can trade a cheap typed
// judgement for frontier-model tokens. Off unless enabled in Settings and a
// key is in the vault (tag integration:jev). Every use fails open: an error
// or an unsure answer leaves things exactly as they would be without Jev.

const jevTag = "integration:jev"

// The compaction plugin the machines install, pinned to a reviewed commit
// (it sees whole Claude Code transcripts, so it is not tracked blindly).
const (
	jevPluginRepo   = "https://github.com/tamaratran/fast-jev-compaction.git"
	jevPluginCommit = "e3f262a7f4d42bd8dd32ced30d26176f7cb545b0"
)

func (s *Server) jevKey(ctx context.Context) (string, error) {
	if !s.box.Available() {
		return "", jev.ErrNoKey
	}
	k, err := s.store.IntegrationSecret(ctx, s.box, jevTag)
	if errors.Is(err, store.ErrNotFound) {
		return "", jev.ErrNoKey
	}
	return k, err
}

// jevOn returns the settings when Jev is enabled and has a key.
func (s *Server) jevOn(ctx context.Context) (store.JevSettings, bool) {
	set, err := s.store.JevSettings(ctx)
	if err != nil || !set.Enabled {
		return set, false
	}
	if k, err := s.jevKey(ctx); err != nil || k == "" {
		return set, false
	}
	return set, true
}

func (s *Server) getJev(w http.ResponseWriter, r *http.Request, u *store.User) {
	set, err := s.store.JevSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	k, _ := s.jevKey(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"settings": set, "key_configured": k != "", "stats": s.jev.Stats()})
}

func (s *Server) updateJev(w http.ResponseWriter, r *http.Request, u *store.User) {
	set, err := s.store.JevSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in struct {
		Enabled, Routing, Context, Compaction *bool
	}
	if !decode(w, r, &in) {
		return
	}
	for dst, v := range map[*bool]*bool{&set.Enabled: in.Enabled, &set.Routing: in.Routing, &set.Context: in.Context,
		&set.Compaction: in.Compaction} {
		if v != nil {
			*dst = *v
		}
	}
	if err := s.store.SetJevSettings(r.Context(), set); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "jev_settings", fmt.Sprintf("enabled=%t routing=%t context=%t compaction=%t", set.Enabled, set.Routing, set.Context, set.Compaction))
	s.getJev(w, r, u)
}

// setJevKey stores the API key as the vault item Forge reads it from.
func (s *Server) setJevKey(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		APIKey string `json:"api_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.APIKey = strings.TrimSpace(in.APIKey)
	if len(in.APIKey) < 16 || strings.ContainsAny(in.APIKey, " \t\n") {
		writeErr(w, r, &store.ValidationError{Field: "api_key", Message: "that does not look like an API key"})
		return
	}
	if err := s.store.SaveIntegrationSecret(r.Context(), s.box, "TypeSafe Jev API key", jevTag, in.APIKey, s.clientIP(r)); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "jev_key", "Jev API key replaced")
	s.getJev(w, r, u)
}

// testJev makes one tiny call so the Settings page can say the key works.
func (s *Server) testJev(w http.ResponseWriter, r *http.Request, u *store.User) {
	start := time.Now()
	ans, err := s.jev.Ask(r.Context(), map[string]string{"text": "Forge settings check"},
		map[string]jev.Question{"ok": jev.Noul("Is this a short test message?")})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ms": time.Since(start).Milliseconds(), "answer": ans["ok"].Noul})
}

// ── Model routing ─────────────────────────────────────────────────────────

// jevRoute picks a model for an agent run queued without one. It only ever
// picks a cheaper model; "hard" or an unsure answer keeps the machine's
// default.
func (s *Server) jevRoute(ctx context.Context, in *store.RunInput) {
	if in.Kind != "agent" || in.Model != "" || in.ResumeRunID != nil || strings.TrimSpace(in.Prompt) == "" {
		return
	}
	set, on := s.jevOn(ctx)
	if !on || !set.Routing {
		return
	}
	prompt := in.Prompt
	if len(prompt) > 4000 {
		prompt = prompt[:4000] + " …"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ans, err := s.jev.Ask(ctx, map[string]string{"coding_agent_task": prompt},
		map[string]jev.Question{"effort": jev.Choice(
			"How much reasoning does a coding agent need to do this task well?",
			map[string]string{
				"light":  "a lookup, a question about the code, a one-line or mechanical edit, running a command and reporting",
				"medium": "an ordinary change across a few files, a routine bug fix, writing tests for existing code",
				"heavy":  "design work, a subtle bug, a large refactor, security-sensitive or ambiguous work",
			})})
	if err != nil {
		slog.Warn("jev routing skipped", "err", err)
		return
	}
	a := ans["effort"]
	model := map[string]string{"light": "haiku", "medium": "sonnet"}[a.Choice]
	if model == "" || a.Confidence < 0.7 {
		return
	}
	in.Model = model
	in.ModelNote = fmt.Sprintf("Jev: %s task (%.0f%% sure) → %s", a.Choice, a.Confidence*100, model)
}

// ── Context trimming ──────────────────────────────────────────────────────

// jevTaskTrimmer, when on, keeps a session's task list to what matters for
// the repo it was opened in. Always kept: focus, in-progress, urgent and
// blocked tasks, and tasks attached to this repo. Answers are cached for a
// while — sessions start often, open tasks change rarely.
func (s *Server) jevTaskTrimmer(ctx context.Context) store.TaskTrimmer {
	set, on := s.jevOn(ctx)
	if !on || !set.Context {
		return nil
	}
	return func(ctx context.Context, repo *store.Repo, tasks []store.Task) ([]store.Task, string) {
		const keepAtMost = 12
		if len(tasks) <= 8 {
			return tasks, ""
		}
		var must, rest []store.Task
		for _, t := range tasks {
			if t.Focus || t.Status == "in_progress" || t.Status == "blocked" || t.Priority == "urgent" ||
				(t.RepoID != nil && *t.RepoID == repo.ID) {
				must = append(must, t)
			} else {
				rest = append(rest, t)
			}
		}
		if len(rest) > 80 {
			rest = rest[:80]
		}
		scores, err := s.jevRelevance(ctx, repo, rest)
		if err != nil {
			slog.Warn("jev context trimming skipped", "err", err)
			return tasks, ""
		}
		sort.SliceStable(rest, func(i, j int) bool { return scores[rest[i].ID] > scores[rest[j].ID] })
		out := must
		for _, t := range rest {
			if len(out) >= keepAtMost || scores[t.ID] < 0.4 {
				break
			}
			out = append(out, t)
		}
		return out, "picked by Jev as relevant to " + repo.Name
	}
}

type relevanceEntry struct {
	at     time.Time
	scores map[int64]float64
}

func (s *Server) jevRelevance(ctx context.Context, repo *store.Repo, tasks []store.Task) (map[int64]float64, error) {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%s|%s", repo.ID, repo.Name, repo.Notes)
	for _, t := range tasks {
		fmt.Fprintf(h, "|%d:%s:%s", t.ID, t.Title, t.UpdatedAt.Format(time.RFC3339))
	}
	key := hex.EncodeToString(h.Sum(nil))
	s.jevCacheMu.Lock()
	if e, ok := s.jevCache[key]; ok && time.Since(e.at) < 30*time.Minute {
		s.jevCacheMu.Unlock()
		return e.scores, nil
	}
	s.jevCacheMu.Unlock()

	list := make([]string, len(tasks))
	questions := map[string]jev.Question{}
	for i, t := range tasks {
		list[i] = fmt.Sprintf("T%d: %s [%s, %s]", i, t.Title, t.Type, t.Priority)
		questions[fmt.Sprintf("t%d", i)] = jev.Noul(fmt.Sprintf(
			"Is task T%d likely to involve work in the repo %q, or be useful to know while working in it?", i, repo.Name))
	}
	state := map[string]any{"repo": fmt.Sprintf("%s (%s) %s", repo.Name, repo.Kind, repo.Notes), "tasks": list}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second) // the SessionStart hook waits for this
	defer cancel()
	ans, err := s.jev.Ask(ctx, state, questions)
	if err != nil {
		return nil, err
	}
	scores := map[int64]float64{}
	for i, t := range tasks {
		scores[t.ID] = ans[fmt.Sprintf("t%d", i)].Noul
	}
	s.jevCacheMu.Lock()
	if len(s.jevCache) > 200 {
		s.jevCache = map[string]relevanceEntry{}
	}
	s.jevCache[key] = relevanceEntry{at: time.Now(), scores: scores}
	s.jevCacheMu.Unlock()
	return scores, nil
}

// ── Machines ──────────────────────────────────────────────────────────────

// runnerJev tells a machine whether to wire Jev compaction into its Claude
// Code, with the key and the pinned plugin. rev changes whenever any of it
// does; the heartbeat carries it so machines re-apply only on change.
func (s *Server) runnerJevConfig(ctx context.Context) map[string]any {
	set, on := s.jevOn(ctx)
	out := map[string]any{"compaction": false, "plugin_repo": jevPluginRepo, "plugin_commit": jevPluginCommit}
	if on && set.Compaction {
		k, _ := s.jevKey(ctx)
		out["compaction"], out["api_key"] = true, k
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%v|%v|%s", out["compaction"], out["api_key"], jevPluginCommit)))
	out["rev"] = hex.EncodeToString(sum[:8])
	return out
}

func (s *Server) runnerJev(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	writeJSON(w, http.StatusOK, s.runnerJevConfig(r.Context()))
}
