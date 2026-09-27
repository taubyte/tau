package node

import (
	"fmt"

	"github.com/taubyte/tau/node/storage"
)

// storage.go opens the one database every bucket on this node keeps its index in.
//
// The adapter that serves the objects is node/storage. What is left here is the
// part only the root can do: reach the database factory an operator gave this
// node, name the database, and register what it opened with the closers — none
// of which an object has an opinion about, which is why it is asked for through
// one method and opened on the first bucket that needs it.

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
