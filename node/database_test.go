package node

import (
	"context"
	"errors"
	"io"
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// A FAKE ENGINE AND NOT A FAKE DRIVER. What this file tests is the composition
// — a grant becomes a Spec, a credential is resolved, a connection is kept —
// and pkg/database's own tests already cover building a DSN and wrapping a
// pool. Registering an engine is the seam; going through database/sql to reach
// it would be testing somebody else's registry.

type fakeEngine struct {
	name string

	specs []database.Spec
	conn  *fakeConn
	err   error
}

func (e *fakeEngine) Name() string           { return e.name }
func (e *fakeEngine) Types() []database.Type { return []database.Type{database.SQL} }

// Open records what it was asked for — EXCEPT the registry's own probe.
//
// Register opens one connection per type to prove the engine answers that
// type's surface, and that open is not a caller's. Counting it would make every
// assertion here one higher than the thing it is about.
func (e *fakeEngine) Open(_ context.Context, spec database.Spec) (database.Conn, error) {
	if e.err != nil {
		return nil, e.err
	}
	if spec.Project == "" {
		return &fakeConn{}, nil // the probe
	}

	e.specs = append(e.specs, spec)
	if e.conn == nil {
		e.conn = &fakeConn{}
	}
	return e.conn, nil
}

type fakeConn struct {
	statements []string
	args       [][]any
	rows       *fakeRows
	execErr    error
	closed     int
}

func (c *fakeConn) Query(_ context.Context, statement string, args ...any) (database.Rows, error) {
	c.statements = append(c.statements, statement)
	c.args = append(c.args, args)
	if c.rows == nil {
		return &fakeRows{}, nil
	}
	return c.rows, nil
}

func (c *fakeConn) Exec(_ context.Context, statement string, args ...any) (int64, error) {
	c.statements = append(c.statements, statement)
	c.args = append(c.args, args)
	if c.execErr != nil {
		return 0, c.execErr
	}
	return int64(len(c.statements)), nil
}

func (c *fakeConn) Close() error { c.closed++; return nil }

type fakeRows struct {
	cols []string
	data [][]any
	at   int
	err  error
}

func (r *fakeRows) Next() bool                 { r.at++; return r.at <= len(r.data) }
func (r *fakeRows) Columns() ([]string, error) { return r.cols, r.err }
func (r *fakeRows) Err() error                 { return r.err }
func (r *fakeRows) Close() error               { return nil }
func (r *fakeRows) Scan(dest ...any) error {
	row := r.data[r.at-1]
	for i := range dest {
		*(dest[i].(*any)) = row[i]
	}
	return nil
}

var _ database.SQLConn = (*fakeConn)(nil)
var _ io.Closer = (*fakeConn)(nil)

// registryWith is a registry holding one engine, registered the way a
// deployment registers its own.
func registryWith(t *testing.T, e database.Engine) *database.Registry {
	t.Helper()
	r := database.NewRegistry()
	require.NoError(t, r.Register(t.Context(), e))
	return r
}

func dbGrantFor(handle string, settings map[string]string) invoke.Grant {
	return invoke.Grant{
		Kind: "database", Label: "ledger", Handle: handle,
		Ops: []string{"query", "exec"}, Settings: settings,
	}
}

func postgresSettings() map[string]string {
	return map[string]string{
		"type":         "sql",
		"sql/engine":   "postgres",
		"sql/host":     "db.example.com:5432",
		"sql/database": "orders",
		"sql/tls":      "verify-full",
	}
}

func backingWith(t *testing.T, e database.Engine, extra ...Option) (*databases, *Node) {
	t.Helper()
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats, Secrets}},
		append([]Option{WithDatabaseEngines(registryWith(t, e))}, extra...)...)
	return &databases{node: n}, n
}

// ── the connection ───────────────────────────────────────────────────────────

// TestAGrantBecomesAConnection, with every field of the compiled resource
// arriving where pkg/database expects it.
func TestAGrantOpensTheDatabaseItNames(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := backingWith(t, engine)

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", postgresSettings()), "insert", nil)
	require.NoError(t, err)

	require.Len(t, engine.specs, 1)
	spec := engine.specs[0]
	require.Equal(t, database.SQL, spec.Type)
	require.Equal(t, "postgres", spec.Engine)
	require.Equal(t, "db.example.com:5432", spec.Address)
	require.Equal(t, "orders", spec.Database)
	require.Equal(t, "shop", spec.Project)
	require.Equal(t, "prod", spec.Environment)
	require.Equal(t, "ledger", spec.Name, "the label the author wrote")
	require.True(t, spec.Credentials.TLS)
}

// TestAConnectionIsKept, because database/sql is already a pool and opening per
// invocation would discard it on every request.
func TestTheConnectionIsOpenedOnce(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := backingWith(t, engine)
	grant := dbGrantFor("shop/prod/QmLedger", postgresSettings())

	for range 3 {
		_, err := d.Exec(t.Context(), grant, "insert", nil)
		require.NoError(t, err)
	}
	require.Len(t, engine.specs, 1, "one open, three statements")
	require.Len(t, engine.conn.statements, 3)
}

// TestTwoDeploymentsAreTwoConnections, because a handle carries the deployment
// and staging must not write to production's server.
func TestTwoHandlesAreTwoConnections(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := backingWith(t, engine)

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", postgresSettings()), "a", nil)
	require.NoError(t, err)
	_, err = d.Exec(t.Context(), dbGrantFor("shop/staging/QmLedger", postgresSettings()), "b", nil)
	require.NoError(t, err)

	require.Len(t, engine.specs, 2)
	require.Equal(t, "prod", engine.specs[0].Environment)
	require.Equal(t, "staging", engine.specs[1].Environment)
}

// ── tls ──────────────────────────────────────────────────────────────────────

// TestTlsIsOnUnlessSomebodyWroteDisable, which is the vocabulary's own
// polarity: the safe state is the one an author reaches by writing nothing.
func TestTlsPolarity(t *testing.T) {
	for value, want := range map[string]bool{
		"":            true,
		"verify-full": true,
		"require":     true,
		"disable":     false,
	} {
		engine := &fakeEngine{name: "postgres"}
		d, _ := backingWith(t, engine)

		settings := postgresSettings()
		if value == "" {
			delete(settings, "sql/tls")
		} else {
			settings["sql/tls"] = value
		}

		_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/Qm"+value, settings), "x", nil)
		require.NoError(t, err)
		require.Equal(t, want, engine.specs[0].Credentials.TLS, "tls %q", value)
	}
}

// ── credentials ──────────────────────────────────────────────────────────────

// TestTheCredentialIsResolvedFromTheVault and never carried in the spec, which
// is logged, cached and compared.
func TestACredentialIsReadFromTheVault(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, n := backingWith(t, engine)

	require.NoError(t, n.Secrets().Local().Set(t.Context(), "shop", "prod", "QmCred", []byte("app:s3cret")))

	settings := postgresSettings()
	settings["credentials/name"] = "QmCred"

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", settings), "x", nil)
	require.NoError(t, err)

	require.Equal(t, "app", engine.specs[0].Credentials.Username)
	require.Equal(t, "s3cret", engine.specs[0].Credentials.Password)
}

// TestAPasswordMayContainAColon, which is why the split is on the FIRST one.
func TestOnlyTheFirstColonSplits(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, n := backingWith(t, engine)

	require.NoError(t, n.Secrets().Local().Set(t.Context(), "shop", "prod", "QmCred", []byte("app:a:b:c")))

	settings := postgresSettings()
	settings["credentials/name"] = "QmCred"
	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", settings), "x", nil)
	require.NoError(t, err)

	require.Equal(t, "app", engine.specs[0].Credentials.Username)
	require.Equal(t, "a:b:c", engine.specs[0].Credentials.Password)
}

// TestASecretWithNoColonIsAPasswordAlone, which is what a server that
// authenticates by password wants.
func TestNoColonIsAPasswordWithNoUser(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, n := backingWith(t, engine)

	require.NoError(t, n.Secrets().Local().Set(t.Context(), "shop", "prod", "QmCred", []byte("justapassword")))

	settings := postgresSettings()
	settings["credentials/name"] = "QmCred"
	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", settings), "x", nil)
	require.NoError(t, err)

	require.Empty(t, engine.specs[0].Credentials.Username)
	require.Equal(t, "justapassword", engine.specs[0].Credentials.Password)
}

// TestACredentialThatCannotBeReadIsNotAConnection, rather than connecting with
// an empty password and being refused by the server for the wrong reason.
func TestAMissingCredentialFailsTheOpen(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := backingWith(t, engine)

	settings := postgresSettings()
	settings["credentials/name"] = "QmNeverSet"

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", settings), "x", nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "reading the credentials")
	require.Empty(t, engine.specs, "nothing was opened")
}

// ── rows ─────────────────────────────────────────────────────────────────────

// TestRowsComeBackFlat, row-major, which is the shape the ABI carries.
func TestAResultSetIsRowMajor(t *testing.T) {
	engine := &fakeEngine{name: "postgres", conn: &fakeConn{rows: &fakeRows{
		cols: []string{"id", "name"},
		data: [][]any{{int64(1), "alice"}, {int64(2), "bob"}},
	}}}
	d, _ := backingWith(t, engine)

	rows, err := d.Query(t.Context(), dbGrantFor("shop/prod/QmLedger", postgresSettings()), "select", nil)
	require.NoError(t, err)
	require.Equal(t, []string{"id", "name"}, rows.Columns)
	require.Equal(t, []any{int64(1), "alice", int64(2), "bob"}, rows.Values)
}

// ── what this node cannot serve ──────────────────────────────────────────────

// TestANodeWithNoEngineSaysSo rather than hanging on a connection nobody can
// make.
func TestNoEngineRegisteredIsNamed(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	n, err := New(Config{Services: []string{Worker, Stats}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}),
		WithRuntimes(&tier{name: "wasm"}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	_, err = (&databases{node: n}).Exec(t.Context(),
		dbGrantFor("shop/prod/QmLedger", postgresSettings()), "x", nil)
	require.ErrorIs(t, err, ErrNoEngines)
}

// TestAnUnregisteredEngineIsNamed, and the message says which engines this
// build does have.
func TestAnEngineNobodyRegisteredIsRefused(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := backingWith(t, engine)

	settings := postgresSettings()
	settings["sql/engine"] = "mysql"

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", settings), "x", nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "mysql")
}

// TestOnlySqlIsServedYet. kv, document and vector are the same kind with
// different verbs, and each is its own interface when it lands — so a resource
// on one is told, not connected to something that answers nothing.
func TestAnotherDataModelIsRefusedByName(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := backingWith(t, engine)

	for _, model := range []string{"kv", "document", "vector", ""} {
		settings := map[string]string{"type": model, model + "/engine": "distributed"}
		_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/Qm"+model, settings), "x", nil)
		require.ErrorIs(t, err, ErrNotSQL, "model %q", model)
	}
}

// TestAServersRefusalIsNotAnOutage, which is what lets the tier tell a caller
// to fix a statement rather than retry it.
func TestAStatementTheServerRefusedIsMarked(t *testing.T) {
	engine := &fakeEngine{name: "postgres", conn: &fakeConn{execErr: errors.New("syntax error")}}
	d, _ := backingWith(t, engine)

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", postgresSettings()), "slect", nil)
	require.ErrorIs(t, err, wasmtier.ErrStatementFailed)
	require.ErrorContains(t, err, "syntax error")
}

// TestConnectionsAreClosedWithTheNode.
func TestClosingReleasesTheConnections(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := backingWith(t, engine)

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", postgresSettings()), "x", nil)
	require.NoError(t, err)

	require.NoError(t, d.Close())
	require.Equal(t, 1, engine.conn.closed)
	require.NoError(t, d.Close(), "Close is idempotent")
}
