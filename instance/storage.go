package instance

import (
	"fmt"

	"github.com/taubyte/tau/services/worker/storage"
)

// The objects are services/worker/storage's; the root reaches the factory, names the database,
// and registers it to close.

// storageDatabase is the one database's name, for cacheDatabase's reason.
const storageDatabase = "storage"

// storageStores opens that database for services/worker/storage, on the first bucket that
// needs it.
type storageStores struct{ node *Instance }

var _ storage.Stores = storageStores{}

func (s storageStores) Store() (storage.Rows, error) {
	db, err := s.node.kvdb(storageDatabase)
	if err != nil {
		return nil, fmt.Errorf("node: opening the storage database: %w", err)
	}
	// THROUGH addCloser AND NOT AN APPEND, because this runs on the request
	// path: two concurrent first-opens of different backings would otherwise
	// append to one slice at once.
	s.node.addCloser(closerFunc(db.Close))
	return cacheStore{db}, nil
}
