package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/devai-io/forge/internal/store"
)

// Interactive runs (see store/interactive.go): the browser answers prompts
// and sends follow-ups; the machine reports prompts and long-polls the run's
// inbox for what to pass on to its Claude Code session.

// ── Browser side ──────────────────────────────────────────────────────────

func (s *Server) answerPrompt(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	pid, err := strconv.ParseInt(r.PathValue("pid"), 10, 64)
	if err != nil || pid <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var in store.PromptAnswer
	if !decode(w, r, &in) {
		return
	}
	p, err := s.store.AnswerPrompt(r.Context(), id, pid, in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.inboxes.notify(id)
	writeJSON(w, http.StatusOK, map[string]any{"prompt": p})
}

func (s *Server) sendRunMessage(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.store.SendRunMessage(r.Context(), id, in.Text); err != nil {
		writeErr(w, r, err)
		return
	}
	s.inboxes.notify(id)
	s.writeRun(w, r, id)
}

func (s *Server) endRunSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.EndRunSession(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	s.inboxes.notify(id)
	s.writeRun(w, r, id)
}

func (s *Server) writeRun(w http.ResponseWriter, r *http.Request, id int64) {
	run, err := s.store.RunByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run.Public())
}

// ── Machine side ──────────────────────────────────────────────────────────

func (s *Server) runnerPrompt(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in store.PromptInput
	if !decode16(w, r, &in) {
		return
	}
	p, err := s.store.AddPrompt(r.Context(), id, rn.ID, in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prompt": p})
}

func (s *Server) runnerExpirePrompt(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID string `json:"request_id"`
	}
	if !decode16(w, r, &in) {
		return
	}
	if err := s.store.ExpirePrompt(r.Context(), id, rn.ID, in.RequestID); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// runnerInbox long-polls like runnerClaim: it answers as soon as there is
// something after `after`, when the run stops running, or after claimWait
// with nothing.
func (s *Server) runnerInbox(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		After int64 `json:"after"`
	}
	if !decode16(w, r, &in) {
		return
	}
	deadline := time.NewTimer(s.claimWait)
	defer deadline.Stop()
	wake := s.inboxes.wait(id)
	for {
		items, done, err := s.store.Inbox(r.Context(), id, rn.ID, in.After)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		if len(items) > 0 || done {
			writeJSON(w, http.StatusOK, map[string]any{"items": items, "done": done})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			writeJSON(w, http.StatusOK, map[string]any{"items": items, "done": false})
			return
		case <-wake:
		case <-time.After(5 * time.Second):
		}
	}
}

func (s *Server) runnerAwaiting(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Awaiting string `json:"awaiting"`
	}
	if !decode16(w, r, &in) {
		return
	}
	if err := s.store.SetAwaiting(r.Context(), id, rn.ID, in.Awaiting); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
