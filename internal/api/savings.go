package api

import (
	"github.com/devai-io/forge/internal/store"
	"net/http"
	"strconv"
)

func (s *Server) getSavings(w http.ResponseWriter, r *http.Request, u *store.User) {
	v, err := s.store.SavingsSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (s *Server) updateSavings(w http.ResponseWriter, r *http.Request, u *store.User) {
	v, err := s.store.SavingsSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in struct {
		Compact   *bool `json:"compact_context"`
		Summaries *bool `json:"early_summaries"`
		Focus     *bool `json:"background_focus_ai"`
		Proactive *bool `json:"proactive_ai"`
	}
	if !decode(w, r, &in) {
		return
	}
	for dst, val := range map[*bool]*bool{&v.CompactContext: in.Compact, &v.EarlySummaries: in.Summaries, &v.BackgroundFocusAI: in.Focus, &v.ProactiveAI: in.Proactive} {
		if val != nil {
			*dst = *val
		}
	}
	if err := s.store.SetSavingsSettings(r.Context(), v); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "savings_settings", "updated token savings controls")
	s.getSavings(w, r, u)
}
func (s *Server) getAIUsage(w http.ResponseWriter, r *http.Request, u *store.User) {
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 365 {
			writeErr(w, r, &store.ValidationError{Field: "days", Message: "must be between 1 and 365"})
			return
		}
		days = n
	}
	rows, err := s.store.AIUsage(r.Context(), days)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "groups": rows, "coverage": "Recorded since this upgrade. Agent snapshots are attributed to their first report date. Codex tmux hooks do not report token counts. Calls for headless agents count reports, not model requests; estimated costs are not invoices."})
}
