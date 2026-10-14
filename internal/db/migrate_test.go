package db

import (
	"context"
	"path/filepath"
	"testing"
)

// The vault rebuild (0011) keeps every row, and the audit rows that point at
// them, when it runs over existing data.
func TestVaultRebuildKeepsRows(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO vault_items (aad, name, kind, secret_sealed, file_name, file_size, file_sealed, notes, tags)
		VALUES (x'0102', 'deploy key', 'private_key', x'aabb', 'id_ed25519', 2, x'ccdd', 'n', '["a"]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO vault_audit (item_id, item_name, action) VALUES (1, 'deploy key', 'create')`); err != nil {
		t.Fatal(err)
	}
	// Run the rebuild again over the data.
	if _, err := d.Exec(ctx, `DELETE FROM schema_migrations WHERE name = '0011_vault_repo_files.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	var name, kind, fileName, tags string
	var secret, file []byte
	if err := d.QueryRow(ctx, `SELECT name, kind, secret_sealed, file_name, file_sealed, tags FROM vault_items WHERE id = 1`).
		Scan(&name, &kind, &secret, &fileName, &file, &tags); err != nil {
		t.Fatal(err)
	}
	if name != "deploy key" || kind != "private_key" || string(secret) != "\xaa\xbb" || fileName != "id_ed25519" ||
		string(file) != "\xcc\xdd" || tags != `["a"]` {
		t.Fatalf("row = %q %q %x %q %x %q", name, kind, secret, fileName, file, tags)
	}
	var audits int
	if err := d.QueryRow(ctx, `SELECT count(*) FROM vault_audit WHERE item_id = 1`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit rows = %d, %v", audits, err)
	}
	// The new kind and the one-file-per-path rule.
	if _, err := d.Exec(ctx, `INSERT INTO vault_items (aad, name, kind) VALUES (x'03', 'config', 'file')`); err != nil {
		t.Fatal(err)
	}
	// ON DELETE SET NULL on the audit's item still works against the rebuilt table.
	if _, err := d.Exec(ctx, `DELETE FROM vault_items WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	var orphan *int64
	if err := d.QueryRow(ctx, `SELECT item_id FROM vault_audit LIMIT 1`).Scan(&orphan); err != nil || orphan != nil {
		t.Fatalf("audit item_id = %v, %v", orphan, err)
	}
}
