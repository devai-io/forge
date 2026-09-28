// Package store holds all SQL. Handlers never see a query; services above
// this do not exist because there is exactly one caller per operation.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/devai-io/forge/internal/db"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// ValidationError is a client mistake in one named field. The API renders it
// as 422 {"error":{"code":"validation","field":…}}.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// pgxTx shortens callback signatures (the name predates SQLite).
type pgxTx = *db.Tx

type Store struct {
	DB *db.DB
	// PublicURL is the app's own address, for links written into text
	// (Claude context). Set by the server; empty in tests.
	PublicURL string
}

func New(d *db.DB) *Store { return &Store{DB: d} }

// querier is what both the pool and a transaction offer, so helpers that log
// activity can run inside either.
type querier = db.Querier

func (s *Store) tx(ctx context.Context, fn func(pgxTx) error) error {
	return s.DB.InTx(ctx, fn)
}

// mapErr turns driver errors into the store's own vocabulary.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, db.ErrNoRows) {
		return ErrNotFound
	}
	if c, ok := db.AsConstraint(err); ok {
		switch c.Kind {
		case "unique":
			return fmt.Errorf("%w: %s", ErrConflict, uniqueWhat(c.Name))
		case "check":
			return &ValidationError{Field: checkField(c.Name), Message: "invalid value"}
		case "foreign_key":
			return &ValidationError{Field: "reference", Message: "refers to something that does not exist"}
		}
	}
	return err
}

func uniqueWhat(name string) string {
	switch {
	case strings.Contains(name, "projects_key"):
		return "a project with that key already exists"
	case strings.Contains(name, "repos_project_id_name"):
		return "this project already has a repo with that name"
	case strings.Contains(name, "servers_name"):
		return "a server with that name already exists"
	case strings.Contains(name, "runners_name"):
		return "a machine with that name already exists"
	case strings.Contains(name, "runners_one_master"):
		return "there is already a master machine"
	case strings.Contains(name, "users_email"):
		return "that e-mail address is taken"
	case strings.Contains(name, "users_username"):
		return "that username is taken"
	}
	return "already exists"
}

// checkField recovers the column from the schema's CHECK constraint names,
// "<table>_<column>_check".
func checkField(name string) string {
	name = strings.TrimSuffix(name, "_check")
	for _, table := range []string{"users", "projects", "repos", "servers", "endpoints", "tasks", "comments",
		"runners", "runs", "run_events", "vault_items", "vault_audit", "checkups"} {
		if rest, ok := strings.CutPrefix(name, table+"_"); ok {
			return rest
		}
	}
	return name
}

// ── PATCH support ─────────────────────────────────────────────────────────

// Patch is a decoded partial-update body: only keys present are changed.
type Patch map[string]json.RawMessage

func (p Patch) Has(key string) bool { _, ok := p[key]; return ok }

// field describes one patchable column: its JSON key's column and how to
// turn the raw JSON into a SQL argument (validating on the way).
type field struct {
	column string
	parse  func(key string, raw json.RawMessage) (any, error)
}

// updateSet builds "col1 = $n, col2 = $n+1" for the keys present in the
// patch, rejecting unknown keys so a typo is an error instead of a no-op.
func updateSet(p Patch, fields map[string]field, ignore []string, argStart int) (string, []any, error) {
	var sets []string
	var args []any
	for key, raw := range p {
		f, ok := fields[key]
		if !ok {
			if OneOf(key, ignore) {
				continue
			}
			return "", nil, invalid(key, "unknown or read-only field")
		}
		v, err := f.parse(key, raw)
		if err != nil {
			return "", nil, err
		}
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", f.column, argStart+len(args)-1))
	}
	return strings.Join(sets, ", "), args, nil
}

func isNull(raw json.RawMessage) bool { return string(raw) == "null" }

func pString(maxLen int, required bool) func(string, json.RawMessage) (any, error) {
	return func(key string, raw json.RawMessage) (any, error) {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, invalid(key, "must be a string")
		}
		v = strings.TrimSpace(v)
		if required && v == "" {
			return nil, invalid(key, "must not be empty")
		}
		if len(v) > maxLen {
			return nil, invalid(key, "must be at most %d characters", maxLen)
		}
		return v, nil
	}
}

func pEnum(allowed []string) func(string, json.RawMessage) (any, error) {
	return func(key string, raw json.RawMessage) (any, error) {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil || !OneOf(v, allowed) {
			return nil, invalid(key, "must be one of %s", strings.Join(allowed, ", "))
		}
		return v, nil
	}
}

func pInt(min, max int) func(string, json.RawMessage) (any, error) {
	return func(key string, raw json.RawMessage) (any, error) {
		var v int
		if err := json.Unmarshal(raw, &v); err != nil || v < min || v > max {
			return nil, invalid(key, "must be an integer between %d and %d", min, max)
		}
		return v, nil
	}
}

func pNullableInt(min, max int) func(string, json.RawMessage) (any, error) {
	return func(key string, raw json.RawMessage) (any, error) {
		if isNull(raw) {
			return nil, nil
		}
		return pInt(min, max)(key, raw)
	}
}

func pBool(key string, raw json.RawMessage) (any, error) {
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, invalid(key, "must be true or false")
	}
	return v, nil
}

func pFloat(key string, raw json.RawMessage) (any, error) {
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, invalid(key, "must be a number")
	}
	return v, nil
}

func pNullableDate(key string, raw json.RawMessage) (any, error) {
	if isNull(raw) {
		return nil, nil
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil || !IsDate(v) {
		return nil, invalid(key, "must be a YYYY-MM-DD date or null")
	}
	return v, nil
}

func pStrings(maxItems, maxLen int) func(string, json.RawMessage) (any, error) {
	return func(key string, raw json.RawMessage) (any, error) {
		var v []string
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, invalid(key, "must be a list of strings")
		}
		return CleanStrings(key, v, maxItems, maxLen)
	}
}

func pClock(key string, raw json.RawMessage) (any, error) {
	var v string
	if err := json.Unmarshal(raw, &v); err != nil || len(v) != 5 || v[2] != ':' ||
		v[0] < '0' || v[0] > '2' || v[1] < '0' || v[1] > '9' || v[3] < '0' || v[3] > '5' || v[4] < '0' || v[4] > '9' ||
		(v[0] == '2' && v[1] > '3') {
		return nil, invalid(key, "must be a HH:MM time")
	}
	return v, nil
}

func pColor(key string, raw json.RawMessage) (any, error) {
	var v string
	if err := json.Unmarshal(raw, &v); err != nil || !IsColor(v) {
		return nil, invalid(key, "must be a #rrggbb colour")
	}
	return strings.ToLower(v), nil
}

// AccentPresets are the named accent colours the web app offers.
var AccentPresets = []string{"indigo", "blue", "sky", "teal", "green", "lime", "amber", "orange", "red", "rose", "pink", "violet"}

func pAccent(key string, raw json.RawMessage) (any, error) {
	var v string
	if err := json.Unmarshal(raw, &v); err != nil || !(v == "" || OneOf(v, AccentPresets) || IsColor(v)) {
		return nil, invalid(key, "must be empty, a preset (%s) or a #rrggbb colour", strings.Join(AccentPresets, ", "))
	}
	return strings.ToLower(v), nil
}

func pLinks(key string, raw json.RawMessage) (any, error) {
	var v []Link
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, invalid(key, "must be a list of {label, url}")
	}
	return CleanLinks(v)
}

// CleanStrings trims, drops empties and duplicates, and bounds a string list.
func CleanStrings(field string, in []string, maxItems, maxLen int) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		if len(s) > maxLen {
			return nil, invalid(field, "each entry must be at most %d characters", maxLen)
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) > maxItems {
		return nil, invalid(field, "at most %d entries", maxItems)
	}
	return out, nil
}

func CleanLinks(in []Link) ([]Link, error) {
	out := make([]Link, 0, len(in))
	for _, l := range in {
		l.Label, l.URL = strings.TrimSpace(l.Label), strings.TrimSpace(l.URL)
		if l.URL == "" {
			continue
		}
		if !strings.HasPrefix(l.URL, "http://") && !strings.HasPrefix(l.URL, "https://") {
			return nil, invalid("links", "URLs must start with http:// or https://")
		}
		if l.Label == "" {
			l.Label = l.URL
		}
		out = append(out, l)
	}
	if len(out) > 30 {
		return nil, invalid("links", "at most 30 links")
	}
	return out, nil
}

func IsDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, c := range s {
		if i == 4 || i == 7 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func IsColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range strings.ToLower(s[1:]) {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func IsProjectKey(s string) bool {
	if len(s) < 2 || len(s) > 10 || s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// nonNil keeps JSON arrays as [] instead of null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
