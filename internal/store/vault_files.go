package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/devai-io/forge/internal/vault"
)

// Repo files: vault items that are git-ignored files of a project's repos
// (.env, keystores, service-account JSONs), each placed at `path` inside its
// repo when a machine is set up. Collected from a machine with
// `forge agent secrets push`, handed out as a tar.gz.

// CleanRepoPath validates a path inside a repo: relative, slash-separated,
// no "..", at most 300 characters. "" stays "" (not a repo file).
func CleanRepoPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	if len(p) > 300 || strings.ContainsAny(p, "\\\x00\r\n") {
		return "", invalid("path", "must be a relative path of at most 300 characters, with / separators")
	}
	if strings.HasPrefix(p, "/") {
		return "", invalid("path", "must be relative to the repo root")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", invalid("path", "must stay inside the repo (no ..)")
		}
	}
	p = path.Clean(p)
	if p == "." || p == "" {
		return "", invalid("path", "must name a file")
	}
	return p, nil
}

// checkVaultRepo: a repo must belong to the item's project.
func checkVaultRepo(ctx context.Context, q querier, projectID, repoID *int64) error {
	if repoID == nil {
		return nil
	}
	var pid int64
	if err := q.QueryRow(ctx, `SELECT project_id FROM repos WHERE id = $1`, *repoID).Scan(&pid); err != nil {
		return invalid("repo_id", "no such repo")
	}
	if projectID == nil || *projectID != pid {
		return invalid("repo_id", "must be a repo of the item's project")
	}
	return nil
}

// checkVaultRow re-checks repo/path consistency after an update.
func checkVaultRow(ctx context.Context, q querier, id int64) error {
	var projectID, repoID *int64
	var p string
	if err := q.QueryRow(ctx, `SELECT project_id, repo_id, path FROM vault_items WHERE id = $1`, id).
		Scan(&projectID, &repoID, &p); err != nil {
		return mapErr(err)
	}
	if err := checkVaultRepo(ctx, q, projectID, repoID); err != nil {
		return err
	}
	if p != "" && repoID == nil {
		return invalid("path", "a repo file needs a repo")
	}
	return nil
}

// RepoFile is one repo file as it would be written on a machine. Content is
// nil when the item has nothing to write (Skipped says why).
type RepoFile struct {
	ItemID   int64  `json:"item_id"`
	Name     string `json:"name"`
	RepoID   int64  `json:"repo_id"`
	RepoName string `json:"repo_name"`
	RepoDir  string `json:"repo_dir"` // the checkout's folder name
	Path     string `json:"path"`
	Content  []byte `json:"-"`
	Skipped  string `json:"skipped,omitempty"`
}

// repoDir is the folder a repo is cloned into: the last element of its known
// path, else its name.
func repoDir(name, p string) string {
	if p = strings.TrimRight(strings.TrimSpace(p), "/"); p != "" {
		if b := filepath.Base(p); b != "." && b != "/" && b != "" {
			return b
		}
	}
	return strings.NewReplacer("/", "_", "\\", "_").Replace(name)
}

// RepoFiles opens every repo file of a project (optionally one repo) for a
// machine setup, and records each one handed out as a download.
func (s *Store) RepoFiles(ctx context.Context, box *vault.Box, projectKey string, repoID int64, ip string) ([]RepoFile, error) {
	if !box.Available() {
		return nil, vault.ErrUnavailable
	}
	pid, err := s.projectID(ctx, s.DB, projectKey)
	if err != nil {
		return nil, err
	}
	q := `SELECT v.id, v.name, v.kind, v.aad, v.secret_sealed, v.file_sealed, r.id, r.name, r.path, v.path
		FROM vault_items v JOIN repos r ON r.id = v.repo_id
		WHERE v.project_id = $1 AND v.path <> ''`
	args := []any{pid}
	if repoID != 0 {
		q += ` AND v.repo_id = $2`
		args = append(args, repoID)
	}
	rows, err := s.DB.Query(ctx, q+` ORDER BY r.sort_order, r.name, v.path`, args...)
	if err != nil {
		return nil, err
	}
	type sealed struct {
		kind               string
		aad, secret, file  []byte
		repoPath, repoName string
	}
	var files []RepoFile
	var blobs []sealed
	for rows.Next() {
		var f RepoFile
		var b sealed
		if err := rows.Scan(&f.ItemID, &f.Name, &b.kind, &b.aad, &b.secret, &b.file, &f.RepoID, &f.RepoName, &b.repoPath, &f.Path); err != nil {
			rows.Close()
			return nil, err
		}
		f.RepoDir = repoDir(f.RepoName, b.repoPath)
		files = append(files, f)
		blobs = append(blobs, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range files {
		b := blobs[i]
		content, why, err := materialize(box, b.kind, b.aad, b.secret, b.file)
		if err != nil {
			return nil, err
		}
		files[i].Content, files[i].Skipped = content, why
		if content != nil {
			if err := s.touchReveal(ctx, files[i].ItemID, files[i].Name, "download", ip); err != nil {
				return nil, err
			}
		}
	}
	if files == nil {
		files = []RepoFile{}
	}
	return files, nil
}

// materialize is what a repo file's item writes: its file; else an env file's
// secret fields as KEY=value lines; else a single secret field's value.
func materialize(box *vault.Box, kind string, aad, sealedSecret, sealedFile []byte) ([]byte, string, error) {
	if sealedFile != nil {
		data, err := box.Open(sealedFile, fileAAD(aad))
		return data, "", err
	}
	if sealedSecret == nil {
		return nil, "nothing stored (no file, no secret)", nil
	}
	plain, err := box.Open(sealedSecret, aad)
	if err != nil {
		return nil, "", err
	}
	m := map[string]string{}
	if err := json.Unmarshal(plain, &m); err != nil {
		return nil, "", err
	}
	switch {
	case kind == "env_file" && len(m) > 0:
		return []byte(RenderDotenv(m)), "", nil
	case len(m) == 1:
		for _, v := range m {
			return []byte(v), "", nil
		}
	}
	return nil, "several secret fields and no file: attach the file, or make it an env file", nil
}

// RenderDotenv writes fields as a .env file, sorted by key. Plain values stay
// bare; others are single-quoted (taken literally by dotenv parsers), or
// double-quoted with escapes when they hold a quote or a newline.
func RenderDotenv(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(dotenvValue(m[k]))
		b.WriteByte('\n')
	}
	return b.String()
}

func dotenvValue(v string) string {
	if v == "" {
		return ""
	}
	if !strings.ContainsAny(v, " \t#\"'\\$`\n\r=") {
		return v
	}
	if !strings.ContainsAny(v, "'\n\r") {
		return "'" + v + "'"
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "$", `\$`, "`", "\\`")
	return `"` + r.Replace(v) + `"`
}

// InferRepoFileKind picks a vault kind from a repo file's name.
func InferRepoFileKind(p string) string {
	base := strings.ToLower(path.Base(p))
	ext := path.Ext(base)
	switch {
	case base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env"):
		return "env_file"
	case ext == ".pem" || ext == ".key" || ext == ".p8" || strings.HasPrefix(base, "id_"):
		return "private_key"
	case ext == ".p12" || ext == ".jks" || ext == ".keystore" || ext == ".pfx":
		return "keystore"
	case ext == ".crt" || ext == ".cer":
		return "certificate"
	case ext == ".mobileprovision":
		return "provisioning_profile"
	case base == "google-services.json" || base == "googleservice-info.plist" ||
		(ext == ".json" && (strings.Contains(base, "service-account") || strings.Contains(base, "service_account") ||
			strings.HasPrefix(base, "credentials"))):
		return "service_account"
	}
	return "file"
}

// RepoFileInput is one file a machine sends from a repo checkout.
type RepoFileInput struct {
	Path          string `json:"path"`
	ContentBase64 string `json:"content_base64"`
}

// RepoFileResult says what a push did (or would do) with one file.
type RepoFileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"` // create | update | same
	ItemID *int64 `json:"item_id"`
}

// PushRepoFiles stores files a machine sent from a checkout of repo: each
// lands on the item at (repo, path), created when missing, replaced when the
// content differs. With dryRun nothing is written. Nothing secret is ever
// returned; actor ("machine NAME") goes into the vault audit.
func (s *Store) PushRepoFiles(ctx context.Context, box *vault.Box, repo *RepoWithProject, files []RepoFileInput, dryRun bool, actor string) ([]RepoFileResult, error) {
	if !box.Available() {
		return nil, vault.ErrUnavailable
	}
	if len(files) == 0 || len(files) > 100 {
		return nil, invalid("files", "send 1-100 files")
	}
	type decoded struct {
		path string
		data []byte
	}
	seen := map[string]bool{}
	in := make([]decoded, 0, len(files))
	for _, f := range files {
		p, err := CleanRepoPath(f.Path)
		if err != nil {
			return nil, err
		}
		if p == "" {
			return nil, invalid("path", "every file needs a path")
		}
		if seen[p] {
			return nil, invalid("path", "%s is listed twice", p)
		}
		seen[p] = true
		data, err := base64.StdEncoding.DecodeString(f.ContentBase64)
		if err != nil {
			return nil, invalid("content_base64", "%s is not base64", p)
		}
		if len(data) == 0 || len(data) > maxVaultFile {
			return nil, invalid("files", "%s must be 1 byte to 1 MB", p)
		}
		in = append(in, decoded{p, data})
	}
	key := repo.ProjectKey
	out := make([]RepoFileResult, 0, len(in))
	for _, f := range in {
		var id int64
		var kind string
		var aad, secret, file []byte
		err := s.DB.QueryRow(ctx, `SELECT id, kind, aad, secret_sealed, file_sealed FROM vault_items WHERE repo_id = $1 AND path = $2`,
			repo.ID, f.path).Scan(&id, &kind, &aad, &secret, &file)
		res := RepoFileResult{Path: f.path}
		upload := &VaultFile{Name: path.Base(f.path), ContentBase64: base64.StdEncoding.EncodeToString(f.data)}
		switch mapped := mapErr(err); {
		case mapped == ErrNotFound:
			res.Action = "create"
			if !dryRun {
				item, err := s.CreateVault(ctx, box, VaultInput{
					Name: repo.Name + " · " + f.path, Kind: InferRepoFileKind(f.path), ProjectKey: &key,
					RepoID: &repo.ID, Path: f.path, File: upload, Tags: []string{"repo-file"},
					Notes: "Collected by " + actor + " (forge agent secrets push).",
				}, actor)
				if err != nil {
					return nil, err
				}
				res.ItemID = &item.ID
			}
		case mapped != nil:
			return nil, mapped
		default:
			res.ItemID = &id
			current, _, err := materialize(box, kind, aad, secret, file)
			if err != nil {
				return nil, err
			}
			if current != nil && bytes.Equal(current, f.data) {
				res.Action = "same"
				break
			}
			res.Action = "update"
			if !dryRun {
				raw, _ := json.Marshal(upload)
				if _, err := s.UpdateVault(ctx, box, id, Patch{"file": raw}, actor); err != nil {
					return nil, err
				}
			}
		}
		out = append(out, res)
	}
	return out, nil
}

// RepoForCheckout finds the repo a machine's checkout is: by its directory
// (as the session context does), else by its git remote, since another
// machine may keep its checkouts elsewhere.
func (s *Store) RepoForCheckout(ctx context.Context, cwd, remoteURL string) (*RepoWithProject, error) {
	if strings.TrimSpace(cwd) != "" {
		if _, repo, err := s.ProjectForPath(ctx, cwd); err != nil {
			return nil, err
		} else if repo != nil {
			return repo, nil
		}
	}
	if want := normalizeRemote(remoteURL); want != "" {
		repos, err := s.AllRepos(ctx)
		if err != nil {
			return nil, err
		}
		for i := range repos {
			if normalizeRemote(repos[i].RemoteURL) == want && repos[i].ProjectStatus != "archived" {
				return &repos[i], nil
			}
		}
	}
	return nil, invalid("cwd", "this folder is not a repo Forge knows — add it to a project first (its path or remote URL)")
}

// normalizeRemote reduces a git remote to host/owner/name:
// "git@github.com:org/app.git" and "https://github.com/org/app" agree.
func normalizeRemote(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	if u == "" {
		return ""
	}
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	} else if at := strings.Index(u, "@"); at >= 0 && strings.Contains(u[at:], ":") {
		u = strings.Replace(u, ":", "/", 1)
	}
	if at := strings.LastIndex(u, "@"); at >= 0 {
		u = u[at+1:]
	}
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	return u
}
