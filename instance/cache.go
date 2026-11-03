package instance

import (
	"context"
	"errors"
	"fmt"

	ds "github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/instance/cache"
	pkgcache "github.com/taubyte/tau/pkg/cache"
)

// The caches are instance/cache's; the root reaches the database factory, names the
// database, and registers what it opened to close.

// ErrNoCacheStore is a node asked for a cache with no database factory. It is
// a composition fault: WithDatabases is what a node is given, and a node
// running a worker without one can run nothing that reaches state.
var ErrNoCacheStore = errors.New("node: no database factory to open a cache with")

// cacheDatabase is the one database's name. It is not a service name — no node
// "runs caches" — so it is spelled here rather than taken from the Services
// list.
const cacheDatabase = "caches"

// cacheStores opens that database for instance/cache.
type cacheStores struct{ node *Instance }

var _ cache.Stores = cacheStores{}

func (s cacheStores) Store() (pkgcache.Store, error) {
	if s.node.opts.databases == nil {
		return nil, ErrNoCacheStore
	}
	db, err := s.node.opts.databases.New(logger, cacheDatabase, rebroadcastSeconds)
	if err != nil {
		return nil, fmt.Errorf("node: opening the cache database: %w", err)
	}
	s.node.addCloser(closerFunc(db.Close))
	return cacheStore{db}, nil
}

func isNotFound(err error) bool { return errors.Is(err, ds.ErrNotFound) }

// cacheStore adapts a kvdb to the four methods a cache needs, separating "not there"
// from "could not look". instance/database.go and instance/admit use it too.
type cacheStore struct{ db kvdb.KVDB }

func (s cacheStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := s.db.Get(ctx, key)
	switch {
	case err == nil:
		return v, true, nil
	case isNotFound(err):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("node: reading a row: %w", err)
	}
}

func (s cacheStore) Put(ctx context.Context, key string, value []byte) error {
	return s.db.Put(ctx, key, value)
}

func (s cacheStore) Delete(ctx context.Context, key string) error {
	if err := s.db.Delete(ctx, key); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

func (s cacheStore) List(ctx context.Context, prefix string) ([]string, error) {
	return s.db.List(ctx, prefix)
}
