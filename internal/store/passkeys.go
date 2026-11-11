package store

import (
	"context"
	"crypto/rand"
	"strings"
	"time"
)

// Passkey is a WebAuthn credential an account signs in with. Credential is
// the verifier's record of it (JSON, opaque here); Synced says the
// authenticator backs it up (iCloud Keychain, Google Password Manager…), as
// the API works out from that record.
type Passkey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	Synced     bool       `json:"synced"`

	UserID       int64  `json:"-"`
	CredentialID []byte `json:"-"`
	Credential   string `json:"-"`
}

const passkeyCols = `id, user_id, credential_id, name, credential, created_at, last_used_at`

func scanPasskey(row interface{ Scan(...any) error }) (*Passkey, error) {
	var p Passkey
	if err := row.Scan(&p.ID, &p.UserID, &p.CredentialID, &p.Name, &p.Credential, &p.CreatedAt, &p.LastUsedAt); err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}

// WebAuthnID is the account's WebAuthn user handle, made (64 random bytes)
// the first time it is asked for.
func (s *Store) WebAuthnID(ctx context.Context, userID int64) ([]byte, error) {
	var id []byte
	if err := s.DB.QueryRow(ctx, `SELECT webauthn_id FROM users WHERE id = $1`, userID).Scan(&id); err != nil {
		return nil, mapErr(err)
	}
	if len(id) > 0 {
		return id, nil
	}
	id = make([]byte, 64)
	_, _ = rand.Read(id)
	if _, err := s.DB.Exec(ctx, `UPDATE users SET webauthn_id = $2 WHERE id = $1 AND webauthn_id IS NULL`, userID, id); err != nil {
		return nil, err
	}
	// Another request may have won the race: read back what is stored.
	err := s.DB.QueryRow(ctx, `SELECT webauthn_id FROM users WHERE id = $1`, userID).Scan(&id)
	return id, mapErr(err)
}

// UserByWebAuthnID finds the account a passkey's user handle names.
func (s *Store) UserByWebAuthnID(ctx context.Context, handle []byte) (*User, error) {
	if len(handle) == 0 {
		return nil, ErrNotFound
	}
	return scanUser(s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE webauthn_id = $1`, handle))
}

// Passkeys lists an account's passkeys, newest first.
func (s *Store) Passkeys(ctx context.Context, userID int64) ([]Passkey, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+passkeyCols+` FROM passkeys WHERE user_id = $1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Passkey{}
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) CountPasskeys(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM passkeys WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

func (s *Store) PasskeyByCredentialID(ctx context.Context, credentialID []byte) (*Passkey, error) {
	return scanPasskey(s.DB.QueryRow(ctx, `SELECT `+passkeyCols+` FROM passkeys WHERE credential_id = $1`, credentialID))
}

// CreatePasskey stores a newly registered passkey. A credential ID already
// on file (on any account) is a conflict.
func (s *Store) CreatePasskey(ctx context.Context, userID int64, credentialID []byte, name, credential string) (*Passkey, error) {
	return scanPasskey(s.DB.QueryRow(ctx, `
		INSERT INTO passkeys (user_id, credential_id, name, credential) VALUES ($1, $2, $3, $4)
		RETURNING `+passkeyCols, userID, credentialID, PasskeyName(name), credential))
}

// UsePasskey records a sign-in with the passkey and its updated record (the
// sign count moves on).
func (s *Store) UsePasskey(ctx context.Context, id int64, credential string) error {
	_, err := s.DB.Exec(ctx, `UPDATE passkeys SET credential = $2, last_used_at = now() WHERE id = $1`, id, credential)
	return err
}

func (s *Store) RenamePasskey(ctx context.Context, userID, id int64, name string) (*Passkey, error) {
	return scanPasskey(s.DB.QueryRow(ctx, `UPDATE passkeys SET name = $3 WHERE id = $1 AND user_id = $2
		RETURNING `+passkeyCols, id, userID, PasskeyName(name)))
}

func (s *Store) DeletePasskey(ctx context.Context, userID, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM passkeys WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePasskeys removes every passkey of an account (the admin's and the
// command line's way back in for an account whose passkeys are lost).
func (s *Store) DeletePasskeys(ctx context.Context, userID int64) (int, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM passkeys WHERE user_id = $1`, userID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// PasskeyName trims a passkey's label to something a list can show.
func PasskeyName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Passkey"
	}
	return truncate(name, 60)
}
