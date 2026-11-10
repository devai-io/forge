package store

import (
	"context"
	"regexp"
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

// MemberAgent is the one agent a member works with: where it runs and how,
// as the owner set it (`forge member-agent`). The member cannot change it.
type MemberAgent struct {
	UserID         int64  `json:"-"`
	RunnerID       int64  `json:"runner_id"`
	RunnerName     string `json:"runner_name"`
	Online         bool   `json:"online"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permission_mode"`
}

// MemberAgent is the user's agent (ErrNotFound when the owner set none).
func (s *Store) MemberAgent(ctx context.Context, userID int64) (*MemberAgent, error) {
	a := MemberAgent{UserID: userID}
	err := s.DB.QueryRow(ctx, `SELECT a.runner_id, rn.name, coalesce(rn.last_seen_at > ts_add(now(), -`+onlineWindow+`), 0),
		a.model, a.effort, a.permission_mode
		FROM member_agents a JOIN runners rn ON rn.id = a.runner_id WHERE a.user_id = $1`, userID).
		Scan(&a.RunnerID, &a.RunnerName, &a.Online, &a.Model, &a.Effort, &a.PermissionMode)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

// memberAgentModel is what an agent's model may be: an alias or a model id.
var memberAgentModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,79}$`)

// SetMemberAgent gives a member its agent: one machine, a model, an effort
// and a permission mode — never bypassPermissions (Claude asks before
// acting; a member does not turn that off).
func (s *Store) SetMemberAgent(ctx context.Context, userID int64, machine, model, effort, mode string) (*MemberAgent, error) {
	var role string
	if err := s.DB.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, userID).Scan(&role); err != nil {
		return nil, mapErr(err)
	}
	if role != RoleMember {
		return nil, invalid("user", "only a member account has a member agent (an owner uses Agents)")
	}
	var runnerID int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM runners WHERE name = $1`, machine).Scan(&runnerID); err != nil {
		return nil, invalid("machine", "no machine named %s", machine)
	}
	if model != "" && !memberAgentModel.MatchString(model) {
		return nil, invalid("model", "not a model name")
	}
	if effort != "" && !OneOf(effort, ClaudeEfforts) {
		return nil, invalid("effort", "must be one of %s", strings.Join(ClaudeEfforts, ", "))
	}
	if mode == "" {
		mode = "acceptEdits"
	}
	if !OneOf(mode, PermissionModes) || mode == "bypassPermissions" {
		return nil, invalid("permission_mode", "must be plan, acceptEdits, auto or dontAsk — a member's agent never skips permissions")
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO member_agents (user_id, runner_id, model, effort, permission_mode)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) DO UPDATE SET runner_id = excluded.runner_id, model = excluded.model,
			effort = excluded.effort, permission_mode = excluded.permission_mode`, userID, runnerID, model, effort, mode); err != nil {
		return nil, mapErr(err)
	}
	return s.MemberAgent(ctx, userID)
}

// ClearMemberAgent takes a member's agent away.
func (s *Store) ClearMemberAgent(ctx context.Context, userID int64) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM member_agents WHERE user_id = $1`, userID)
	return mapErr(err)
}

// MemberMachineOpen reports whether this member's prompts may run on the
// machine: with an agent of its own, on that machine and no other;
// otherwise on the machines opened to members.
func (s *Store) MemberMachineOpen(ctx context.Context, userID, runnerID int64) bool {
	if a, err := s.MemberAgent(ctx, userID); err == nil {
		return a.RunnerID == runnerID
	}
	return s.RunnerMembersAllowed(ctx, runnerID)
}

// MemberAgentRun is the conversation a member's agent has open: the latest
// open agent run that member started (ErrNotFound when there is none).
func (s *Store) MemberAgentRun(ctx context.Context, userID int64) (*Run, error) {
	runs, err := s.listRunsWhere(ctx, `r.user_id = $1 AND r.kind = 'agent' AND r.chat_turn_id IS NULL
		AND r.status IN ('queued', 'running', 'waiting') ORDER BY r.id DESC LIMIT 1`, userID)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, ErrNotFound
	}
	return &runs[0], nil
}

// MemberRuns is the runs a member started, newest first.
func (s *Store) MemberRuns(ctx context.Context, userID int64, limit int) ([]Run, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.listRunsWhere(ctx, `r.user_id = $1 AND r.kind = 'agent' AND r.chat_turn_id IS NULL ORDER BY r.id DESC LIMIT $2`, userID, limit)
}
