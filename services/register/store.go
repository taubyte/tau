package register

import (
	"context"
	"errors"
	"fmt"

	ds "github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// Replication is entirely kvdb's: a node handed this store is a replica of the whole
// register, one handed a MemStore is a single-node development register. The key
// space is pkg/register's, untouched; a prefix would orphan every existing pointer
// if it ever changed.

// NewKVStore adapts a kvdb to what pkg/register needs.
func NewKVStore(db kvdb.KVDB) (pkgregister.Store, error) {
	if db == nil {
		return nil, errors.New("register: no database to store into")
	}
	return kvStore{db}, nil
}

type kvStore struct{ db kvdb.KVDB }

// Get separates "not there" from "could not look", so Resolve never reports an
// unreachable swarm as never deployed.
func (s kvStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := s.db.Get(ctx, key)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, ds.ErrNotFound):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("register: reading %s: %w", key, err)
	}
}

// List is kvdb's own prefix listing. Nothing under the prefix is an empty list,
// not an error.
func (s kvStore) List(ctx context.Context, prefix string) ([]string, error) {
	keys, err := s.db.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("register: listing %s: %w", prefix, err)
	}
	return keys, nil
}

func (s kvStore) Put(ctx context.Context, key string, value []byte) error {
	if err := s.db.Put(ctx, key, value); err != nil {
		return fmt.Errorf("register: writing %s: %w", key, err)
	}
	return nil
}
