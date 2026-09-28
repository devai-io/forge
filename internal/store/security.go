package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The security log: every sign-in and every sensitive action, so "what
// happened on my account" is answered from the app rather than from container
// logs that rotate.

type SecurityEvent struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Detail    string    `json:"detail"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	SessionID *int64    `json:"session_id"`
	At        time.Time `json:"at"`
}

func (s *Store) LogSecurity(ctx context.Context, kind, detail, ip, userAgent string, sessionID int64) error {
	var sid *int64
	if sessionID > 0 {
		sid = &sessionID
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO security_events (kind, detail, ip, user_agent, session_id)
		VALUES ($1, $2, $3, $4, $5)`, kind, truncate(detail, 500), truncate(ip, 64), truncate(userAgent, 300), sid)
	return err
}

func (s *Store) ListSecurity(ctx context.Context, kind string, limit int) ([]SecurityEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, kind, detail, ip, user_agent, session_id, at FROM security_events`
	args := []any{limit}
	if kind != "" {
		q += ` WHERE kind = $2`
		args = append(args, kind)
	}
	rows, err := s.DB.Query(ctx, q+` ORDER BY at DESC, id DESC LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecurityEvent{}
	for rows.Next() {
		var e SecurityEvent
		if err := rows.Scan(&e.ID, &e.Kind, &e.Detail, &e.IP, &e.UserAgent, &e.SessionID, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// deviceKey reduces a user agent to the part that identifies the browser and
// platform, so a browser update is not "a new device" while a new browser or
// a new machine is.
func deviceKey(userAgent string) string {
	ua := strings.ToLower(userAgent)
	browser := "other"
	for _, b := range []string{"edg", "opr", "firefox", "chrome", "safari", "curl"} {
		if strings.Contains(ua, b) {
			browser = b
			break
		}
	}
	platform := "other"
	for _, p := range []string{"iphone", "ipad", "android", "macintosh", "windows", "linux"} {
		if strings.Contains(ua, p) {
			platform = p
			break
		}
	}
	return browser + "/" + platform
}

// SeenDevice records a sign-in and reports whether that address+browser was
// already known (within the last 90 days).
func (s *Store) SeenDevice(ctx context.Context, ip, userAgent string) (known bool, err error) {
	key := deviceKey(userAgent)
	ip = truncate(ip, 64)
	if err = s.DB.QueryRow(ctx, `SELECT exists(SELECT 1 FROM known_devices
		WHERE ip = $1 AND user_agent = $2 AND last_seen > ts_add(now(), -7776000))`, ip, key).Scan(&known); err != nil {
		return false, err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO known_devices (ip, user_agent) VALUES ($1, $2)
		ON CONFLICT (ip, user_agent) DO UPDATE SET last_seen = now()`, ip, key)
	return known, err
}

// NewDeviceLogins counts sign-ins from previously unknown devices in a window.
func (s *Store) NewDeviceLogins(ctx context.Context, since time.Duration) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM security_events
		WHERE kind = 'login_new_device' AND at > ts_add(now(), -$1)`, since.Seconds()).Scan(&n)
	return n, err
}

// RevokeOtherSessions ends every session but the current one.
func (s *Store) RevokeOtherSessions(ctx context.Context, userID, keep int64) (int, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND id <> $2`, userID, keep)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// InitialPasswordUnchanged is true while the password in use is one Forge
// generated and printed (create-user / reset-password on the command line).
func (s *Store) InitialPasswordUnchanged(ctx context.Context, userID int64) (bool, error) {
	var unchanged bool
	err := s.DB.QueryRow(ctx, `SELECT password_generated FROM users WHERE id = $1`,
		userID).Scan(&unchanged)
	return unchanged, err
}

// ── System facts (the Documentation page) ─────────────────────────────────

type SystemCounts struct {
	Projects        int `json:"projects"`
	Repos           int `json:"repos"`
	ReposScanned    int `json:"repos_scanned"`
	Servers         int `json:"servers"`
	Endpoints       int `json:"endpoints"`
	TasksOpen       int `json:"tasks_open"`
	TasksDone       int `json:"tasks_done"`
	VaultItems      int `json:"vault_items"`
	VaultWithValues int `json:"vault_with_values"`
	Checkups        int `json:"checkups"`
	Runs            int `json:"runs"`
	SecurityEvents  int `json:"security_events"`
}

func (s *Store) SystemCounts(ctx context.Context) (SystemCounts, error) {
	var c SystemCounts
	err := s.DB.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM projects WHERE status <> 'archived'),
		(SELECT count(*) FROM repos), (SELECT count(*) FROM repos WHERE git IS NOT NULL),
		(SELECT count(*) FROM servers), (SELECT count(*) FROM endpoints WHERE enabled),
		(SELECT count(*) FROM tasks WHERE status <> 'done'), (SELECT count(*) FROM tasks WHERE status = 'done'),
		(SELECT count(*) FROM vault_items), (SELECT count(*) FROM vault_items WHERE secret_sealed IS NOT NULL OR file_sealed IS NOT NULL),
		(SELECT count(*) FROM checkups WHERE finished_at IS NOT NULL), (SELECT count(*) FROM runs),
		(SELECT count(*) FROM security_events)`).Scan(&c.Projects, &c.Repos, &c.ReposScanned, &c.Servers, &c.Endpoints,
		&c.TasksOpen, &c.TasksDone, &c.VaultItems, &c.VaultWithValues, &c.Checkups, &c.Runs, &c.SecurityEvents)
	return c, err
}

func (s *Store) SeededAt(ctx context.Context) (*time.Time, error) {
	var t *time.Time
	err := s.DB.QueryRow(ctx, `SELECT min(created_at) FROM activity WHERE kind = 'forge.seeded'`).Scan(&t)
	return t, err
}

// NextCheckup is the next scheduled run: today at checkup_time in the user's
// zone if that is still ahead (and today's has not run), else tomorrow.
func NextCheckup(now time.Time, tz, hhmm string, ranToday bool) time.Time {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	n := now.In(loc)
	var h, m int
	_, _ = fmt.Sscanf(hhmm, "%d:%d", &h, &m)
	next := time.Date(n.Year(), n.Month(), n.Day(), h, m, 0, 0, loc)
	if !next.After(n) || ranToday {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
