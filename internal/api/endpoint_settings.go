package api

import (
	"net/http"

	"github.com/devai-io/forge/internal/store"
)

// Endpoint retries: how many quick rechecks a failing endpoint gets before it
// is called down, and how long each waits.

func (s *Server) getEndpointSettings(w http.ResponseWriter, r *http.Request, u *store.User) {
	set, err := s.store.EndpointSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *Server) updateEndpointSettings(w http.ResponseWriter, r *http.Request, u *store.User) {
	set, err := s.store.EndpointSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in struct {
		Retries        *int  `json:"retries"`
		RecheckSeconds []int `json:"recheck_seconds"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Retries != nil {
		set.Retries = *in.Retries
	}
	if in.RecheckSeconds != nil {
		set.RecheckSeconds = in.RecheckSeconds
	}
	if err := s.store.SetEndpointSettings(r.Context(), set); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}
