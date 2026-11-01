package cache_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/cache"
	"github.com/taubyte/tau/pkg/storekey"
)

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

	// And nothing was written anywhere.
	require.Empty(t, store.held)
}

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
