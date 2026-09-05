package secrets

import (
	"context"
	"errors"
	"testing"

	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
)

// fakeKV is a kvdb.KVDB with only the four methods this adapter uses. The
// embedded interface is nil: anything else this file starts calling panics
// loudly in a test rather than quietly widening what the adapter depends on.
type fakeKV struct {
	kvdb.KVDB
	rows map[string][]byte
	err  error
}

func newFakeKV() *fakeKV { return &fakeKV{rows: map[string][]byte{}} }

func (f *fakeKV) Get(_ context.Context, key string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	v, ok := f.rows[key]
	if !ok {
		return nil, ds.ErrNotFound
	}
	return v, nil
}

func (f *fakeKV) Put(_ context.Context, key string, value []byte) error {
	if f.err != nil {
		return f.err
	}
	f.rows[key] = value
	return nil
}

func (f *fakeKV) Delete(_ context.Context, key string) error {
	if f.err != nil {
		return f.err
	}
	if _, ok := f.rows[key]; !ok {
		return ds.ErrNotFound
	}
	delete(f.rows, key)
	return nil
}

func (f *fakeKV) List(_ context.Context, prefix string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []string
	for k := range f.rows {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, k)
		}
	}
	return out, nil
}

func TestAStoreNeedsADatabase(t *testing.T) {
	_, err := NewKVStore(nil)
	require.ErrorContains(t, err, "no database")
}

// TestTheKVStoreIsAVaultsFourMethods, round-tripped through a Vault so the key
// composition and the adapter are exercised as the pair they ship as.
func TestTheKVStoreIsAVaultsFourMethods(t *testing.T) {
	db := newFakeKV()
	store, err := NewKVStore(db)
	require.NoError(t, err)
	v, err := NewVault(store)
	require.NoError(t, err)
	ctx := t.Context()

	require.NoError(t, v.Set(ctx, "shop", "production", "TOKEN", []byte("v")))
	require.Equal(t, map[string][]byte{"shop/production/TOKEN": []byte("v")}, db.rows,
		"the vocabulary's key, and nothing prefixed onto it")

	value, err := v.Get(ctx, "shop", "production", "TOKEN")
	require.NoError(t, err)
	require.Equal(t, "v", string(value))

	names, err := v.List(ctx, "shop", "production")
	require.NoError(t, err)
	require.Equal(t, []string{"TOKEN"}, names)

	require.NoError(t, v.Delete(ctx, "shop", "production", "TOKEN"))
	_, err = v.Get(ctx, "shop", "production", "TOKEN")
	require.ErrorIs(t, err, ErrNotSet)
}

// TestNotFoundIsAMissAndEverythingElseIsAFailure, which is the one distinction
// this adapter exists to preserve.
func TestNotFoundIsAMissAndEverythingElseIsAFailure(t *testing.T) {
	db := newFakeKV()
	store, err := NewKVStore(db)
	require.NoError(t, err)
	ctx := t.Context()

	_, found, err := store.Get(ctx, "shop/production/TOKEN")
	require.NoError(t, err)
	require.False(t, found)

	// Deleting what is not there succeeds: the datastore says NotFound, the
	// caller wanted it gone, and it is gone.
	require.NoError(t, store.Delete(ctx, "shop/production/TOKEN"))

	db.err = errors.New("the swarm is unreachable")
	_, _, err = store.Get(ctx, "shop/production/TOKEN")
	require.Error(t, err)
	require.Error(t, store.Put(ctx, "shop/production/TOKEN", nil))
	require.Error(t, store.Delete(ctx, "shop/production/TOKEN"))
	_, err = store.List(ctx, "shop/production/")
	require.Error(t, err)
}

// TestAFailedReadDoesNotNameTheKey. A project, an environment and a secret
// name together are a map of what an environment holds, and a log line is not
// where that belongs.
func TestAFailedReadDoesNotNameTheKey(t *testing.T) {
	db := newFakeKV()
	db.err = errors.New("the swarm is unreachable")
	store, err := NewKVStore(db)
	require.NoError(t, err)

	_, _, err = store.Get(t.Context(), "shop/production/STRIPE_LIVE_KEY")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "STRIPE_LIVE_KEY")
	require.NotContains(t, err.Error(), "shop")
}
