package hosted

import (
	"fmt"

	pkgcache "github.com/taubyte/tau/pkg/cache"
	"github.com/taubyte/tau/services/worker/cache"
)

// The caches are services/worker/cache's; the worker opens the database, names it,
// and registers what it opened to close.

// cacheDatabase is the one database's name. It is not a service name — no node
// "runs caches" — so it is spelled here rather than taken from the Services
// list.
const cacheDatabase = "caches"

// cacheStores opens that database for services/worker/cache.
type cacheStores struct{ node *Service }

var _ cache.Stores = cacheStores{}

func (s cacheStores) Store() (pkgcache.Store, error) {
	db, err := s.node.dbs.Database(cacheDatabase)
	if err != nil {
		return nil, fmt.Errorf("worker: opening the cache database: %w", err)
	}
	s.node.addCloser(closerFunc(db.Close))
	return pkgcache.Over(db), nil
}
