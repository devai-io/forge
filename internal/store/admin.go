package store

import (
	"context"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

// Admin: the owner's view of the accounts — who exists, what each may do
// (role, projects, a member's agent) — and the changes an owner makes to
// them. The command line (`forge create-member`, `grant`, `member-agent`, …)
// does the same things through the same functions.

// AdminUser is an account as the Admin page lists it.
type AdminUser struct {
	ID                int64        `json:"id"`
	Username          string       `json:"username"`
	Email             string       `json:"email"`
	DisplayName       string       `json:"display_name"`
	Role              string       `json:"role"`
	TOTPEnabled       bool         `json:"totp_enabled"`
	Passkeys          int          `json:"passkeys"`           // how many passkeys sign it in
	PasswordGenerated bool         `json:"password_generated"` // still the one Forge made up
	CreatedAt         time.Time    `json:"created_at"`
	LastSeenAt        *time.Time   `json:"last_seen_at"` // its most recent session's last request
	Sessions          int          `json:"sessions"`
	Projects          []string     `json:"projects"` // a member's granted project keys
	Agent             *MemberAgent `json:"agent"`    // a member's own agent, if it has one
}

func (s *Store) ListAdminUsers(ctx context.Context) ([]AdminUser, error) {
	rows, err := s.DB.Query(ctx, `SELECT u.id, u.username, u.email, u.display_name, u.role, u.totp_enabled, u.password_generated,
		u.created_at, (SELECT max(last_seen_at) FROM sessions se WHERE se.user_id = u.id AND se.expires_at > now()),
		(SELECT count(*) FROM sessions se WHERE se.user_id = u.id AND se.expires_at > now()),
		(SELECT count(*) FROM passkeys p WHERE p.user_id = u.id)
		FROM users u ORDER BY (u.role = 'owner') DESC, lower(u.username)`)
	if err != nil {
		return nil, err
	}
	out := []AdminUser{}
	for rows.Next() {
		var u AdminUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.Role, &u.TOTPEnabled, &u.PasswordGenerated,
			&u.CreatedAt, &u.LastSeenAt, &u.Sessions, &u.Passkeys); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Projects, err = s.MemberProjectKeys(ctx, out[i].ID); err != nil {
			return nil, err
		}
		if a, err := s.MemberAgent(ctx, out[i].ID); err == nil {
			out[i].Agent = a
		}
	}
	return out, nil
}

// AdminUserByID is one account as the Admin page shows it.
func (s *Store) AdminUserByID(ctx context.Context, id int64) (*AdminUser, error) {
	all, err := s.ListAdminUsers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, ErrNotFound
}

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,31}$`)

// ValidUsername: 2–32 of a–z, 0–9, dot, dash and underscore, starting with a
// letter or digit.
func ValidUsername(name string) bool { return usernameRe.MatchString(name) }

func validEmail(email string) bool {
	a, err := mail.ParseAddress(email)
	return err == nil && a.Address == email && len(email) <= 254
}

// CreateAccount adds an account with a password Forge made up (its hash is
// passed in), as an owner or as a member of the given projects.
func (s *Store) CreateAccount(ctx context.Context, username, email, displayName, role, passwordHash, timezone string, projects []string) (*AdminUser, error) {
	username, email, displayName = strings.ToLower(strings.TrimSpace(username)), strings.TrimSpace(email), strings.TrimSpace(displayName)
	if !ValidUsername(username) {
		return nil, invalid("username", "2–32 characters: a–z, 0–9, dot, dash, underscore")
	}
	if !validEmail(email) {
		return nil, invalid("email", "must be a plain e-mail address")
	}
	if !OneOf(role, []string{RoleOwner, RoleMember}) {
		return nil, invalid("role", "must be owner or member")
	}
	if displayName == "" {
		displayName = username
	}
	if len(displayName) > 100 {
		return nil, invalid("display_name", "at most 100 characters")
	}
	if _, err := s.UserByLogin(ctx, username); err == nil {
		return nil, invalid("username", "that username is taken")
	}
	if _, err := s.UserByLogin(ctx, email); err == nil {
		return nil, invalid("email", "an account with that e-mail exists")
	}
	u, err := s.CreateUser(ctx, username, email, displayName, passwordHash, timezone, true)
	if err != nil {
		return nil, err
	}
	if err := s.SetUserRole(ctx, u.ID, role); err != nil {
		return nil, err
	}
	if role == RoleMember {
		if err := s.SetMemberProjects(ctx, u.ID, projects); err != nil {
			return nil, err
		}
	}
	return s.AdminUserByID(ctx, u.ID)
}

// SetMemberProjects makes keys the projects a member may work in.
func (s *Store) SetMemberProjects(ctx context.Context, userID int64, keys []string) error {
	return s.tx(ctx, func(tx pgxTx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM project_members WHERE user_id = $1`, userID); err != nil {
			return err
		}
		for _, k := range keys {
			tag, err := tx.Exec(ctx, `INSERT OR IGNORE INTO project_members (user_id, project_id)
				SELECT $1, id FROM projects WHERE key = $2`, userID, strings.ToUpper(strings.TrimSpace(k)))
			if err != nil {
				return mapErr(err)
			}
			if tag.RowsAffected() == 0 {
				var n int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM projects WHERE key = $1`, strings.ToUpper(strings.TrimSpace(k))).Scan(&n); err != nil || n == 0 {
					return invalid("projects", "no project %s", k)
				}
			}
		}
		return nil
	})
}

// AdminUserPatch is what an owner may change on an account (nil: leave it).
type AdminUserPatch struct {
	DisplayName *string
	Email       *string
	Role        *string
	Projects    *[]string
}

// CountOwners is how many owner accounts there are.
func (s *Store) CountOwners(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'owner'`).Scan(&n)
	return n, err
}

// UpdateAccount applies an owner's changes to an account. The last owner
// stays an owner; a member who becomes an owner loses its member agent (an
// owner uses Agents).
func (s *Store) UpdateAccount(ctx context.Context, id int64, p AdminUserPatch) (*AdminUser, error) {
	u, err := s.UserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.DisplayName != nil {
		name := strings.TrimSpace(*p.DisplayName)
		if name == "" || len(name) > 100 {
			return nil, invalid("display_name", "1–100 characters")
		}
		if _, err := s.DB.Exec(ctx, `UPDATE users SET display_name = $2, updated_at = now() WHERE id = $1`, id, name); err != nil {
			return nil, mapErr(err)
		}
	}
	if p.Email != nil {
		email := strings.TrimSpace(*p.Email)
		if !validEmail(email) {
			return nil, invalid("email", "must be a plain e-mail address")
		}
		if other, err := s.UserByLogin(ctx, email); err == nil && other.ID != id {
			return nil, invalid("email", "an account with that e-mail exists")
		}
		if _, err := s.DB.Exec(ctx, `UPDATE users SET email = $2, updated_at = now() WHERE id = $1`, id, email); err != nil {
			return nil, mapErr(err)
		}
	}
	role := u.Role
	if p.Role != nil && *p.Role != u.Role {
		if !OneOf(*p.Role, []string{RoleOwner, RoleMember}) {
			return nil, invalid("role", "must be owner or member")
		}
		if u.Role == RoleOwner {
			if n, err := s.CountOwners(ctx); err != nil || n <= 1 {
				return nil, invalid("role", "the last owner stays an owner")
			}
		}
		if err := s.SetUserRole(ctx, id, *p.Role); err != nil {
			return nil, err
		}
		role = *p.Role
		if role == RoleOwner {
			if err := s.ClearMemberAgent(ctx, id); err != nil {
				return nil, err
			}
		}
	}
	if p.Projects != nil && role == RoleMember {
		if err := s.SetMemberProjects(ctx, id, *p.Projects); err != nil {
			return nil, err
		}
	}
	return s.AdminUserByID(ctx, id)
}

// DeleteAccount removes an account with its sessions, grants and agent; the
// runs it started stay (without a starter). The last owner cannot go.
func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	u, err := s.UserByID(ctx, id)
	if err != nil {
		return err
	}
	if u.Role == RoleOwner {
		if n, err := s.CountOwners(ctx); err != nil || n <= 1 {
			return invalid("id", "the last owner cannot be deleted")
		}
	}
	_, err = s.DB.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	return mapErr(err)
}

// SetRunnerMembersAllowedByID opens (or closes) a machine to every member.
func (s *Store) SetRunnerMembersAllowedByID(ctx context.Context, id int64, allowed bool) error {
	tag, err := s.DB.Exec(ctx, `UPDATE runners SET members_allowed = $2 WHERE id = $1`, id, allowed)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
