package register

import (
	"context"
	"errors"
	"testing"

	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
)

// fakeKV is a kvdb.KVDB with only the two methods this adapter uses. The
// embedded interface is nil: anything else this file starts calling panics
// loudly in a test rather than quietly widening what the adapter depends on.
type fakeKV struct {
	kvdb.KVDB
	rows map[string][]byte
	err  error
}

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

func (f *fakeKV) Put(_ context.Context, key string, v []byte) error {
	if f.err != nil {
		return f.err
	}
	f.rows[key] = v
	return nil
}

// TestTheKVStoreSeparatesMissingFromBroken. This is the whole reason
// pkg/register's Store has three results: a store that answered both with a
// miss would make Resolve report an environment as never deployed while the
// swarm was simply unreachable.
func TestTheKVStoreSeparatesMissingFromBroken(t *testing.T) {
	db := &fakeKV{rows: map[string][]byte{}}
	s, err := NewKVStore(db)
	require.NoError(t, err)

	_, found, err := s.Get(t.Context(), "p/shop/prod")
	require.NoError(t, err)
	require.False(t, found, "not there is not an error")

	require.NoError(t, s.Put(t.Context(), "p/shop/prod", []byte("1 abc")))

	v, found, err := s.Get(t.Context(), "p/shop/prod")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("1 abc"), v)

	db.err = errors.New("the swarm is unreachable")

	_, found, err = s.Get(t.Context(), "p/shop/prod")
	require.Error(t, err)
	require.False(t, found)
	require.Contains(t, err.Error(), "p/shop/prod", "the error names the row")

	err = s.Put(t.Context(), "p/shop/prod", []byte("2 def"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "p/shop/prod")
}

func TestTheKVStoreRefusesNoDatabase(t *testing.T) {
	_, err := NewKVStore(nil)
	require.Error(t, err)
}
