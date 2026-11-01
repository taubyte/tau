// Package sql backs the `sql` data model with any database/sql driver. The driver is
// the composer's import:
//
//	import _ "github.com/jackc/pgx/v5/stdlib"   // registers "pgx"
//	registry.Register(ctx, sql.Postgres("pgx"))
//
// so this repository carries none. A connection string is built from the Spec, never
// accepted, so TLS off is something somebody wrote (Credentials.TLS), not a hidden
// `sslmode=disable`.
package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/taubyte/tau/pkg/database"
)

// Engine backs the sql type over one database/sql driver.
type Engine struct {
	name   string
	driver string
	dsn    DSN
}

// DSN turns a spec into a connection string for one driver's dialect.
type DSN func(database.Spec) (string, error)

var (
	ErrNoName    = errors.New("sql: an engine with no name cannot be selected by a config")
	ErrNoDriver  = errors.New("sql: an engine with no driver cannot connect")
	ErrNoAddress = errors.New("sql: an external database needs an address")
	ErrWrongType = errors.New("sql: this engine backs sql and nothing else")
)

// New builds an engine over a registered database/sql driver. engine is the
// vocabulary's name (`postgres`), driver the registered one (`pgx`).
func New(engine, driver string, dsn DSN) (*Engine, error) {
	if engine == "" {
		return nil, ErrNoName
	}
	if driver == "" {
		return nil, ErrNoDriver
	}
	if dsn == nil {
		return nil, errors.New("sql: no way to build a connection string")
	}
	return &Engine{name: engine, driver: driver, dsn: dsn}, nil
}

// Postgres is the `postgres` engine over a driver that speaks libpq's URL form
// — `pgx`, `postgres`, or a proxy registered under another name.
func Postgres(driver string) *Engine {
	e, _ := New("postgres", driver, PostgresDSN)
	return e
}

// MySQL is the `mysql` engine over go-sql-driver's DSN form.
func MySQL(driver string) *Engine {
	e, _ := New("mysql", driver, MySQLDSN)
	return e
}

func (e *Engine) Name() string { return e.name }

// Types is sql and only sql. A driver that also answered document queries would
// be claiming a data model whose verbs it does not have.
func (e *Engine) Types() []database.Type { return []database.Type{database.SQL} }

// Open connects without pinging: the first query finds a missing server and reports it
// to the caller who asked.
func (e *Engine) Open(ctx context.Context, spec database.Spec) (database.Conn, error) {
	if spec.Type != database.SQL {
		return nil, fmt.Errorf("%w: was asked for %q", ErrWrongType, spec.Type)
	}

	dsn, err := e.dsn(spec)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(e.driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("sql: opening %s: %w", e.name, err)
	}
	return &conn{db: db}, nil
}

func PostgresDSN(spec database.Spec) (string, error) {
	if spec.Address == "" {
		return "", ErrNoAddress
	}

	u := url.URL{Scheme: "postgres", Host: spec.Address, Path: "/" + spec.Database}
	if spec.Credentials.Username != "" {
		u.User = url.UserPassword(spec.Credentials.Username, spec.Credentials.Password)
	}

	// TLS IS SPELLED OUT IN BOTH DIRECTIONS. libpq's default is `prefer`, which
	// falls back to plaintext without saying so — the exact shape of a security
	// decision made by omission that Credentials.TLS exists to prevent.
	mode := "disable"
	if spec.Credentials.TLS {
		mode = "require"
	}
	u.RawQuery = url.Values{"sslmode": {mode}}.Encode()

	return u.String(), nil
}

// MySQLDSN builds go-sql-driver's form:
// user:password@tcp(host)/database?tls=…
func MySQLDSN(spec database.Spec) (string, error) {
	if spec.Address == "" {
		return "", ErrNoAddress
	}

	var b strings.Builder
	if spec.Credentials.Username != "" {
		b.WriteString(spec.Credentials.Username)
		if spec.Credentials.Password != "" {
			b.WriteString(":" + spec.Credentials.Password)
		}
		b.WriteString("@")
	}
	fmt.Fprintf(&b, "tcp(%s)/%s", spec.Address, spec.Database)

	tls := "false"
	if spec.Credentials.TLS {
		tls = "true"
	}
	b.WriteString("?tls=" + tls)

	return b.String(), nil
}

// conn is a database/sql pool wearing the operation surface the vocabulary
// declares for `sql`: query and exec, and nothing else.
type conn struct{ db *sql.DB }

var _ database.SQLConn = (*conn)(nil)

func (c *conn) Query(ctx context.Context, statement string, args ...any) (database.Rows, error) {
	r, err := c.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	types, err := r.ColumnTypes()
	if err != nil {
		r.Close()
		return nil, err
	}
	binary := make([]bool, len(types))
	for i, t := range types {
		binary[i] = isBinary(t.DatabaseTypeName())
	}
	return &rows{Rows: r, binary: binary}, nil
}

// Exec answers what the statement changed. Each half is asked independently, and a
// driver that cannot say is not an error (PostgreSQL answers LastInsertId with
// RETURNING instead).
func (c *conn) Exec(ctx context.Context, statement string, args ...any) (database.Outcome, error) {
	result, err := c.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return database.Outcome{}, err
	}

	var out database.Outcome
	if affected, err := result.RowsAffected(); err == nil {
		out.Affected = affected
	}
	if id, err := result.LastInsertId(); err == nil {
		out.LastInsertID = id
	}
	return out, nil
}

func (c *conn) Close() error { return c.db.Close() }

// rows adapts database/sql's result set to the seam's. It also says which bytes are
// text: a driver may hand a text column over as []byte (go-sql-driver/mysql does for
// VARCHAR), so the column's declared type decides.
type rows struct {
	*sql.Rows
	binary []bool
}

func (r *rows) Columns() ([]string, error) { return r.Rows.Columns() }

// Scan is database/sql's, turning bytes scanned into an `any` back into a string
// unless the column is binary.
func (r *rows) Scan(dest ...any) error {
	if err := r.Rows.Scan(dest...); err != nil {
		return err
	}
	for i, d := range dest {
		p, ok := d.(*any)
		if !ok || i >= len(r.binary) || r.binary[i] {
			continue
		}
		if b, ok := (*p).([]byte); ok {
			*p = string(b)
		}
	}
	return nil
}

// isBinary is whether a declared column type holds bytes rather than text:
// BLOB and its sizes and BINARY/VARBINARY in MySQL, BYTEA in PostgreSQL.
func isBinary(declared string) bool {
	t := strings.ToUpper(declared)
	return strings.Contains(t, "BLOB") || strings.Contains(t, "BINARY") || t == "BYTEA"
}
