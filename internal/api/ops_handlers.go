package api

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/monitoring"
	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/vault"
)

// ── Vault ─────────────────────────────────────────────────────────────────

func (s *Server) listVault(w http.ResponseWriter, r *http.Request, u *store.User) {
	q := r.URL.Query()
	items, err := s.store.ListVault(r.Context(), store.VaultFilter{ProjectKey: q.Get("project"), Query: q.Get("q"), Kind: q.Get("kind")})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "available": s.box.Available()})
}

func (s *Server) createVault(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in store.VaultInput
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateVault(r.Context(), s.box, in, s.clientIP(r))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getVault(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	item, err := s.store.VaultByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateVault(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, ok := decodePatch(w, r)
	if !ok {
		return
	}
	item, err := s.store.UpdateVault(r.Context(), s.box, id, p, s.clientIP(r))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteVault(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteVault(r.Context(), id, s.clientIP(r)); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revealVault(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	secret, err := s.store.RevealVault(r.Context(), s.box, id, s.clientIP(r))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("vault reveal", "item", id, "ip", s.clientIP(r))
	s.sec(r, "vault_reveal", fmt.Sprintf("item %d", id))
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret})
}

func (s *Server) vaultFile(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	name, data, err := s.store.VaultFileContent(r.Context(), s.box, id, s.clientIP(r))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("vault download", "item", id, "ip", s.clientIP(r))
	s.sec(r, "vault_download", fmt.Sprintf("item %d (%s)", id, name))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

func (s *Server) vaultAudit(w http.ResponseWriter, r *http.Request, u *store.User) {
	events, err := s.store.ListVaultAudit(r.Context(), queryInt(r, "limit", 50))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// ── Monitoring ────────────────────────────────────────────────────────────

func (s *Server) monitoring(w http.ResponseWriter, r *http.Request, u *store.User) {
	if s.mon == nil {
		writeJSON(w, http.StatusOK, &monitoring.Snapshot{FetchedAt: time.Now().UTC(), Hosts: []monitoring.HostMetrics{},
			Probes: []monitoring.Probe{}, Nomad: []monitoring.NomadHost{},
			Grafana: monitoring.Grafana{Alerts: []monitoring.Alert{}, Dashboards: []monitoring.Dashboard{}}})
		return
	}
	writeJSON(w, http.StatusOK, s.mon.Snapshot(r.Context(), queryBool(r, "fresh")))
}

// MonitoringSources adapts the store and vault to what the monitoring
// client reads from the database.
type MonitoringSources struct {
	Store *store.Store
	Box   *vault.Box
}

func (m MonitoringSources) MonitoredServers(ctx context.Context) ([]monitoring.Server, error) {
	servers, err := m.Store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]monitoring.Server, 0, len(servers))
	for _, sv := range servers {
		out = append(out, monitoring.Server{ID: sv.ID, Name: sv.Name, TailscaleIP: sv.TailscaleIP, Tags: sv.Tags})
	}
	return out, nil
}

func (m MonitoringSources) GrafanaToken(ctx context.Context) (string, error) {
	if !m.Box.Available() {
		return "", vault.ErrUnavailable
	}
	return m.Store.IntegrationSecret(ctx, m.Box, "integration:grafana")
}

// ── Check-ups ─────────────────────────────────────────────────────────────

func (s *Server) listCheckups(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, err := s.store.ListCheckups(r.Context(), queryInt(r, "limit", 30))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checkups": list})
}

func (s *Server) latestCheckup(w http.ResponseWriter, r *http.Request, u *store.User) {
	c, err := s.store.LatestCheckup(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) getCheckup(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := s.store.CheckupByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) runCheckup(w http.ResponseWriter, r *http.Request, u *store.User) {
	if s.checkups == nil {
		writeError(w, http.StatusServiceUnavailable, "internal", "check-ups are not configured")
		return
	}
	// Detached from the request: a phone that locks mid-run should still
	// leave a finished check-up behind.
	c, err := s.checkups.Run(context.WithoutCancel(r.Context()), "manual", store.TodayFor(u.Timezone))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) updateCheckupItem(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Done bool `json:"done"`
	}
	if !decode(w, r, &in) {
		return
	}
	c, err := s.store.UpdateCheckupItem(r.Context(), id, r.PathValue("key"), func(it *store.CheckItem) error {
		it.Done = in.Done
		return nil
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// checkupItemTask turns an action into a task (label "checkup", due today),
// in the item's project unless another is named — or Servers, where fleet
// chores live.
func (s *Server) checkupItemTask(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		ProjectKey string `json:"project_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	c, err := s.store.CheckupByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	key := r.PathValue("key")
	var it *store.CheckItem
	for i := range c.Items {
		if c.Items[i].Key == key {
			it = &c.Items[i]
		}
	}
	if it == nil {
		writeErr(w, r, store.ErrNotFound)
		return
	}
	if it.TaskID != nil {
		writeError(w, http.StatusConflict, "conflict", "already a task: "+deref(it.TaskRef))
		return
	}
	project := strings.TrimSpace(in.ProjectKey)
	if project == "" && it.ProjectKey != nil {
		project = *it.ProjectKey
	}
	if project == "" {
		project = "SRV"
	}
	desc := it.Detail
	if it.Action != "" {
		desc += "\n\n**Action:** " + it.Action
	}
	if it.Link != nil {
		desc += "\n\n" + *it.Link
	}
	desc += fmt.Sprintf("\n\n_From the %s check-up (%s)._", c.Date, it.Category)
	priority := "medium"
	if it.Severity == "fail" {
		priority = "high"
	}
	today := store.TodayFor(u.Timezone)
	task, err := s.store.CreateTask(r.Context(), store.TaskInput{ProjectKey: project, Title: it.Title,
		Description: strings.TrimSpace(desc), Priority: priority, Type: "ops", Labels: []string{"checkup", it.Category},
		DueDate: &today})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	c, err = s.store.UpdateCheckupItem(r.Context(), id, key, func(ci *store.CheckItem) error {
		ci.TaskID, ci.TaskRef = &task.ID, &task.Ref
		return nil
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"task": task, "checkup": c})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
