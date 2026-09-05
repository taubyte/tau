package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// database.go backs `taubyte:handler/database` on this node, over pkg/database.
//
// # No driver is linked here, and that is pkg/database's decision rather than
// # this file's
//
// database/sql is a registry: a driver registers itself from its own init, so a
// deployment that wants Postgres imports one and registers the engine —
//
//	import _ "github.com/jackc/pgx/v5/stdlib"
//	node.WithDatabaseEngines(reg)  // reg holds sql.Postgres("pgx")
//
// This repository therefore links no database driver, which is what lets the
// same binary serve a cloud that has one and a cloud that does not. A node with
// nothing registered answers every database call by naming what IS registered,
// which is a message an operator can act on rather than a connection that hangs.
//
// # The credential is resolved here and nowhere earlier
//
// The compiled resource carries a REFERENCE to a secret. It is resolved at the
// moment a connection is opened, through the same vault the worker reads a
// run-time variable from — so a password is never in a spec, which is logged,
// cached, compared and handed to a tier.

// databases is the node's Databases: one registry, one connection per handle.
type databases struct {
	node *Node

	mu   sync.Mutex
	open map[string]database.SQLConn
}

var _ wasmtier.Databases = (*databases)(nil)

var (
	// ErrNoEngines is a node asked for a database with no engine registered.
	ErrNoEngines = errors.New("node: no database engine is registered in this build")

	// ErrNotSQL is a database whose data model this tier does not serve yet.
	ErrNotSQL = errors.New("node: only the sql data model is served")
)

// Query runs a statement that reads.
func (d *databases) Query(ctx context.Context, grant invoke.Grant, statement string, args []any) (wasmtier.Rows, error) {
	conn, err := d.conn(ctx, grant)
	if err != nil {
		return wasmtier.Rows{}, err
	}

	rows, err := conn.Query(ctx, statement, args...)
	if err != nil {
		return wasmtier.Rows{}, refused(err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return wasmtier.Rows{}, refused(err)
	}

	// ROW-MAJOR AND FLAT, which is the shape the ABI carries — see the
	// interface doc for why it is not a list of lists.
	out := wasmtier.Rows{Columns: columns}
	for rows.Next() {
		cells := make([]any, len(columns))
		into := make([]any, len(columns))
		for i := range cells {
			into[i] = &cells[i]
		}
		if err := rows.Scan(into...); err != nil {
			return wasmtier.Rows{}, refused(err)
		}
		out.Values = append(out.Values, cells...)
	}
	if err := rows.Err(); err != nil {
		return wasmtier.Rows{}, refused(err)
	}
	return out, nil
}

// Exec runs a statement that writes and answers with how many rows moved.
func (d *databases) Exec(ctx context.Context, grant invoke.Grant, statement string, args []any) (int64, error) {
	conn, err := d.conn(ctx, grant)
	if err != nil {
		return 0, err
	}

	affected, err := conn.Exec(ctx, statement, args...)
	if err != nil {
		return 0, refused(err)
	}
	return affected, nil
}

// refused marks an error as the SERVER'S answer rather than a failure to reach
// it, so the tier can tell a caller to fix a statement instead of retrying one.
//
// EVERYTHING FROM AN OPEN CONNECTION IS THE SERVER'S. Reaching it already
// succeeded; a driver that lost the connection mid-statement is rare enough
// that reporting it as the server's "no" costs a retry nobody was going to make
// anyway, and the alternative — parsing driver errors to classify them — is a
// table that drifts per driver.
func refused(err error) error {
	return fmt.Errorf("%w: %s", wasmtier.ErrStatementFailed, err.Error())
}

// conn opens this database, once, and keeps it.
//
// A CONNECTION IS KEPT BECAUSE A POOL IS WHAT database/sql ALREADY IS: the
// handle behind it pools, so opening per invocation would discard the pool on
// every request and hand the server a new connection each time.
func (d *databases) conn(ctx context.Context, grant invoke.Grant) (database.SQLConn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if held, ok := d.open[grant.Handle]; ok {
		return held, nil
	}
	if d.node.opts.engines == nil {
		return nil, ErrNoEngines
	}

	spec, err := d.specOf(ctx, grant)
	if err != nil {
		return nil, err
	}

	opened, err := d.node.opts.engines.Open(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("node: opening %s %q: %w", spec.Engine, grant.Label, err)
	}

	sqlConn, ok := opened.(database.SQLConn)
	if !ok {
		opened.Close()
		return nil, fmt.Errorf("%w: %s answers no sql surface", ErrNotSQL, spec.Engine)
	}

	if d.open == nil {
		d.open = map[string]database.SQLConn{}
	}
	d.open[grant.Handle] = sqlConn
	return sqlConn, nil
}

// specOf turns a grant into what pkg/database opens from.
func (d *databases) specOf(ctx context.Context, grant invoke.Grant) (database.Spec, error) {
	model := grant.Settings[databaseTypeSetting]
	if database.Type(model) != database.SQL {
		return database.Spec{}, fmt.Errorf("%w: %q is %q", ErrNotSQL, grant.Label, model)
	}

	project, environment, _ := splitHandle(grant.Handle)
	spec := database.Spec{
		Type:        database.SQL,
		Engine:      grant.Settings[model+"/engine"],
		Project:     project,
		Environment: environment,
		Name:        grant.Label,
		Address:     grant.Settings[model+"/host"],
		Database:    grant.Settings[model+"/database"],
	}

	// TLS IS ON UNLESS THE AUTHOR WROTE `disable`, which is the vocabulary's
	// own polarity: absent means verify-full and turning it off is a word
	// somebody had to write.
	//
	// `require` IS SERVED AS verify-full, which is STRONGER than asked.
	// pkg/database carries a bool and cannot say "encrypted but unverified";
	// serving the weaker of the two would be quietly accepting a network
	// attacker as the server, so the stronger one is what happens — and a
	// server with a certificate this node will not verify fails to connect,
	// loudly, instead of connecting to whoever answered.
	spec.Credentials.TLS = grant.Settings[model+"/tls"] != "disable"

	if reference := grant.Settings[databaseCredentialsSetting]; reference != "" {
		username, password, err := d.credentials(ctx, project, environment, reference)
		if err != nil {
			return database.Spec{}, err
		}
		spec.Credentials.Username, spec.Credentials.Password = username, password
	}
	return spec, nil
}

// The setting keys services/worker writes.
const (
	databaseTypeSetting        = "type"
	databaseCredentialsSetting = "credentials/name"
)

// credentials reads the secret the configuration referenced.
//
// THE SECRET IS "username:password", split on the FIRST colon — the userinfo
// shape a connection URL already uses, and the one that lets a password contain
// a colon. A secret with no colon is a password with no user, which is what a
// server that authenticates by password alone wants.
//
// The vocabulary says only "Secret holding the credentials to connect with", so
// this is a convention rather than a rule it states. It is written down here
// because the alternative — inventing a username — is worse.
func (d *databases) credentials(ctx context.Context, project, environment, reference string) (username, password string, err error) {
	vault := &vault{node: d.node}
	defer vault.Close()

	value, err := vault.Secret(ctx, project, environment, reference)
	if err != nil {
		return "", "", fmt.Errorf("node: reading the credentials for %s/%s: %w", project, environment, err)
	}

	if user, pass, found := strings.Cut(value, ":"); found {
		return user, pass, nil
	}
	return "", value, nil
}

// splitHandle reads back what services/worker composed: project, environment,
// and the resource's id.
func splitHandle(handle string) (project, environment, id string) {
	parts := strings.SplitN(handle, "/", 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

// Close releases every connection this node opened.
func (d *databases) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var err error
	for _, conn := range d.open {
		err = errors.Join(err, conn.Close())
	}
	d.open = nil
	return err
}
