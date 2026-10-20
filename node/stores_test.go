package node

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
)

// EVERY BACKING OPENS ITS ONE DATABASE THE SAME WAY: by name, from the
// factory, closed with the node — and says so by name when it cannot.
func TestEveryBackingOpensItsDatabaseByName(t *testing.T) {
	type opener func(*Node) (any, error)
	for name, open := range map[string]opener{
		cacheDatabase:     func(n *Node) (any, error) { return cacheStores{node: n}.Store() },
		admissionDatabase: func(n *Node) (any, error) { return admissionStores{node: n}.Store() },
		storageDatabase:   func(n *Node) (any, error) { return storageStores{node: n}.Store() },
		volumeDatabase:    func(n *Node) (any, error) { return volumeStores{node: n}.Store() },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := open(&Node{})
			require.ErrorIs(t, err, ErrNoCacheStore, "no factory is said, not panicked on")

			refused := errors.New("disk full")
			_, err = open(&Node{opts: options{databases: &memFactory{err: refused}}})
			require.ErrorIs(t, err, refused)

			factory := &memFactory{}
			n := &Node{opts: options{databases: factory}}
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

// A ROW THAT IS NOT THERE IS AN ANSWER; A STORE THAT CANNOT LOOK IS NOT.
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

// A NODE WITH A ROOT HAS ONE VOLUME BACKING, made once and closed with the
// node; a node without one has none, which is what makes a tier refuse a mount.
func TestTheVolumeBackingIsOnePerNode(t *testing.T) {
	none, err := (&Node{}).disksOrNil()
	require.NoError(t, err)
	require.Nil(t, none)

	n := &Node{opts: options{root: t.TempDir()}}
	first, err := n.disksOrNil()
	require.NoError(t, err)
	require.NotNil(t, first)
	again, err := n.disksOrNil()
	require.NoError(t, err)
	require.Same(t, first, again)
	require.Len(t, n.closers, 1)
}
