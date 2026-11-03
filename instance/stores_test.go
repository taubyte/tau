package instance

import (
	"context"
	"errors"
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
)

func TestEveryBackingOpensItsDatabaseByName(t *testing.T) {
	type opener func(*Instance) (any, error)
	for name, open := range map[string]opener{
		cacheDatabase:     func(n *Instance) (any, error) { return cacheStores{node: n}.Store() },
		admissionDatabase: func(n *Instance) (any, error) { return admissionStores{node: n}.Store() },
		storageDatabase:   func(n *Instance) (any, error) { return storageStores{node: n}.Store() },
		volumeDatabase:    func(n *Instance) (any, error) { return volumeStores{node: n}.Store() },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := open(&Instance{})
			require.ErrorIs(t, err, ErrNoCacheStore, "no factory is said, not panicked on")

			refused := errors.New("disk full")
			_, err = open(&Instance{opts: options{databases: &memFactory{err: refused}}})
			require.ErrorIs(t, err, refused)

			factory := &memFactory{}
			n := &Instance{opts: options{databases: factory}}
			rows, err := open(n)
			require.NoError(t, err)
			require.NotNil(t, rows)
			opened, _ := factory.counts()
			require.Equal(t, []string{name}, opened)
			require.Len(t, n.closers, 1, "the database is closed with the node")
		})
	}
}

// failingKV is a database that answers every read and delete with a failure
// that is not "not there".
type failingKV struct {
	kvdb.KVDB
	err error
}

func (f failingKV) Get(context.Context, string) ([]byte, error) { return nil, f.err }
func (f failingKV) Delete(context.Context, string) error        { return f.err }

func TestACacheStoreSeparatesAbsentFromUnreadable(t *testing.T) {
	ctx := t.Context()
	s := cacheStore{&memKV{rows: map[string][]byte{}, factory: &memFactory{}}}

	_, found, err := s.Get(ctx, "/missing")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, s.Delete(ctx, "/missing"), "deleting what is not there is done")

	require.NoError(t, s.Put(ctx, "/k", []byte("v")))
	v, found, err := s.Get(ctx, "/k")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("v"), v)
	require.NoError(t, s.Delete(ctx, "/k"))
	_, found, err = s.Get(ctx, "/k")
	require.NoError(t, err)
	require.False(t, found)

	broken := errors.New("io error")
	_, _, err = cacheStore{failingKV{err: broken}}.Get(ctx, "/k")
	require.ErrorIs(t, err, broken)
	require.ErrorIs(t, cacheStore{failingKV{err: broken}}.Delete(ctx, "/k"), broken)
}

func TestTheVolumeBackingIsOnePerNode(t *testing.T) {
	none, err := (&Instance{}).disksOrNil()
	require.NoError(t, err)
	require.Nil(t, none)

	n := &Instance{opts: options{root: t.TempDir()}}
	first, err := n.disksOrNil()
	require.NoError(t, err)
	require.NotNil(t, first)
	again, err := n.disksOrNil()
	require.NoError(t, err)
	require.Same(t, first, again)
	require.Len(t, n.closers, 1)
}

func TestADatabaseStoreIsOpenedAndReleasedByName(t *testing.T) {
	ctx := t.Context()
	_, err := databaseStores{node: &Instance{}}.Store("orders")
	require.ErrorIs(t, err, ErrNoCacheStore)
	require.ErrorIs(t, databaseStores{node: &Instance{}}.Release(ctx, "orders"), ErrNoCacheStore)

	refused := errors.New("disk full")
	broken := databaseStores{node: &Instance{opts: options{databases: &memFactory{err: refused}}}}
	_, err = broken.Store("orders")
	require.ErrorIs(t, err, refused)
	require.ErrorContains(t, err, "orders")
	require.ErrorIs(t, broken.Release(ctx, "orders"), refused)

	// A STORE THAT CANNOT BE DESTROYED IS NOT RELEASED QUIETLY: the copy
	// would stay on disk while the node believed it gone.
	require.ErrorContains(t, databaseStores{node: &Instance{opts: options{databases: &memFactory{}}}}.Release(ctx, "orders"),
		"cannot release")
}

func TestADatabaseCredentialNeedsAVault(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register}})
	_, err := databaseSecrets{node: n}.Secret(t.Context(), "shop", "prod", "dbpass")
	require.ErrorIs(t, err, ErrNoSecrets)
}

func TestADatabaseNodeNeedsAFactory(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)
	_, err := New(Config{Services: []string{Database}}, WithHost(h), WithPubSub(newPubSub(t, h)))
	require.ErrorIs(t, err, ErrNoCacheStore)
}
