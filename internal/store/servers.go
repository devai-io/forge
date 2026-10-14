package store

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

const serverSelect = `
SELECT sv.id, sv.name, sv.role, sv.provider, sv.arch, sv.public_address, sv.tailscale_ip, sv.environment,
       sv.critical, sv.tags, sv.notes, sv.ssh_target, sv.created_at, sv.updated_at,
       coalesce((SELECT json_group_array(json_object('key', p.key, 'name', p.name, 'color', p.color, 'role', ps.role)
                                ORDER BY p.priority, p.name)
                 FROM project_servers ps JOIN projects p ON p.id = ps.project_id
                 WHERE ps.server_id = sv.id), '[]')
FROM servers sv`

func scanServer(row interface{ Scan(...any) error }) (*Server, error) {
	var x Server
	var projects []byte
	if err := row.Scan(&x.ID, &x.Name, &x.Role, &x.Provider, &x.Arch, &x.PublicAddress, &x.TailscaleIP,
		&x.Environment, &x.Critical, &x.Tags, &x.Notes, &x.SSHTarget, &x.CreatedAt, &x.UpdatedAt, &projects); err != nil {
		return nil, mapErr(err)
	}
	x.Tags = nonNil(x.Tags)
	x.Projects = []ServerProject{}
	_ = json.Unmarshal(projects, &x.Projects)
	return &x, nil
}

func (s *Store) ListServers(ctx context.Context) ([]Server, error) {
	rows, err := s.DB.Query(ctx, serverSelect+`
		ORDER BY sv.critical DESC,
		         CASE sv.environment WHEN 'production' THEN 1 WHEN 'staging' THEN 2 WHEN 'infra' THEN 3 ELSE 4 END, sv.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		x, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

func (s *Store) ServerByID(ctx context.Context, id int64) (*Server, error) {
	return scanServer(s.DB.QueryRow(ctx, serverSelect+` WHERE sv.id = $1`, id))
}

type ServerInput struct {
	Name          string   `json:"name"`
	Role          string   `json:"role"`
	Provider      string   `json:"provider"`
	Arch          string   `json:"arch"`
	PublicAddress string   `json:"public_address"`
	TailscaleIP   string   `json:"tailscale_ip"`
	Environment   string   `json:"environment"`
	Critical      bool     `json:"critical"`
	Tags          []string `json:"tags"`
	Notes         string   `json:"notes"`
	SSHTarget     string   `json:"ssh_target"`
}

func (s *Store) CreateServer(ctx context.Context, in ServerInput) (*Server, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return nil, invalid("name", "must be 1-100 characters")
	}
	if in.Environment == "" {
		in.Environment = "production"
	}
	if !OneOf(in.Environment, Environments) {
		return nil, invalid("environment", "must be one of %s", strings.Join(Environments, ", "))
	}
	tags, err := CleanStrings("tags", in.Tags, 30, 40)
	if err != nil {
		return nil, err
	}
	in.SSHTarget = strings.TrimSpace(in.SSHTarget)
	if in.SSHTarget != "" && !ValidSSHTarget(in.SSHTarget) {
		return nil, invalid("ssh_target", "must be user@host, an address or an ssh alias (no spaces, no leading dash)")
	}
	var id int64
	err = s.DB.QueryRow(ctx, `
		INSERT INTO servers (name, role, provider, arch, public_address, tailscale_ip, environment, critical, tags, notes, ssh_target)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
		in.Name, in.Role, in.Provider, in.Arch, in.PublicAddress, in.TailscaleIP, in.Environment, in.Critical,
		tags, in.Notes, in.SSHTarget).Scan(&id)
	if err != nil {
		return nil, mapErr(err)
	}
	return s.ServerByID(ctx, id)
}

var serverFields = map[string]field{
	"name":           {"name", pString(100, true)},
	"role":           {"role", pString(300, false)},
	"provider":       {"provider", pString(100, false)},
	"arch":           {"arch", pString(40, false)},
	"public_address": {"public_address", pString(200, false)},
	"tailscale_ip":   {"tailscale_ip", pString(100, false)},
	"environment":    {"environment", pEnum(Environments)},
	"critical":       {"critical", pBool},
	"tags":           {"tags", pStrings(30, 40)},
	"notes":          {"notes", pString(50000, false)},
	"ssh_target":     {"ssh_target", pSSHTarget},
}

// sshTargetRe is what "Open shell" will type after `ssh`: user@host, an
// address (IPv6 in brackets) or an ssh-config alias. No spaces, and no
// leading dash, so it can never be read as an option.
var sshTargetRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@:\[\]-]*$`)

// ValidSSHTarget reports whether t is a usable SSH destination.
func ValidSSHTarget(t string) bool { return len(t) <= 200 && sshTargetRe.MatchString(t) }

func pSSHTarget(key string, raw json.RawMessage) (any, error) {
	v, err := pString(200, false)(key, raw)
	if err != nil {
		return nil, err
	}
	if t := v.(string); t != "" && !ValidSSHTarget(t) {
		return nil, invalid(key, "must be user@host, an address or an ssh alias (no spaces, no leading dash)")
	}
	return v, nil
}

func (s *Store) UpdateServer(ctx context.Context, id int64, p Patch) (*Server, error) {
	set, args, err := updateSet(p, serverFields, []string{"id", "projects", "created_at", "updated_at"}, 2)
	if err != nil {
		return nil, err
	}
	if set != "" {
		tag, err := s.DB.Exec(ctx, `UPDATE servers SET `+set+`, updated_at = now() WHERE id = $1`,
			append([]any{id}, args...)...)
		if err != nil {
			return nil, mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return nil, ErrNotFound
		}
	}
	return s.ServerByID(ctx, id)
}

func (s *Store) DeleteServer(ctx context.Context, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM servers WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
