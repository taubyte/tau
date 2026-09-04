package register

import (
	"context"
	"errors"
	"testing"

	ds "github.com/ipfs/go-datastore"
	logpkg "github.com/ipfs/go-log/v2"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
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

// LIST RETURNS WHOLE KEYS, NOT SUFFIXES, and pkg/register's Environments
// trims the prefix off what comes back — so if kvdb ever answered relative
// keys, every deployment name would come back as the whole pointer key and a
// build would look for a deployment nobody declared.
//
// It is checked against a REAL kvdb rather than the fake above, because the
// fake is this file's own answer to the question and would agree with itself.
func TestListReturnsWholeKeys(t *testing.T) {
	factory := kvdbmock.New()
	t.Cleanup(factory.Close)

	db, err := factory.New(logpkg.Logger("register.test"), "register", 1)
	require.NoError(t, err)

	for _, key := range []string{"p/shop/production", "p/shop/staging", "p/other/production"} {
		require.NoError(t, db.Put(t.Context(), key, []byte("x")))
	}

	store, err := NewKVStore(db)
	require.NoError(t, err)

	keys, err := store.List(t.Context(), "p/shop/")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"p/shop/production", "p/shop/staging"}, keys)

	// A prefix nothing is under is an empty list, not an error: "nothing
	// published" and "could not look" are different answers.
	keys, err = store.List(t.Context(), "p/nobody/")
	require.NoError(t, err)
	require.Empty(t, keys)
}

func TestListReportsAStoreThatCannotLook(t *testing.T) {
	store, err := NewKVStore(&fakeKV{rows: map[string][]byte{}, err: errors.New("the swarm is unreachable")})
	require.NoError(t, err)

	_, err = store.List(t.Context(), "p/")
	require.ErrorContains(t, err, "unreachable")
}
