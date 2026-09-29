// Package db is Forge's storage engine: one SQLite file in the workspace,
// opened through the pure-Go modernc driver (no cgo, so the binary
// cross-compiles), plus the embedded migrations.
//
// The store writes Postgres-flavoured placeholders ($1, $2 …) and relies on a
// few conventions this package makes true:
//
//   - $N is rewritten to SQLite's ?N, so a placeholder may repeat or appear
//     out of order.
//   - Timestamps are TEXT in one fixed-width UTC format (TimeFormat): Go
//     time.Time arguments are written that way, and now(), ts_add() and the
//     schema's column defaults produce the same, so comparing text compares
//     time.
//   - Slices, maps and structs passed as arguments are stored as JSON text;
//     scanning into them decodes it. []byte stays a BLOB.
//   - Scanning TEXT into time.Time / *time.Time parses TimeFormat (and the
//     date-only form).
package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// TimeFormat is how every timestamp is stored.
const TimeFormat = "2006-01-02T15:04:05.000Z"

// ErrNoRows is returned by Row.Scan when the query matched nothing.
var ErrNoRows = sql.ErrNoRows

// DB is the database handle. Its Exec/Query/QueryRow mirror a Tx's, so store
// helpers take either (Querier).
type DB struct {
	sql  *sql.DB
	path string
}

type Querier interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Query(ctx context.Context, query string, args ...any) (*Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *Row
}

// Open opens (creating if needed) the database file at path. WAL lets reads
// go on during a write; BEGIN IMMEDIATE (the _txlock) takes the write lock at
// the start of a transaction, so two writers wait for each other instead of
// deadlocking on a lock upgrade; busy_timeout is how long they wait.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	sqldb, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	sqldb.SetMaxOpenConns(8)
	sqldb.SetConnMaxIdleTime(5 * time.Minute)
	if err := sqldb.PingContext(ctx); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	_ = os.Chmod(path, 0o600)
	return &DB{sql: sqldb, path: path}, nil
}

func (d *DB) Close() error                   { return d.sql.Close() }
func (d *DB) Ping(ctx context.Context) error { return d.sql.PingContext(ctx) }
func (d *DB) Path() string                   { return d.path }

// SQL is the underlying handle, for tools that need database/sql itself.
func (d *DB) SQL() *sql.DB { return d.sql }

func (d *DB) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	res, err := d.sql.ExecContext(ctx, rebind(query), convertArgs(args)...)
	return result(res, err)
}

func (d *DB) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	rows, err := d.sql.QueryContext(ctx, rebind(query), convertArgs(args)...)
	if err != nil {
		return nil, err
	}
	return &Rows{rows}, nil
}

func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *Row {
	return &Row{d.sql.QueryRowContext(ctx, rebind(query), convertArgs(args)...)}
}

// Tx is a transaction; it holds SQLite's write lock from its first statement.
type Tx struct{ tx *sql.Tx }

func (t *Tx) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	res, err := t.tx.ExecContext(ctx, rebind(query), convertArgs(args)...)
	return result(res, err)
}

func (t *Tx) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	rows, err := t.tx.QueryContext(ctx, rebind(query), convertArgs(args)...)
	if err != nil {
		return nil, err
	}
	return &Rows{rows}, nil
}

func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *Row {
	return &Row{t.tx.QueryRowContext(ctx, rebind(query), convertArgs(args)...)}
}

// InTx runs fn in a transaction: committed if fn returns nil, rolled back
// otherwise.
func (d *DB) InTx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&Tx{tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Backup writes a consistent copy of the database to path (VACUUM INTO: a
// compact snapshot, safe while the server runs).
func (d *DB) Backup(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Remove(path)
	if _, err := d.sql.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// Result is what Exec returns.
type Result struct{ rows int64 }

func (r Result) RowsAffected() int64 { return r.rows }

func result(res sql.Result, err error) (Result, error) {
	if err != nil {
		return Result{}, err
	}
	n, _ := res.RowsAffected()
	return Result{n}, nil
}

// Rows wraps sql.Rows so Scan understands the storage conventions.
type Rows struct{ *sql.Rows }

func (r *Rows) Scan(dest ...any) error { return r.Rows.Scan(adaptDest(dest)...) }

// Row is a single-row result.
type Row struct{ row *sql.Row }

func (r *Row) Scan(dest ...any) error { return r.row.Scan(adaptDest(dest)...) }

// ── Placeholders and arguments ────────────────────────────────────────────

var (
	placeholder = regexp.MustCompile(`\$(\d+)`)
	rebound     sync.Map // query → rewritten query
)

// rebind rewrites $N to ?N. The store never puts a literal "$<digit>" in SQL
// text (values always travel as arguments), so a plain replace is safe.
func rebind(q string) string {
	if v, ok := rebound.Load(q); ok {
		return v.(string)
	}
	out := placeholder.ReplaceAllString(q, "?$1")
	rebound.Store(q, out)
	return out
}

var (
	timeType  = reflect.TypeOf(time.Time{})
	bytesType = reflect.TypeOf([]byte(nil))
	rawType   = reflect.TypeOf(json.RawMessage(nil))
)

func convertArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = convertArg(a)
	}
	return out
}

func convertArg(a any) any {
	switch v := a.(type) {
	case nil:
		return nil
	case time.Time:
		return v.UTC().Format(TimeFormat)
	case *time.Time:
		if v == nil {
			return nil
		}
		return v.UTC().Format(TimeFormat)
	case json.RawMessage:
		if v == nil {
			return nil
		}
		return string(v)
	case []byte, string, int, int32, int64, float64, bool:
		return v
	case driver.Valuer:
		return v
	}
	rv := reflect.ValueOf(a)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		return convertArg(rv.Elem().Interface())
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.Struct:
		if rv.Kind() == reflect.Slice && rv.IsNil() && rv.Type().Elem().Kind() != reflect.Uint8 {
			return "[]"
		}
		b, err := json.Marshal(a)
		if err != nil {
			return a // let the driver report it
		}
		return string(b)
	case reflect.String:
		return rv.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(rv.Uint())
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	case reflect.Bool:
		return rv.Bool()
	}
	return a
}

// ── Scanning ──────────────────────────────────────────────────────────────

func adaptDest(dest []any) []any {
	out := make([]any, len(dest))
	for i, d := range dest {
		out[i] = adapt(d)
	}
	return out
}

func adapt(d any) any {
	switch d.(type) {
	case *time.Time, **time.Time:
		return timeScanner{d}
	case *string, **string, *int, *int64, *int32, **int, **int64, **int32, *float64, **float64,
		*bool, **bool, *[]byte, *any, sql.Scanner:
		return d
	case *json.RawMessage:
		return jsonScanner{d}
	}
	rv := reflect.ValueOf(d)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return d
	}
	switch rv.Elem().Kind() {
	case reflect.Slice, reflect.Map, reflect.Struct, reflect.Array:
		if rv.Elem().Type() == bytesType {
			return d
		}
		return jsonScanner{d}
	case reflect.Pointer:
		switch rv.Elem().Type().Elem().Kind() {
		case reflect.Slice, reflect.Map, reflect.Struct:
			return jsonScanner{d}
		}
	}
	return d
}

// ParseTime reads a stored timestamp (TimeFormat, RFC 3339, or a bare date).
func ParseTime(s string) (time.Time, error) {
	for _, layout := range []string{TimeFormat, time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("not a timestamp: %q", s)
}

type timeScanner struct{ dest any }

func (t timeScanner) Scan(src any) error {
	var tm time.Time
	switch v := src.(type) {
	case nil:
		switch d := t.dest.(type) {
		case **time.Time:
			*d = nil
			return nil
		case *time.Time:
			return errors.New("NULL into a non-nullable time")
		}
	case time.Time:
		tm = v.UTC()
	case string:
		p, err := ParseTime(v)
		if err != nil {
			return err
		}
		tm = p
	case []byte:
		p, err := ParseTime(string(v))
		if err != nil {
			return err
		}
		tm = p
	default:
		return fmt.Errorf("cannot scan %T into a time", src)
	}
	switch d := t.dest.(type) {
	case *time.Time:
		*d = tm
	case **time.Time:
		*d = &tm
	}
	return nil
}

type jsonScanner struct{ dest any }

func (j jsonScanner) Scan(src any) error {
	var raw []byte
	switch v := src.(type) {
	case nil:
		rv := reflect.ValueOf(j.dest).Elem()
		rv.Set(reflect.Zero(rv.Type()))
		return nil
	case string:
		raw = []byte(v)
	case []byte:
		raw = append([]byte(nil), v...)
	default:
		return fmt.Errorf("cannot scan %T as JSON", src)
	}
	if r, ok := j.dest.(*json.RawMessage); ok {
		*r = raw
		return nil
	}
	return json.Unmarshal(raw, j.dest)
}

// ── SQL functions ─────────────────────────────────────────────────────────

var locations sync.Map // name → *time.Location

func location(name string) *time.Location {
	if v, ok := locations.Load(name); ok {
		return v.(*time.Location)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc = time.UTC
	}
	locations.Store(name, loc)
	return loc
}

func argTime(v driver.Value) (time.Time, bool) {
	switch x := v.(type) {
	case string:
		t, err := ParseTime(x)
		return t, err == nil
	case []byte:
		t, err := ParseTime(string(x))
		return t, err == nil
	case time.Time:
		return x.UTC(), true
	}
	return time.Time{}, false
}

func argFloat(v driver.Value) float64 {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	}
	return 0
}

func argString(v driver.Value) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return ""
}

func init() {
	// now() — the current time in TimeFormat.
	sqlite.MustRegisterScalarFunction("now", 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		return time.Now().UTC().Format(TimeFormat), nil
	})
	// ts_add(ts, seconds) — ts moved by a (possibly negative, fractional)
	// number of seconds.
	sqlite.MustRegisterDeterministicScalarFunction("ts_add", 2, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		t, ok := argTime(a[0])
		if !ok {
			return nil, nil
		}
		return t.Add(time.Duration(argFloat(a[1]) * float64(time.Second))).Format(TimeFormat), nil
	})
	// ts_norm(text) — any RFC 3339 timestamp (a runner's git dates carry
	// their own offset) in TimeFormat, so it compares with stored times.
	sqlite.MustRegisterDeterministicScalarFunction("ts_norm", 1, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		t, ok := argTime(a[0])
		if !ok {
			return nil, nil
		}
		return t.Format(TimeFormat), nil
	})
	// epoch(ts) — seconds since 1970, for durations and averages.
	sqlite.MustRegisterDeterministicScalarFunction("epoch", 1, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		t, ok := argTime(a[0])
		if !ok {
			return nil, nil
		}
		return float64(t.UnixNano()) / 1e9, nil
	})
	// local_date(ts, zone) — the calendar date ("YYYY-MM-DD") of ts in an
	// IANA time zone: "today" is always the user's, never UTC's.
	sqlite.MustRegisterDeterministicScalarFunction("local_date", 2, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		t, ok := argTime(a[0])
		if !ok {
			return nil, nil
		}
		return t.In(location(argString(a[1]))).Format("2006-01-02"), nil
	})
	// local_hour(ts, zone) — hour of day in a zone (0-23).
	sqlite.MustRegisterDeterministicScalarFunction("local_hour", 2, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		t, ok := argTime(a[0])
		if !ok {
			return nil, nil
		}
		return int64(t.In(location(argString(a[1]))).Hour()), nil
	})
}

// ── Migrations ────────────────────────────────────────────────────────────

// Migrate applies every embedded migration that has not run yet, in name
// order, each in its own transaction.
func Migrate(ctx context.Context, d *DB) error {
	if _, err := d.sql.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		var done bool
		if err := d.QueryRow(ctx, `SELECT exists(SELECT 1 FROM schema_migrations WHERE name = $1)`, name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if strings.HasPrefix(string(body), fkOffDirective) {
			if err := migrateWithoutForeignKeys(ctx, d, name, string(body)); err != nil {
				return fmt.Errorf("migration %s: %w", name, err)
			}
			continue
		}
		err = d.InTx(ctx, func(tx *Tx) error {
			for _, stmt := range splitStatements(string(body)) {
				if _, err := tx.tx.ExecContext(ctx, stmt); err != nil {
					return err
				}
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name)
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

// fkOffDirective, as a migration's first line, runs it with foreign keys
// off: SQLite's way to rebuild a table (create new, copy, drop, rename)
// without the drop cascading into the tables that reference it. The pragma
// only takes effect outside a transaction, so the migration gets a
// connection of its own; foreign_key_check must come back clean before it
// commits.
const fkOffDirective = "-- forge:foreign-keys-off"

func migrateWithoutForeignKeys(ctx context.Context, d *DB, name, body string) error {
	conn, err := d.sql.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`) //nolint:errcheck
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, stmt := range splitStatements(body) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	broken := rows.Next()
	rows.Close()
	if broken {
		return errors.New("foreign key check failed after the migration")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
		return err
	}
	return tx.Commit()
}

// splitStatements cuts a migration file at the semicolons that end
// statements (outside quotes and -- comments).
func splitStatements(sqlText string) []string {
	var out []string
	var cur strings.Builder
	inQuote, inComment := false, false
	for i := 0; i < len(sqlText); i++ {
		c := sqlText[i]
		switch {
		case inComment:
			if c == '\n' {
				inComment = false
			}
			continue
		case inQuote:
			if c == '\'' {
				inQuote = false
			}
		case c == '-' && i+1 < len(sqlText) && sqlText[i+1] == '-':
			inComment = true
			continue
		case c == '\'':
			inQuote = true
		case c == ';':
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// ── Errors ────────────────────────────────────────────────────────────────

// Constraint describes a failed constraint, from SQLite's error text.
type Constraint struct {
	Kind string // "unique", "check", "foreign_key", "not_null"
	// Name is the constraint or index name when SQLite reports one, else
	// "<table>_<col>[_<col>…]".
	Name string
}

var constraintRe = regexp.MustCompile(`(UNIQUE|CHECK|FOREIGN KEY|NOT NULL) constraint failed(?::\s*(.*))?`)

// AsConstraint reports whether err is a constraint violation, and which.
func AsConstraint(err error) (Constraint, bool) {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return Constraint{}, false
	}
	m := constraintRe.FindStringSubmatch(se.Error())
	if m == nil {
		return Constraint{}, false
	}
	c := Constraint{Kind: strings.ToLower(strings.ReplaceAll(m[1], " ", "_"))}
	detail := strings.TrimSpace(m[2])
	if cut := strings.Index(detail, " ("); cut >= 0 {
		detail = detail[:cut]
	}
	switch {
	case strings.HasPrefix(detail, "index '"):
		c.Name = strings.TrimSuffix(strings.TrimPrefix(detail, "index '"), "'")
	case strings.Contains(detail, "."):
		// "repos.project_id, repos.name" → "repos_project_id_name"
		var table string
		var cols []string
		for _, part := range strings.Split(detail, ",") {
			t, col, _ := strings.Cut(strings.TrimSpace(part), ".")
			table = t
			cols = append(cols, col)
		}
		c.Name = table + "_" + strings.Join(cols, "_")
	default:
		c.Name = detail
	}
	return c, true
}
