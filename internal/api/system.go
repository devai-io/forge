package api

import (
	"net/http"
	"net/url"
	"time"

	"github.com/devai-io/forge/internal/store"
)

var startedAt = time.Now().UTC()

// systemFacts is what the Documentation page overlays on its text: the real
// intervals, which integrations are on, and how much is in the database.
func (s *Server) systemFacts(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	counts, err := s.store.SystemCounts(ctx)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	runners, err := s.store.ListRunners(ctx)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var master map[string]any
	rs := make([]map[string]any, 0, len(runners))
	for _, rn := range runners {
		rs = append(rs, map[string]any{"name": rn.Name, "role": rn.Role, "online": rn.Online,
			"commands": len(rn.Capabilities.Commands), "terminal": rn.Capabilities.Terminal})
		if rn.Role == "master" {
			master = map[string]any{"name": rn.Name, "online": rn.Online, "code": rn.Capabilities.Code,
				"terminal": rn.Capabilities.Terminal}
		}
	}

	var lastAt *time.Time
	var lastTrigger *string
	emailed, ranToday := false, false
	if c, err := s.store.LatestCheckup(ctx); err == nil {
		lastAt, lastTrigger, emailed = &c.StartedAt, &c.Trigger, c.Emailed
		ranToday = c.Date == store.TodayFor(u.Timezone) && c.Trigger == "schedule"
	}
	seeded, _ := s.store.SeededAt(ctx)

	grafana := false
	sources := []string{}
	if s.mon != nil {
		snap := s.mon.Snapshot(ctx, false)
		grafana = snap.Grafana.Connected
		if s.cfg.VictoriaMetricsURL != "" {
			sources = append(sources, "VictoriaMetrics ("+hostOf(s.cfg.VictoriaMetricsURL)+")")
		}
		if len(snap.Nomad) > 0 {
			sources = append(sources, "Nomad on every host")
		}
		if grafana {
			sources = append(sources, "Grafana ("+hostOf(s.cfg.GrafanaPublicURL)+")")
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.cfg.Version, "started_at": startedAt, "public_url": s.cfg.PublicURL,
		"intervals": map[string]string{
			"endpoint_check": s.cfg.MonitorInterval.String(), "repo_scan": "5m0s", "ci_status": "15m0s",
			"runner_heartbeat": "10s", "monitoring_cache": "30s", "checkup_time": u.CheckupTime, "timezone": u.Timezone,
			"session_ttl": sessionTTL.String(), "session_max": (90 * 24 * time.Hour).String(),
			"elevation": elevationTTL.String(), "code_session": codeTTL.String(),
		},
		"features": map[string]any{
			"smtp": s.mailer.Enabled(), "vault": s.box.Available(), "totp_enabled": u.TOTPEnabled,
			"grafana_connected": grafana, "victoriametrics": s.cfg.VictoriaMetricsURL != "",
			"monitoring_sources": sources,
		},
		"master": master, "runners": rs, "counts": counts,
		"checkup": map[string]any{"last_at": lastAt, "last_trigger": lastTrigger,
			"next_at": store.NextCheckup(time.Now(), u.Timezone, u.CheckupTime, ranToday), "emailed_last": emailed},
		"seeded_at": seeded,
	})
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}
