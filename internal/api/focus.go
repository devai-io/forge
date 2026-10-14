package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/llm"
	"github.com/devai-io/forge/internal/store"
)

// Focus: per project, what to do now, what comes next, the roadmap and what
// blocks it. The store marks a project dirty when a task, a run or the
// check-up changes it; RunFocus recomputes it once the changes have settled
// for focusSettle — with the provider's model when there is a key, by rules
// otherwise (or when the model fails).

const (
	focusSettle  = 2 * time.Minute
	focusTick    = 30 * time.Second
	focusPerTick = 3
)

// focusLocks serialises the computation per project (the loop and a
// Recalculate click must not race).
type focusLocks struct{ m sync.Map }

func (l *focusLocks) lock(projectID int64) func() {
	v, _ := l.m.LoadOrStore(projectID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Server) getFocus(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, err := s.store.ProjectIDByKey(r.Context(), strings.ToUpper(r.PathValue("key")))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	f, err := s.store.FocusByProject(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		// The first look: rules now (instant), the model's version follows
		// from the loop.
		f, err = s.computeFocus(r.Context(), id, store.TodayFor(u.Timezone), "first look", false)
		if err == nil {
			s.store.MarkFocusDirty(r.Context(), id, "first look")
			f.Dirty, f.DirtyReason = true, "first look"
		}
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) refreshFocus(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, err := s.store.ProjectIDByKey(r.Context(), strings.ToUpper(r.PathValue("key")))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	f, err := s.computeFocus(ctx, id, store.TodayFor(u.Timezone), "recalculated by hand", true)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// computeFocus works out and stores a project's focus. useAI asks the model
// when there is a key; any failure there falls back to rules, noted in
// Error.
func (s *Server) computeFocus(ctx context.Context, projectID int64, today, trigger string, useAI bool) (*store.ProjectFocus, error) {
	unlock := s.focusLocks.lock(projectID)
	defer unlock()
	started := time.Now()
	in, err := s.store.FocusInputs(ctx, projectID, today)
	if err != nil {
		return nil, err
	}
	focus, source, model, errText := store.FocusRules(in), "rules", "", ""
	if useAI {
		if k, _ := s.providerKey(ctx); k != "" {
			set, _ := s.store.EngineSettings(ctx)
			model = set.Model
			if model == "" {
				model = llm.DefaultModel
			}
			f, err := s.focusFromModel(ctx, projectID, model, in)
			if err != nil {
				slog.Warn("focus: model failed, using rules", "project", in.Key, "err", err)
				errText, model = "The model did not answer usably ("+err.Error()+"); showing the rules' version.", ""
			} else {
				focus, source = f, "ai"
			}
		}
	}
	if err := s.store.SaveFocus(ctx, projectID, focus, source, model, trigger, errText, started); err != nil {
		return nil, err
	}
	return s.store.FocusByProject(ctx, projectID)
}

const focusPrompt = `You keep a software project's focus for its single owner: what to do right now, what comes next, the roadmap and what blocks it. You read the project's state as JSON (open tasks, tasks done in the last two weeks, recent agent runs, the daily check-up's open actions, repo status, endpoints down, key memories) and answer with ONE JSON object and nothing else:

{"headline": "one sentence: where the project stands and the single most important next move",
 "now": [{"title": "imperative action", "why": "short reason grounded in the data", "ref": "KEY-12 or null", "kind": "task|checkup|repo|endpoint|run|other"}],
 "next": [same shape],
 "roadmap": [{"title": "milestone or phase", "status": "done|active|planned", "refs": ["KEY-3"]}],
 "blockers": [{"title": "...", "why": "...", "ref": "KEY-7 or null", "kind": "..."}]}

Rules: "now" has at most 5 concrete, immediately actionable steps, most important first (in-progress work, failing CI or check-up actions, urgent or overdue tasks, follow-ups of failed or finished agent runs). "next" has at most 7. "roadmap" has 2 to 8 steps in order, derived from the tasks (labels, themes, what was shipped), mixing done, active and planned. "blockers" only when something really blocks progress (may be empty). Only use task refs that appear in the data. Keep titles under 100 characters and reasons under 200. Be specific; no filler.`

func (s *Server) focusFromModel(ctx context.Context, projectID int64, model string, in *store.FocusInputs) (store.Focus, error) {
	state, _ := json.Marshal(in)
	msg, _, err := s.llmClient(ctx).Chat(ctx, model, "off", []llm.Message{
		{Role: "system", Content: focusPrompt},
		{Role: "user", Content: string(state)},
	}, nil)
	if err != nil {
		return store.Focus{}, err
	}
	f, err := parseFocus(msg.Content)
	if err != nil {
		return store.Focus{}, err
	}
	refs, err := s.store.TaskRefs(ctx, projectID)
	if err != nil {
		return store.Focus{}, err
	}
	f = store.CleanFocus(f, refs)
	if f.Headline == "" && len(f.Now) == 0 {
		return store.Focus{}, errors.New("empty focus")
	}
	return f, nil
}

// parseFocus reads the model's answer: JSON, possibly fenced or with prose
// around it.
func parseFocus(text string) (store.Focus, error) {
	var f store.Focus
	text = strings.TrimSpace(text)
	i, j := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if i < 0 || j <= i {
		return f, errors.New("no JSON object in the answer")
	}
	if err := json.Unmarshal([]byte(text[i:j+1]), &f); err != nil {
		return f, fmt.Errorf("unreadable JSON: %w", err)
	}
	return f, nil
}

// RunFocus recomputes the focus of projects that changed, once the changes
// have settled. Runs until ctx ends.
func (s *Server) RunFocus(ctx context.Context) {
	t := time.NewTicker(s.focusTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.focusPass(ctx)
	}
}

func (s *Server) focusPass(ctx context.Context) {
	due, err := s.store.DirtyFocuses(ctx, s.focusSettle, focusPerTick)
	if err != nil || len(due) == 0 {
		return
	}
	tz := "UTC"
	if u, err := s.store.FirstUser(ctx); err == nil && u.Timezone != "" {
		tz = u.Timezone
	}
	for _, d := range due {
		if ctx.Err() != nil {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		trigger := "after " + d.Reason
		if d.Reason == "" {
			trigger = "after changes"
		}
		if _, err := s.computeFocus(cctx, d.ProjectID, store.TodayFor(tz), trigger, true); err != nil {
			slog.Warn("focus: recompute failed", "project_id", d.ProjectID, "err", err)
		}
		cancel()
	}
}
