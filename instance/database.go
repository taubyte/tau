package instance

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/database"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	databaseService "github.com/taubyte/tau/services/database"
	nodeDatabase "github.com/taubyte/tau/services/worker/database"
)

// The database adapters are services/worker/database's; the root reaches the factory, names
// stores, registers closers, and hands over raft, the vault and the composer's
// registry. A node running the database service serves kv from it; one without falls
// back to these engines (see kvBacking).

// databaseBacking is one consumer's databases: connections, their stores, and the
// groups ledgers are ordered in. One per consumer, so the wasm tier's fallback and the
// database service keep separate store tables; nothing crosses today since the
// adapter refuses a ledger before asking for a store.
type databaseBacking struct {
	*nodeDatabase.Databases

	holdings *databaseService.Copies
	ledgers  *databaseService.RaftLedgers
}

// newDatabases wires one backing over what this node was given.
//
// THE GROUP TABLE IS BUILT HERE AND NOT ON FIRST USE, because building it costs
// nothing — it opens no group, which is the promise instance/ledger keeps — and
// because a table built lazily would have to be built again after Close, on a
// node whose groups are gone.
func newDatabases(n *Instance) (*databaseBacking, error) {
	b := &databaseBacking{}

	ledgers, err := databaseService.NewRaftLedgers(n.raftGroup, databaseService.LedgerBudget)
	if err != nil {
		return nil, fmt.Errorf("node: %s: %w", Database, err)
	}
	b.ledgers = ledgers

	holdings, err := databaseService.NewCopies(
		databaseStores{node: n},
		b.ledgers,
		// Holding is a fact about this instance's bytes, so it is its own set
		// that answers it.
		n.dbs,
	)
	if err != nil {
		return nil, fmt.Errorf("node: %s: %w", Database, err)
	}
	b.holdings = holdings

	// THE COMPOSER'S REGISTRY AS IT IS, nil included: nothing here links a
	// database driver, and which engines a deployment has is WithDatabaseEngines'
	// to say. tau's own three are registered by the adapter on first use.
	databases, err := nodeDatabase.New(n.opts.engines, holdings.Entries, databaseSecrets{node: n})
	if err != nil {
		return nil, fmt.Errorf("node: %s: %w", Database, err)
	}
	b.Databases = databases
	return b, nil
}

// Close releases the connections and then the groups this backing opened.
//
// IN THAT ORDER, because a ledger's connection is a prefix over a group: closing
// the group first would leave the kv connection above it writing into a store
// whose cluster is gone.
func (b *databaseBacking) Close() error {
	err := b.Databases.Close()
	if b.ledgers != nil {
		err = errors.Join(err, b.ledgers.Close())
	}
	return err
}

// databaseSecrets reads the secret a connection's credentials were declared as, with
// its own vault per read so resolving does not depend on which other services run.
type databaseSecrets struct{ node *Instance }

var _ nodeDatabase.Secrets = databaseSecrets{}

func (s databaseSecrets) Secret(ctx context.Context, project, environment, name string) (string, error) {
	vault := newVault(s.node)
	defer vault.Close()
	return vault.Secret(ctx, project, environment, name)
}

// databaseStores opens the store one database's entries live in, on the first
// request that reaches that database — the same lazy seam instance/cache.go,
// instance/storage.go and instance/volume.go keep, with the name as its argument because
// a node carries one store per database rather than one for all of them.
type databaseStores struct{ node *Instance }

var _ databaseService.Stores = databaseStores{}

func (s databaseStores) Store(name string) (database.Store, error) {
	db, err := s.node.kvdb(name)
	if err != nil {
		return nil, fmt.Errorf("node: opening the store for %s: %w", name, err)
	}
	// THROUGH addCloser AND NOT AN APPEND, because this runs on the request
	// path: two concurrent first-opens of different backings would otherwise
	// append to one slice at once.
	s.node.addCloser(closerFunc(db.Close))
	return cacheStore{db}, nil
}

// Release lets go of this node's copy.
//
// IT IS OPENED TO BE DESTROYED AND NOT REGISTERED WITH THE CLOSERS. Not open is
// not absent — a restarted node holds bytes it has never opened — and the
// factory caches by path, so this is the store the node was serving from when
// there was one. Destroying closes it and drops it from the factory, so a later
// open builds a new one from whatever the fleet still holds.
func (s databaseStores) Release(ctx context.Context, name string) error {
	db, err := s.node.kvdb(name)
	if err != nil {
		return err
	}

	destroyer, ok := db.(interface {
		Destroy(context.Context) error
	})
	if !ok {
		return fmt.Errorf("node: this store cannot release a database it holds")
	}
	return destroyer.Destroy(ctx)
}

// kvBacking is what the wasm tier opens a kv through: the local database service
// when there is one (Local(), no hop), otherwise this node's own engines. Reaching a
// remote database service is not wired yet.
func (n *Instance) kvBacking(own wasmtier.KVs) wasmtier.KVs {
	if n.database != nil {
		return n.database.Local()
	}
	return own
}
