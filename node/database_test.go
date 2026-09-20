package node

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/ipfs/go-log/v2"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
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

func (c *fakeConn) Exec(_ context.Context, statement string, args ...any) (database.Outcome, error) {
	c.statements = append(c.statements, statement)
	c.args = append(c.args, args)
	if c.execErr != nil {
		return database.Outcome{}, c.execErr
	}
	return database.Outcome{Affected: int64(len(c.statements)), LastInsertID: int64(len(c.statements))}, nil
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
	require.Equal(t, "QmLedger", spec.Name,
		"the RESOLVED id: a label is local to the resource that wrote it, and two "+
			"different databases may both be called `ledger`")
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
	settings["sql/credentials/name"] = "QmCred"

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
	settings["sql/credentials/name"] = "QmCred"
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
	settings["sql/credentials/name"] = "QmCred"
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
	settings["sql/credentials/name"] = "QmNeverSet"

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

// ── tau's own kv engine ──────────────────────────────────────────────────────

func kvGrantFor(handle string) invoke.Grant {
	return invoke.Grant{
		Kind: "database", Label: "tokens", Handle: handle,
		Ops:      []string{"get", "put", "delete", "list"},
		Settings: map[string]string{"type": "kv", "kv/engine": "distributed"},
	}
}

// TestAKvDatabaseNEEDSNOTHINGPROVISIONED, which is the whole difference between
// declaring a database and going to get one first. No composer registered an
// engine here.
func TestAKvDatabaseWorksWithNoEngineRegistered(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	d := &databases{node: n}
	ctx := t.Context()

	db, err := d.OpenKV(ctx, kvGrantFor("shop/prod/QmTokens"))
	require.NoError(t, err)

	require.NoError(t, db.Put(ctx, "session:1", []byte("alice")))

	value, err := db.Get(ctx, "session:1")
	require.NoError(t, err)
	require.Equal(t, "alice", string(value))

	keys, err := db.Keys(ctx, "session:")
	require.NoError(t, err)
	require.Equal(t, []string{"session:1"}, keys)

	require.NoError(t, db.Delete(ctx, "session:1"))
	_, err = db.Get(ctx, "session:1")
	require.ErrorIs(t, err, wasmtier.ErrNotFound, "the tier's miss, so it is not read as an outage")
}

// TestAStoreIsOpenedPerDatabaseAndOnlyIfUsed.
//
// LAZY is the rule the cache backing keeps: a node serving no kv database opens
// nothing. PER DATABASE is what replaced "one store for all of them" — a store
// is a pubsub topic, so a node now carries as many as it HOLDS rather than one
// carrying every database's rows.
func TestTheKvStoreIsLazyAndPerDatabase(t *testing.T) {
	n, _, factory := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	d := &databases{node: n}

	opened, _ := factory.counts()
	require.NotContains(t, opened, databaseStore, "nothing has asked for a database yet")

	for _, handle := range []string{"shop/prod/QmA", "shop/prod/QmB", "other/prod/QmA"} {
		db, err := d.OpenKV(t.Context(), kvGrantFor(handle))
		require.NoError(t, err)
		require.NoError(t, db.Put(t.Context(), "k", []byte("v")))
	}

	opened, _ = factory.counts()
	for _, want := range []string{
		databaseStore + "/shop/prod/kv/QmA",
		databaseStore + "/shop/prod/kv/QmB",
		databaseStore + "/other/prod/kv/QmA",
	} {
		require.Contains(t, opened, want, "%v", opened)
	}
	require.NotContains(t, opened, databaseStore, "a store holding all three: %v", opened)

	// AND ASKING AGAIN OPENS NOTHING MORE: the factory caches by path, so a
	// database this node already holds costs no second topic.
	before := len(opened)
	_, err := d.OpenKV(t.Context(), kvGrantFor("shop/prod/QmA"))
	require.NoError(t, err)
	opened, _ = factory.counts()
	require.Len(t, opened, before, "reopening a held database opened another store")
}

// TestTwoKvDatabasesDoNotSeeEachOther, and neither do two deployments of one.
func TestKvDatabasesAreIsolated(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	d := &databases{node: n}
	ctx := t.Context()

	mine, err := d.OpenKV(ctx, kvGrantFor("shop/prod/QmA"))
	require.NoError(t, err)
	theirs, err := d.OpenKV(ctx, kvGrantFor("shop/prod/QmB"))
	require.NoError(t, err)
	staging, err := d.OpenKV(ctx, kvGrantFor("shop/staging/QmA"))
	require.NoError(t, err)

	require.NoError(t, mine.Put(ctx, "k", []byte("mine")))
	require.NoError(t, theirs.Put(ctx, "k", []byte("theirs")))
	require.NoError(t, staging.Put(ctx, "k", []byte("staging")))

	for _, c := range []struct {
		db   wasmtier.KV
		want string
	}{{mine, "mine"}, {theirs, "theirs"}, {staging, "staging"}} {
		value, err := c.db.Get(ctx, "k")
		require.NoError(t, err)
		require.Equal(t, c.want, string(value))
	}
}

// TestAComposersOwnKvEngineWins, so a deployment that has something better than
// the swarm's own store is not overridden by it.
func TestARegisteredKvEngineIsNotReplaced(t *testing.T) {
	engine := &fakeKV{}
	n, _, factory := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithDatabaseEngines(registryWith(t, engine)))

	d := &databases{node: n}
	db, err := d.OpenKV(t.Context(), kvGrantFor("shop/prod/QmA"))
	require.NoError(t, err)
	require.NoError(t, db.Put(t.Context(), "k", []byte("v")))

	require.Equal(t, 1, engine.opened, "the composer's engine served it")

	opened, _ := factory.counts()
	require.NotContains(t, opened, databaseStore, "and the node's own store was never opened")
}

// TestAKvDatabaseNeedsSomewhereToPutIt, so a node with no database factory
// says so rather than panicking on the first write.
func TestAKvDatabaseNeedsAStore(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	n, err := New(Config{Services: []string{Worker, Stats}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithRuntimes(&tier{name: "wasm"}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	_, err = (&databases{node: n}).OpenKV(t.Context(), kvGrantFor("shop/prod/QmA"))
	require.ErrorIs(t, err, ErrNoCacheStore)
}

// fakeKV is a composer's own kv engine.
type fakeKV struct{ opened int }

func (f *fakeKV) Name() string           { return "distributed" }
func (f *fakeKV) Types() []database.Type { return []database.Type{database.KV} }
func (f *fakeKV) Open(_ context.Context, spec database.Spec) (database.Conn, error) {
	if spec.Project != "" {
		f.opened++
	}
	return &fakeKVConn{}, nil
}

type fakeKVConn struct{}

func (fakeKVConn) Get(context.Context, string) ([]byte, error) { return nil, nil }
func (fakeKVConn) Put(context.Context, string, []byte) error   { return nil }
func (fakeKVConn) Delete(context.Context, string) error        { return nil }
func (fakeKVConn) Close() error                                { return nil }
func (fakeKVConn) List(context.Context, string) ([]string, error) {
	return nil, nil
}
func (fakeKVConn) Watch(context.Context, string) (<-chan string, error) {
	return nil, errors.New("not served")
}

// ── tau's own document engine ────────────────────────────────────────────────

func docGrantFor(handle string) invoke.Grant {
	return invoke.Grant{
		Kind: "database", Label: "orders", Handle: handle,
		Ops:      []string{"get", "put", "delete", "query"},
		Settings: map[string]string{"type": "document", "document/engine": "distributed"},
	}
}

// TestADocumentDatabaseAlsoNeedsNothingProvisioned, and it crosses this backing
// as JSON — which is where pkg/database's map meets the ABI's bytes.
func TestADocumentDatabaseWorksWithNoEngineRegistered(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	d := &databases{node: n}
	ctx := t.Context()

	docs, err := d.OpenDocuments(ctx, docGrantFor("shop/prod/QmOrders"))
	require.NoError(t, err)

	require.NoError(t, docs.Put(ctx, "order:1", []byte(`{"status":"paid","total":12.5}`)))

	raw, err := docs.Get(ctx, "order:1")
	require.NoError(t, err)
	require.JSONEq(t, `{"status":"paid","total":12.5}`, string(raw))

	found, err := docs.Find(ctx, []byte(`{"status":"paid"}`))
	require.NoError(t, err)
	require.JSONEq(t, `[{"status":"paid","total":12.5}]`, string(found))

	require.NoError(t, docs.Delete(ctx, "order:1"))
	_, err = docs.Get(ctx, "order:1")
	require.ErrorIs(t, err, wasmtier.ErrNotFound, "the tier's miss, not the engine's")
}

// TestAnEmptyFilterIsEverythingAndAnEmptyAnswerIsAnArray, so a guest parses the
// answer the same way whether or not anything matched.
func TestFindWithNoFilterAnswersAnArray(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	d := &databases{node: n}
	ctx := t.Context()

	docs, err := d.OpenDocuments(ctx, docGrantFor("shop/prod/QmEmpty"))
	require.NoError(t, err)

	found, err := docs.Find(ctx, nil)
	require.NoError(t, err)
	require.JSONEq(t, `[]`, string(found), "an empty array and not null")
}

// TestBadJsonIsMalformedInBothDirections, which is its own arm because a caller
// fixes it and does not retry it.
func TestABadDocumentIsMalformed(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	d := &databases{node: n}
	ctx := t.Context()

	docs, err := d.OpenDocuments(ctx, docGrantFor("shop/prod/QmOrders"))
	require.NoError(t, err)

	require.ErrorIs(t, docs.Put(ctx, "1", []byte("not json")), wasmtier.ErrMalformed)

	_, err = docs.Find(ctx, []byte("not json"))
	require.ErrorIs(t, err, wasmtier.ErrMalformed)
}

// TestTheTwoOwnEnginesDoNotShareAStoreAtALL, which is stronger than the key-space
// separation this used to assert: they are not in the same store, so they cannot
// collide and a drop in one cannot reach the other's replicas.
func TestKvAndDocumentsDoNotCollide(t *testing.T) {
	n, _, factory := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	d := &databases{node: n}
	ctx := t.Context()

	kvDB, err := d.OpenKV(ctx, kvGrantFor("shop/prod/QmTokens"))
	require.NoError(t, err)
	docs, err := d.OpenDocuments(ctx, docGrantFor("shop/prod/QmOrders"))
	require.NoError(t, err)

	require.NoError(t, kvDB.Put(ctx, "k", []byte("kv")))
	require.NoError(t, docs.Put(ctx, "k", []byte(`{"where":"document"}`)))

	value, err := kvDB.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "kv", string(value))

	raw, err := docs.Get(ctx, "k")
	require.NoError(t, err)
	require.JSONEq(t, `{"where":"document"}`, string(raw))

	// A STORE EACH, named by the same function that addresses the database —
	// which is a pubsub topic each, so which replicas a write or a delete reaches
	// is decided per database rather than per node.
	opened, _ := factory.counts()
	require.Contains(t, opened, databaseStore+"/shop/prod/kv/QmTokens")
	require.Contains(t, opened, databaseStore+"/shop/prod/document/QmOrders")
	require.NotContains(t, opened, databaseStore,
		"a store holding every database was opened after all: %v", opened)
}

// ── tau's own vector engine ──────────────────────────────────────────────────

func vecGrantFor(handle, metric string) invoke.Grant {
	return invoke.Grant{
		Kind: "database", Label: "knowledge", Handle: handle,
		Ops: []string{"search", "upsert", "delete"},
		Settings: map[string]string{
			"type": "vector", "vector/engine": "distributed",
			"vector/dimensions": "2", "vector/metric": metric,
		},
	}
}

// TestNearestNeighbourWithNothingProvisioned, which completes the four data
// models the `database` kind declares.
func TestAVectorDatabaseWorksWithNoEngineRegistered(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	d := &databases{node: n}
	ctx := t.Context()

	index, err := d.OpenVectors(ctx, vecGrantFor("shop/prod/QmK", "cosine"))
	require.NoError(t, err)

	require.NoError(t, index.Upsert(ctx, "east", []float32{1, 0}, []byte(`{"where":"east"}`)))
	require.NoError(t, index.Upsert(ctx, "north", []float32{0, 1}, nil))

	found, err := index.Search(ctx, []float32{1, 0}, 0)
	require.NoError(t, err)
	require.Len(t, found, 2)
	require.Equal(t, "east", found[0].ID)
	require.JSONEq(t, `{"where":"east"}`, string(found[0].Meta))
	require.Empty(t, found[1].Meta, "an empty meta is no meta and not an empty object")

	require.NoError(t, index.Delete(ctx, "east"))
	found, err = index.Search(ctx, []float32{1, 0}, 0)
	require.NoError(t, err)
	require.Len(t, found, 1)
}

// TestTheDeclaredMetricReachesTheEngine, because it has to match how the model
// was trained and getting it wrong ranks badly rather than erroring.
func TestTheMetricComesOffTheResource(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	d := &databases{node: n}
	ctx := t.Context()

	// dot ranks the LONGER vector first where cosine would not.
	index, err := d.OpenVectors(ctx, vecGrantFor("shop/prod/QmDot", "dot"))
	require.NoError(t, err)

	require.NoError(t, index.Upsert(ctx, "close", []float32{1, 0.1}, nil))
	require.NoError(t, index.Upsert(ctx, "long", []float32{10, 5}, nil))

	found, err := index.Search(ctx, []float32{1, 0}, 1)
	require.NoError(t, err)
	require.Equal(t, "long", found[0].ID, "dot carries magnitude")
}

// TestTheDeclaredWidthIsEnforcedThroughTheBacking.
func TestAWrongWidthIsTheTiersError(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	d := &databases{node: n}
	ctx := t.Context()

	index, err := d.OpenVectors(ctx, vecGrantFor("shop/prod/QmK", "cosine"))
	require.NoError(t, err)

	err = index.Upsert(ctx, "a", []float32{1, 0, 0}, nil)
	require.ErrorIs(t, err, wasmtier.ErrWrongWidth, "the tier's arm, so a guest can act on it")
}

func TestABadMetaIsMalformed(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	d := &databases{node: n}

	index, err := d.OpenVectors(t.Context(), vecGrantFor("shop/prod/QmK", "cosine"))
	require.NoError(t, err)

	require.ErrorIs(t, index.Upsert(t.Context(), "a", []float32{1, 0}, []byte("not json")),
		wasmtier.ErrMalformed)
}

// ── letting go of a copy ─────────────────────────────────────────────────────

// releasing is a kvdb.Factory whose stores can be destroyed, which memFactory's
// cannot. Destroying behaves like the real one: the store is closed and dropped
// from the factory, so a later open builds a new one from whatever the fleet
// still holds.
type releasing struct {
	mu     sync.Mutex
	byPath map[string]*releasable
}

func (f *releasing) New(_ log.StandardLogger, path string, _ int) (kvdb.KVDB, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.byPath == nil {
		f.byPath = map[string]*releasable{}
	}
	if held, ok := f.byPath[path]; ok {
		return held, nil
	}
	db := &releasable{factory: f, path: path}
	f.byPath[path] = db
	return db, nil
}

func (f *releasing) Close() {}

type releasable struct {
	kvdb.KVDB
	factory *releasing
	path    string

	destroyed bool
}

func (r *releasable) Destroy(context.Context) error {
	r.factory.mu.Lock()
	defer r.factory.mu.Unlock()
	r.destroyed = true
	delete(r.factory.byPath, r.path)
	return nil
}

// TestARELEASEDDATABASEISNOTSERVEDFROMTHETABLEAGAIN.
//
// Destroying a database closes the store it destroys. A node that kept that
// store in the table it serves from went on answering every later call out of a
// closed store — not-found for rows another node holds, and the CRDT's own
// bookkeeping written back under the prefix that was just removed, which makes
// this node advertise a database it does not have.
func TestAReleasedDatabaseIsNotServedFromTheNodesTableAgain(t *testing.T) {
	factory := &releasing{}
	d := &databases{node: &Node{opts: options{databases: factory}}}
	spec := database.Spec{Type: database.KV, Project: "shop", Environment: "prod", Name: "QmA"}

	first, err := d.storesFor(t.Context(), spec)
	require.NoError(t, err)

	require.NoError(t, nodeHoldings{d: d}.Destroy(t.Context(), spec))
	require.True(t, first.(cacheStore).db.(*releasable).destroyed,
		"the release destroyed a store the node was not serving from")

	again, err := d.storesFor(t.Context(), spec)
	require.NoError(t, err)
	require.False(t, again.(cacheStore).db.(*releasable).destroyed,
		"the node went on serving the store it destroyed")
}
