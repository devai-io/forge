package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/vault"
)

// SeedProject is one project in pkg/seed/seed.json or an import file.
type SeedProject struct {
	ProjectInput
	Repos     []RepoInput     `json:"repos"`
	Endpoints []EndpointInput `json:"endpoints"`
	Servers   []struct {
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"servers"`
	Tasks []struct {
		TaskInput
		Repo *string `json:"repo"`
	} `json:"tasks"`
}

// SeedData is pkg/seed/seed.json: the real projects, servers and backlog
// Forge starts with. It is applied once, to an empty database, and is then
// the user's data — nothing re-applies it. `forge_api import` reads the same
// shape (plus server note additions and vault items) and only adds.
type SeedData struct {
	Servers       []ServerInput `json:"servers"`
	Projects      []SeedProject `json:"projects"`
	ServerUpdates []struct {
		Name        string `json:"name"`
		NotesAppend string `json:"notes_append"`
	} `json:"server_updates"`
	Vault []VaultInput `json:"vault"`
}

// SeedIfEmpty loads the seed when there are no projects yet. All or nothing:
// one transaction, so a bad seed file leaves an empty database to retry on.
func (s *Store) SeedIfEmpty(ctx context.Context, raw []byte) (bool, error) {
	var n int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM projects`).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	var data SeedData
	if err := json.Unmarshal(raw, &data); err != nil {
		return false, fmt.Errorf("seed: %w", err)
	}
	err := s.tx(ctx, func(tx pgxTx) error {
		serverIDs := map[string]int64{}
		for _, sv := range data.Servers {
			id, err := seedServer(ctx, tx, sv)
			if err != nil {
				return err
			}
			serverIDs[strings.TrimSpace(sv.Name)] = id
		}
		for _, p := range data.Projects {
			if err := seedProject(ctx, tx, p, serverIDs); err != nil {
				return err
			}
		}
		return logActivity(ctx, tx, ActivityInput{Kind: "forge.seeded",
			Summary: fmt.Sprintf("Forge set up with %d projects and %d servers", len(data.Projects), len(data.Servers))})
	})
	return err == nil, err
}

// ImportReport says what an import added and what it left alone.
type ImportReport struct {
	ServersAdded, ProjectsAdded, NotesAppended, VaultAdded []string
	Skipped                                                []string
}

// Import adds servers, projects (with their repos, links, endpoints and
// tasks), server-note additions and vault items. Anything that already
// exists — a server or vault item by name, a project by key — is skipped, so
// running the same file twice is harmless. One transaction for everything but
// the vault (which seals each item on its own).
func (s *Store) Import(ctx context.Context, box *vault.Box, raw []byte, ip string) (*ImportReport, error) {
	var data SeedData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("import: %w", err)
	}
	rep := &ImportReport{}
	err := s.tx(ctx, func(tx pgxTx) error {
		serverIDs := map[string]int64{}
		rows, err := tx.Query(ctx, `SELECT id, name FROM servers`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				rows.Close()
				return err
			}
			serverIDs[name] = id
		}
		rows.Close()

		for _, sv := range data.Servers {
			name := strings.TrimSpace(sv.Name)
			if _, ok := serverIDs[name]; ok {
				rep.Skipped = append(rep.Skipped, "server "+name)
				continue
			}
			id, err := seedServer(ctx, tx, sv)
			if err != nil {
				return err
			}
			serverIDs[name] = id
			rep.ServersAdded = append(rep.ServersAdded, name)
		}
		for _, u := range data.ServerUpdates {
			add := strings.TrimSpace(u.NotesAppend)
			if add == "" {
				continue
			}
			tag, err := tx.Exec(ctx, `UPDATE servers SET notes = rtrim(notes) || char(10, 10) || $2, updated_at = now()
				WHERE name = $1 AND position($2 in notes) = 0`, u.Name, add)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				rep.NotesAppended = append(rep.NotesAppended, u.Name)
			}
		}
		for _, p := range data.Projects {
			key := strings.ToUpper(strings.TrimSpace(p.Key))
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT exists(SELECT 1 FROM projects WHERE key = $1)`, key).Scan(&exists); err != nil {
				return err
			}
			if exists {
				rep.Skipped = append(rep.Skipped, "project "+key)
				continue
			}
			if err := seedProject(ctx, tx, p, serverIDs); err != nil {
				return err
			}
			rep.ProjectsAdded = append(rep.ProjectsAdded, key)
			var pid int64
			_ = tx.QueryRow(ctx, `SELECT id FROM projects WHERE key = $1`, key).Scan(&pid)
			if err := logActivity(ctx, tx, ActivityInput{ProjectID: &pid, Kind: "project.created",
				Summary: fmt.Sprintf("Imported project %s with %d tasks", p.Name, len(p.Tasks))}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return rep, err
	}

	for _, v := range data.Vault {
		var exists bool
		if err := s.DB.QueryRow(ctx, `SELECT exists(SELECT 1 FROM vault_items WHERE name = $1)`, strings.TrimSpace(v.Name)).
			Scan(&exists); err != nil {
			return rep, err
		}
		if exists {
			rep.Skipped = append(rep.Skipped, "vault "+v.Name)
			continue
		}
		if _, err := s.CreateVault(ctx, box, v, ip); err != nil {
			return rep, fmt.Errorf("vault item %q: %w", v.Name, err)
		}
		rep.VaultAdded = append(rep.VaultAdded, v.Name)
	}
	return rep, nil
}

func seedServer(ctx context.Context, tx pgxTx, sv ServerInput) (int64, error) {
	if sv.Environment == "" {
		sv.Environment = "production"
	}
	tags, _ := CleanStrings("tags", sv.Tags, 30, 40)
	var id int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO servers (name, role, provider, arch, public_address, tailscale_ip, environment, critical, tags, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		strings.TrimSpace(sv.Name), sv.Role, sv.Provider, sv.Arch, sv.PublicAddress, sv.TailscaleIP,
		sv.Environment, sv.Critical, tags, sv.Notes).Scan(&id); err != nil {
		return 0, fmt.Errorf("server %s: %w", sv.Name, mapErr(err))
	}
	return id, nil
}

// seedProject inserts one project with everything hanging off it.
func seedProject(ctx context.Context, tx pgxTx, p SeedProject, serverIDs map[string]int64) error {
	// Imported "done" items are history, not today's work: backdating them
	// keeps them out of today's count and the streak honest.
	yesterday := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	in := p.ProjectInput
	if err := in.normalize(); err != nil {
		return fmt.Errorf("project %s: %w", p.Key, err)
	}
	var pid int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO projects (key, name, category, status, priority, color, summary, description,
		                      infra_notes, target_date, links)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
		in.Key, in.Name, in.Category, in.Status, in.Priority, in.Color, in.Summary, in.Description,
		in.InfraNotes, in.TargetDate, in.Links).Scan(&pid); err != nil {
		return fmt.Errorf("project %s: %w", in.Key, mapErr(err))
	}

	repoIDs := map[string]int64{}
	for i, r := range p.Repos {
		if r.DefaultBranch == "" {
			r.DefaultBranch = "main"
		}
		if !OneOf(r.Kind, RepoKinds) {
			r.Kind = "other"
		}
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO repos (project_id, name, path, remote_url, default_branch, kind, deploy, notes, sort_order)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			pid, r.Name, r.Path, r.RemoteURL, r.DefaultBranch, r.Kind, r.Deploy, r.Notes, i+1).Scan(&id); err != nil {
			return fmt.Errorf("repo %s/%s: %w", in.Key, r.Name, mapErr(err))
		}
		repoIDs[r.Name] = id
	}

	for _, link := range p.Servers {
		sid, ok := serverIDs[link.Name]
		if !ok {
			return fmt.Errorf("project %s links unknown server %q", in.Key, link.Name)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO project_servers (project_id, server_id, role) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, pid, sid, link.Role); err != nil {
			return err
		}
	}

	for _, e := range p.Endpoints {
		if e.Kind == "" {
			e.Kind = "web"
		}
		if e.ExpectStatus == 0 {
			e.ExpectStatus = 200
		}
		if _, err := tx.Exec(ctx, `INSERT INTO endpoints (project_id, name, url, kind, expect_status)
			VALUES ($1, $2, $3, $4, $5)`, pid, e.Name, e.URL, e.Kind, e.ExpectStatus); err != nil {
			return fmt.Errorf("endpoint %s/%s: %w", in.Key, e.Name, mapErr(err))
		}
	}

	sortOrder := map[string]float64{}
	for i, t := range p.Tasks {
		ti := t.TaskInput
		if err := ti.normalize(); err != nil {
			return fmt.Errorf("task %s #%d: %w", in.Key, i+1, err)
		}
		var repoID *int64
		if t.Repo != nil {
			if id, ok := repoIDs[*t.Repo]; ok {
				repoID = &id
			}
		}
		var completed *string
		if ti.Status == "done" {
			completed = &yesterday
		}
		sortOrder[ti.Status] += 1000
		if _, err := tx.Exec(ctx, `
			INSERT INTO tasks (project_id, number, title, description, status, priority, type, labels,
			                   due_date, focus, repo_id, estimate, sort_order, completed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			pid, i+1, ti.Title, ti.Description, ti.Status, ti.Priority, ti.Type, ti.Labels, ti.DueDate,
			ti.Focus, repoID, ti.Estimate, sortOrder[ti.Status], completed); err != nil {
			return fmt.Errorf("task %s #%d: %w", in.Key, i+1, mapErr(err))
		}
	}
	_, err := tx.Exec(ctx, `UPDATE projects SET next_task_number = $2 WHERE id = $1`, pid, len(p.Tasks)+1)
	return err
}
