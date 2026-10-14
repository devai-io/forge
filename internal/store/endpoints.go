package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const endpointSelect = `
SELECT e.id, e.project_id, p.key, p.name, p.color, e.name, e.url, e.kind, e.expect_status, e.enabled,
       e.last_status, e.fail_streak, e.last_code, e.last_latency_ms, e.last_error, e.last_checked_at, e.last_change_at,
       (SELECT avg(CASE WHEN c.ok THEN 1.0 ELSE 0.0 END)
          FROM endpoint_checks c WHERE c.endpoint_id = e.id AND c.at > ts_add(now(), -86400))
FROM endpoints e JOIN projects p ON p.id = e.project_id`

func scanEndpoint(row interface{ Scan(...any) error }) (*Endpoint, error) {
	var e Endpoint
	if err := row.Scan(&e.ID, &e.ProjectID, &e.ProjectKey, &e.ProjectName, &e.ProjectColor, &e.Name, &e.URL,
		&e.Kind, &e.ExpectStatus, &e.Enabled, &e.LastStatus, &e.FailStreak, &e.LastCode, &e.LastLatencyMS, &e.LastError,
		&e.LastCheckedAt, &e.LastChangeAt, &e.Uptime24h); err != nil {
		return nil, mapErr(err)
	}
	return &e, nil
}

// withRetries fills in what the endpoint settings decide: how many rechecks
// it gets, and when a "warning" endpoint is checked next.
func (e *Endpoint) withRetries(v EndpointSettings) {
	e.RetryLimit = v.Retries
	e.NextCheckAt = nil
	if e.LastStatus == "warning" && e.LastCheckedAt != nil {
		next := e.LastCheckedAt.Add(v.Delay(e.FailStreak))
		e.NextCheckAt = &next
	}
}

// EndpointRetries is how many quick rechecks a failing endpoint gets before
// it is called down, until Settings change it: one failed check puts it at
// "warning", and it is "down" once that many more checks in a row have
// failed too.
const EndpointRetries = 2

// EndpointSettings tune the retries: Retries quick rechecks (0 = down on the
// first failure), RecheckSeconds the waits before each (the last one
// repeats).
type EndpointSettings struct {
	Retries        int   `json:"retries"`
	RecheckSeconds []int `json:"recheck_seconds"`
}

func DefaultEndpointSettings() EndpointSettings {
	return EndpointSettings{Retries: EndpointRetries, RecheckSeconds: []int{30, 60}}
}

// Validate checks the ranges the settings page offers.
func (v EndpointSettings) Validate() error {
	if v.Retries < 0 || v.Retries > 5 {
		return invalid("retries", "must be between 0 and 5")
	}
	if len(v.RecheckSeconds) < 1 || len(v.RecheckSeconds) > 5 {
		return invalid("recheck_seconds", "give 1 to 5 delays")
	}
	for _, d := range v.RecheckSeconds {
		if d < 10 || d > 900 {
			return invalid("recheck_seconds", "each delay is 10 to 900 seconds")
		}
	}
	return nil
}

// Delay is how long after its streak-th consecutive failure a "warning"
// endpoint is checked again.
func (v EndpointSettings) Delay(streak int) time.Duration {
	d := v.RecheckSeconds
	if len(d) == 0 {
		d = DefaultEndpointSettings().RecheckSeconds
	}
	if streak < 1 {
		streak = 1
	}
	if streak > len(d) {
		streak = len(d)
	}
	return time.Duration(d[streak-1]) * time.Second
}

// EndpointSettings are the retry settings; the defaults until saved, or
// when what is stored no longer validates.
func (s *Store) EndpointSettings(ctx context.Context) (EndpointSettings, error) {
	v := DefaultEndpointSettings()
	if _, err := s.getSetting(ctx, "endpoints", &v); err != nil {
		return DefaultEndpointSettings(), err
	}
	if v.Validate() != nil {
		return DefaultEndpointSettings(), nil
	}
	return v, nil
}

func (s *Store) SetEndpointSettings(ctx context.Context, v EndpointSettings) error {
	if err := v.Validate(); err != nil {
		return err
	}
	return s.putSetting(ctx, "endpoints", v)
}

func (s *Store) queryEndpoints(ctx context.Context, where string, args ...any) ([]Endpoint, error) {
	set, err := s.EndpointSettings(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, endpointSelect+" "+where+` ORDER BY p.priority, p.name, e.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Endpoint{}
	for rows.Next() {
		e, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		e.withRetries(set)
		out = append(out, *e)
	}
	return out, rows.Err()
}

// ListEndpoints lists one project's endpoints, or every endpoint for 0.
func (s *Store) ListEndpoints(ctx context.Context, projectID int64) ([]Endpoint, error) {
	if projectID == 0 {
		return s.queryEndpoints(ctx, "")
	}
	return s.queryEndpoints(ctx, "WHERE e.project_id = $1", projectID)
}

func (s *Store) DownEndpoints(ctx context.Context) ([]Endpoint, error) {
	return s.queryEndpoints(ctx, "WHERE e.enabled AND e.last_status = 'down' AND p.status <> 'archived'")
}

// WarningEndpoints are the ones failing but still being rechecked.
func (s *Store) WarningEndpoints(ctx context.Context) ([]Endpoint, error) {
	return s.queryEndpoints(ctx, "WHERE e.enabled AND e.last_status = 'warning' AND p.status <> 'archived'")
}

func (s *Store) EnabledEndpoints(ctx context.Context) ([]Endpoint, error) {
	return s.queryEndpoints(ctx, "WHERE e.enabled AND p.status <> 'archived'")
}

func (s *Store) EndpointByID(ctx context.Context, id int64) (*Endpoint, error) {
	set, err := s.EndpointSettings(ctx)
	if err != nil {
		return nil, err
	}
	e, err := scanEndpoint(s.DB.QueryRow(ctx, endpointSelect+` WHERE e.id = $1`, id))
	if err != nil {
		return nil, err
	}
	e.withRetries(set)
	return e, nil
}

type EndpointInput struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	Kind         string `json:"kind"`
	ExpectStatus int    `json:"expect_status"`
	Enabled      *bool  `json:"enabled"`
}

func validURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (s *Store) CreateEndpoint(ctx context.Context, projectKey string, in EndpointInput) (*Endpoint, error) {
	in.Name, in.URL = strings.TrimSpace(in.Name), strings.TrimSpace(in.URL)
	if in.Name == "" || len(in.Name) > 100 {
		return nil, invalid("name", "must be 1-100 characters")
	}
	if !validURL(in.URL) {
		return nil, invalid("url", "must be an http(s) URL")
	}
	if in.Kind == "" {
		in.Kind = "web"
	}
	if !OneOf(in.Kind, EndpointKinds) {
		return nil, invalid("kind", "must be one of %s", strings.Join(EndpointKinds, ", "))
	}
	if in.ExpectStatus == 0 {
		in.ExpectStatus = 200
	}
	if in.ExpectStatus < 100 || in.ExpectStatus > 599 {
		return nil, invalid("expect_status", "must be an HTTP status code")
	}
	enabled := in.Enabled == nil || *in.Enabled
	pid, err := s.projectID(ctx, s.DB, projectKey)
	if err != nil {
		return nil, err
	}
	var id int64
	if err := s.DB.QueryRow(ctx, `
		INSERT INTO endpoints (project_id, name, url, kind, expect_status, enabled)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		pid, in.Name, in.URL, in.Kind, in.ExpectStatus, enabled).Scan(&id); err != nil {
		return nil, mapErr(err)
	}
	return s.EndpointByID(ctx, id)
}

func pURL(key string, raw json.RawMessage) (any, error) {
	v, err := pString(1000, true)(key, raw)
	if err != nil {
		return nil, err
	}
	if !validURL(v.(string)) {
		return nil, invalid(key, "must be an http(s) URL")
	}
	return v, nil
}

var endpointFields = map[string]field{
	"name":          {"name", pString(100, true)},
	"url":           {"url", pURL},
	"kind":          {"kind", pEnum(EndpointKinds)},
	"expect_status": {"expect_status", pInt(100, 599)},
	"enabled":       {"enabled", pBool},
}

func (s *Store) UpdateEndpoint(ctx context.Context, id int64, p Patch) (*Endpoint, error) {
	set, args, err := updateSet(p, endpointFields, []string{"id", "project_id", "project_key", "project_name",
		"project_color", "last_status", "last_code", "last_latency_ms", "last_error", "last_checked_at",
		"last_change_at", "uptime_24h", "fail_streak", "retry_limit", "next_check_at"}, 2)
	if err != nil {
		return nil, err
	}
	if set != "" {
		// A new URL or expectation invalidates the last verdict.
		if p.Has("url") || p.Has("expect_status") {
			set += ", last_status = 'unknown', fail_streak = 0"
		}
		tag, err := s.DB.Exec(ctx, `UPDATE endpoints SET `+set+`, updated_at = now() WHERE id = $1`,
			append([]any{id}, args...)...)
		if err != nil {
			return nil, mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return nil, ErrNotFound
		}
	}
	return s.EndpointByID(ctx, id)
}

func (s *Store) DeleteEndpoint(ctx context.Context, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM endpoints WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CheckResult is one probe of an endpoint.
type CheckResult struct {
	OK        bool
	Code      *int
	LatencyMS *int
	Error     string
}

// RecordCheck stores a probe and moves the endpoint's verdict: a success
// is "up"; a failure is "warning" until the configured rechecks in a row
// have failed as well, then "down". It returns the new status and failure
// streak, so the caller can schedule the recheck. Only confirmed changes
// (→ down, down → up) land in the activity feed; a warning that clears is
// not news.
func (s *Store) RecordCheck(ctx context.Context, e Endpoint, r CheckResult) (status string, streak int, err error) {
	set, err := s.EndpointSettings(ctx)
	if err != nil {
		return "", 0, err
	}
	var prev string
	err = s.tx(ctx, func(tx pgxTx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO endpoint_checks (endpoint_id, ok, code, latency_ms, error)
			VALUES ($1, $2, $3, $4, $5)`, e.ID, r.OK, r.Code, r.LatencyMS, truncate(r.Error, 500)); err != nil {
			return err
		}
		var prevStreak int
		if err := tx.QueryRow(ctx, `SELECT last_status, fail_streak FROM endpoints WHERE id = $1`, e.ID).
			Scan(&prev, &prevStreak); err != nil {
			return mapErr(err)
		}
		status, streak = nextEndpointState(prev, prevStreak, r.OK, set.Retries)
		if _, err := tx.Exec(ctx, `
			UPDATE endpoints SET last_status = $2, fail_streak = $3, last_code = $4, last_latency_ms = $5,
			       last_error = $6, last_checked_at = now(),
			       last_change_at = CASE WHEN last_status <> $2 THEN now() ELSE last_change_at END
			WHERE id = $1`, e.ID, status, streak, r.Code, r.LatencyMS, truncate(r.Error, 500)); err != nil {
			return mapErr(err)
		}
		var summary string
		switch {
		case status == "down" && prev != "down":
			summary = fmt.Sprintf("%s is down", e.Name)
			if r.Error != "" {
				summary += ": " + truncate(r.Error, 120)
			} else if r.Code != nil {
				summary += fmt.Sprintf(" (HTTP %d)", *r.Code)
			}
			if streak > 1 {
				summary += fmt.Sprintf(" — %d checks in a row failed", streak)
			}
		case status == "up" && prev == "down":
			summary = fmt.Sprintf("%s is back up", e.Name)
		default:
			return nil
		}
		return logActivity(ctx, tx, ActivityInput{ProjectID: &e.ProjectID, Kind: "endpoint." + status, Summary: summary})
	})
	if err == nil && status != prev && (status == "down" || prev == "down") {
		s.MarkFocusDirty(ctx, e.ProjectID, "endpoint "+e.Name+" is "+status) // a confirmed change moves the blockers
	}
	return status, streak, err
}

// nextEndpointState is the verdict state machine: up/unknown/warning --fail-->
// warning (streak+1) until the streak exceeds retries, then down; any
// success --> up with the streak reset. A down endpoint stays down until it
// answers.
func nextEndpointState(prev string, prevStreak int, ok bool, retries int) (string, int) {
	if ok {
		return "up", 0
	}
	streak := prevStreak + 1
	if prev == "up" || prev == "unknown" {
		streak = 1 // a stale streak from before a reset does not count
	}
	if prev == "down" || streak > retries {
		return "down", streak
	}
	return "warning", streak
}

func (s *Store) EndpointChecks(ctx context.Context, id int64, hours int) ([]EndpointCheck, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT at, ok, code, latency_ms, error FROM endpoint_checks
		WHERE endpoint_id = $1 AND at > ts_add(now(), -$2 * 3600)
		ORDER BY at DESC LIMIT 2000`, id, float64(hours))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EndpointCheck{}
	for rows.Next() {
		var c EndpointCheck
		if err := rows.Scan(&c.At, &c.OK, &c.Code, &c.LatencyMS, &c.Error); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) PruneChecks(ctx context.Context, keep time.Duration) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM endpoint_checks WHERE at < ts_add(now(), -$1)`,
		keep.Seconds())
	return err
}
