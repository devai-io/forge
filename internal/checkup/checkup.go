// Package checkup runs the daily check-up: one pass over everything Forge
// can see (endpoints, TLS, hosts, Nomad, Grafana alerts, backups, runners,
// repos and CI, tasks, vault expiries) that turns into the day's action list.
package checkup

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/mail"
	"github.com/devai-io/forge/internal/monitoring"
	"github.com/devai-io/forge/internal/store"
)

type Runner struct {
	store     *store.Store
	mon       *monitoring.Client
	mailer    *mail.Mailer
	publicURL string

	mu sync.Mutex // one check-up at a time
}

func New(st *store.Store, mon *monitoring.Client, m *mail.Mailer, publicURL string) *Runner {
	return &Runner{store: st, mon: mon, mailer: m, publicURL: publicURL}
}

// Loop starts the scheduled check-up once a day, at or after the user's
// checkup_time in their timezone. It looks every minute, so a restart at
// 07:45 still produces the 07:30 run.
func (r *Runner) Loop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		u, err := r.store.FirstUser(ctx)
		if err != nil {
			continue
		}
		loc, err := time.LoadLocation(u.Timezone)
		if err != nil {
			loc = time.UTC
		}
		now := time.Now().In(loc)
		if now.Format("15:04") < u.CheckupTime {
			continue
		}
		today := now.Format("2006-01-02")
		if done, err := r.store.CheckupExistsFor(ctx, today); err != nil || done {
			continue
		}
		c, err := r.Run(ctx, "schedule", today)
		if err != nil {
			slog.Error("scheduled check-up", "err", err)
			continue
		}
		slog.Info("daily check-up done", "status", c.Status, "fail", c.Counts.Fail, "warn", c.Counts.Warn)
		if u.CheckupEmail && r.mailer.Enabled() {
			if err := r.mailer.Send(u.Email, subject(c), r.body(c)); err != nil {
				slog.Error("check-up e-mail", "err", err)
			} else {
				_ = r.store.MarkCheckupEmailed(ctx, c.ID)
			}
		}
	}
}

// Run executes every check and stores the result.
func (r *Runner) Run(ctx context.Context, trigger, date string) (*store.Checkup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, err := r.store.StartCheckup(ctx, date, trigger)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	snap := r.mon.Snapshot(ctx, true)
	var items []store.CheckItem
	add := func(list []store.CheckItem) { items = append(items, list...) }
	add(r.endpoints(ctx))
	add(r.tls(ctx))
	add(hostChecks(snap))
	add(nomadChecks(snap))
	add(alertChecks(snap))
	add(r.backups(ctx))
	add(r.runners(ctx))
	add(r.repos(ctx))
	add(r.tasks(ctx, date))
	add(r.vault(ctx, date))
	add(r.security(ctx))

	rank := map[string]int{"fail": 0, "warn": 1, "ok": 2}
	sort.SliceStable(items, func(i, j int) bool { return rank[items[i].Severity] < rank[items[j].Severity] })
	status := "ok"
	for _, it := range items {
		if it.Severity == "fail" {
			status = "fail"
			break
		}
		if it.Severity == "warn" {
			status = "warn"
		}
	}
	return r.store.FinishCheckup(context.WithoutCancel(ctx), id, status, items)
}

func sp(s string) *string { return &s }

func item(key, cat, sev, title, detail, action, link, project string) store.CheckItem {
	it := store.CheckItem{Key: key, Category: cat, Severity: sev, Title: title, Detail: detail, Action: action}
	if link != "" {
		it.Link = sp(link)
	}
	if project != "" {
		it.ProjectKey = sp(project)
	}
	return it
}

func allClear(cat, title string) store.CheckItem {
	return item("ok:"+cat, cat, "ok", title, "", "", "", "")
}

// ── Endpoints & TLS ───────────────────────────────────────────────────────

func (r *Runner) endpoints(ctx context.Context) []store.CheckItem {
	eps, err := r.store.EnabledEndpoints(ctx)
	if err != nil {
		return []store.CheckItem{item("endpoints:error", "endpoints", "warn", "Could not read endpoints", err.Error(), "Check forge-api logs", "", "")}
	}
	var out []store.CheckItem
	for _, e := range eps {
		key := fmt.Sprintf("endpoint:%d", e.ID)
		link := "/p/" + e.ProjectKey
		switch {
		case e.LastStatus == "down":
			detail := e.LastError
			out = append(out, item(key, "endpoints", "fail", e.Name+" is down", detail+" — "+e.URL,
				"Check the service and its Nomad job; see Monitoring for the host", link, e.ProjectKey))
		case e.Uptime24h != nil && *e.Uptime24h < 0.98:
			out = append(out, item(key, "endpoints", "warn", fmt.Sprintf("%s flapped (%.1f%% up in 24 h)", e.Name, *e.Uptime24h*100),
				e.URL, "Look at the check history for when it failed and why", link, e.ProjectKey))
		case e.LastLatencyMS != nil && *e.LastLatencyMS > 3000:
			out = append(out, item(key, "endpoints", "warn", fmt.Sprintf("%s is slow (%d ms)", e.Name, *e.LastLatencyMS),
				e.URL, "Check host load and the service's logs", link, e.ProjectKey))
		}
	}
	if len(out) == 0 {
		out = append(out, allClear("endpoints", fmt.Sprintf("All %d endpoints up", len(eps))))
	}
	return out
}

// tls dials each distinct https host and reads the leaf certificate, so an
// expiring certificate shows up weeks before a browser starts refusing it.
func (r *Runner) tls(ctx context.Context) []store.CheckItem {
	eps, err := r.store.EnabledEndpoints(ctx)
	if err != nil {
		return nil
	}
	type target struct{ host, project string }
	seen := map[string]target{}
	for _, e := range eps {
		u, err := url.Parse(e.URL)
		if err != nil || u.Scheme != "https" {
			continue
		}
		if _, ok := seen[u.Hostname()]; !ok {
			seen[u.Hostname()] = target{u.Hostname(), e.ProjectKey}
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var out []store.CheckItem
	for _, t := range seen {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			days, err := certDaysLeft(ctx, t.host)
			var it *store.CheckItem
			switch {
			case err != nil:
				x := item("tls:"+t.host, "tls", "warn", "TLS check failed for "+t.host, err.Error(),
					"Open the site and check the certificate", "https://"+t.host, t.project)
				it = &x
			case days < 7:
				x := item("tls:"+t.host, "tls", "fail", fmt.Sprintf("Certificate for %s expires in %d days", t.host, days),
					"Caddy renews at 30 days left — renewal is failing", "Check Caddy's ACME logs on the serving host", "https://"+t.host, t.project)
				it = &x
			case days < 21:
				x := item("tls:"+t.host, "tls", "warn", fmt.Sprintf("Certificate for %s expires in %d days", t.host, days),
					"Renewal normally happens at 30 days left", "Check Caddy's ACME logs on the serving host", "https://"+t.host, t.project)
				it = &x
			}
			if it != nil {
				mu.Lock()
				out = append(out, *it)
				mu.Unlock()
			}
		}(t)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if len(out) == 0 {
		out = append(out, allClear("tls", fmt.Sprintf("All %d certificates valid for 3+ weeks", len(seen))))
	}
	return out
}

func certDaysLeft(ctx context.Context, host string) (int, error) {
	d := &net.Dialer{Timeout: 8 * time.Second}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := d.DialContext(dctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return 0, err
	}
	conn := tls.Client(raw, &tls.Config{ServerName: host})
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := conn.HandshakeContext(dctx); err != nil {
		return 0, err
	}
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return 0, fmt.Errorf("no certificate")
	}
	return int(time.Until(certs[0].NotAfter).Hours() / 24), nil
}

// ── Hosts, Nomad, alerts ──────────────────────────────────────────────────

func hostChecks(s *monitoring.Snapshot) []store.CheckItem {
	var out []store.CheckItem
	for _, h := range s.Hosts {
		key := "host:" + h.Name
		if !h.Up {
			detail := "no metrics received"
			if h.LastSeen != nil {
				detail = "last metrics " + h.LastSeen.Format(time.RFC3339)
			}
			out = append(out, item(key, "hosts", "fail", h.Name+" is not reporting metrics", detail,
				"SSH in and check the metrics agent / the host itself", "/monitoring", ""))
			continue
		}
		if h.DiskPct != nil && *h.DiskPct >= 80 {
			sev := "warn"
			if *h.DiskPct >= 90 {
				sev = "fail"
			}
			out = append(out, item("disk:"+h.Name, "hosts", sev, fmt.Sprintf("%s root disk %.0f%% full", h.Name, *h.DiskPct), "",
				"Prune docker images/registry, old generations (nix-collect-garbage), logs", "/monitoring", ""))
		}
		if h.MemPct != nil && *h.MemPct >= 90 {
			out = append(out, item("mem:"+h.Name, "hosts", "warn", fmt.Sprintf("%s memory %.0f%% used", h.Name, *h.MemPct), "",
				"Find the heavy allocation (Nomad job, database) before it OOMs", "/monitoring", ""))
		}
		if h.CPUPct != nil && *h.CPUPct >= 90 {
			out = append(out, item("cpu:"+h.Name, "hosts", "warn", fmt.Sprintf("%s CPU %.0f%% busy (5 min)", h.Name, *h.CPUPct), "",
				"Check what is spinning (runner builds, a hot loop)", "/monitoring", ""))
		}
	}
	if len(out) == 0 {
		out = append(out, allClear("hosts", fmt.Sprintf("All %d hosts reporting, no resource pressure", len(s.Hosts))))
	}
	return out
}

func nomadChecks(s *monitoring.Snapshot) []store.CheckItem {
	var out []store.CheckItem
	jobs := 0
	for _, h := range s.Nomad {
		if !h.Reachable {
			out = append(out, item("nomad:"+h.Host, "nomad", "fail", "Nomad on "+h.Host+" is unreachable", h.Error,
				"SSH in and check nomad.service", "/monitoring", ""))
			continue
		}
		for _, j := range h.Jobs {
			jobs++
			if j.Type != "service" && j.Type != "system" {
				continue // batch jobs are "dead" when they are done
			}
			key := "job:" + h.Host + ":" + j.ID
			switch {
			case j.Status == "dead":
				out = append(out, item(key, "nomad", "fail", fmt.Sprintf("Job %s is dead on %s", j.ID, h.Host),
					"a stopped service job", "Redeploy it (re-run its workflow) or `nomad job run`", "/monitoring", ""))
			case j.Running == 0:
				out = append(out, item(key, "nomad", "fail", fmt.Sprintf("Job %s has no running allocation on %s", j.ID, h.Host),
					fmt.Sprintf("status %s, %d queued", j.Status, j.Queued), "`nomad job status "+j.ID+"` on the host", "/monitoring", ""))
			case j.Queued > 0:
				out = append(out, item(key, "nomad", "warn", fmt.Sprintf("Job %s has %d allocation(s) waiting on %s", j.ID, j.Queued, h.Host),
					"", "Check placement failures (resources, host volumes)", "/monitoring", ""))
			}
		}
	}
	if len(out) == 0 {
		out = append(out, allClear("nomad", fmt.Sprintf("All %d Nomad jobs healthy on %d hosts", jobs, len(s.Nomad))))
	}
	return out
}

func alertChecks(s *monitoring.Snapshot) []store.CheckItem {
	if !s.Grafana.Connected {
		return []store.CheckItem{item("alerts:connect", "alerts", "warn", "Grafana alerts not connected", s.Grafana.Error,
			"Create a Viewer service-account token in Grafana and add it on the Monitoring page", "/monitoring", "")}
	}
	var out []store.CheckItem
	for _, a := range s.Grafana.Alerts {
		sev := "warn"
		if a.State == "firing" && (a.Severity == "critical" || a.Severity == "") {
			sev = "fail"
		}
		where := a.Labels["host"]
		if where == "" {
			where = a.Labels["instance"]
		}
		title := a.Name
		if where != "" {
			title += " on " + where
		}
		out = append(out, item("alert:"+a.Name+":"+where, "alerts", sev, title+" ("+a.State+")", a.Summary,
			"Open the dashboard in Grafana and follow the alert's runbook", s.Grafana.URL+"/alerting/list", ""))
	}
	if len(out) == 0 {
		out = append(out, allClear("alerts", "No Grafana alerts firing"))
	}
	return out
}

func (r *Runner) backups(ctx context.Context) []store.CheckItem {
	ages, err := r.mon.Scalar(ctx, `(time() - max by (host) (pgbackup_last_success_timestamp)) / 3600`)
	if err != nil || len(ages) == 0 {
		return nil // not exported on this fleet (yet): no item rather than a false alarm
	}
	var out []store.CheckItem
	for host, hours := range ages {
		if hours > 26 {
			out = append(out, item("backup:"+host, "backups", "fail", fmt.Sprintf("Database backup on %s is %.0f h old", host, hours), "",
				"Check the backup job and its destination on that host", "/monitoring", ""))
		}
	}
	if len(out) == 0 {
		out = append(out, allClear("backups", fmt.Sprintf("Database backups fresh on %d hosts", len(ages))))
	}
	return out
}

// ── Runners, repos, CI ────────────────────────────────────────────────────

// runners: the master must be up — it is the machine everything runs on.
// Other runners (a laptop, the Mac) sleep, and that is not news.
func (r *Runner) runners(ctx context.Context) []store.CheckItem {
	rs, err := r.store.ListRunners(ctx)
	if err != nil || len(rs) == 0 {
		return nil
	}
	online := 0
	for _, rn := range rs {
		if rn.Online {
			online++
		}
	}
	for _, rn := range rs {
		if rn.Role != "master" {
			continue
		}
		if rn.Online || (rn.LastSeenAt != nil && time.Since(*rn.LastSeenAt) < 10*time.Minute) {
			return []store.CheckItem{allClear("runners", fmt.Sprintf("Master %s online (%d of %d runners up)", rn.Name, online, len(rs)))}
		}
		detail := "never connected"
		if rn.LastSeenAt != nil {
			detail = "last seen " + rn.LastSeenAt.Format("2006-01-02 15:04")
		}
		return []store.CheckItem{item("runner:"+rn.Name, "runners", "fail", "Master "+rn.Name+" is offline", detail,
			"It should always be on: check power/network, then `systemctl --user status forge-agent` on it", "/agents", "")}
	}
	return []store.CheckItem{item("runners:no-master", "runners", "warn", "No master runner elected", "",
		"Make your always-on machine the master on the Agents page", "/agents", "")}
}

func (r *Runner) repos(ctx context.Context) []store.CheckItem {
	repos, err := r.store.AllRepos(ctx)
	if err != nil {
		return nil
	}
	var out, ci []store.CheckItem
	scanned := 0
	for _, rp := range repos {
		if rp.Git == nil || rp.ProjectStatus == "archived" {
			continue
		}
		scanned++
		g := rp.Git
		link := "/p/" + rp.ProjectKey
		key := fmt.Sprintf("repo:%d", rp.ID)
		if g.Error != "" {
			out = append(out, item(key+":err", "repos", "warn", rp.Name+": git error", g.Error, "Look at the repo on "+g.RunnerName, link, rp.ProjectKey))
		}
		if g.Ahead > 0 {
			out = append(out, item(key+":ahead", "repos", "warn", fmt.Sprintf("%s: %d unpushed commit(s) on %s", rp.Name, g.Ahead, g.Branch), "",
				"Push them (or open a PR) — unpushed work exists on one machine only", link, rp.ProjectKey))
		}
		if g.Dirty > 0 {
			out = append(out, item(key+":dirty", "repos", "warn", fmt.Sprintf("%s: %d uncommitted change(s) on %s", rp.Name, g.Dirty, g.Branch), "",
				"Commit, stash or discard", link, rp.ProjectKey))
		}
		if g.Behind > 0 {
			detail := ""
			if g.Sync != nil && g.Sync.Result == "skipped" {
				detail = "Not pulled automatically: " + g.Sync.Detail
			}
			out = append(out, item(key+":behind", "repos", "warn", fmt.Sprintf("%s: %d commit(s) behind origin", rp.Name, g.Behind), detail,
				"Pull before starting work", link, rp.ProjectKey))
		}
		if g.Sync != nil && g.Sync.Result == "error" {
			out = append(out, item(key+":sync", "repos", "warn", rp.Name+": automatic pull failed", g.Sync.Detail,
				"Check the remote and this machine's git credentials (the agent runs without a terminal)", link, rp.ProjectKey))
		}
		if g.CI != nil && g.CI.Status == "completed" && g.CI.Conclusion != "" && g.CI.Conclusion != "success" && g.CI.Conclusion != "skipped" {
			x := item(key+":ci", "ci", "fail", fmt.Sprintf("%s: CI %s (%s)", rp.Name, g.CI.Conclusion, g.CI.Workflow), g.CI.Title,
				"Open the run, fix, re-run", g.CI.URL, rp.ProjectKey)
			ci = append(ci, x)
		}
	}
	if len(out) == 0 && scanned > 0 {
		out = append(out, allClear("repos", fmt.Sprintf("All %d repos clean and in sync", scanned)))
	}
	if len(ci) == 0 && scanned > 0 {
		ci = append(ci, allClear("ci", "No failing CI on default branches"))
	}
	return append(out, ci...)
}

// ── Tasks & vault ─────────────────────────────────────────────────────────

func (r *Runner) tasks(ctx context.Context, today string) []store.CheckItem {
	var out []store.CheckItem
	overdue, _ := r.store.OverdueTasks(ctx, today)
	for i, t := range overdue {
		if i == 10 {
			out = append(out, item("tasks:overdue-more", "tasks", "warn", fmt.Sprintf("…and %d more overdue tasks", len(overdue)-10), "",
				"Reschedule or drop them", "/tasks", ""))
			break
		}
		out = append(out, item("task:"+t.Ref, "tasks", "warn", fmt.Sprintf("%s overdue since %s: %s", t.Ref, deref(t.DueDate), t.Title), "",
			"Do it today, move the date, or drop it", fmt.Sprintf("/p/%s?task=%d", t.ProjectKey, t.ID), t.ProjectKey))
	}
	due, _ := r.store.DueOn(ctx, today)
	for _, t := range due {
		out = append(out, item("due:"+t.Ref, "tasks", "warn", fmt.Sprintf("%s due today: %s", t.Ref, t.Title), "",
			"Get it done or move it", fmt.Sprintf("/p/%s?task=%d", t.ProjectKey, t.ID), t.ProjectKey))
	}
	blocked, _ := r.store.StaleBlocked(ctx, 3)
	for _, t := range blocked {
		out = append(out, item("blocked:"+t.Ref, "tasks", "warn", fmt.Sprintf("%s blocked for 3+ days: %s", t.Ref, t.Title), "",
			"Unblock it (who/what is it waiting on?) or park it in the backlog", fmt.Sprintf("/p/%s?task=%d", t.ProjectKey, t.ID), t.ProjectKey))
	}
	if len(out) == 0 {
		out = append(out, allClear("tasks", "Nothing overdue, due today or stuck"))
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (r *Runner) vault(ctx context.Context, today string) []store.CheckItem {
	t, _ := time.Parse("2006-01-02", today)
	items, err := r.store.ExpiringVault(ctx, t.AddDate(0, 0, 30).Format("2006-01-02"))
	if err != nil {
		return nil
	}
	var out []store.CheckItem
	for _, v := range items {
		sev, what := "warn", "expires"
		if *v.ExpiresAt < today {
			sev, what = "fail", "expired"
		}
		project := ""
		if v.ProjectKey != nil {
			project = *v.ProjectKey
		}
		out = append(out, item(fmt.Sprintf("vault:%d", v.ID), "vault", sev, fmt.Sprintf("%s %s %s", v.Name, what, *v.ExpiresAt),
			v.Location, "Renew it, then update the vault item (and wherever it is installed)", "/vault", project))
	}
	if len(out) == 0 {
		out = append(out, allClear("vault", "No credentials expiring within 30 days"))
	}
	return out
}

// ── Security ──────────────────────────────────────────────────────────────

// security looks at the account itself: Forge opens shells and hands out
// signing keys, so an unchanged setup password or no second factor is a
// finding, not a preference.
func (r *Runner) security(ctx context.Context) []store.CheckItem {
	u, err := r.store.FirstUser(ctx)
	if err != nil {
		return nil
	}
	var out []store.CheckItem
	if unchanged, err := r.store.InitialPasswordUnchanged(ctx, u.ID); err == nil && unchanged {
		out = append(out, item("security:initial-password", "security", "warn", "The setup password is still in use",
			"It was printed on a terminal when the account was created", "Change it under Settings → Password", "/settings", ""))
	}
	if !u.TOTPEnabled {
		out = append(out, item("security:totp", "security", "warn", "Two-factor authentication is off", "",
			"Enrol an authenticator app under Settings → Two-factor", "/settings", ""))
	}
	if n, err := r.store.NewDeviceLogins(ctx, 7*24*time.Hour); err == nil && n > 0 {
		out = append(out, item("security:new-devices", "security", "warn",
			fmt.Sprintf("%d sign-in(s) from new devices in the last 7 days", n), "",
			"Check Settings → Security; if one is not yours, change the password (signs the others out)", "/settings", ""))
	}
	if len(out) == 0 {
		out = append(out, allClear("security", "Password changed, two-factor on, no new devices this week"))
	}
	return out
}

// ── E-mail ────────────────────────────────────────────────────────────────

func subject(c *store.Checkup) string {
	if c.ActionsTotal == 0 {
		return "Forge check-up " + c.Date + ": all clear"
	}
	return fmt.Sprintf("Forge check-up %s: %d to act on (%d failing)", c.Date, c.ActionsTotal, c.Counts.Fail)
}

func (r *Runner) body(c *store.Checkup) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Daily check-up for %s — %d failing, %d warnings, %d clear.\n\n", c.Date, c.Counts.Fail, c.Counts.Warn, c.Counts.OK)
	for _, it := range c.Items {
		if it.Severity == "ok" {
			continue
		}
		mark := "!"
		if it.Severity == "fail" {
			mark = "✗"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, it.Title)
		if it.Action != "" {
			fmt.Fprintf(&b, "    → %s\n", it.Action)
		}
	}
	fmt.Fprintf(&b, "\nAction list: %s/checkup\n", r.publicURL)
	return b.String()
}
