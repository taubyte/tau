package secrets

import (
	"context"
	"errors"
	"fmt"

	ds "github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
)

// store.go is the production store: this package's four-method door over a
// replicated kvdb.
//
// REPLICATION IS THIS FILE'S WHOLE CONTRIBUTION, and it is entirely kvdb's
// doing — the same arrangement services/register keeps. A node handed this
// store holds every project's secrets; a node handed a map is a single-node
// development vault. Neither is a mode this package knows about.
//
// AND THAT REPLICATION IS EXACTLY WHAT AN ENTERPRISE DEPLOYMENT CANNOT ACCEPT.
// Every value here is at rest, in the clear, on every node in the swarm. In OSS
// that is the stated trust model and the swarm key is the boundary. What
// replaces it is not an option on this store — it is a different Store whose
// values are encrypted to a key no single node holds, which is why Store is an
// interface and why nothing above it knows what a value looks like on disk.

// NewKVStore adapts a kvdb to what a Vault needs.
func NewKVStore(db kvdb.KVDB) (Store, error) {
	if db == nil {
		return nil, errors.New("secrets: no database to store into")
	}
	return kvStore{db}, nil
}

type kvStore struct{ db kvdb.KVDB }

func (s kvStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := s.db.Get(ctx, key)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, ds.ErrNotFound):
		return nil, false, nil
	default:
		// The key is NOT in this message. Everywhere else in this repo a
		// failure names what it was reading; here the name is a project, an
		// environment and a secret, and the three together are a map of what
		// an environment holds. The caller already knows what it asked for.
		return nil, false, errors.New("secrets: reading: " + err.Error())
	}
}

func (s kvStore) Put(ctx context.Context, key string, value []byte) error {
	if err := s.db.Put(ctx, key, value); err != nil {
		return fmt.Errorf("secrets: writing: %w", err)
	}
	return nil
}

// Delete of a key that is not there succeeds, which is the datastore's own
// behaviour and the one Vault.Delete promises.
func (s kvStore) Delete(ctx context.Context, key string) error {
	if err := s.db.Delete(ctx, key); err != nil && !errors.Is(err, ds.ErrNotFound) {
		return fmt.Errorf("secrets: deleting: %w", err)
	}
	return nil
}

func (s kvStore) List(ctx context.Context, prefix string) ([]string, error) {
	keys, err := s.db.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("secrets: listing %s: %w", prefix, err)
	}
	return keys, nil
}
