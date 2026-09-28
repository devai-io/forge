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
       e.last_status, e.last_code, e.last_latency_ms, e.last_error, e.last_checked_at, e.last_change_at,
       (SELECT avg(CASE WHEN c.ok THEN 1.0 ELSE 0.0 END)::float8
          FROM endpoint_checks c WHERE c.endpoint_id = e.id AND c.at > now() - interval '24 hours')
FROM endpoints e JOIN projects p ON p.id = e.project_id`

func scanEndpoint(row interface{ Scan(...any) error }) (*Endpoint, error) {
	var e Endpoint
	if err := row.Scan(&e.ID, &e.ProjectID, &e.ProjectKey, &e.ProjectName, &e.ProjectColor, &e.Name, &e.URL,
		&e.Kind, &e.ExpectStatus, &e.Enabled, &e.LastStatus, &e.LastCode, &e.LastLatencyMS, &e.LastError,
		&e.LastCheckedAt, &e.LastChangeAt, &e.Uptime24h); err != nil {
		return nil, mapErr(err)
	}
	return &e, nil
}

func (s *Store) queryEndpoints(ctx context.Context, where string, args ...any) ([]Endpoint, error) {
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

func (s *Store) EnabledEndpoints(ctx context.Context) ([]Endpoint, error) {
	return s.queryEndpoints(ctx, "WHERE e.enabled AND p.status <> 'archived'")
}

func (s *Store) EndpointByID(ctx context.Context, id int64) (*Endpoint, error) {
	return scanEndpoint(s.DB.QueryRow(ctx, endpointSelect+` WHERE e.id = $1`, id))
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
		"last_change_at", "uptime_24h"}, 2)
	if err != nil {
		return nil, err
	}
	if set != "" {
		// A new URL or expectation invalidates the last verdict.
		if p.Has("url") || p.Has("expect_status") {
			set += ", last_status = 'unknown'"
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

// RecordCheck stores a probe and moves the endpoint's verdict. A change of
// verdict (up → down or back) lands in the activity feed; "unknown" → up is
// not news and stays out of it.
func (s *Store) RecordCheck(ctx context.Context, e Endpoint, r CheckResult) error {
	status := "down"
	if r.OK {
		status = "up"
	}
	return s.tx(ctx, func(tx pgxTx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO endpoint_checks (endpoint_id, ok, code, latency_ms, error)
			VALUES ($1, $2, $3, $4, $5)`, e.ID, r.OK, r.Code, r.LatencyMS, truncate(r.Error, 500)); err != nil {
			return err
		}
		var prev string
		err := tx.QueryRow(ctx, `
			UPDATE endpoints e SET last_status = $2, last_code = $3, last_latency_ms = $4, last_error = $5,
			       last_checked_at = now(),
			       last_change_at = CASE WHEN e.last_status <> $2 THEN now() ELSE e.last_change_at END
			FROM (SELECT id, last_status FROM endpoints WHERE id = $1 FOR UPDATE) old
			WHERE e.id = old.id
			RETURNING old.last_status`, e.ID, status, r.Code, r.LatencyMS, truncate(r.Error, 500)).Scan(&prev)
		if err != nil {
			return mapErr(err)
		}
		if prev == status || (prev == "unknown" && status == "up") {
			return nil
		}
		summary := fmt.Sprintf("%s is down", e.Name)
		if r.OK {
			summary = fmt.Sprintf("%s is back up", e.Name)
		} else if r.Error != "" {
			summary += ": " + truncate(r.Error, 120)
		} else if r.Code != nil {
			summary += fmt.Sprintf(" (HTTP %d)", *r.Code)
		}
		return logActivity(ctx, tx, ActivityInput{ProjectID: &e.ProjectID, Kind: "endpoint." + status, Summary: summary})
	})
}

func (s *Store) EndpointChecks(ctx context.Context, id int64, hours int) ([]EndpointCheck, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT at, ok, code, latency_ms, error FROM endpoint_checks
		WHERE endpoint_id = $1 AND at > now() - $2::float8 * interval '1 hour'
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
	_, err := s.DB.Exec(ctx, `DELETE FROM endpoint_checks WHERE at < now() - $1::float8 * interval '1 second'`,
		keep.Seconds())
	return err
}
