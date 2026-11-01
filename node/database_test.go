package node

import (
	"context"
	"sync"
	"testing"

	"github.com/ipfs/go-log/v2"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
)

// What the root owes node/database: stores opened through the operator's factory and
// closed with the node, release through the one path that removes bytes, and the disk
// surface asserted off a factory that may lack it.

// releasing is a kvdb.Factory whose stores can be destroyed, which memFactory's
// cannot. Destroying behaves like the real one: the store is closed and dropped
// from the factory, so a later open builds a new one from whatever the fleet
// still holds.
type releasing struct {
	mu     sync.Mutex
	byPath map[string]*releasable
}

func (f *releasing) New(_ log.StandardLogger, path string, _ int) (kvdb.KVDB, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.byPath == nil {
		f.byPath = map[string]*releasable{}
	}
	if held, ok := f.byPath[path]; ok {
		return held, nil
	}
	db := &releasable{factory: f, path: path}
	f.byPath[path] = db
	return db, nil
}

func (f *releasing) Close() {}

func (f *releasing) held(path string) (*releasable, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	db, ok := f.byPath[path]
	return db, ok
}

type releasable struct {
	kvdb.KVDB
	factory *releasing
	path    string

	destroyed bool
}

func (r *releasable) Destroy(context.Context) error {
	r.factory.mu.Lock()
	defer r.factory.mu.Unlock()
	r.destroyed = true
	delete(r.factory.byPath, r.path)
	return nil
}

func TestTheRootReleasesThroughTheFactoryItWasGiven(t *testing.T) {
	factory := &releasing{}
	n := &Node{opts: options{databases: factory}}
	seam := databaseStores{node: n}
	const name = "databases/shop/prod/kv/QmA"

	store, err := seam.Store(name)
	require.NoError(t, err)
	require.NotNil(t, store)
	require.Len(t, n.closers, 1, "a store the node serves from was not registered with the closers")

	require.NoError(t, seam.Release(t.Context(), name))

	_, still := factory.held(name)
	require.False(t, still, "the destroyed store is still the factory's")
	require.Len(t, n.closers, 1, "a store opened only to be destroyed was registered with the closers")

	// AND A COPY THIS NODE NEVER OPENED IS STILL RELEASED, which is the case
	// this branch exists for.
	require.NoError(t, seam.Release(t.Context(), "databases/shop/prod/kv/QmNeverOpened"))
	released, ok := factory.held("databases/shop/prod/kv/QmNeverOpened")
	require.False(t, ok)
	require.Nil(t, released)
}

func TestAStoreThatCannotBeReleasedIsNamed(t *testing.T) {
	n, _, factory := makeNode(t, Config{Services: []string{Register, Worker, Discovery}})
	seam := databaseStores{node: n}

	require.ErrorContains(t, seam.Release(t.Context(), "databases/shop/prod/kv/QmA"),
		"cannot release a database it holds")

	opened, _ := factory.counts()
	require.Contains(t, opened, "databases/shop/prod/kv/QmA")
}

func TestTheStoreSeamNeedsAFactory(t *testing.T) {
	seam := databaseStores{node: &Node{opts: options{}}}

	_, err := seam.Store("databases/shop/prod/kv/QmA")
	require.ErrorIs(t, err, ErrNoCacheStore)
	require.ErrorIs(t, seam.Release(t.Context(), "databases/shop/prod/kv/QmA"), ErrNoCacheStore)
}

func TestTheDiskSurfaceIsAssertedAndMayBeAbsent(t *testing.T) {
	require.Nil(t, databaseDisk(nil), "a node with no factory has no disk to ask")
	require.Nil(t, databaseDisk(&memFactory{}), "a factory that keeps no bytes answered as a disk")
	require.NotNil(t, databaseDisk(&probing{}))
}

// probing is a factory that can be asked what it holds, which pkg/kvdb's is.
type probing struct{ memFactory }

func (p *probing) Holding(context.Context, string) (bool, error) { return false, nil }
func (p *probing) Paths(context.Context, string, int) ([]string, error) {
	return nil, nil
}
