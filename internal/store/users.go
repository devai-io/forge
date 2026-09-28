package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const userCols = `id, username, email, display_name, timezone, weekly_goal, accent, totp_enabled, checkup_time,
	checkup_email, created_at, password_hash, totp_secret, totp_pending, totp_last_counter`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.Timezone, &u.WeeklyGoal, &u.Accent, &u.TOTPEnabled,
		&u.CheckupTime, &u.CheckupEmail, &u.CreatedAt, &u.PasswordHash, &u.TOTPSecret, &u.TOTPPending,
		&u.TOTPLastCounter); err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser adds an account. generated says the password was made up by
// Forge (printed on the command line) rather than chosen by its owner.
func (s *Store) CreateUser(ctx context.Context, username, email, displayName, passwordHash, timezone string, generated bool) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `
		INSERT INTO users (username, email, display_name, password_hash, timezone, password_generated)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+userCols,
		username, email, displayName, passwordHash, timezone, generated))
}

// CreateFirstUser is CreateUser for the setup page: it only succeeds while
// there is no account at all, so two racing setups cannot both win.
func (s *Store) CreateFirstUser(ctx context.Context, username, email, displayName, passwordHash, timezone string) (*User, error) {
	var u *User
	err := s.tx(ctx, func(tx pgxTx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: an account already exists", ErrConflict)
		}
		var err error
		u, err = scanUser(tx.QueryRow(ctx, `
			INSERT INTO users (username, email, display_name, password_hash, timezone)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING `+userCols,
			username, email, displayName, passwordHash, timezone))
		return err
	})
	return u, err
}

// UserByLogin finds an account by username or e-mail, case-insensitively.
func (s *Store) UserByLogin(ctx context.Context, login string) (*User, error) {
	login = strings.TrimSpace(login)
	return scanUser(s.DB.QueryRow(ctx, `
		SELECT `+userCols+` FROM users
		WHERE lower(username) = lower($1) OR lower(email) = lower($1)
		LIMIT 1`, login))
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

var userFields = map[string]field{
	"email":         {"email", pString(254, true)},
	"accent":        {"accent", pAccent},
	"display_name":  {"display_name", pString(100, false)},
	"timezone":      {"timezone", pString(64, true)},
	"weekly_goal":   {"weekly_goal", pInt(1, 500)},
	"checkup_time":  {"checkup_time", pClock},
	"checkup_email": {"checkup_email", pBool},
}

func (s *Store) UpdateUser(ctx context.Context, id int64, p Patch) (*User, error) {
	set, args, err := updateSet(p, userFields, []string{"id", "username", "created_at", "totp_enabled"}, 2)
	if err != nil {
		return nil, err
	}
	if set == "" {
		return s.UserByID(ctx, id)
	}
	return scanUser(s.DB.QueryRow(ctx,
		`UPDATE users SET `+set+`, updated_at = now() WHERE id = $1 RETURNING `+userCols,
		append([]any{id}, args...)...))
}

// SetPassword replaces the hash and revokes every session except keepSession
// (0 revokes them all) — a changed password should end whatever the old one
// let in. generated marks a password Forge made up (reset on the command
// line), which the check-up then asks to change.
func (s *Store) SetPassword(ctx context.Context, userID int64, hash string, keepSession int64, generated bool) error {
	return s.tx(ctx, func(tx pgxTx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, password_generated = $3, password_changed_at = now(),
			updated_at = now() WHERE id = $1`, userID, hash, generated)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND id <> $2`, userID, keepSession)
		return err
	})
}

// ── Sessions ──────────────────────────────────────────────────────────────

func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash []byte, userAgent, ip string, ttl time.Duration) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, user_agent, ip, expires_at)
		VALUES ($1, $2, $3, $4, ts_add(now(), $5))
		RETURNING id`,
		userID, tokenHash, truncate(userAgent, 300), ip, ttl.Seconds()).Scan(&id)
	return id, err
}

// SessionUser resolves a cookie to its user and slides the expiry forward.
// The touch is throttled to once a minute so every request is not a write.
// Sliding stops at 90 days after sign-in: a cookie is not forever.
func (s *Store) SessionUser(ctx context.Context, tokenHash []byte, ttl time.Duration) (*User, *SessionInfo, error) {
	var info SessionInfo
	var uid int64
	var lastSeen time.Time
	err := s.DB.QueryRow(ctx, `
		SELECT id, user_id, elevated_until, last_seen_at FROM sessions
		WHERE token_hash = $1 AND expires_at > now() AND created_at > ts_add(now(), -7776000)`,
		tokenHash).Scan(&info.ID, &uid, &info.ElevatedUntil, &lastSeen)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	if time.Since(lastSeen) > time.Minute {
		if _, err := s.DB.Exec(ctx, `UPDATE sessions SET last_seen_at = now(), expires_at = ts_add(now(), $2) WHERE id = $1`,
			info.ID, ttl.Seconds()); err != nil {
			return nil, nil, err
		}
	}
	u, err := s.UserByID(ctx, uid)
	if err != nil {
		return nil, nil, err
	}
	return u, &info, nil
}

// SessionInfo is what a request knows about its own session.
type SessionInfo struct {
	ID            int64
	ElevatedUntil *time.Time
}

// Elevated reports whether the session re-confirmed the password recently.
func (i *SessionInfo) Elevated() bool {
	return i != nil && i.ElevatedUntil != nil && i.ElevatedUntil.After(time.Now())
}

func (s *Store) ElevateSession(ctx context.Context, sessionID int64, d time.Duration) (time.Time, error) {
	var until time.Time
	err := s.DB.QueryRow(ctx, `UPDATE sessions SET elevated_until = ts_add(now(), $2)
		WHERE id = $1 RETURNING elevated_until`, sessionID, d.Seconds()).Scan(&until)
	return until, mapErr(err)
}

// ── Two-factor ────────────────────────────────────────────────────────────

func (s *Store) SetTOTPPending(ctx context.Context, userID int64, sealed []byte) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET totp_pending = $2, updated_at = now() WHERE id = $1`, userID, sealed)
	return err
}

// EnableTOTP promotes the pending secret and records the counter of the code
// that proved it, so that code cannot be used again to sign in.
func (s *Store) EnableTOTP(ctx context.Context, userID, counter int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET totp_secret = totp_pending, totp_pending = NULL, totp_enabled = true,
		totp_last_counter = $2, updated_at = now() WHERE id = $1 AND totp_pending IS NOT NULL`, userID, counter)
	return err
}

func (s *Store) DisableTOTP(ctx context.Context, userID int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET totp_secret = NULL, totp_pending = NULL, totp_enabled = false,
		totp_last_counter = 0, updated_at = now() WHERE id = $1`, userID)
	return err
}

// UseTOTPCounter records a used code's counter; it fails if a code at or after
// that step was already used (replay, or a race between two logins).
func (s *Store) UseTOTPCounter(ctx context.Context, userID, counter int64) (bool, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE users SET totp_last_counter = $2 WHERE id = $1 AND totp_last_counter < $2`,
		userID, counter)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) DeleteSessionByToken(ctx context.Context, tokenHash []byte) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, userID, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListSessions(ctx context.Context, userID, currentID int64) ([]Session, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, user_agent, ip, created_at, last_seen_at, expires_at
		FROM sessions WHERE user_id = $1 AND expires_at > now()
		ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var x Session
		if err := rows.Scan(&x.ID, &x.UserAgent, &x.IP, &x.CreatedAt, &x.LastSeenAt, &x.ExpiresAt); err != nil {
			return nil, err
		}
		x.Current = x.ID == currentID
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) PruneSessions(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now() OR created_at < ts_add(now(), -7776000)`)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `DELETE FROM password_resets WHERE expires_at < ts_add(now(), -86400)`)
	return err
}

// ── Password resets ───────────────────────────────────────────────────────

func (s *Store) CreatePasswordReset(ctx context.Context, userID int64, tokenHash []byte, ttl time.Duration) error {
	// One live link at a time: asking again invalidates the previous e-mail.
	return s.tx(ctx, func(tx pgxTx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM password_resets WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO password_resets (user_id, token_hash, expires_at)
			VALUES ($1, $2, ts_add(now(), $3))`, userID, tokenHash, ttl.Seconds())
		return err
	})
}

// ConsumePasswordReset marks a live token used and returns its user; a token
// works exactly once, and only before it expires.
func (s *Store) ConsumePasswordReset(ctx context.Context, tokenHash []byte) (int64, error) {
	var userID int64
	err := s.DB.QueryRow(ctx, `
		UPDATE password_resets SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING user_id`, tokenHash).Scan(&userID)
	return userID, mapErr(err)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
