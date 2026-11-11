package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/devai-io/forge/internal/auth"
	"github.com/devai-io/forge/internal/store"
)

// Admin: an owner manages the accounts — who exists, their role, the
// projects a member works in, a member's own agent, which machines are open
// to members — and may act as another account for a while (impersonation)
// to see what it sees. None of these routes is open to members; every change
// needs a recent password confirmation and goes to the security log.

type adminMachine struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Role            string   `json:"role"`
	Online          bool     `json:"online"`
	MembersAllowed  bool     `json:"members_allowed"`
	PermissionModes []string `json:"permission_modes"`
}

type adminProject struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (s *Server) adminView(ctx context.Context) (map[string]any, error) {
	users, err := s.store.ListAdminUsers(ctx)
	if err != nil {
		return nil, err
	}
	runners, err := s.store.ListRunners(ctx)
	if err != nil {
		return nil, err
	}
	machines := []adminMachine{}
	for _, rn := range runners {
		machines = append(machines, adminMachine{ID: rn.ID, Name: rn.Name, Role: rn.Role, Online: rn.Online,
			MembersAllowed: rn.MembersAllowed, PermissionModes: rn.Capabilities.PermissionModes})
	}
	list, err := s.store.ListProjects(ctx, store.TodayFor("UTC"), false)
	if err != nil {
		return nil, err
	}
	projects := []adminProject{}
	for _, p := range list {
		projects = append(projects, adminProject{Key: p.Key, Name: p.Name, Color: p.Color})
	}
	return map[string]any{"users": users, "machines": machines, "projects": projects}, nil
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request, u *store.User) {
	v, err := s.adminView(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Username    string   `json:"username"`
		Email       string   `json:"email"`
		DisplayName string   `json:"display_name"`
		Role        string   `json:"role"`
		Projects    []string `json:"projects"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Role == "" {
		in.Role = store.RoleMember
	}
	password := auth.GeneratePassword()
	hash, err := auth.HashPassword(password)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	created, err := s.store.CreateAccount(r.Context(), in.Username, in.Email, in.DisplayName, in.Role, hash, s.cfg.DefaultTimezone, in.Projects)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "user_created", fmt.Sprintf("%s (%s)", created.Username, created.Role))
	// The password is shown once, here; Forge keeps only its hash.
	writeJSON(w, http.StatusCreated, map[string]any{"user": created, "password": password})
}

func pathUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("user"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "no such account")
		return 0, false
	}
	return id, true
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	p, ok := decodePatch(w, r)
	if !ok {
		return
	}
	var in struct {
		DisplayName *string   `json:"display_name"`
		Email       *string   `json:"email"`
		Role        *string   `json:"role"`
		Projects    *[]string `json:"projects"`
		// Agent: a member's own agent — {machine, model, effort,
		// permission_mode} sets it, null takes it away.
		Agent *struct {
			Machine        string `json:"machine"`
			Model          string `json:"model"`
			Effort         string `json:"effort"`
			PermissionMode string `json:"permission_mode"`
		} `json:"agent"`
	}
	if err := decodeInto(p, &in); err != nil {
		writeErr(w, r, &store.ValidationError{Field: "body", Message: "not what an account takes"})
		return
	}
	ctx := r.Context()
	real := realUser(r)
	if in.Role != nil && id == real.ID && *in.Role != real.Role {
		writeErr(w, r, &store.ValidationError{Field: "role", Message: "you cannot change your own role"})
		return
	}
	if _, err := s.store.UpdateAccount(ctx, id, store.AdminUserPatch{DisplayName: in.DisplayName, Email: in.Email, Role: in.Role, Projects: in.Projects}); err != nil {
		writeErr(w, r, err)
		return
	}
	if _, has := p["agent"]; has {
		if in.Agent == nil {
			if err := s.store.ClearMemberAgent(ctx, id); err != nil {
				writeErr(w, r, err)
				return
			}
		} else if _, err := s.store.SetMemberAgent(ctx, id, in.Agent.Machine, in.Agent.Model, in.Agent.Effort, in.Agent.PermissionMode); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	out, err := s.store.AdminUserByID(ctx, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "user_updated", out.Username)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminResetPassword(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	target, err := s.store.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if target.ID == realUser(r).ID {
		writeErr(w, r, &store.ValidationError{Field: "id", Message: "change your own password under Settings"})
		return
	}
	password := auth.GeneratePassword()
	hash, err := auth.HashPassword(password)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.store.SetPassword(r.Context(), id, hash, 0, true); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "user_password_reset", target.Username)
	writeJSON(w, http.StatusOK, map[string]any{"password": password}) // shown once; every session of that account is signed out
}

func (s *Server) adminDisableTOTP(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	target, err := s.store.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.store.DisableTOTP(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "user_updated", target.Username+" (two-factor off)")
	w.WriteHeader(http.StatusNoContent)
}

// adminRemovePasskeys: an account that lost its passkeys (or one that may
// have been copied) signs in with its password and code until it adds new ones.
func (s *Server) adminRemovePasskeys(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	target, err := s.store.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	n, err := s.store.DeletePasskeys(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "user_updated", fmt.Sprintf("%s (%d passkey(s) removed)", target.Username, n))
	writeJSON(w, http.StatusOK, map[string]int{"removed": n})
}

func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	if id == realUser(r).ID {
		writeErr(w, r, &store.ValidationError{Field: "id", Message: "you cannot delete your own account"})
		return
	}
	target, err := s.store.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.store.DeleteAccount(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "user_deleted", target.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminUpdateMachine(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		MembersAllowed *bool `json:"members_allowed"`
	}
	if !decode(w, r, &in) {
		return
	}
	rn, err := s.store.RunnerByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if in.MembersAllowed != nil {
		if err := s.store.SetRunnerMembersAllowedByID(r.Context(), id, *in.MembersAllowed); err != nil {
			writeErr(w, r, err)
			return
		}
		s.sec(r, "user_updated", fmt.Sprintf("machine %s %s to members", rn.Name, map[bool]string{true: "opened", false: "closed"}[*in.MembersAllowed]))
	}
	s.adminUsers(w, r, u)
}

// ── Impersonation ─────────────────────────────────────────────────────────

// impersonate makes this (owner's) session act as another account until it
// is stopped: every request is then answered as that account — a member's
// limits included — except its sign-in settings (password, two-factor,
// sessions), which stay out of reach.
func (s *Server) impersonate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		UserID int64 `json:"user_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	real := realUser(r)
	if real.Role != store.RoleOwner || impersonating(r) {
		writeError(w, http.StatusForbidden, "forbidden", "stop the current impersonation first")
		return
	}
	target, err := s.store.UserByID(r.Context(), in.UserID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if target.ID == real.ID {
		writeErr(w, r, &store.ValidationError{Field: "user_id", Message: "that is you"})
		return
	}
	if err := s.store.SetSessionImpersonation(r.Context(), sessionID(r), &target.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "impersonate", "as "+target.Username)
	writeJSON(w, http.StatusOK, map[string]any{"user": target})
}

// stopImpersonating is always within reach, whatever the account acted as
// may do.
func (s *Server) stopImpersonating(w http.ResponseWriter, r *http.Request, u *store.User) {
	if impersonating(r) {
		if err := s.store.SetSessionImpersonation(r.Context(), sessionID(r), nil); err != nil {
			writeErr(w, r, err)
			return
		}
		s.sec(r, "impersonate", "stopped (was "+u.Username+")")
	}
	w.WriteHeader(http.StatusNoContent)
}
