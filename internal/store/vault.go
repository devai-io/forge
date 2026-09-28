package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/vault"
)

var VaultKinds = []string{"password", "api_key", "private_key", "keystore", "certificate",
	"provisioning_profile", "service_account", "token", "env_file", "note", "reference"}

// maxVaultFile bounds one stored file. Keystores, .p8/.p12 and service-account
// JSONs are a few KB; this is a vault, not a file share.
const maxVaultFile = 1 << 20

type VaultItem struct {
	ID             int64             `json:"id"`
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	ProjectKey     *string           `json:"project_key"`
	ProjectColor   *string           `json:"project_color"`
	Platform       string            `json:"platform"`
	Host           string            `json:"host"`
	Identifier     string            `json:"identifier"`
	Fields         map[string]string `json:"fields"`
	SecretKeys     []string          `json:"secret_keys"`
	HasFile        bool              `json:"has_file"`
	FileName       string            `json:"file_name"`
	FileSize       int               `json:"file_size"`
	Location       string            `json:"location"`
	ExpiresAt      *string           `json:"expires_at"`
	Notes          string            `json:"notes"`
	Tags           []string          `json:"tags"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	LastRevealedAt *time.Time        `json:"last_revealed_at"`
}

type VaultAudit struct {
	ID       int64     `json:"id"`
	ItemID   *int64    `json:"item_id"`
	ItemName string    `json:"item_name"`
	Action   string    `json:"action"`
	IP       string    `json:"ip"`
	At       time.Time `json:"at"`
}

const vaultSelect = `
SELECT v.id, v.name, v.kind, p.key, p.color, v.platform, v.host, v.identifier, v.fields, v.secret_keys,
       v.file_sealed IS NOT NULL, v.file_name, v.file_size, v.location, v.expires_at,
       v.notes, v.tags, v.created_at, v.updated_at, v.last_revealed_at
FROM vault_items v LEFT JOIN projects p ON p.id = v.project_id`

func scanVault(row interface{ Scan(...any) error }) (*VaultItem, error) {
	var v VaultItem
	var fields []byte
	if err := row.Scan(&v.ID, &v.Name, &v.Kind, &v.ProjectKey, &v.ProjectColor, &v.Platform, &v.Host,
		&v.Identifier, &fields, &v.SecretKeys, &v.HasFile, &v.FileName, &v.FileSize, &v.Location, &v.ExpiresAt,
		&v.Notes, &v.Tags, &v.CreatedAt, &v.UpdatedAt, &v.LastRevealedAt); err != nil {
		return nil, mapErr(err)
	}
	v.Fields = map[string]string{}
	_ = json.Unmarshal(fields, &v.Fields)
	v.SecretKeys, v.Tags = nonNil(v.SecretKeys), nonNil(v.Tags)
	return &v, nil
}

type VaultFilter struct {
	ProjectKey, Query, Kind string
}

func (s *Store) ListVault(ctx context.Context, f VaultFilter) ([]VaultItem, error) {
	var where []string
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if f.ProjectKey != "" {
		where = append(where, "p.key = "+arg(strings.ToUpper(f.ProjectKey)))
	}
	if f.Kind != "" {
		where = append(where, "v.kind = "+arg(f.Kind))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		n := arg("%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%")
		where = append(where, fmt.Sprintf("(v.name LIKE %[1]s ESCAPE '\\' OR v.identifier LIKE %[1]s ESCAPE '\\' OR v.location LIKE %[1]s ESCAPE '\\' OR v.notes LIKE %[1]s ESCAPE '\\' OR v.tags LIKE %[1]s ESCAPE '\\')", n))
	}
	q := vaultSelect
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY p.priority NULLS LAST, p.name NULLS LAST, v.platform, lower(v.name)"
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VaultItem{}
	for rows.Next() {
		v, err := scanVault(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (s *Store) VaultByID(ctx context.Context, id int64) (*VaultItem, error) {
	return scanVault(s.DB.QueryRow(ctx, vaultSelect+` WHERE v.id = $1`, id))
}

// VaultFile is an uploaded file: name + base64 content.
type VaultFile struct {
	Name          string `json:"name"`
	ContentBase64 string `json:"content_base64"`
}

type VaultInput struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	ProjectKey *string           `json:"project_key"`
	Platform   string            `json:"platform"`
	Host       string            `json:"host"`
	Identifier string            `json:"identifier"`
	Fields     map[string]string `json:"fields"`
	Secret     map[string]string `json:"secret"`
	File       *VaultFile        `json:"file"`
	Location   string            `json:"location"`
	ExpiresAt  *string           `json:"expires_at"`
	Notes      string            `json:"notes"`
	Tags       []string          `json:"tags"`
}

func secretKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func cleanSecret(m map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range m {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if len(k) > 60 {
			return nil, invalid("secret", "field names must be at most 60 characters")
		}
		if len(v) > 256<<10 {
			return nil, invalid("secret", "field %q is too large", k)
		}
		out[k] = v
	}
	if len(out) > 30 {
		return nil, invalid("secret", "at most 30 secret fields")
	}
	return out, nil
}

func cleanFields(m map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range m {
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		if len(k) > 60 || len(v) > 2000 {
			return nil, invalid("fields", "metadata keys are at most 60 characters and values 2000")
		}
		out[k] = v
	}
	return out, nil
}

func decodeFile(f *VaultFile) (string, []byte, error) {
	name := strings.TrimSpace(f.Name)
	if name == "" || len(name) > 200 || strings.ContainsAny(name, "/\\\r\n\"") {
		return "", nil, invalid("file", "needs a plain file name")
	}
	data, err := base64.StdEncoding.DecodeString(f.ContentBase64)
	if err != nil {
		return "", nil, invalid("file", "content_base64 is not base64")
	}
	if len(data) == 0 || len(data) > maxVaultFile {
		return "", nil, invalid("file", "must be 1 byte to 1 MB")
	}
	return name, data, nil
}

func (s *Store) vaultProjectID(ctx context.Context, q querier, key *string) (*int64, error) {
	if key == nil || strings.TrimSpace(*key) == "" {
		return nil, nil
	}
	id, err := s.projectID(ctx, q, *key)
	if err != nil {
		return nil, invalid("project_key", "no such project")
	}
	return &id, nil
}

func (s *Store) CreateVault(ctx context.Context, box *vault.Box, in VaultInput, ip string) (*VaultItem, error) {
	if !box.Available() {
		return nil, vault.ErrUnavailable
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 {
		return nil, invalid("name", "must be 1-200 characters")
	}
	if !OneOf(in.Kind, VaultKinds) {
		return nil, invalid("kind", "must be one of %s", strings.Join(VaultKinds, ", "))
	}
	if in.ExpiresAt != nil && !IsDate(*in.ExpiresAt) {
		return nil, invalid("expires_at", "must be a YYYY-MM-DD date")
	}
	fields, err := cleanFields(in.Fields)
	if err != nil {
		return nil, err
	}
	secret, err := cleanSecret(in.Secret)
	if err != nil {
		return nil, err
	}
	tags, err := CleanStrings("tags", in.Tags, 20, 60)
	if err != nil {
		return nil, err
	}
	aad := make([]byte, 16)
	if _, err := rand.Read(aad); err != nil {
		return nil, err
	}
	var sealedSecret, sealedFile []byte
	if len(secret) > 0 {
		raw, _ := json.Marshal(secret)
		if sealedSecret, err = box.Seal(raw, aad); err != nil {
			return nil, err
		}
	}
	var fileName string
	var fileSize int
	if in.File != nil {
		name, data, err := decodeFile(in.File)
		if err != nil {
			return nil, err
		}
		if sealedFile, err = box.Seal(data, fileAAD(aad)); err != nil {
			return nil, err
		}
		fileName, fileSize = name, len(data)
	}

	var id int64
	err = s.tx(ctx, func(tx pgxTx) error {
		pid, err := s.vaultProjectID(ctx, tx, in.ProjectKey)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO vault_items (aad, name, kind, project_id, platform, host, identifier, fields, secret_keys,
			                         secret_sealed, file_name, file_size, file_sealed, location, expires_at, notes, tags)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) RETURNING id`,
			aad, in.Name, in.Kind, pid, strings.TrimSpace(in.Platform), strings.TrimSpace(in.Host),
			strings.TrimSpace(in.Identifier), fields, secretKeys(secret), sealedSecret, fileName, fileSize,
			sealedFile, strings.TrimSpace(in.Location), in.ExpiresAt, in.Notes, tags).Scan(&id); err != nil {
			return mapErr(err)
		}
		return auditVault(ctx, tx, &id, in.Name, "create", ip)
	})
	if err != nil {
		return nil, err
	}
	return s.VaultByID(ctx, id)
}

// UpdateVault applies a partial update. `secret` and `file` replace
// wholesale; null removes them.
func (s *Store) UpdateVault(ctx context.Context, box *vault.Box, id int64, p Patch, ip string) (*VaultItem, error) {
	var sets []string
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)+1) }

	var aad []byte
	var name string
	if err := s.DB.QueryRow(ctx, `SELECT aad, name FROM vault_items WHERE id = $1`, id).Scan(&aad, &name); err != nil {
		return nil, mapErr(err)
	}
	for key, raw := range p {
		switch key {
		case "name", "platform", "host", "identifier", "location", "notes":
			max, required := 2000, false
			if key == "name" {
				max, required = 200, true
			}
			if key == "notes" {
				max = 50000
			}
			v, err := pString(max, required)(key, raw)
			if err != nil {
				return nil, err
			}
			sets = append(sets, key+" = "+arg(v))
		case "kind":
			v, err := pEnum(VaultKinds)(key, raw)
			if err != nil {
				return nil, err
			}
			sets = append(sets, "kind = "+arg(v))
		case "expires_at":
			v, err := pNullableDate(key, raw)
			if err != nil {
				return nil, err
			}
			sets = append(sets, "expires_at = "+arg(v))
		case "tags":
			v, err := pStrings(20, 60)(key, raw)
			if err != nil {
				return nil, err
			}
			sets = append(sets, "tags = "+arg(v))
		case "fields":
			var m map[string]string
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, invalid(key, "must be an object of strings")
			}
			m, err := cleanFields(m)
			if err != nil {
				return nil, err
			}
			sets = append(sets, "fields = "+arg(m))
		case "project_key":
			var k *string
			if err := json.Unmarshal(raw, &k); err != nil {
				return nil, invalid(key, "must be a project key or null")
			}
			pid, err := s.vaultProjectID(ctx, s.DB, k)
			if err != nil {
				return nil, err
			}
			sets = append(sets, "project_id = "+arg(pid))
		case "secret":
			if isNull(raw) {
				sets = append(sets, "secret_sealed = NULL", "secret_keys = '[]'")
				continue
			}
			if !box.Available() {
				return nil, vault.ErrUnavailable
			}
			var m map[string]string
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, invalid(key, "must be an object of strings")
			}
			m, err := cleanSecret(m)
			if err != nil {
				return nil, err
			}
			plain, _ := json.Marshal(m)
			sealed, err := box.Seal(plain, aad)
			if err != nil {
				return nil, err
			}
			sets = append(sets, "secret_sealed = "+arg(sealed), "secret_keys = "+arg(secretKeys(m)))
		case "file":
			if isNull(raw) {
				sets = append(sets, "file_sealed = NULL", "file_name = ''", "file_size = 0")
				continue
			}
			if !box.Available() {
				return nil, vault.ErrUnavailable
			}
			var f VaultFile
			if err := json.Unmarshal(raw, &f); err != nil {
				return nil, invalid(key, "must be {name, content_base64}")
			}
			fname, data, err := decodeFile(&f)
			if err != nil {
				return nil, err
			}
			sealed, err := box.Seal(data, fileAAD(aad))
			if err != nil {
				return nil, err
			}
			sets = append(sets, "file_sealed = "+arg(sealed), "file_name = "+arg(fname), "file_size = "+arg(len(data)))
		case "id", "project_color", "secret_keys", "has_file", "file_name", "file_size", "created_at", "updated_at",
			"last_revealed_at":
		default:
			return nil, invalid(key, "unknown or read-only field")
		}
	}
	if len(sets) > 0 {
		err := s.tx(ctx, func(tx pgxTx) error {
			if _, err := tx.Exec(ctx, `UPDATE vault_items SET `+strings.Join(sets, ", ")+`, updated_at = now() WHERE id = $1`,
				append([]any{id}, args...)...); err != nil {
				return mapErr(err)
			}
			return auditVault(ctx, tx, &id, name, "update", ip)
		})
		if err != nil {
			return nil, err
		}
	}
	return s.VaultByID(ctx, id)
}

func (s *Store) DeleteVault(ctx context.Context, id int64, ip string) error {
	return s.tx(ctx, func(tx pgxTx) error {
		var name string
		if err := tx.QueryRow(ctx, `DELETE FROM vault_items WHERE id = $1 RETURNING name`, id).Scan(&name); err != nil {
			return mapErr(err)
		}
		return auditVault(ctx, tx, nil, name, "delete", ip)
	})
}

// RevealVault opens an item's secret fields and records who asked.
func (s *Store) RevealVault(ctx context.Context, box *vault.Box, id int64, ip string) (map[string]string, error) {
	var aad, sealed []byte
	var name string
	if err := s.DB.QueryRow(ctx, `SELECT aad, secret_sealed, name FROM vault_items WHERE id = $1`, id).
		Scan(&aad, &sealed, &name); err != nil {
		return nil, mapErr(err)
	}
	out := map[string]string{}
	if sealed != nil {
		plain, err := box.Open(sealed, aad)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(plain, &out); err != nil {
			return nil, err
		}
	}
	return out, s.touchReveal(ctx, id, name, "reveal", ip)
}

func (s *Store) VaultFileContent(ctx context.Context, box *vault.Box, id int64, ip string) (string, []byte, error) {
	var aad, sealed []byte
	var name, fileName string
	if err := s.DB.QueryRow(ctx, `SELECT aad, file_sealed, name, file_name FROM vault_items WHERE id = $1`, id).
		Scan(&aad, &sealed, &name, &fileName); err != nil {
		return "", nil, mapErr(err)
	}
	if sealed == nil {
		return "", nil, ErrNotFound
	}
	data, err := box.Open(sealed, fileAAD(aad))
	if err != nil {
		return "", nil, err
	}
	return fileName, data, s.touchReveal(ctx, id, name, "download", ip)
}

// fileAAD derives the file blob's associated data from the row's, so the
// secret blob and the file blob of one row cannot be swapped either.
func fileAAD(aad []byte) []byte { return append(append([]byte{}, aad...), 'f') }

func (s *Store) touchReveal(ctx context.Context, id int64, name, action, ip string) error {
	return s.tx(ctx, func(tx pgxTx) error {
		if _, err := tx.Exec(ctx, `UPDATE vault_items SET last_revealed_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		return auditVault(ctx, tx, &id, name, action, ip)
	})
}

func auditVault(ctx context.Context, q querier, id *int64, name, action, ip string) error {
	_, err := q.Exec(ctx, `INSERT INTO vault_audit (item_id, item_name, action, ip) VALUES ($1, $2, $3, $4)`,
		id, name, action, ip)
	return err
}

func (s *Store) ListVaultAudit(ctx context.Context, limit int) ([]VaultAudit, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `SELECT id, item_id, item_name, action, ip, at FROM vault_audit
		ORDER BY at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VaultAudit{}
	for rows.Next() {
		var a VaultAudit
		if err := rows.Scan(&a.ID, &a.ItemID, &a.ItemName, &a.Action, &a.IP, &a.At); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// IntegrationSecret returns the `value` of the newest vault item carrying the
// tag (e.g. "integration:grafana"), for the server's own use. Not audited as
// a reveal: nothing leaves the server.
func (s *Store) IntegrationSecret(ctx context.Context, box *vault.Box, tag string) (string, error) {
	var aad, sealed []byte
	err := s.DB.QueryRow(ctx, `SELECT aad, secret_sealed FROM vault_items
		WHERE EXISTS (SELECT 1 FROM json_each(tags) WHERE value = $1) AND secret_sealed IS NOT NULL ORDER BY updated_at DESC LIMIT 1`, tag).Scan(&aad, &sealed)
	if err != nil {
		return "", mapErr(err)
	}
	plain, err := box.Open(sealed, aad)
	if err != nil {
		return "", err
	}
	var m map[string]string
	if err := json.Unmarshal(plain, &m); err != nil {
		return "", err
	}
	return strings.TrimSpace(m["value"]), nil
}

// ExpiringVault lists items whose expiry falls before `before` (YYYY-MM-DD).
func (s *Store) ExpiringVault(ctx context.Context, before string) ([]VaultItem, error) {
	rows, err := s.DB.Query(ctx, vaultSelect+` WHERE v.expires_at IS NOT NULL AND v.expires_at < $1
		ORDER BY v.expires_at`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VaultItem{}
	for rows.Next() {
		v, err := scanVault(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// SaveIntegrationSecret stores value as the `value` secret of the vault item
// the server reads for tag (IntegrationSecret): the newest item with that tag
// is updated, or one is created. Audited like any vault change.
func (s *Store) SaveIntegrationSecret(ctx context.Context, box *vault.Box, name, tag, value, ip string) error {
	if !box.Available() {
		return vault.ErrUnavailable
	}
	var id int64
	err := s.DB.QueryRow(ctx, `SELECT id FROM vault_items WHERE EXISTS (SELECT 1 FROM json_each(tags) WHERE value = $1)
		ORDER BY updated_at DESC LIMIT 1`, tag).Scan(&id)
	secret, _ := json.Marshal(map[string]string{"value": value})
	switch {
	case err == nil:
		_, err = s.UpdateVault(ctx, box, id, Patch{"secret": secret}, ip)
		return err
	case errors.Is(mapErr(err), ErrNotFound):
		_, err = s.CreateVault(ctx, box, VaultInput{Name: name, Kind: "api_key", Tags: []string{tag},
			Secret: map[string]string{"value": value}, Notes: "Used by the Forge server (" + tag + ")."}, ip)
		return err
	default:
		return err
	}
}
