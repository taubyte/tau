package node

import (
	"fmt"

	"github.com/taubyte/tau/node/storage"
)

// The objects are node/storage's; the root reaches the factory, names the database,
// and registers it to close.

// storageDatabase is the one database's name, for cacheDatabase's reason.
const storageDatabase = "storage"

// storageStores opens that database for node/storage, on the first bucket that
// needs it.
type storageStores struct{ node *Node }

var _ storage.Stores = storageStores{}

func (s storageStores) Store() (storage.Rows, error) {
	if s.node.opts.databases == nil {
		return nil, ErrNoCacheStore
	}
	db, err := s.node.opts.databases.New(logger, storageDatabase, rebroadcastSeconds)
	if err != nil {
		return nil, fmt.Errorf("node: opening the storage database: %w", err)
	}
	// THROUGH addCloser AND NOT AN APPEND, because this runs on the request
	// path: two concurrent first-opens of different backings would otherwise
	// append to one slice at once.
	s.node.addCloser(closerFunc(db.Close))
	return cacheStore{db}, nil
}
