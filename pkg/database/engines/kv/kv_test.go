package kv_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/database/engines/kv"
)

// memStore is a Store over a map, returning WHOLE KEYS from List the way a real
// kvdb does — so the prefix stripping is exercised against something that
// behaves like the thing it will meet.
type memStore struct {
	mu     sync.RWMutex
	values map[string][]byte
	err    error
}

func newStore() *memStore { return &memStore{values: map[string][]byte{}} }

func (m *memStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return nil, false, m.err
	}
	v, ok := m.values[key]
	return v, ok, nil
}

func (m *memStore) Put(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.values[key] = value
	return nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	delete(m.values, key)
	return nil
}

func (m *memStore) List(_ context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return nil, m.err
	}
	var keys []string
	for k := range m.values {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (m *memStore) keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for k := range m.values {
		out = append(out, k)
	}
	return out
}

func spec(project, environment, name string) database.Spec {
	return database.Spec{
		Type: database.KV, Engine: kv.Name,
		Project: project, Environment: environment, Name: name,
	}
}

func open(t *testing.T, store database.Store, s database.Spec) database.KVConn {
	t.Helper()

	engine, err := kv.New(database.OneStore(store))
	require.NoError(t, err)

	conn, err := engine.Open(t.Context(), s)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	typed, ok := conn.(database.KVConn)
	require.True(t, ok, "an engine that claims kv must answer kv's surface")
	return typed
}

func TestAnEngineNeedsAStore(t *testing.T) {
	_, err := kv.New(nil)
	require.ErrorIs(t, err, database.ErrNoStore)
}

func TestTheEngineSatisfiesTheRegistry(t *testing.T) {
	engine, err := kv.New(database.OneStore(newStore()))
	require.NoError(t, err)

	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), engine))
	require.Contains(t, registry.EnginesFor(database.KV), kv.Name)
}

func TestPutGetDelete(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "ledger"))
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "order:1", []byte("paid")))

	value, err := c.Get(ctx, "order:1")
	require.NoError(t, err)
	require.Equal(t, "paid", string(value))

	require.NoError(t, c.Delete(ctx, "order:1"))
	_, err = c.Get(ctx, "order:1")
	require.ErrorIs(t, err, kv.ErrNotFound)

	require.NoError(t, c.Delete(ctx, "order:1"), "deleting what is gone succeeds")
}

func TestAMissIsItsOwnAnswer(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "ledger"))
	ctx := t.Context()

	_, err := c.Get(ctx, "never-written")
	require.ErrorIs(t, err, kv.ErrNotFound)

	require.NoError(t, c.Put(ctx, "empty", nil))
	value, err := c.Get(ctx, "empty")
	require.NoError(t, err)
	require.Empty(t, value)
}

func TestThereIsNoLifetime(t *testing.T) {
	store := newStore()
	c := open(t, store, spec("shop", "prod", "ledger"))
	require.NoError(t, c.Put(t.Context(), "k", []byte("v")))

	// The stored bytes are the value and nothing else: no header, no deadline.
	require.Equal(t, []byte("v"), store.values["shop/prod/kv/ledger/k"])
}

func TestListIsScopedSortedAndStripped(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "ledger"))
	ctx := t.Context()

	for _, k := range []string{"order:2", "order:1", "user:9"} {
		require.NoError(t, c.Put(ctx, k, []byte("v")))
	}

	keys, err := c.List(ctx, "order:")
	require.NoError(t, err)
	require.Equal(t, []string{"order:1", "order:2"}, keys,
		"sorted, and without this database's own prefix")
}

func TestEachDatabaseIsItsOwnKeySpace(t *testing.T) {
	store := newStore()
	ctx := context.Background()

	ledger := open(t, store, spec("shop", "prod", "ledger"))
	tokens := open(t, store, spec("shop", "prod", "tokens"))
	staging := open(t, store, spec("shop", "staging", "ledger"))

	require.NoError(t, ledger.Put(ctx, "k", []byte("ledger")))
	require.NoError(t, tokens.Put(ctx, "k", []byte("tokens")))
	require.NoError(t, staging.Put(ctx, "k", []byte("staging")))

	type testCase struct {
		conn database.KVConn
		want string
	}
	for _, c := range []testCase{{ledger, "ledger"}, {tokens, "tokens"}, {staging, "staging"}} {
		value, err := c.conn.Get(ctx, "k")
		require.NoError(t, err)
		require.Equal(t, c.want, string(value))
	}

	keys, err := ledger.List(ctx, "")
	require.NoError(t, err)
	require.Equal(t, []string{"k"}, keys, "one key, its own")
}

func TestASeparatorInANameIsRefused(t *testing.T) {
	engine, err := kv.New(database.OneStore(newStore()))
	require.NoError(t, err)

	for _, s := range []database.Spec{
		spec("shop", "staging", "../production/ledger"),
		spec("shop", "a/b", "ledger"),
		spec("a/b", "prod", "ledger"),
	} {
		// The open succeeds and every operation fails (see Open).
		conn, err := engine.Open(context.Background(), s)
		require.NoError(t, err)

		typed := conn.(database.KVConn)
		_, err = typed.Get(context.Background(), "k")
		require.ErrorIs(t, err, database.ErrSeparator, "%v", s)
		require.ErrorIs(t, typed.Put(context.Background(), "k", nil), database.ErrSeparator)
	}
}

func TestAnEmptySegmentAddressesNothing(t *testing.T) {
	engine, err := kv.New(database.OneStore(newStore()))
	require.NoError(t, err)

	for _, s := range []database.Spec{
		spec("", "prod", "ledger"), spec("shop", "", "ledger"), spec("shop", "prod", ""),
	} {
		conn, err := engine.Open(context.Background(), s)
		require.NoError(t, err, "the registry's probe is one of these")

		_, err = conn.(database.KVConn).Get(context.Background(), "k")
		require.ErrorContains(t, err, "addresses nothing")
	}
}

func TestAPrefixCannotReachALongerOne(t *testing.T) {
	store := newStore()
	ctx := context.Background()

	long := open(t, store, spec("shop", "prod", "ledgers"))
	require.NoError(t, long.Put(ctx, "k", []byte("v")))

	short := open(t, store, spec("shop", "prod", "ledger"))
	keys, err := short.List(ctx, "")
	require.NoError(t, err)
	require.Empty(t, keys)
}

func TestAnEmptyKeyIsRefused(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "ledger"))
	ctx := t.Context()

	_, err := c.Get(ctx, "")
	require.ErrorIs(t, err, kv.ErrNoKey)
	require.ErrorIs(t, c.Put(ctx, "", nil), kv.ErrNoKey)
	require.ErrorIs(t, c.Delete(ctx, ""), kv.ErrNoKey)
}

func TestWatchSaysItIsNotServed(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "ledger"))

	_, err := c.Watch(t.Context(), "")
	require.ErrorIs(t, err, kv.ErrNoWatch)
}

func TestTheWrongTypeIsRefused(t *testing.T) {
	engine, err := kv.New(database.OneStore(newStore()))
	require.NoError(t, err)

	// THE TYPE IS REFUSED AT OPEN and not deferred: unlike an address, it is a
	// statement about what this engine is, and the registry never probes with
	// a type the engine did not claim.
	_, err = engine.Open(context.Background(), database.Spec{
		Type: database.SQL, Project: "shop", Environment: "prod", Name: "ledger",
	})
	require.ErrorContains(t, err, "backs kv")
}

func TestAStoreFailureIsNotAMiss(t *testing.T) {
	store := newStore()
	c := open(t, store, spec("shop", "prod", "ledger"))
	ctx := t.Context()

	store.err = errors.New("the swarm is unreachable")

	_, err := c.Get(ctx, "k")
	require.ErrorIs(t, err, store.err)
	require.NotErrorIs(t, err, kv.ErrNotFound)

	require.ErrorIs(t, c.Put(ctx, "k", nil), store.err)
	require.ErrorIs(t, c.Delete(ctx, "k"), store.err)
	_, err = c.List(ctx, "")
	require.ErrorIs(t, err, store.err)
}

func TestTheStoredKeyIsScoped(t *testing.T) {
	store := newStore()
	c := open(t, store, spec("shop", "prod", "ledger"))
	require.NoError(t, c.Put(t.Context(), "order:1", []byte("v")))

	require.Equal(t, []string{"shop/prod/kv/ledger/order:1"}, store.keys())
}

func TestEachDatabaseGetsItsOwnStore(t *testing.T) {
	stores := map[string]*memStore{}
	var asked []string

	engine, err := kv.New(func(_ context.Context, s database.Spec) (database.Store, error) {
		name, err := s.Prefix()
		if err != nil {
			return nil, err
		}
		asked = append(asked, name)
		if _, held := stores[name]; !held {
			stores[name] = newStore()
		}
		return stores[name], nil
	})
	require.NoError(t, err)

	ctx := t.Context()
	openOne := func(name string) database.KVConn {
		conn, err := engine.Open(ctx, spec("shop", "prod", name))
		require.NoError(t, err)
		return conn.(database.KVConn)
	}

	ledger, tokens := openOne("ledger"), openOne("tokens")
	require.Equal(t, []string{"shop/prod/kv/ledger/", "shop/prod/kv/tokens/"}, asked,
		"the provider was not asked per database")
	require.Len(t, stores, 2, "two databases shared one store")

	require.NoError(t, ledger.Put(ctx, "k", []byte("v")))
	require.NoError(t, tokens.Put(ctx, "k", []byte("v")))

	// DROPPING ONE LEAVES THE OTHER ALONE, which is the whole property: this
	// delete reaches the ledger's replicas and nobody else's.
	require.NoError(t, ledger.Delete(ctx, "k"))
	_, err = ledger.Get(ctx, "k")
	require.ErrorIs(t, err, kv.ErrNotFound)

	value, err := tokens.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, []byte("v"), value, "a drop in one database reached another")
}

func TestTheRegistrationProbeAsksForNoStore(t *testing.T) {
	asked := 0
	engine, err := kv.New(func(context.Context, database.Spec) (database.Store, error) {
		asked++
		return nil, errors.New("kv: the probe asked for a store")
	})
	require.NoError(t, err)

	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), engine))
	require.Zero(t, asked, "registration asked the provider for a store")
}
