package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/monitoring"
	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/vault"
)

// ── Vault ─────────────────────────────────────────────────────────────────

func (s *Server) listVault(w http.ResponseWriter, r *http.Request, u *store.User) {
	q := r.URL.Query()
	items, err := s.store.ListVault(r.Context(), store.VaultFilter{ProjectKey: q.Get("project"), Query: q.Get("q"), Kind: q.Get("kind"),
		RepoID: int64(queryInt(r, "repo", 0))})
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

// updateCheckupItem ticks an item (`done`) and/or snoozes it: `snooze`
// absent leaves it, null clears it, {"until": "YYYY-MM-DD"} hides it to that
// date, {"until": null} hides it until the item's title changes.
func (s *Server) updateCheckupItem(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Done   *bool           `json:"done"`
		Snooze json.RawMessage `json:"snooze"`
	}
	if !decode(w, r, &in) {
		return
	}
	key := r.PathValue("key")
	var c *store.Checkup
	var err error
	if len(in.Snooze) > 0 && string(in.Snooze) != "null" {
		var sn struct {
			Until *string `json:"until"`
		}
		if json.Unmarshal(in.Snooze, &sn) != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "snooze must be an object with until")
			return
		}
		if sn.Until != nil {
			if _, perr := time.Parse("2006-01-02", *sn.Until); perr != nil || *sn.Until <= store.TodayFor(u.Timezone) {
				writeErr(w, r, &store.ValidationError{Field: "snooze", Message: "until must be a date after today"})
				return
			}
		}
		if c, err = s.store.SnoozeCheckItem(r.Context(), id, key, sn.Until); err != nil {
			writeErr(w, r, err)
			return
		}
	} else if len(in.Snooze) > 0 {
		if c, err = s.store.UnsnoozeCheckItem(r.Context(), id, key); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	if in.Done != nil {
		if c, err = s.store.UpdateCheckupItem(r.Context(), id, key, func(it *store.CheckItem) error {
			it.Done = *in.Done
			return nil
		}); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	if c == nil {
		if c, err = s.store.CheckupByID(r.Context(), id); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, c)
}

// recheckCheckupItem re-runs the one check behind an item and writes what it
// says now: still there (fresh detail) or gone (resolved).
func (s *Server) recheckCheckupItem(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.recheck(w, r, u, r.PathValue("key"))
}

// recheckCheckup re-runs the checks behind every open item of a check-up.
func (s *Server) recheckCheckup(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.recheck(w, r, u, "")
}

func (s *Server) recheck(w http.ResponseWriter, r *http.Request, u *store.User, key string) {
	if s.checkups == nil {
		writeError(w, http.StatusServiceUnavailable, "internal", "check-ups are not configured")
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := s.store.CheckupByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var targets []store.CheckItem
	for _, it := range c.Items {
		if it.Severity == "ok" || it.Snoozed || (key != "" && it.Key != key) {
			continue
		}
		targets = append(targets, it)
	}
	if len(targets) == 0 {
		if key != "" {
			writeErr(w, r, store.ErrNotFound)
			return
		}
		writeJSON(w, http.StatusOK, c)
		return
	}
	// Detached from the request: a closed tab must not leave half the items
	// re-checked.
	ctx := context.WithoutCancel(r.Context())
	fresh := s.checkups.Recheck(ctx, store.TodayFor(u.Timezone), targets)
	keys := make([]string, 0, len(targets))
	for _, it := range targets {
		keys = append(keys, it.Key)
	}
	c, err = s.store.ApplyRecheck(ctx, id, keys, fresh, time.Now().UTC())
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
	if it.TaskID != nil && it.TaskStatus != "done" {
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
		ci.TaskID, ci.TaskRef, ci.TaskStatus = &task.ID, &task.Ref, task.Status
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

// fixCheckupItem does one of the item's suggested fixes, then re-checks the
// item so the answer says whether it is gone: a task marked done or moved,
// an endpoint probed now, or a repo pulled/fetched through the master's own
// command (waiting for that run and the repo scan after it, up to ~2 min).
func (s *Server) fixCheckupItem(w http.ResponseWriter, r *http.Request, u *store.User) {
	if s.checkups == nil {
		writeError(w, http.StatusServiceUnavailable, "internal", "check-ups are not configured")
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Fix string `json:"fix"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := context.WithoutCancel(r.Context()) // a fix that ran must get its re-check
	c, err := s.store.CheckupByID(ctx, id)
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
	offered := false
	for _, f := range it.Fixes {
		offered = offered || f.ID == in.Fix
	}
	if !offered {
		writeErr(w, r, &store.ValidationError{Field: "fix", Message: "not a fix this item offers"})
		return
	}
	today := store.TodayFor(u.Timezone)
	var run *store.Run
	note := ""
	switch it.Category {
	case "tasks":
		ref := key[strings.Index(key, ":")+1:]
		t, err := s.store.TaskByRef(ctx, ref)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		patch := store.Patch{}
		switch in.Fix {
		case "done":
			patch["status"] = json.RawMessage(`"done"`)
		case "week":
			d, _ := time.Parse("2006-01-02", today)
			patch["due_date"] = json.RawMessage(`"` + d.AddDate(0, 0, 7).Format("2006-01-02") + `"`)
		case "clear_due":
			patch["due_date"] = json.RawMessage(`null`)
		case "unblock":
			patch["status"] = json.RawMessage(`"todo"`)
		case "backlog":
			patch["status"] = json.RawMessage(`"backlog"`)
		}
		if _, err := s.store.UpdateTask(ctx, t.ID, patch); err != nil {
			writeErr(w, r, err)
			return
		}
	case "endpoints":
		eid, err := strconv.ParseInt(strings.TrimPrefix(key, "endpoint:"), 10, 64)
		if err != nil {
			writeErr(w, r, store.ErrNotFound)
			return
		}
		e, err := s.store.EndpointByID(ctx, eid)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		if err := s.monitor.Check(ctx, *e); err != nil {
			writeErr(w, r, err)
			return
		}
	case "repos":
		parts := strings.Split(key, ":") // repo:<id>:<what>
		if len(parts) != 3 {
			writeErr(w, r, store.ErrNotFound)
			return
		}
		repoID, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			writeErr(w, r, store.ErrNotFound)
			return
		}
		master, err := s.store.MasterRunner(ctx)
		if err != nil {
			writeErr(w, r, &store.ValidationError{Field: "fix", Message: "no master machine to run it on"})
			return
		}
		run, err = s.store.CreateRun(ctx, store.RunInput{RunnerID: master.ID, RepoID: repoID, Kind: "command", Command: in.Fix})
		if err != nil {
			writeErr(w, r, err)
			return
		}
		s.wakeups.notify(master.ID)
		if run, err = s.waitRunAndScan(ctx, run.ID, repoID); err != nil {
			writeErr(w, r, err)
			return
		}
		if run.Status != "succeeded" {
			note = fmt.Sprintf("%s %s on %s", in.Fix, run.Status, master.Name)
		}
	default:
		writeErr(w, r, &store.ValidationError{Field: "fix", Message: "nothing to do for this item"})
		return
	}
	fresh := s.checkups.Recheck(ctx, today, []store.CheckItem{*it})
	c, err = s.store.ApplyRecheck(ctx, id, []string{key}, fresh, time.Now().UTC())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := map[string]any{"checkup": c, "note": note}
	if run != nil {
		out["run"] = run.Public()
	}
	writeJSON(w, http.StatusOK, out)
}

// waitRunAndScan waits for a command run to end, then for the master's repo
// scan that follows every run, so a re-check reads the repo as it is now.
func (s *Server) waitRunAndScan(ctx context.Context, runID, repoID int64) (*store.Run, error) {
	deadline := time.Now().Add(90 * time.Second)
	var run *store.Run
	var err error
	for {
		if run, err = s.store.RunByID(ctx, runID); err != nil {
			return nil, err
		}
		if !store.OneOf(run.Status, store.RunOpenStatuses) || time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if run.FinishedAt == nil {
		return run, nil // still going: re-check what is known
	}
	finished := run.FinishedAt.UTC().Format(time.RFC3339)
	for scanBy := time.Now().Add(20 * time.Second); time.Now().Before(scanBy); time.Sleep(500 * time.Millisecond) {
		repo, err := s.store.RepoByID(ctx, repoID)
		if err == nil && repo.Git != nil && repo.Git.ScannedAt >= finished {
			break
		}
	}
	return run, nil
}
