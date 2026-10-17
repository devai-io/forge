package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/jev"
	"github.com/devai-io/forge/internal/store"
)

// Jev (TypeSafe's decision model, asked through the provider) where Forge
// can trade a cheap typed judgement for frontier-model tokens. Off unless
// enabled in Settings and the provider key is in the vault. Every use fails
// open: an error or an unsure answer leaves things exactly as they would be
// without Jev.

// jevOn returns the settings when Jev is enabled and has a key.
func (s *Server) jevOn(ctx context.Context) (store.JevSettings, bool) {
	set, err := s.store.JevSettings(ctx)
	if err != nil || !set.Enabled {
		return set, false
	}
	if k, err := s.providerKey(ctx); err != nil || k == "" {
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
	k, _ := s.providerKey(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"settings": set, "key_configured": k != "", "stats": s.jev.Stats()})
}

func (s *Server) updateJev(w http.ResponseWriter, r *http.Request, u *store.User) {
	set, err := s.store.JevSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in struct {
		Enabled, Routing, Context, Match *bool
		Model                            *string
	}
	if !decode(w, r, &in) {
		return
	}
	for dst, v := range map[*bool]*bool{&set.Enabled: in.Enabled, &set.Routing: in.Routing, &set.Context: in.Context,
		&set.Match: in.Match} {
		if v != nil {
			*dst = *v
		}
	}
	if in.Model != nil {
		v := strings.TrimSpace(*in.Model)
		if v == "" {
			v = store.DefaultJevModel
		}
		if !modelNameRe.MatchString(v) {
			writeErr(w, r, &store.ValidationError{Field: "model", Message: "not a model name"})
			return
		}
		set.Model = v
	}
	if err := s.store.SetJevSettings(r.Context(), set); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "jev_settings", fmt.Sprintf("enabled=%t model=%s routing=%t context=%t match=%t", set.Enabled, set.Model, set.Routing, set.Context, set.Match))
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

// jevRoute picks a model for an agent run queued without one. On Claude it
// only ever picks a cheaper model (haiku / sonnet); "heavy" or an unsure
// answer keeps the machine's default. On DeepSeek the default is already the
// cheap model, so only a confident "heavy" moves the run up to HeavyModel.
func (s *Server) jevRoute(ctx context.Context, in *store.RunInput, eng store.EngineSettings) {
	if in.Engine == "codex" || in.Kind != "agent" || in.Model != "" || in.ResumeRunID != nil || strings.TrimSpace(in.Prompt) == "" {
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
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second) // a router call, usually ~2 s
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
	if in.Engine == "deepseek" {
		model = map[string]string{"heavy": eng.HeavyModel}[a.Choice]
	}
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
	// The SessionStart hook waits for this (agents before v0.1.23 for 4 s in
	// all); answers are cached, so a miss only costs the first session.
	ctx, cancel := context.WithTimeout(ctx, 3500*time.Millisecond)
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

// runnerJevConfig is what machines are told about Jev in their Claude Code.
// Forge used to wire TypeSafe's compaction plugin in, which calls TypeSafe's
// own API with its own key; with Jev reached through OpenRouter that cannot
// work, so the answer is always "off" and machines that had it remove the
// plugin and the settings.json keys Forge added. The heartbeat carries rev.
func (s *Server) runnerJevConfig(ctx context.Context) map[string]any {
	return map[string]any{"compaction": false, "rev": "off"}
}

func (s *Server) runnerJev(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	writeJSON(w, http.StatusOK, s.runnerJevConfig(r.Context()))
}
