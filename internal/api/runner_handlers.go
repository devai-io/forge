package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/devai-io/forge/internal/store"
)

// The runner protocol. A runner is a machine of the user's (the desktop, a
// server) that polls out to Forge: it never has to accept a connection, so it
// works from behind NAT and needs no port opened anywhere.

func (s *Server) runnerHeartbeat(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	var in store.HeartbeatInput
	if !decode(w, r, &in) {
		return
	}
	cancel, err := s.store.Heartbeat(r.Context(), rn.ID, in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	repos, err := s.store.RunnerRepos(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	// Only the master scans (and reads CI): see SaveRepoScans. With no master
	// elected, everyone does, as before.
	scan := rn.Role == "master"
	if !scan {
		if _, err := s.store.MasterRunner(r.Context()); errors.Is(err, store.ErrNotFound) {
			scan = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runner_id": rn.ID, "role": rn.Role, "scan": scan,
		"cancel": cancel, "repos": repos, "jev_rev": s.runnerJevConfig(r.Context())["rev"]})
}

// runnerClaim long-polls for up to claimWait (25 s). It wakes early when a run is queued
// for this runner (notifier) and re-checks every 5 s regardless, in case the
// run was queued by another API instance or the wake-up was missed.
func (s *Server) runnerClaim(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	deadline := time.NewTimer(s.claimWait)
	defer deadline.Stop()
	wake := s.wakeups.wait(rn.ID)
	for {
		run, err := s.store.ClaimRun(r.Context(), rn.ID)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"run": run, "engine": s.runnerEngine(r.Context(), run)})
			return
		}
		if !errors.Is(err, store.ErrNotFound) {
			writeErr(w, r, err)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-wake:
		case <-time.After(5 * time.Second):
		}
	}
}

func (s *Server) runnerEvents(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Events []store.EventInput `json:"events"`
	}
	if !decode16(w, r, &in) {
		return
	}
	if err := s.store.AppendEvents(r.Context(), id, rn.ID, in.Events); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runnerFinish(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in store.FinishInput
	if !decode16(w, r, &in) {
		return
	}
	if err := s.store.FinishRun(r.Context(), id, rn.ID, in); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runnerRepos(w http.ResponseWriter, r *http.Request, rn *store.Runner) {
	var in struct {
		Repos []store.RepoScan `json:"repos"`
	}
	if !decode16(w, r, &in) {
		return
	}
	if err := s.store.SaveRepoScans(r.Context(), rn.Name, in.Repos); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decode16 decodes without re-capping the body: runnerAuth already allows
// runner bodies up to 16 MB, where browser bodies stop at 2 MB.
func decode16(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
