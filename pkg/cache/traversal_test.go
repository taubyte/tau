package cache_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/cache"
	"github.com/taubyte/tau/pkg/storekey"
)

// TestAGuestKeyCannotReachAnotherTenant.
//
// THE KEY IS THE ONE THING A GUEST CONTROLS ENTIRELY, and every store here
// addresses an entry as `<prefix><key>` with the prefix being the tenant. The
// stores put that joined string through ds.NewKey, which is path.Clean, so a
// key holding `..` collapsed the prefix away BEFORE anything was read:
//
//	"shopA/prod/cache-id/" + "../../../victim/prod/their-cache/session"
//	    -> /victim/prod/their-cache/session
//
// A cross-tenant read and a cross-tenant write, from text a workload wrote.
func TestAGuestKeyCannotReachAnotherTenant(t *testing.T) {
	store := &memStore{held: map[string][]byte{}}
	c, err := cache.Open(context.Background(), store, cache.Config{Prefix: "shopA/prod/cache-id"})
	require.NoError(t, err)

	escaping := "../../../victim/prod/their-cache/session"

	require.ErrorIs(t, c.Put(context.Background(), escaping, []byte("mine now")), storekey.ErrEscapes,
		"a write must not land outside the tenant")

	_, err = c.Get(context.Background(), escaping)
	require.ErrorIs(t, err, storekey.ErrEscapes, "and neither must a read")

	require.ErrorIs(t, c.Delete(context.Background(), escaping), storekey.ErrEscapes,
		"nor a delete, which is the one that destroys somebody else's data")

	// AND NOTHING WAS WRITTEN ANYWHERE, which is the assertion that would have
	// failed loudest: a refusal that still touched the store would be a bug
	// with a reassuring error on top.
	require.Empty(t, store.held)
}

// TestAnOrdinaryKeyStillWorks — the other half, so the guard is not a store
// that refuses everything.
func TestAHierarchicalKeyIsStillFine(t *testing.T) {
	store := &memStore{held: map[string][]byte{}}
	c, err := cache.Open(context.Background(), store, cache.Config{Prefix: "shopA/prod/cache-id"})
	require.NoError(t, err)

	require.NoError(t, c.Put(context.Background(), "users/42/name", []byte("ok")))
	got, err := c.Get(context.Background(), "users/42/name")
	require.NoError(t, err)
	require.Equal(t, []byte("ok"), got)
}

type memStore struct{ held map[string][]byte }

func (m *memStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	v, ok := m.held[key]
	return v, ok, nil
}
func (m *memStore) Put(_ context.Context, key string, value []byte) error {
	m.held[key] = value
	return nil
}
func (m *memStore) Delete(_ context.Context, key string) error {
	delete(m.held, key)
	return nil
}
func (m *memStore) List(_ context.Context, prefix string) ([]string, error) {
	var out []string
	for k := range m.held {
		out = append(out, k)
	}
	return out, nil
}
