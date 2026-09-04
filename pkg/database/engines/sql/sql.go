// Package sql backs the `sql` data model with any database/sql driver.
//
// # The driver is not a dependency of this package
//
// database/sql is a registry: a driver registers itself from its own init, and
// whoever composes a node imports the one it wants —
//
//	import _ "github.com/jackc/pgx/v5/stdlib"   // registers "pgx"
//	registry.Register(ctx, sql.Postgres("pgx"))
//
// So this repository carries no driver, and adding Postgres to a deployment is
// an import in that deployment rather than a dependency everybody links whether
// they use it or not. It is also the only shape that lets an operator swap
// `pgx` for `postgres`, or point at a proxy, without this package knowing.
//
// # What an engine is allowed to be
//
// The vocabulary names which engines back a type — `sql` is backed by
// `postgres` and `mysql` — and pkg/database's Registry decides what a node can
// do by what got registered. An engine here is therefore a NAME plus a way to
// reach it, and nothing about it is a policy: a build that registers a managed
// Postgres beside these is a registration, not a fork.
//
// # A connection string is built, never taken
//
// A Spec carries an address, a database name and credentials, and this turns
// them into a DSN. It does not accept a DSN, and that is deliberate: a DSN is a
// place to put `sslmode=disable`, and TLS is a field on Credentials precisely so
// that turning it off is a thing somebody wrote rather than a default nobody
// read.
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

// New builds an engine over a registered database/sql driver.
//
// engine is the name the vocabulary uses (`postgres`); driver is the name the
// driver registered itself under (`pgx`). They differ often enough that
// conflating them would make swapping the driver a schema change.
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

// Name is the engine name the vocabulary uses.
func (e *Engine) Name() string { return e.name }

// Types is sql and only sql. A driver that also answered document queries would
// be claiming a data model whose verbs it does not have.
func (e *Engine) Types() []database.Type { return []database.Type{database.SQL} }

// Open connects.
//
// IT DOES NOT PING. database/sql opens lazily and a pool that has not been used
// has nothing to say; the first query is what finds a server that is not there,
// and it reports it to the caller that asked rather than to whoever happened to
// be composing the node.
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

// PostgresDSN builds libpq's URL form.
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
	return &rows{Rows: r}, nil
}

// Exec answers how many rows changed.
//
// A DRIVER THAT CANNOT SAY IS NOT AN ERROR. Several do not support
// RowsAffected, and failing a statement that ran because its result cannot be
// counted would be reporting success as failure.
func (c *conn) Exec(ctx context.Context, statement string, args ...any) (int64, error) {
	result, err := c.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return 0, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return affected, nil
}

func (c *conn) Close() error { return c.db.Close() }

// rows adapts database/sql's result set to the seam's, which is deliberately
// not database/sql's: pkg/database has no driver dependency, and this wrapper
// is what that costs.
type rows struct{ *sql.Rows }

func (r *rows) Columns() ([]string, error) { return r.Rows.Columns() }
