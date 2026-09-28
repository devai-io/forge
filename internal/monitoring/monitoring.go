// Package monitoring reads the fleet's existing observability over the
// private network — VictoriaMetrics and Grafana, Nomad on each host — rather
// than collecting anything itself. Every source is optional; the server just
// has to be able to reach it (a VPN/tailnet is the usual way).
package monitoring

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	VictoriaMetricsURL string // e.g. http://metrics.internal:8428
	GrafanaURL         string // API base the server can reach
	GrafanaPublicURL   string // what a browser opens for dashboard links
}

type HostMetrics struct {
	Name          string     `json:"name"`
	ServerID      *int64     `json:"server_id"`
	Up            bool       `json:"up"`
	LastSeen      *time.Time `json:"last_seen"`
	UptimeSeconds *float64   `json:"uptime_seconds"`
	CPUPct        *float64   `json:"cpu_pct"`
	MemPct        *float64   `json:"mem_pct"`
	DiskPct       *float64   `json:"disk_pct"`
	Load1         *float64   `json:"load1"`
}

type Probe struct {
	URL         string   `json:"url"`
	Up          bool     `json:"up"`
	SSLDaysLeft *float64 `json:"ssl_days_left"`
}

type NomadJob struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	Running int    `json:"running"`
	Failed  int    `json:"failed"`
	Queued  int    `json:"queued"`
}

type NomadHost struct {
	Host      string     `json:"host"`
	Reachable bool       `json:"reachable"`
	Error     string     `json:"error"`
	Jobs      []NomadJob `json:"jobs"`
}

type Alert struct {
	Name     string            `json:"name"`
	State    string            `json:"state"`
	Severity string            `json:"severity"`
	Summary  string            `json:"summary"`
	Labels   map[string]string `json:"labels"`
	Since    *time.Time        `json:"since"`
}

type Dashboard struct {
	UID    string `json:"uid"`
	Title  string `json:"title"`
	Folder string `json:"folder"`
	URL    string `json:"url"`
}

type Grafana struct {
	URL        string      `json:"url"`
	Connected  bool        `json:"connected"`
	Error      string      `json:"error"`
	Alerts     []Alert     `json:"alerts"`
	Dashboards []Dashboard `json:"dashboards"`
}

type Snapshot struct {
	FetchedAt time.Time     `json:"fetched_at"`
	Hosts     []HostMetrics `json:"hosts"`
	Probes    []Probe       `json:"probes"`
	Nomad     []NomadHost   `json:"nomad"`
	Grafana   Grafana       `json:"grafana"`
}

// Server is the part of a Forge server row monitoring needs.
type Server struct {
	ID          int64
	Name        string
	TailscaleIP string
	Tags        []string
}

// Sources are what a snapshot is built from that live in the database.
type Sources interface {
	MonitoredServers(ctx context.Context) ([]Server, error)
	GrafanaToken(ctx context.Context) (string, error)
}

//go:embed dashboards.json
var repoDashboards []byte

type Client struct {
	cfg  Config
	src  Sources
	http *http.Client

	mu     sync.Mutex
	cached *Snapshot
}

func New(cfg Config, src Sources) *Client {
	return &Client{cfg: cfg, src: src, http: &http.Client{Timeout: 8 * time.Second}}
}

// Snapshot returns the cached view if it is younger than 30 s.
func (c *Client) Snapshot(ctx context.Context, fresh bool) *Snapshot {
	c.mu.Lock()
	if !fresh && c.cached != nil && time.Since(c.cached.FetchedAt) < 30*time.Second {
		s := c.cached
		c.mu.Unlock()
		return s
	}
	c.mu.Unlock()

	s := &Snapshot{FetchedAt: time.Now().UTC(), Hosts: []HostMetrics{}, Probes: []Probe{}, Nomad: []NomadHost{}}
	servers, _ := c.src.MonitoredServers(ctx)
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); s.Hosts = c.hosts(ctx, servers) }()
	go func() { defer wg.Done(); s.Probes = c.probes(ctx) }()
	go func() { defer wg.Done(); s.Nomad = c.nomad(ctx, servers) }()
	go func() { defer wg.Done(); s.Grafana = c.grafana(ctx) }()
	wg.Wait()

	c.mu.Lock()
	c.cached = s
	c.mu.Unlock()
	return s
}

// ── VictoriaMetrics ───────────────────────────────────────────────────────

type sample struct {
	labels map[string]string
	value  float64
	at     time.Time
}

func (c *Client) query(ctx context.Context, q string) ([]sample, error) {
	if c.cfg.VictoriaMetricsURL == "" {
		return nil, errors.New("VICTORIAMETRICS_URL not set")
	}
	u := c.cfg.VictoriaMetricsURL + "/api/v1/query?query=" + url.QueryEscape(q)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return nil, err
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("victoriametrics: %s", body.Error)
	}
	out := make([]sample, 0, len(body.Data.Result))
	for _, r := range body.Data.Result {
		ts, _ := r.Value[0].(float64)
		str, _ := r.Value[1].(string)
		v, err := strconv.ParseFloat(str, 64)
		if err != nil {
			continue
		}
		out = append(out, sample{labels: r.Metric, value: v, at: time.Unix(int64(ts), 0).UTC()})
	}
	return out, nil
}

// byHost runs q and indexes the results by their `host` label.
func (c *Client) byHost(ctx context.Context, q string) map[string]float64 {
	m := map[string]float64{}
	samples, err := c.query(ctx, q)
	if err != nil {
		return m
	}
	for _, s := range samples {
		if h := s.labels["host"]; h != "" {
			m[h] = s.value
		}
	}
	return m
}

func ptr(m map[string]float64, k string) *float64 {
	if v, ok := m[k]; ok {
		return &v
	}
	return nil
}

func (c *Client) hosts(ctx context.Context, servers []Server) []HostMetrics {
	lastSeen := c.byHost(ctx, `max by (host) (timestamp(host_uptime))`)
	uptime := c.byHost(ctx, `max by (host) (host_uptime)`)
	cpu := c.byHost(ctx, `100 * (1 - avg by (host) (rate(host_cpu_seconds_total{mode="idle"}[5m])))`)
	mem := c.byHost(ctx, `100 * (1 - max by (host) (host_memory_available_bytes) / max by (host) (host_memory_total_bytes))`)
	disk := c.byHost(ctx, `100 * max by (host) (host_filesystem_used_ratio{mountpoint="/"})`)
	load := c.byHost(ctx, `max by (host) (host_load1)`)

	ids := map[string]int64{}
	names := map[string]bool{}
	for _, s := range servers {
		ids[s.Name] = s.ID
		names[s.Name] = true
	}
	for h := range lastSeen {
		names[h] = true
	}
	out := make([]HostMetrics, 0, len(names))
	for name := range names {
		h := HostMetrics{Name: name, UptimeSeconds: ptr(uptime, name), CPUPct: ptr(cpu, name),
			MemPct: ptr(mem, name), DiskPct: ptr(disk, name), Load1: ptr(load, name)}
		if id, ok := ids[name]; ok {
			h.ServerID = &id
		}
		if ts, ok := lastSeen[name]; ok {
			t := time.Unix(int64(ts), 0).UTC()
			h.LastSeen = &t
			h.Up = time.Since(t) < 3*time.Minute
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (c *Client) probes(ctx context.Context) []Probe {
	up, err := c.query(ctx, `probe_success`)
	if err != nil {
		return []Probe{}
	}
	ssl := map[string]float64{}
	if samples, err := c.query(ctx, `(probe_ssl_earliest_cert_expiry - time()) / 86400`); err == nil {
		for _, s := range samples {
			ssl[s.labels["instance"]] = s.value
		}
	}
	seen := map[string]bool{}
	out := []Probe{}
	for _, s := range up {
		inst := s.labels["instance"]
		if inst == "" || seen[inst] {
			continue
		}
		seen[inst] = true
		p := Probe{URL: inst, Up: s.value == 1}
		if d, ok := ssl[inst]; ok {
			d = float64(int(d*10)) / 10
			p.SSLDaysLeft = &d
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out
}

// Scalar evaluates a PromQL expression and returns the per-host values;
// the check-up uses it for thresholds without re-querying the whole snapshot.
func (c *Client) Scalar(ctx context.Context, q string) (map[string]float64, error) {
	samples, err := c.query(ctx, q)
	if err != nil {
		return nil, err
	}
	m := map[string]float64{}
	for _, s := range samples {
		key := s.labels["host"]
		if key == "" {
			key = s.labels["instance"]
		}
		m[key] = s.value
	}
	return m, nil
}

// ── Nomad ─────────────────────────────────────────────────────────────────

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}

// nomad reads every host's job list anonymously (each cluster's ACLs grant
// anonymous read; writes need a token Forge does not have). Hosts that refuse
// the connection and are not tagged "nomad" simply do not run it.
func (c *Client) nomad(ctx context.Context, servers []Server) []NomadHost {
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := []NomadHost{}
	for _, s := range servers {
		if s.TailscaleIP == "" {
			continue
		}
		wg.Add(1)
		go func(s Server) {
			defer wg.Done()
			h := NomadHost{Host: s.Name, Jobs: []NomadJob{}}
			jobs, err := c.nomadJobs(ctx, s.TailscaleIP)
			if err != nil {
				if !hasTag(s.Tags, "nomad") {
					return
				}
				h.Error = err.Error()
			} else {
				h.Reachable, h.Jobs = true, jobs
			}
			mu.Lock()
			out = append(out, h)
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

func (c *Client) nomadJobs(ctx context.Context, ip string) ([]NomadJob, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ip+":4646/v1/jobs", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, shortErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nomad answered HTTP %d", resp.StatusCode)
	}
	var raw []struct {
		ID         string `json:"ID"`
		Type       string `json:"Type"`
		Status     string `json:"Status"`
		Periodic   bool   `json:"Periodic"`
		JobSummary struct {
			Summary map[string]struct {
				Queued, Running, Failed, Starting, Lost int
			} `json:"Summary"`
		} `json:"JobSummary"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	jobs := make([]NomadJob, 0, len(raw))
	for _, j := range raw {
		nj := NomadJob{ID: j.ID, Type: j.Type, Status: j.Status}
		for _, g := range j.JobSummary.Summary {
			nj.Running += g.Running
			nj.Failed += g.Failed
			nj.Queued += g.Queued + g.Starting
		}
		jobs = append(jobs, nj)
	}
	sort.Slice(jobs, func(i, k int) bool { return jobs[i].ID < jobs[k].ID })
	return jobs, nil
}

// ── Grafana ───────────────────────────────────────────────────────────────

func (c *Client) grafana(ctx context.Context) Grafana {
	g := Grafana{URL: c.cfg.GrafanaPublicURL, Alerts: []Alert{}, Dashboards: c.fallbackDashboards()}
	token, err := c.src.GrafanaToken(ctx)
	if err != nil || token == "" {
		g.Error = "no Grafana token in the vault (tag it integration:grafana)"
		return g
	}
	alerts, err := c.grafanaAlerts(ctx, token)
	if err != nil {
		g.Error = err.Error()
		return g
	}
	g.Connected, g.Alerts = true, alerts
	if dash, err := c.grafanaDashboards(ctx, token); err == nil && len(dash) > 0 {
		g.Dashboards = dash
	}
	return g
}

func (c *Client) grafanaGet(ctx context.Context, token, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.GrafanaURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return shortErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("grafana %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

// grafanaAlerts reads Grafana-managed rule states (the fleet's alerting is
// Grafana-managed alert rules).
func (c *Client) grafanaAlerts(ctx context.Context, token string) ([]Alert, error) {
	var body struct {
		Data struct {
			Groups []struct {
				Rules []struct {
					Name        string            `json:"name"`
					State       string            `json:"state"`
					Labels      map[string]string `json:"labels"`
					Annotations map[string]string `json:"annotations"`
					Alerts      []struct {
						State    string            `json:"state"`
						ActiveAt *time.Time        `json:"activeAt"`
						Labels   map[string]string `json:"labels"`
					} `json:"alerts"`
				} `json:"rules"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err := c.grafanaGet(ctx, token, "/api/prometheus/grafana/api/v1/rules", &body); err != nil {
		return nil, err
	}
	out := []Alert{}
	for _, g := range body.Data.Groups {
		for _, r := range g.Rules {
			state := normState(r.State)
			if state == "normal" {
				continue
			}
			// One entry per firing instance, so "DiskSpaceWarning on web-1" and
			// "… on db-1" are separate lines, not one.
			emitted := false
			for _, a := range r.Alerts {
				as := normState(a.State)
				if as == "normal" {
					continue
				}
				labels := map[string]string{}
				for k, v := range r.Labels {
					labels[k] = v
				}
				for k, v := range a.Labels {
					labels[k] = v
				}
				out = append(out, Alert{Name: r.Name, State: as, Severity: labels["severity"],
					Summary: r.Annotations["summary"], Labels: labels, Since: a.ActiveAt})
				emitted = true
			}
			if !emitted {
				out = append(out, Alert{Name: r.Name, State: state, Severity: r.Labels["severity"],
					Summary: r.Annotations["summary"], Labels: r.Labels})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].State == "firing" && out[j].State != "firing" })
	return out, nil
}

func normState(s string) string {
	switch strings.ToLower(s) {
	case "firing", "alerting":
		return "firing"
	case "pending":
		return "pending"
	}
	return "normal"
}

func (c *Client) grafanaDashboards(ctx context.Context, token string) ([]Dashboard, error) {
	var raw []struct {
		UID         string `json:"uid"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		FolderTitle string `json:"folderTitle"`
	}
	if err := c.grafanaGet(ctx, token, "/api/search?type=dash-db&limit=500", &raw); err != nil {
		return nil, err
	}
	out := make([]Dashboard, 0, len(raw))
	for _, d := range raw {
		folder := d.FolderTitle
		if folder == "" {
			folder = "General"
		}
		out = append(out, Dashboard{UID: d.UID, Title: d.Title, Folder: folder, URL: c.cfg.GrafanaPublicURL + d.URL})
	}
	return out, nil
}

// fallbackDashboards is an optional static list (dashboards.json), so links
// work before a token is configured.
func (c *Client) fallbackDashboards() []Dashboard {
	var raw []struct {
		UID    string `json:"uid"`
		Title  string `json:"title"`
		Folder string `json:"folder"`
	}
	_ = json.Unmarshal(repoDashboards, &raw)
	out := make([]Dashboard, 0, len(raw))
	for _, d := range raw {
		out = append(out, Dashboard{UID: d.UID, Title: d.Title, Folder: d.Folder,
			URL: c.cfg.GrafanaPublicURL + "/d/" + d.UID})
	}
	return out
}

func shortErr(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return errors.New("connection refused")
	case strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "timeout"):
		return errors.New("timed out")
	}
	return err
}
