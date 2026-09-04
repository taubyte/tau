package register

import (
	"context"
	"errors"
	"fmt"

	ds "github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// store.go is the production store: pkg/register's two-method door over a
// replicated kvdb.
//
// REPLICATION IS THIS FILE'S WHOLE CONTRIBUTION, and it is entirely kvdb's
// doing. pkg/register is a state machine over an injected store and says so;
// which node's write another node sees, and when, is a property of the CRDT
// underneath and not of anything the register decides. A node handed this store
// is a replica of the whole register; a node handed a MemStore is a
// single-node development register. Neither is a mode the service knows about.
//
// THE KEY SPACE IS pkg/register's, UNTOUCHED. Its keys are "o/<hex>" and
// "p/<project>/<environment>", with the separator refused inside a name — a
// grammar this file must not add a prefix to, because a prefix that ever
// changed would orphan every pointer written under the old one.

// NewKVStore adapts a kvdb to what pkg/register needs.
func NewKVStore(db kvdb.KVDB) (pkgregister.Store, error) {
	if db == nil {
		return nil, errors.New("register: no database to store into")
	}
	return kvStore{db}, nil
}

type kvStore struct{ db kvdb.KVDB }

// Get separates "not there" from "could not look", which is the distinction
// pkg/register's Store exists to preserve: a store that answered both with a
// miss would make Resolve report an environment as never deployed while the
// swarm was simply unreachable.
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

func (s kvStore) Put(ctx context.Context, key string, value []byte) error {
	if err := s.db.Put(ctx, key, value); err != nil {
		return fmt.Errorf("register: writing %s: %w", key, err)
	}
	return nil
}
