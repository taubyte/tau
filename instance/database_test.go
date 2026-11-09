package instance

import (
	"context"
	"sync"
	"testing"

	"github.com/ipfs/go-log/v2"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
)

// What the root owes services/worker/database: stores opened through the operator's factory and
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

func (f *releasing) New(_ log.StandardLogger, path string, _ int) (kvdb.Instance, error) {
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
	kvdb.Instance
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
	n := overDouble(t, factory)
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
	n, _, factory := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	seam := databaseStores{node: n}

	require.ErrorContains(t, seam.Release(t.Context(), "databases/shop/prod/kv/QmA"),
		"cannot release a database it holds")

	opened, _ := factory.counts()
	require.Contains(t, opened, "databases/shop/prod/kv/QmA")
}
