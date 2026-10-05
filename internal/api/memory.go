package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/devai-io/forge/internal/store"
)

// Memory (store/memory.go): the web app manages it; Claude sessions recall
// and save through the runner routes (`forge agent mcp`); the prompt hook
// recalls what a request seems to need.

func (s *Server) listMemories(w http.ResponseWriter, r *http.Request, u *store.User) {
	q := r.URL.Query()
	f := store.MemoryFilter{Query: q.Get("q"), Limit: queryInt(r, "limit", 200)}
	switch p := q.Get("project"); p {
	case "":
	case "global":
		f.Global = true
	default:
		f.ProjectKey = p
	}
	ms, err := s.store.ListMemories(r.Context(), f)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	counts, err := s.store.CountMemories(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": ms, "counts": counts})
}

func (s *Server) createMemory(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in store.MemoryInput
	if !decode(w, r, &in) {
		return
	}
	in.Source, in.Origin = "user", "web"
	saved, err := s.store.CreateMemory(r.Context(), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) updateMemory(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var p store.MemoryPatch
	if !decode(w, r, &p) {
		return
	}
	m, err := s.store.UpdateMemory(r.Context(), id, p)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteMemory(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Runner side ───────────────────────────────────────────────────────────

// memoryProject is the project a runner request is about: the key it names,
// else the project of the directory it was made from.
func (s *Server) memoryProject(ctx context.Context, key, cwd string) string {
	if key = strings.ToUpper(strings.TrimSpace(key)); key != "" {
		return key
	}
	if cwd != "" {
		k, _, _ := s.store.ProjectForPath(ctx, cwd)
		return k
	}
	return ""
}

func (s *Server) runnerMemories(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	q := r.URL.Query()
	key := s.memoryProject(r.Context(), q.Get("project"), q.Get("cwd"))
	f := store.MemoryFilter{ProjectKey: key, WithGlobal: key != "", Query: q.Get("q"), Limit: queryInt(r, "limit", 15)}
	if q.Get("project") == "global" {
		f = store.MemoryFilter{Global: true, Query: q.Get("q"), Limit: f.Limit}
	}
	ms, err := s.store.ListMemories(r.Context(), f)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if f.Query != "" {
		ids := make([]int64, len(ms))
		for i, m := range ms {
			ids[i] = m.ID
		}
		s.store.MarkRecalled(r.Context(), ids)
	}
	writeJSON(w, http.StatusOK, map[string]any{"project_key": key, "memories": ms})
}

func (s *Server) runnerRemember(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	var in struct {
		store.MemoryInput
		Cwd    string `json:"cwd"`
		Global bool   `json:"global"` // about the user, not the project
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Global {
		in.ProjectKey, in.Repo = "", ""
	} else if in.ProjectKey = s.memoryProject(r.Context(), in.ProjectKey, in.Cwd); in.ProjectKey == "" {
		writeErr(w, r, &store.ValidationError{Field: "project_key",
			Message: "this directory belongs to no Forge project: name the project, or set global for something about the user"})
		return
	}
	in.Source, in.Origin = "claude", "Claude on "+rn.Name
	saved, err := s.store.CreateMemory(r.Context(), in.MemoryInput)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) runnerUpdateMemory(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	s.updateMemory(w, r, nil)
}

func (s *Server) runnerForget(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	s.deleteMemory(w, r, nil)
}

// ── Recall on a request ───────────────────────────────────────────────────

const (
	recallMax      = 3
	recallMinWords = 2 // significant words a memory must share with the request
)

// recallMemories finds memories a request typed into Claude Code seems to
// need: full-text candidates from the project (and about the user), kept
// only when they share at least two significant words with the request.
func (s *Server) recallMemories(ctx context.Context, key, prompt string, known []int64) []store.Memory {
	out := []store.Memory{}
	prompt = strings.TrimSpace(prompt)
	if key == "" || len([]rune(prompt)) < matchMinPrompt || strings.HasPrefix(prompt, "/") {
		return out
	}
	if len(prompt) > 2000 {
		prompt = prompt[:2000]
	}
	skip := map[int64]bool{}
	for _, id := range known {
		skip[id] = true
	}
	cands, err := s.store.ListMemories(ctx, store.MemoryFilter{ProjectKey: key, WithGlobal: true, Query: prompt, Limit: 12})
	if err != nil {
		return out
	}
	words := keywords(prompt)
	for _, m := range cands {
		if skip[m.ID] || len(out) >= recallMax {
			continue
		}
		shared := 0
		for w := range keywords(m.Content) {
			if words[w] {
				shared++
			}
		}
		if shared >= recallMinWords {
			out = append(out, m)
		}
	}
	ids := make([]int64, len(out))
	for i, m := range out {
		ids[i] = m.ID
	}
	s.store.MarkRecalled(ctx, ids)
	return out
}

func recallMarkdown(ms []store.Memory) string {
	if len(ms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Forge memory that may matter for this request\n")
	for _, m := range ms {
		fmt.Fprintf(&b, "- %s\n", m.Line(500))
	}
	b.WriteString("Earlier sessions saved these. If one is wrong or out of date, fix it (forge_update_memory) or drop it (forge_forget).\n")
	return b.String()
}
