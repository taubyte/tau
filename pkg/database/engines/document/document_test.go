package document_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/database/engines/document"
)

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

func spec(project, environment, name string) database.Spec {
	return database.Spec{
		Type: database.Document, Engine: document.Name,
		Project: project, Environment: environment, Name: name,
	}
}

func open(t *testing.T, store database.Store, s database.Spec) database.DocumentConn {
	t.Helper()

	engine, err := document.New(database.OneStore(store))
	require.NoError(t, err)

	conn, err := engine.Open(t.Context(), s)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	typed, ok := conn.(database.DocumentConn)
	require.True(t, ok, "an engine that claims document must answer document's surface")
	return typed
}

func seed(t *testing.T, c database.DocumentConn) {
	t.Helper()
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "order:1", map[string]any{
		"status": "paid", "region": "eu", "total": 12.5,
		"address": map[string]any{"city": "algiers"},
	}))
	require.NoError(t, c.Put(ctx, "order:2", map[string]any{
		"status": "paid", "region": "us", "total": 30,
		"address": map[string]any{"city": "boston"},
	}))
	require.NoError(t, c.Put(ctx, "order:3", map[string]any{
		"status": "open", "region": "eu", "total": 5,
	}))
}

func TestAnEngineNeedsAStore(t *testing.T) {
	_, err := document.New(nil)
	require.ErrorIs(t, err, database.ErrNoStore)
}

func TestTheEngineSatisfiesTheRegistry(t *testing.T) {
	engine, err := document.New(database.OneStore(newStore()))
	require.NoError(t, err)

	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), engine))
	require.Contains(t, registry.EnginesFor(database.Document), document.Name)
}

func TestPutGetDelete(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "orders"))
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "order:1", map[string]any{"status": "paid"}))

	doc, err := c.Get(ctx, "order:1")
	require.NoError(t, err)
	require.Equal(t, "paid", doc["status"])

	require.NoError(t, c.Delete(ctx, "order:1"))
	_, err = c.Get(ctx, "order:1")
	require.ErrorIs(t, err, document.ErrNotFound)
}

func TestADocumentIsStoredAsJson(t *testing.T) {
	store := newStore()
	c := open(t, store, spec("shop", "prod", "orders"))
	require.NoError(t, c.Put(t.Context(), "order:1", map[string]any{"status": "paid"}))

	require.JSONEq(t, `{"status":"paid"}`, string(store.values["shop/prod/document/orders/order:1"]))
}

func TestAQueryMatchesOnEveryNamedField(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "orders"))
	seed(t, c)
	ctx := t.Context()

	paid, err := c.Query(ctx, map[string]any{"status": "paid"})
	require.NoError(t, err)
	require.Len(t, paid, 2)

	both, err := c.Query(ctx, map[string]any{"status": "paid", "region": "eu"})
	require.NoError(t, err)
	require.Len(t, both, 1, "both fields, not either")
	require.Equal(t, "algiers", both[0]["address"].(map[string]any)["city"])
}

func TestAnEmptyFilterReturnsAll(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "orders"))
	seed(t, c)

	all, err := c.Query(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, all, 3)

	// ORDERED BY ID, so two runs of one query answer the same way.
	require.Equal(t, "paid", all[0]["status"])
	require.Equal(t, "open", all[2]["status"])
}

func TestADottedPathDescends(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "orders"))
	seed(t, c)

	found, err := c.Query(t.Context(), map[string]any{"address.city": "boston"})
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "us", found[0]["region"])
}

func TestAnIntegerFilterMatchesAJsonNumber(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "orders"))
	seed(t, c)
	ctx := t.Context()

	for _, want := range []any{30, int64(30), 30.0, float32(30)} {
		found, err := c.Query(ctx, map[string]any{"total": want})
		require.NoError(t, err, "%T", want)
		require.Len(t, found, 1, "%T(%v)", want, want)
	}

	// And a number that is not there finds nothing.
	found, err := c.Query(ctx, map[string]any{"total": 99})
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestAnAbsentFieldDoesNotMatch(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "orders"))
	seed(t, c)

	found, err := c.Query(t.Context(), map[string]any{"address.city": "algiers", "missing": "x"})
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestEachDatabaseIsItsOwnCollection(t *testing.T) {
	store := newStore()
	ctx := context.Background()

	orders := open(t, store, spec("shop", "prod", "orders"))
	users := open(t, store, spec("shop", "prod", "users"))
	staging := open(t, store, spec("shop", "staging", "orders"))

	require.NoError(t, orders.Put(ctx, "1", map[string]any{"where": "orders"}))
	require.NoError(t, users.Put(ctx, "1", map[string]any{"where": "users"}))
	require.NoError(t, staging.Put(ctx, "1", map[string]any{"where": "staging"}))

	for _, c := range []struct {
		conn database.DocumentConn
		want string
	}{{orders, "orders"}, {users, "users"}, {staging, "staging"}} {
		all, err := c.conn.Query(ctx, nil)
		require.NoError(t, err)
		require.Len(t, all, 1)
		require.Equal(t, c.want, all[0]["where"])
	}
}

func TestASeparatorInANameIsRefused(t *testing.T) {
	engine, err := document.New(database.OneStore(newStore()))
	require.NoError(t, err)

	conn, err := engine.Open(context.Background(), spec("shop", "staging", "../production/orders"))
	require.NoError(t, err, "the registry's probe is address-less; refusing here would make this unregistrable")

	_, err = conn.(database.DocumentConn).Get(context.Background(), "1")
	require.ErrorIs(t, err, database.ErrSeparator)
}

func TestAnEmptyIdIsRefused(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "orders"))
	ctx := t.Context()

	_, err := c.Get(ctx, "")
	require.ErrorIs(t, err, document.ErrNoID)
	require.ErrorIs(t, c.Put(ctx, "", map[string]any{}), document.ErrNoID)
	require.ErrorIs(t, c.Delete(ctx, ""), document.ErrNoID)
	require.ErrorIs(t, c.Put(ctx, "x", nil), document.ErrNotAnObject)
}

func TestAnUnreadableRowDoesNotBreakAQuery(t *testing.T) {
	store := newStore()
	c := open(t, store, spec("shop", "prod", "orders"))
	require.NoError(t, c.Put(t.Context(), "good", map[string]any{"status": "paid"}))
	require.NoError(t, store.Put(t.Context(), "shop/prod/document/orders/junk", []byte("not json")))

	all, err := c.Query(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, "paid", all[0]["status"])

	// And reading it directly says what it is.
	_, err = c.Get(t.Context(), "junk")
	require.ErrorIs(t, err, document.ErrNotAnObject)
}

func TestAStoreFailureIsReported(t *testing.T) {
	store := newStore()
	c := open(t, store, spec("shop", "prod", "orders"))
	store.err = errors.New("the swarm is unreachable")

	_, err := c.Get(t.Context(), "1")
	require.ErrorIs(t, err, store.err)
	require.NotErrorIs(t, err, document.ErrNotFound)

	_, err = c.Query(t.Context(), nil)
	require.ErrorIs(t, err, store.err)
}

func TestTheWrongTypeIsRefused(t *testing.T) {
	engine, err := document.New(database.OneStore(newStore()))
	require.NoError(t, err)

	_, err = engine.Open(context.Background(), database.Spec{
		Type: database.KV, Project: "shop", Environment: "prod", Name: "orders",
	})
	require.ErrorContains(t, err, "backs document")
}

func TestAnUnaddressableConnectionFailsEverything(t *testing.T) {
	engine, err := document.New(database.OneStore(newStore()))
	require.NoError(t, err)

	conn, err := engine.Open(context.Background(), spec("shop", "", "orders"))
	require.NoError(t, err)
	typed := conn.(database.DocumentConn)
	ctx := context.Background()

	_, err = typed.Get(ctx, "1")
	require.ErrorContains(t, err, "addresses nothing")
	require.ErrorContains(t, typed.Put(ctx, "1", map[string]any{}), "addresses nothing")
	require.ErrorContains(t, typed.Delete(ctx, "1"), "addresses nothing")
	_, err = typed.Query(ctx, nil)
	require.ErrorContains(t, err, "addresses nothing")
}

func TestAnUnencodableDocumentIsRefused(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "orders"))

	// A channel has no JSON, which is what a caller carrying a live value into
	// a document looks like.
	err := c.Put(t.Context(), "1", map[string]any{"ch": make(chan int)})
	require.ErrorContains(t, err, "not encodable")

	_, err = c.Get(t.Context(), "1")
	require.ErrorIs(t, err, document.ErrNotFound, "nothing was written")
}

func TestNumbersOfEveryKindCompare(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "counts"))
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "1", map[string]any{"n": 7}))

	for _, want := range []any{7, int32(7), int64(7), uint(7), uint64(7), 7.0, float32(7)} {
		found, err := c.Query(ctx, map[string]any{"n": want})
		require.NoError(t, err)
		require.Len(t, found, 1, "%T", want)
	}

	// A NUMBER AGAINST A STRING IS NOT A MATCH, so "7" does not find 7.
	found, err := c.Query(ctx, map[string]any{"n": "7"})
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestADottedPathIntoAScalarDoesNotMatch(t *testing.T) {
	c := open(t, newStore(), spec("shop", "prod", "orders"))
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "1", map[string]any{"total": 5}))

	found, err := c.Query(ctx, map[string]any{"total.currency": "eur"})
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestARowTakenBetweenTheListAndTheReadIsSkipped(t *testing.T) {
	store := newStore()
	c := open(t, store, spec("shop", "prod", "orders"))
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "gone", map[string]any{"status": "paid"}))
	require.NoError(t, c.Put(ctx, "here", map[string]any{"status": "paid"}))

	// List will name it; the read will not find it.
	store.mu.Lock()
	delete(store.values, "shop/prod/document/orders/gone")
	store.mu.Unlock()

	all, err := c.Query(ctx, nil)
	require.NoError(t, err)
	require.Len(t, all, 1)
}
