package database_test

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	backing "github.com/taubyte/tau/instance/database"
	"github.com/taubyte/tau/pkg/database"
	docengine "github.com/taubyte/tau/pkg/database/engines/document"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// The copies a node keeps: which store a database's entries go into, what it holds,
// and what releasing one does to the table.

// stores is a Stores double: one store per NAME, which is what a real factory
// gives — a database is named, and opening it twice is opening the same rows. A
// double that handed back a fresh map each time would hide every place two
// consumers are meant to see each other's writes.
type stores struct {
	mu     sync.Mutex
	opened []string
	byName map[string]*rows

	openErr    error
	releaseErr error
	released   []string
}

func newStores() *stores { return &stores{byName: map[string]*rows{}} }

func (s *stores) Store(name string) (database.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.openErr != nil {
		return nil, s.openErr
	}
	s.opened = append(s.opened, name)
	if held, ok := s.byName[name]; ok {
		return held, nil
	}
	held := &rows{held: map[string][]byte{}}
	s.byName[name] = held
	return held, nil
}

// Release destroys the way the real one does: the store is dropped from the
// factory, so a later open builds a new one from whatever the fleet still holds.
func (s *stores) Release(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.releaseErr != nil {
		return s.releaseErr
	}
	s.released = append(s.released, name)
	if held, ok := s.byName[name]; ok {
		held.destroyed = true
		delete(s.byName, name)
	}
	return nil
}

func (s *stores) did() (opened, released []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.opened...), append([]string(nil), s.released...)
}

// rows is one database's store, over a map.
type rows struct {
	mu        sync.Mutex
	held      map[string][]byte
	destroyed bool
}

func (r *rows) Get(_ context.Context, key string) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, held := r.held[key]
	return value, held, nil
}

func (r *rows) Put(_ context.Context, key string, value []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.held[key] = value
	return nil
}

func (r *rows) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.held, key)
	return nil
}

func (r *rows) List(_ context.Context, prefix string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for key := range r.held {
		if strings.HasPrefix(key, prefix) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (r *rows) gone() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.destroyed
}

// ledgers is a Ledgers double: the other arm of the consensus axis, which is a
// store like any other from here.
type ledgers struct {
	mu    sync.Mutex
	asked []database.Spec
	store database.Store
	err   error
}

func (l *ledgers) Store(spec database.Spec) (database.Store, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked = append(l.asked, spec)
	if l.err != nil {
		return nil, l.err
	}
	return l.store, nil
}

func (l *ledgers) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.asked)
}

// disk is a Disk double: what a node has WITHOUT opening it.
type disk struct {
	holds map[string]bool
	paths []string

	holdErr error
	pathErr error
}

func (d *disk) Holding(_ context.Context, path string) (bool, error) {
	if d.holdErr != nil {
		return false, d.holdErr
	}
	return d.holds[path], nil
}

func (d *disk) Paths(_ context.Context, under string, depth int) ([]string, error) {
	if d.pathErr != nil {
		return nil, d.pathErr
	}
	var out []string
	for _, path := range d.paths {
		if !strings.HasPrefix(path, under+"/") {
			continue
		}
		if len(strings.Split(strings.TrimPrefix(path, under+"/"), "/")) > depth {
			continue
		}
		out = append(out, path)
	}
	return out, nil
}

func kvSpec(project, name string) database.Spec {
	return database.Spec{
		Type: database.KV, Engine: database.Tau, Consensus: database.CRDT,
		Project: project, Environment: "prod", Name: name,
	}
}

func ledgerSpec() database.Spec {
	spec := kvSpec("shop", "QmLedger")
	spec.Consensus = database.Raft
	return spec
}

// unaddressable is a spec that names nothing: a separator in a segment is a
// readable path into somebody else's database, so Spec.Prefix refuses it.
func unaddressable() database.Spec {
	return database.Spec{Type: database.KV, Name: "ledger"}
}

func holdings(t *testing.T, s backing.Stores, l backing.Ledgers, d backing.Disk) *backing.Holdings {
	t.Helper()
	h, err := backing.NewHoldings(s, l, d)
	require.NoError(t, err)
	return h
}

func TestOneNameAddressesADatabaseAndNamesItsTopic(t *testing.T) {
	name, err := backing.Name(kvSpec("shop", "QmSessions"))
	require.NoError(t, err)
	require.Equal(t, backing.Prefix+"/shop/prod/kv/QmSessions", name)

	// AND A SPEC THAT ADDRESSES NOTHING NAMES NOTHING.
	_, err = backing.Name(unaddressable())
	require.ErrorIs(t, err, database.ErrUnaddressable)
}

func TestADatabaseIsNamedUnderItsOwnPrefix(t *testing.T) {
	require.Equal(t, "databases", backing.Prefix)
}

func TestNewHoldingsRefusesWhatItCannotDoWithout(t *testing.T) {
	_, err := backing.NewHoldings(nil, &ledgers{}, nil)
	require.ErrorContains(t, err, "nowhere to keep")

	_, err = backing.NewHoldings(newStores(), nil, nil)
	require.ErrorContains(t, err, "not even a refusal")

	// AND A FACTORY THAT CANNOT BE ASKED WHAT IT HOLDS IS ALLOWED, because the
	// interface a composer satisfies is also satisfied by handles that keep no
	// bytes at all.
	_, err = backing.NewHoldings(newStores(), &ledgers{}, nil)
	require.NoError(t, err)
}

func TestAStoreIsOpenedPerDatabaseAndKept(t *testing.T) {
	kept := newStores()
	h := holdings(t, kept, &ledgers{}, nil)

	for _, spec := range []database.Spec{
		kvSpec("shop", "QmA"), kvSpec("shop", "QmB"), kvSpec("other", "QmA"),
	} {
		store, err := h.Entries(t.Context(), spec)
		require.NoError(t, err)
		require.NoError(t, store.Put(t.Context(), "k", []byte("v")))
	}

	opened, _ := kept.did()
	require.Equal(t, []string{
		backing.Prefix + "/shop/prod/kv/QmA",
		backing.Prefix + "/shop/prod/kv/QmB",
		backing.Prefix + "/other/prod/kv/QmA",
	}, opened)

	// AND ASKING AGAIN OPENS NOTHING MORE: a database this node already holds
	// costs no second topic and no second subscription.
	for range 3 {
		_, err := h.Entries(t.Context(), kvSpec("shop", "QmA"))
		require.NoError(t, err)
	}
	opened, _ = kept.did()
	require.Len(t, opened, 3, "reopening a held database opened another store: %v", opened)
}

func TestTwoDatabasesAreTwoSetsOfRows(t *testing.T) {
	h := holdings(t, newStores(), &ledgers{}, nil)

	mine, err := h.Entries(t.Context(), kvSpec("shop", "QmA"))
	require.NoError(t, err)
	theirs, err := h.Entries(t.Context(), kvSpec("shop", "QmB"))
	require.NoError(t, err)
	elsewhere, err := h.Entries(t.Context(), kvSpec("market", "QmA"))
	require.NoError(t, err)

	require.NoError(t, mine.Put(t.Context(), "k", []byte("mine")))
	require.NoError(t, theirs.Put(t.Context(), "k", []byte("theirs")))
	require.NoError(t, elsewhere.Put(t.Context(), "k", []byte("elsewhere")))

	for want, store := range map[string]database.Store{
		"mine": mine, "theirs": theirs, "elsewhere": elsewhere,
	} {
		value, held, err := store.Get(t.Context(), "k")
		require.NoError(t, err)
		require.True(t, held)
		require.Equal(t, want, string(value))
	}
}

func TestTheConsensusPicksTheStoreAndNotASecondEngine(t *testing.T) {
	kept, ordered := newStores(), &ledgers{store: &rows{held: map[string][]byte{}}}
	h := holdings(t, kept, ordered, nil)

	ledger, err := h.Entries(t.Context(), ledgerSpec())
	require.NoError(t, err)
	require.Same(t, ordered.store, ledger)
	require.Equal(t, 1, ordered.count())

	opened, _ := kept.did()
	require.Empty(t, opened, "a ledger opened a crdt store as well: %v", opened)

	// AND A DATABASE THAT DECLARED NOTHING IS A CRDT ONE, which is what every
	// caller that predates the field keeps getting.
	silent := kvSpec("shop", "QmSessions")
	silent.Consensus = ""
	_, err = h.Entries(t.Context(), silent)
	require.NoError(t, err)
	require.Equal(t, 1, ordered.count(), "a database with no consensus was sent to a group")

	// AND A LEDGER IS ASKED EVERY TIME rather than cached here: which group is
	// open, and whether it has elected, is the ledger table's to answer.
	_, err = h.Entries(t.Context(), ledgerSpec())
	require.NoError(t, err)
	require.Equal(t, 2, ordered.count())

	ordered.err = errors.New("no group")
	_, err = h.Entries(t.Context(), ledgerSpec())
	require.ErrorContains(t, err, "no group")
}

func TestAnUnaddressableSpecOpensNoStore(t *testing.T) {
	kept := newStores()
	h := holdings(t, kept, &ledgers{}, nil)

	_, err := h.Entries(t.Context(), unaddressable())
	require.ErrorIs(t, err, database.ErrUnaddressable)

	opened, _ := kept.did()
	require.Empty(t, opened)
}

func TestAStoreThatCouldNotBeOpenedIsNotKept(t *testing.T) {
	kept := newStores()
	kept.openErr = errors.New("no database factory")
	h := holdings(t, kept, &ledgers{}, nil)

	_, err := h.Entries(t.Context(), kvSpec("shop", "QmA"))
	require.ErrorContains(t, err, "no database factory")

	kept.openErr = nil
	store, err := h.Entries(t.Context(), kvSpec("shop", "QmA"))
	require.NoError(t, err)
	require.NotNil(t, store)
}

func TestHeldIsAnsweredFromTheDiskAndNotTheTable(t *testing.T) {
	has := &disk{holds: map[string]bool{backing.Prefix + "/shop/prod/kv/QmA": true}}
	h := holdings(t, newStores(), &ledgers{}, has)

	require.True(t, h.Held(t.Context(), kvSpec("shop", "QmA")),
		"a copy this node has never opened in this run was reported as not held")
	require.False(t, h.Held(t.Context(), kvSpec("shop", "QmB")))

	// A SPEC THAT ADDRESSES NOTHING IS NOT HELD, because there is no path to
	// ask about.
	require.False(t, h.Held(t.Context(), unaddressable()))

	// AND A DISK THAT COULD NOT BE READ HOLDS NOTHING, as far as anybody else is
	// concerned: answering "yes" on a failed read would let a peer drop the last
	// copy.
	has.holdErr = errors.New("the datastore is closed")
	require.False(t, h.Held(t.Context(), kvSpec("shop", "QmA")))
}

func TestAFactoryThatCannotBeAskedHoldsNothing(t *testing.T) {
	h := holdings(t, newStores(), &ledgers{}, nil)

	require.False(t, h.Held(t.Context(), kvSpec("shop", "QmA")))
	require.Nil(t, h.OnDisk(t.Context()))
}

func TestOnDiskReadsBackWhatTheNameWrote(t *testing.T) {
	has := &disk{paths: []string{
		backing.Prefix + "/shop/prod/kv/QmA",
		backing.Prefix + "/shop/staging/document/QmOrders",
		// Not four segments: the CRDT's own bookkeeping, and a database at the
		// wrong depth is not a database.
		backing.Prefix + "/shop/prod/kv",
		backing.Prefix + "/shop/prod/kv/QmA/heads/one",
		// Another kind's store entirely, which Paths already filters by prefix.
		"caches/shop/prod/QmCache",
	}}
	h := holdings(t, newStores(), &ledgers{}, has)

	require.Equal(t, []database.Spec{
		{Project: "shop", Environment: "prod", Type: database.KV, Name: "QmA"},
		{Project: "shop", Environment: "staging", Type: database.Document, Name: "QmOrders"},
	}, h.OnDisk(t.Context()))

	// AND A DISK THAT COULD NOT BE WALKED IS NOT AN EMPTY NODE. A sweep told
	// this node holds nothing would move everything off it.
	has.pathErr = errors.New("the datastore is closed")
	require.Nil(t, h.OnDisk(t.Context()))
}

func TestAReleasedDatabaseIsNotServedFromTheTableAgain(t *testing.T) {
	kept := newStores()
	h := holdings(t, kept, &ledgers{}, nil)
	spec := kvSpec("shop", "QmA")

	first, err := h.Entries(t.Context(), spec)
	require.NoError(t, err)

	require.NoError(t, h.Destroy(t.Context(), spec))
	require.True(t, first.(*rows).gone(),
		"the release destroyed a store the node was not serving from")

	_, released := kept.did()
	require.Equal(t, []string{backing.Prefix + "/shop/prod/kv/QmA"}, released)

	again, err := h.Entries(t.Context(), spec)
	require.NoError(t, err)
	require.False(t, again.(*rows).gone(), "the node went on serving the store it destroyed")
	require.NotSame(t, first, again)
}

func TestADatabaseThatWasNeverOpenedIsStillReleased(t *testing.T) {
	kept := newStores()
	h := holdings(t, kept, &ledgers{}, nil)

	require.NoError(t, h.Destroy(t.Context(), kvSpec("shop", "QmNeverOpened")))

	opened, released := kept.did()
	require.Empty(t, opened, "the release served the request path a store: %v", opened)
	require.Equal(t, []string{backing.Prefix + "/shop/prod/kv/QmNeverOpened"}, released)
}

func TestAReleaseThatFailedSaysSo(t *testing.T) {
	kept := newStores()
	kept.releaseErr = errors.New("this store cannot release a database it holds")
	h := holdings(t, kept, &ledgers{}, nil)

	require.ErrorContains(t, h.Destroy(t.Context(), kvSpec("shop", "QmA")),
		"cannot release a database it holds")

	// AND A SPEC THAT ADDRESSES NOTHING IS REFUSED BEFORE ANYTHING IS TOUCHED.
	require.ErrorIs(t, h.Destroy(t.Context(), unaddressable()), database.ErrUnaddressable)
	_, released := kept.did()
	require.Empty(t, released)
}

func TestAReleaseRacingTheRequestPath(t *testing.T) {
	stores := newStores()
	h := holdings(t, stores, &ledgers{}, nil)

	specs := []database.Spec{kvSpec("shop", "QmA"), kvSpec("shop", "QmB"), kvSpec("other", "QmA")}

	var wg sync.WaitGroup
	for range 8 {
		for _, spec := range specs {
			wg.Add(2)
			go func(spec database.Spec) {
				defer wg.Done()
				store, err := h.Entries(t.Context(), spec)
				if err == nil {
					store.Put(t.Context(), "k", []byte("v"))
				}
			}(spec)
			go func(spec database.Spec) {
				defer wg.Done()
				h.Destroy(t.Context(), spec)
			}(spec)
		}
	}
	wg.Wait()

	// Every database still answers, and every release that ran named a database
	// rather than a torn fragment of one.
	for _, spec := range specs {
		store, err := h.Entries(t.Context(), spec)
		require.NoError(t, err)
		require.NotNil(t, store)
	}
	_, released := stores.did()
	require.NotEmpty(t, released)
	for _, name := range released {
		require.True(t, strings.HasPrefix(name, backing.Prefix+"/"), "released %q", name)
	}
}

// A fake engine, not a fake driver: this tests the composition (grant to Spec,
// credential resolved, connection kept); DSN building is pkg/database's test.

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
	queryErr   error
	execErr    error
	closed     int
}

func (c *fakeConn) Query(_ context.Context, statement string, args ...any) (database.Rows, error) {
	c.statements = append(c.statements, statement)
	c.args = append(c.args, args)
	if c.queryErr != nil {
		return nil, c.queryErr
	}
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

	// err is a result set that could not even name its columns, scanErr one that
	// died on a row, and errAfter the failure a driver only reports once the
	// rows are done — three different moments, each its own arm here because
	// each is its own arm in database/sql.
	err      error
	scanErr  error
	errAfter error
}

func (r *fakeRows) Next() bool                 { r.at++; return r.at <= len(r.data) }
func (r *fakeRows) Columns() ([]string, error) { return r.cols, r.err }
func (r *fakeRows) Err() error                 { return errors.Join(r.err, r.errAfter) }
func (r *fakeRows) Close() error               { return nil }
func (r *fakeRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
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

// vault is a Secrets double: the secrets a deployment has set.
type vault struct {
	held map[string]string
	err  error
}

func newVault() *vault { return &vault{held: map[string]string{}} }

func (v *vault) set(project, environment, name, value string) {
	v.held[project+"/"+environment+"/"+name] = value
}

func (v *vault) Secret(_ context.Context, project, environment, name string) (string, error) {
	if v.err != nil {
		return "", v.err
	}
	value, held := v.held[project+"/"+environment+"/"+name]
	if !held {
		return "", errors.New("vault: no such secret")
	}
	return value, nil
}

// tierOver is the four-world adapter over one registry, with a store per
// database behind tau's own engines and a vault behind a declared credential.
func tierOver(t *testing.T, registry *database.Registry, v *vault) (*backing.Databases, *stores) {
	t.Helper()

	kept := newStores()
	d, err := backing.New(registry, holdings(t, kept, &ledgers{}, nil).Entries, v)
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	return d, kept
}

// tierWith is the adapter over one composer-registered engine.
func tierWith(t *testing.T, e database.Engine) (*backing.Databases, *vault) {
	t.Helper()
	v := newVault()
	d, _ := tierOver(t, registryWith(t, e), v)
	return d, v
}

// tauOnly is the adapter a composer registered nothing with, which is the
// ordinary build: tau's own three engines and nothing else.
func tauOnly(t *testing.T) (*backing.Databases, *stores) {
	t.Helper()
	return tierOver(t, nil, newVault())
}

func TestAGrantOpensTheDatabaseItNames(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := tierWith(t, engine)

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

func TestTheConnectionIsOpenedOnce(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := tierWith(t, engine)
	grant := dbGrantFor("shop/prod/QmLedger", postgresSettings())

	for range 3 {
		_, err := d.Exec(t.Context(), grant, "insert", nil)
		require.NoError(t, err)
	}
	require.Len(t, engine.specs, 1, "one open, three statements")
	require.Len(t, engine.conn.statements, 3)
}

func TestTwoHandlesAreTwoConnections(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := tierWith(t, engine)

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", postgresSettings()), "a", nil)
	require.NoError(t, err)
	_, err = d.Exec(t.Context(), dbGrantFor("shop/staging/QmLedger", postgresSettings()), "b", nil)
	require.NoError(t, err)

	require.Len(t, engine.specs, 2)
	require.Equal(t, "prod", engine.specs[0].Environment)
	require.Equal(t, "staging", engine.specs[1].Environment)
}

func TestTlsPolarity(t *testing.T) {
	for value, want := range map[string]bool{
		"":            true,
		"verify-full": true,
		"require":     true,
		"disable":     false,
	} {
		engine := &fakeEngine{name: "postgres"}
		d, _ := tierWith(t, engine)

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

func TestACredentialIsReadFromTheVault(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, v := tierWith(t, engine)

	v.set("shop", "prod", "QmCred", ("app:s3cret"))

	settings := postgresSettings()
	settings["sql/credentials/name"] = "QmCred"

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", settings), "x", nil)
	require.NoError(t, err)

	require.Equal(t, "app", engine.specs[0].Credentials.Username)
	require.Equal(t, "s3cret", engine.specs[0].Credentials.Password)
}

func TestOnlyTheFirstColonSplits(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, v := tierWith(t, engine)

	v.set("shop", "prod", "QmCred", ("app:a:b:c"))

	settings := postgresSettings()
	settings["sql/credentials/name"] = "QmCred"
	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", settings), "x", nil)
	require.NoError(t, err)

	require.Equal(t, "app", engine.specs[0].Credentials.Username)
	require.Equal(t, "a:b:c", engine.specs[0].Credentials.Password)
}

func TestNoColonIsAPasswordWithNoUser(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, v := tierWith(t, engine)

	v.set("shop", "prod", "QmCred", ("justapassword"))

	settings := postgresSettings()
	settings["sql/credentials/name"] = "QmCred"
	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", settings), "x", nil)
	require.NoError(t, err)

	require.Empty(t, engine.specs[0].Credentials.Username)
	require.Equal(t, "justapassword", engine.specs[0].Credentials.Password)
}

func TestAMissingCredentialFailsTheOpen(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := tierWith(t, engine)

	settings := postgresSettings()
	settings["sql/credentials/name"] = "QmNeverSet"

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", settings), "x", nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "reading the credentials")
	require.Empty(t, engine.specs, "nothing was opened")
}

func TestAResultSetIsRowMajor(t *testing.T) {
	engine := &fakeEngine{name: "postgres", conn: &fakeConn{rows: &fakeRows{
		cols: []string{"id", "name"},
		data: [][]any{{int64(1), "alice"}, {int64(2), "bob"}},
	}}}
	d, _ := tierWith(t, engine)

	rows, err := d.Query(t.Context(), dbGrantFor("shop/prod/QmLedger", postgresSettings()), "select", nil)
	require.NoError(t, err)
	require.Equal(t, []string{"id", "name"}, rows.Columns)
	require.Equal(t, []any{int64(1), "alice", int64(2), "bob"}, rows.Values)
}

func TestNoEngineRegisteredIsNamed(t *testing.T) {
	d, _ := tauOnly(t)

	_, err := d.Exec(t.Context(),
		dbGrantFor("shop/prod/QmLedger", postgresSettings()), "x", nil)
	require.ErrorIs(t, err, backing.ErrNoEngines)
}

func TestAnEngineNobodyRegisteredIsRefused(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := tierWith(t, engine)

	settings := postgresSettings()
	settings["sql/engine"] = "mysql"

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", settings), "x", nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "mysql")
}

func TestAnotherDataModelIsRefusedByName(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := tierWith(t, engine)

	for _, model := range []string{"kv", "document", "vector", ""} {
		settings := map[string]string{"type": model, model + "/engine": "distributed"}
		_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/Qm"+model, settings), "x", nil)
		require.ErrorIs(t, err, backing.ErrNotSQL, "model %q", model)
	}
}

func TestAStatementTheServerRefusedIsMarked(t *testing.T) {
	engine := &fakeEngine{name: "postgres", conn: &fakeConn{execErr: errors.New("syntax error")}}
	d, _ := tierWith(t, engine)

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", postgresSettings()), "slect", nil)
	require.ErrorIs(t, err, wasmtier.ErrStatementFailed)
	require.ErrorContains(t, err, "syntax error")
}

func TestClosingReleasesTheConnections(t *testing.T) {
	engine := &fakeEngine{name: "postgres"}
	d, _ := tierWith(t, engine)

	_, err := d.Exec(t.Context(), dbGrantFor("shop/prod/QmLedger", postgresSettings()), "x", nil)
	require.NoError(t, err)

	require.NoError(t, d.Close())
	require.Equal(t, 1, engine.conn.closed)
	require.NoError(t, d.Close(), "Close is idempotent")
}

func kvGrantFor(handle string) invoke.Grant {
	return invoke.Grant{
		Kind: "database", Label: "tokens", Handle: handle,
		Ops:      []string{"get", "put", "delete", "list"},
		Settings: map[string]string{"type": "kv", "kv/engine": "distributed"},
	}
}

func TestAKvDatabaseWorksWithNoEngineRegistered(t *testing.T) {
	d, _ := tauOnly(t)
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

func TestTheKvStoreIsLazyAndPerDatabase(t *testing.T) {
	d, kept := tauOnly(t)

	opened, _ := kept.did()
	require.NotContains(t, opened, backing.Prefix, "nothing has asked for a database yet")

	for _, handle := range []string{"shop/prod/QmA", "shop/prod/QmB", "other/prod/QmA"} {
		db, err := d.OpenKV(t.Context(), kvGrantFor(handle))
		require.NoError(t, err)
		require.NoError(t, db.Put(t.Context(), "k", []byte("v")))
	}

	opened, _ = kept.did()
	for _, want := range []string{
		backing.Prefix + "/shop/prod/kv/QmA",
		backing.Prefix + "/shop/prod/kv/QmB",
		backing.Prefix + "/other/prod/kv/QmA",
	} {
		require.Contains(t, opened, want, "%v", opened)
	}
	require.NotContains(t, opened, backing.Prefix, "a store holding all three: %v", opened)

	// AND ASKING AGAIN OPENS NOTHING MORE: the factory caches by path, so a
	// database this node already holds costs no second topic.
	before := len(opened)
	_, err := d.OpenKV(t.Context(), kvGrantFor("shop/prod/QmA"))
	require.NoError(t, err)
	opened, _ = kept.did()
	require.Len(t, opened, before, "reopening a held database opened another store")
}

func TestKvDatabasesAreIsolated(t *testing.T) {
	d, _ := tauOnly(t)
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

	type testCase struct {
		db   wasmtier.KV
		want string
	}
	for _, c := range []testCase{{mine, "mine"}, {theirs, "theirs"}, {staging, "staging"}} {
		value, err := c.db.Get(ctx, "k")
		require.NoError(t, err)
		require.Equal(t, c.want, string(value))
	}
}

func TestARegisteredKvEngineIsNotReplaced(t *testing.T) {
	engine := &fakeKV{}
	d, kept := tierOver(t, registryWith(t, engine), newVault())
	db, err := d.OpenKV(t.Context(), kvGrantFor("shop/prod/QmA"))
	require.NoError(t, err)
	require.NoError(t, db.Put(t.Context(), "k", []byte("v")))

	require.Equal(t, 1, engine.opened, "the composer's engine served it")

	opened, _ := kept.did()
	require.NotContains(t, opened, backing.Prefix, "and the node's own store was never opened")
}

func TestAKvDatabaseNeedsAStore(t *testing.T) {
	nowhere := newStores()
	nowhere.openErr = errors.New("node: no database factory to open a cache with")
	d, err := backing.New(nil, holdings(t, nowhere, &ledgers{}, nil).Entries, newVault())
	require.NoError(t, err)

	_, err = d.OpenKV(t.Context(), kvGrantFor("shop/prod/QmA"))
	require.ErrorContains(t, err, "no database factory")
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

func docGrantFor(handle string) invoke.Grant {
	return invoke.Grant{
		Kind: "database", Label: "orders", Handle: handle,
		Ops:      []string{"get", "put", "delete", "query"},
		Settings: map[string]string{"type": "document", "document/engine": "distributed"},
	}
}

func TestADocumentDatabaseWorksWithNoEngineRegistered(t *testing.T) {
	d, _ := tauOnly(t)
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

func TestFindWithNoFilterAnswersAnArray(t *testing.T) {
	d, _ := tauOnly(t)
	ctx := t.Context()

	docs, err := d.OpenDocuments(ctx, docGrantFor("shop/prod/QmEmpty"))
	require.NoError(t, err)

	found, err := docs.Find(ctx, nil)
	require.NoError(t, err)
	require.JSONEq(t, `[]`, string(found), "an empty array and not null")
}

func TestABadDocumentIsMalformed(t *testing.T) {
	d, _ := tauOnly(t)
	ctx := t.Context()

	docs, err := d.OpenDocuments(ctx, docGrantFor("shop/prod/QmOrders"))
	require.NoError(t, err)

	require.ErrorIs(t, docs.Put(ctx, "1", []byte("not json")), wasmtier.ErrMalformed)

	_, err = docs.Find(ctx, []byte("not json"))
	require.ErrorIs(t, err, wasmtier.ErrMalformed)
}

func TestKvAndDocumentsDoNotCollide(t *testing.T) {
	d, kept := tauOnly(t)
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
	opened, _ := kept.did()
	require.Contains(t, opened, backing.Prefix+"/shop/prod/kv/QmTokens")
	require.Contains(t, opened, backing.Prefix+"/shop/prod/document/QmOrders")
	require.NotContains(t, opened, backing.Prefix,
		"a store holding every database was opened after all: %v", opened)
}

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

func TestAVectorDatabaseWorksWithNoEngineRegistered(t *testing.T) {
	d, _ := tauOnly(t)
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

func TestTheMetricComesOffTheResource(t *testing.T) {
	d, _ := tauOnly(t)
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

func TestAWrongWidthIsTheTiersError(t *testing.T) {
	d, _ := tauOnly(t)
	ctx := t.Context()

	index, err := d.OpenVectors(ctx, vecGrantFor("shop/prod/QmK", "cosine"))
	require.NoError(t, err)

	err = index.Upsert(ctx, "a", []float32{1, 0, 0}, nil)
	require.ErrorIs(t, err, wasmtier.ErrWrongWidth, "the tier's arm, so a guest can act on it")
}

func TestABadMetaIsMalformed(t *testing.T) {
	d, _ := tauOnly(t)

	index, err := d.OpenVectors(t.Context(), vecGrantFor("shop/prod/QmK", "cosine"))
	require.NoError(t, err)

	require.ErrorIs(t, index.Upsert(t.Context(), "a", []float32{1, 0}, []byte("not json")),
		wasmtier.ErrMalformed)
}

func TestALedgerIsNotKeptByThisNodesOwnEngines(t *testing.T) {
	d, kept := tauOnly(t)

	grant := kvGrantFor("shop/prod/QmLedger")
	grant.Settings["kv/consensus"] = database.Raft

	_, err := d.OpenKV(t.Context(), grant)
	require.ErrorIs(t, err, backing.ErrConsensusUnserved)
	require.ErrorContains(t, err, "raft")

	opened, _ := kept.did()
	require.Empty(t, opened, "a ledger reached a store through these engines: %v", opened)

	// AND THE OTHER HALF OF THE AXIS IS UNCHANGED, which is the point of reading
	// the word rather than guessing from what is registered.
	crdt := kvGrantFor("shop/prod/QmSessions")
	crdt.Settings["kv/consensus"] = database.CRDT
	_, err = d.OpenKV(t.Context(), crdt)
	require.NoError(t, err)
}

// twoFaced is an engine that answers its model's surface for the registry's
// PROBE and something else for a real database.
//
// NOTHING SHOULD DO THIS, which is exactly why it is a fixture. Registration
// opens one connection with no project and checks the surface, so a registered
// engine has already proved it answers its model — and the assertion after every
// Open is the guard against an engine that proved it once. A guard with no test
// is a guard nobody knows is there.
type twoFaced struct {
	name  string
	model database.Type
	probe database.Conn
	err   error
}

func (e *twoFaced) Name() string           { return e.name }
func (e *twoFaced) Types() []database.Type { return []database.Type{e.model} }

func (e *twoFaced) Open(_ context.Context, spec database.Spec) (database.Conn, error) {
	if spec.Project == "" {
		return e.probe, nil
	}
	if e.err != nil {
		return nil, e.err
	}
	return closeOnly{}, nil
}

// closeOnly answers none of any model's operations.
type closeOnly struct{}

func (closeOnly) Close() error { return nil }

func TestAConnectionWithoutItsModelsSurfaceIsRefused(t *testing.T) {
	type testCase struct {
		model   database.Type
		probe   database.Conn
		open    func(*backing.Databases, invoke.Grant) error
		surface string
	}
	for _, c := range []testCase{
		{
			model: database.SQL, probe: &fakeConn{},
			open: func(d *backing.Databases, g invoke.Grant) error {
				_, err := d.Exec(t.Context(), g, "x", nil)
				return err
			},
			surface: "no sql surface",
		},
		{
			model: database.KV, probe: fakeKVConn{},
			open: func(d *backing.Databases, g invoke.Grant) error {
				_, err := d.OpenKV(t.Context(), g)
				return err
			},
			surface: "no kv surface",
		},
		{
			model: database.Document, probe: pretend{},
			open: func(d *backing.Databases, g invoke.Grant) error {
				_, err := d.OpenDocuments(t.Context(), g)
				return err
			},
			surface: "no document surface",
		},
		{
			model: database.Vector, probe: pretend{},
			open: func(d *backing.Databases, g invoke.Grant) error {
				_, err := d.OpenVectors(t.Context(), g)
				return err
			},
			surface: "no vector surface",
		},
	} {
		engine := &twoFaced{name: "elsewhere", model: c.model, probe: c.probe}
		d, _ := tierWith(t, engine)

		model := string(c.model)
		grant := dbGrantFor("shop/prod/Qm"+model, map[string]string{
			"type": model, model + "/engine": "elsewhere",
		})
		require.ErrorContains(t, c.open(d, grant), c.surface, "model %q", model)

		// AND AN OPEN THAT FAILED IS THE ENGINE'S OWN SENTENCE, carried with the
		// engine and the label so an operator knows which database it was.
		engine.err = errors.New("the server refused the connection")
		failing := dbGrantFor("shop/prod/QmOther"+model, grant.Settings)
		err := c.open(d, failing)
		require.ErrorContains(t, err, "the server refused the connection")
		require.ErrorContains(t, err, "elsewhere", "the refusal did not name the engine")
	}
}

// pretend answers every model's operations and nothing real, which is what the
// registry's probe asks of a document or a vector engine.
type pretend struct{}

func (pretend) Close() error { return nil }
func (pretend) Get(context.Context, string) (map[string]any, error) {
	return nil, nil
}
func (pretend) Query(context.Context, map[string]any) ([]map[string]any, error) {
	return nil, nil
}
func (pretend) Put(context.Context, string, map[string]any) error { return nil }
func (pretend) Delete(context.Context, string) error              { return nil }
func (pretend) Search(context.Context, []float32, int) ([]database.Match, error) {
	return nil, nil
}
func (pretend) Upsert(context.Context, string, []float32, map[string]any) error { return nil }

// answering is a document and vector engine a test drives directly: the two
// models with no corpus cell anywhere, so what their adapters do with a nil
// answer, an engine sentinel and a value that will not marshal is asserted here
// or nowhere.
type answering struct {
	model database.Type
	conn  *answeringConn
}

// NAMED AS TAU'S OWN, because a composer's engine under that name is the one
// that wins — which is how a test reaches these adapters at all without the
// swarm's store underneath.
func (e *answering) Name() string           { return database.Tau }
func (e *answering) Types() []database.Type { return []database.Type{e.model} }
func (e *answering) Open(context.Context, database.Spec) (database.Conn, error) {
	return e.conn, nil
}

type answeringConn struct {
	docs    []map[string]any
	matches []database.Match
	err     error
}

func (c *answeringConn) Close() error { return nil }

func (c *answeringConn) Get(context.Context, string) (map[string]any, error) {
	return nil, c.err
}

func (c *answeringConn) Query(context.Context, map[string]any) ([]map[string]any, error) {
	return c.docs, c.err
}

func (c *answeringConn) Put(context.Context, string, map[string]any) error { return c.err }
func (c *answeringConn) Delete(context.Context, string) error              { return c.err }

func (c *answeringConn) Search(context.Context, []float32, int) ([]database.Match, error) {
	return c.matches, c.err
}

func (c *answeringConn) Upsert(context.Context, string, []float32, map[string]any) error {
	return c.err
}

func TestADocumentEngineAnsweringNilIsStillAnArray(t *testing.T) {
	conn := &answeringConn{}
	d, _ := tierWith(t, &answering{model: database.Document, conn: conn})

	docs, err := d.OpenDocuments(t.Context(), docGrantFor("shop/prod/QmOrders"))
	require.NoError(t, err)

	found, err := docs.Find(t.Context(), nil)
	require.NoError(t, err)
	require.JSONEq(t, `[]`, string(found), "an empty array and not null")
}

func TestTheDocumentEnginesSentinelsBecomeTheTiers(t *testing.T) {
	conn := &answeringConn{}
	d, _ := tierWith(t, &answering{model: database.Document, conn: conn})

	docs, err := d.OpenDocuments(t.Context(), docGrantFor("shop/prod/QmOrders"))
	require.NoError(t, err)

	conn.err = docengine.ErrNotFound
	_, err = docs.Get(t.Context(), "order:1")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)

	conn.err = docengine.ErrNotAnObject
	require.ErrorIs(t, docs.Put(t.Context(), "order:1", []byte(`{"paid":true}`)), wasmtier.ErrMalformed)
	require.ErrorIs(t, docs.Delete(t.Context(), "order:1"), wasmtier.ErrMalformed)

	// AND ANYTHING ELSE TRAVELS AS ITSELF. Folding an error this tier has no arm
	// for into one it does would tell a guest its json was bad when the truth is
	// that the store could not be read.
	conn.err = errors.New("the store is closed")
	_, err = docs.Find(t.Context(), nil)
	require.ErrorContains(t, err, "the store is closed")
	require.NotErrorIs(t, err, wasmtier.ErrMalformed)
	require.NotErrorIs(t, err, wasmtier.ErrNotFound)
}

func TestAMatchWhoseMetaCannotBeJsonIsMalformed(t *testing.T) {
	conn := &answeringConn{matches: []database.Match{
		{ID: "east", Score: 1, Meta: map[string]any{"where": make(chan int)}},
	}}
	d, _ := tierWith(t, &answering{model: database.Vector, conn: conn})

	index, err := d.OpenVectors(t.Context(), vecGrantFor("shop/prod/QmK", "cosine"))
	require.NoError(t, err)

	_, err = index.Search(t.Context(), []float32{1, 0}, 1)
	require.ErrorIs(t, err, wasmtier.ErrMalformed)

	// AND A SEARCH THE ENGINE REFUSED IS THE ENGINE'S ANSWER.
	conn.matches, conn.err = nil, errors.New("the index is rebuilding")
	_, err = index.Search(t.Context(), []float32{1, 0}, 1)
	require.ErrorContains(t, err, "the index is rebuilding")
}

func TestEveryFailureReadingAResultSetIsTheServers(t *testing.T) {
	for _, conn := range []*fakeConn{
		{queryErr: errors.New("the statement was refused")},
		{rows: &fakeRows{err: errors.New("the columns could not be described")}},
		{rows: &fakeRows{cols: []string{"id"}, data: [][]any{{int64(1)}},
			scanErr: errors.New("a value would not convert")}},
		{rows: &fakeRows{cols: []string{"id"}, data: [][]any{{int64(1)}},
			errAfter: errors.New("the connection dropped")}},
	} {
		d, _ := tierWith(t, &fakeEngine{name: "postgres", conn: conn})

		_, err := d.Query(t.Context(), dbGrantFor("shop/prod/QmLedger", postgresSettings()), "select", nil)
		require.ErrorIs(t, err, wasmtier.ErrStatementFailed)
	}

	// AND A QUERY THAT NEVER REACHED A CONNECTION IS NOT A STATEMENT THAT
	// FAILED: the caller has a resource to fix, not a statement.
	d, _ := tauOnly(t)
	_, err := d.Query(t.Context(), dbGrantFor("shop/prod/QmLedger", postgresSettings()), "select", nil)
	require.ErrorIs(t, err, backing.ErrNoEngines)
	require.NotErrorIs(t, err, wasmtier.ErrStatementFailed)
}

func TestNewRefusesWhatItCannotDoWithout(t *testing.T) {
	kept := newStores()
	entries := holdings(t, kept, &ledgers{}, nil).Entries

	_, err := backing.New(nil, nil, newVault())
	require.ErrorContains(t, err, "nowhere to keep")

	_, err = backing.New(nil, entries, nil)
	require.ErrorContains(t, err, "no vault")

	// AND A NIL REGISTRY IS THE ORDINARY BUILD: nothing here links a driver.
	_, err = backing.New(nil, entries, newVault())
	require.NoError(t, err)
}
