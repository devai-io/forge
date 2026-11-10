package store

import (
	"context"
	"strings"
)

const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// MemberProjectKeys lists the project keys a member may work in (never nil,
// so it can be passed as an OnlyKeys filter).
func (s *Store) MemberProjectKeys(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT p.key FROM project_members m JOIN projects p ON p.id = m.project_id
		WHERE m.user_id = $1 ORDER BY p.key`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// IsProjectMember reports whether the user was granted the project (by id).
func (s *Store) IsProjectMember(ctx context.Context, userID, projectID int64) bool {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM project_members WHERE user_id = $1 AND project_id = $2`,
		userID, projectID).Scan(&n)
	return err == nil && n > 0
}

// IsProjectKeyMember is IsProjectMember by project key.
func (s *Store) IsProjectKeyMember(ctx context.Context, userID int64, key string) bool {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM project_members m JOIN projects p ON p.id = m.project_id
		WHERE m.user_id = $1 AND p.key = $2`, userID, strings.ToUpper(key)).Scan(&n)
	return err == nil && n > 0
}

// SetUserRole makes an account an owner or a member.
func (s *Store) SetUserRole(ctx context.Context, userID int64, role string) error {
	if !OneOf(role, []string{RoleOwner, RoleMember}) {
		return invalid("role", "must be owner or member")
	}
	tag, err := s.DB.Exec(ctx, `UPDATE users SET role = $2, updated_at = now() WHERE id = $1`, userID, role)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GrantProject gives a member access to a project (idempotent).
func (s *Store) GrantProject(ctx context.Context, userID int64, key string) error {
	tag, err := s.DB.Exec(ctx, `INSERT OR IGNORE INTO project_members (user_id, project_id)
		SELECT $1, id FROM projects WHERE key = $2`, userID, strings.ToUpper(key))
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 && !s.IsProjectKeyMember(ctx, userID, key) {
		return ErrNotFound
	}
	return nil
}

// RevokeProject removes a member's access to a project.
func (s *Store) RevokeProject(ctx context.Context, userID int64, key string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM project_members WHERE user_id = $1
		AND project_id = (SELECT id FROM projects WHERE key = $2)`, userID, strings.ToUpper(key))
	return mapErr(err)
}

// SetRunnerMembersAllowed opens (or closes) a machine to members' agent runs.
func (s *Store) SetRunnerMembersAllowed(ctx context.Context, name string, allowed bool) error {
	tag, err := s.DB.Exec(ctx, `UPDATE runners SET members_allowed = $2 WHERE name = $1`, name, allowed)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RunnerMembersAllowed reports whether members may run agents on the runner.
func (s *Store) RunnerMembersAllowed(ctx context.Context, runnerID int64) bool {
	var ok bool
	err := s.DB.QueryRow(ctx, `SELECT members_allowed FROM runners WHERE id = $1`, runnerID).Scan(&ok)
	return err == nil && ok
}
