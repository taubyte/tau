package instance

import (
	"context"
	"errors"
	"fmt"

	nodeDatabase "github.com/taubyte/tau/instance/database"
	"github.com/taubyte/tau/instance/ledger"
	"github.com/taubyte/tau/pkg/database"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/placement"
	databaseService "github.com/taubyte/tau/services/database"
)

// The database adapters are instance/database's; the root reaches the factory, names
// stores, registers closers, and hands over raft, the vault and the composer's
// registry. It also starts the database service; a node without one falls back to
// these engines (see kvBacking).

// databaseBacking is one consumer's databases: connections, their stores, and the
// groups ledgers are ordered in. One per consumer, so the wasm tier's fallback and the
// database service keep separate store tables; nothing crosses today since the
// adapter refuses a ledger before asking for a store.
type databaseBacking struct {
	*nodeDatabase.Databases

	holdings *nodeDatabase.Holdings
	ledgers  *ledger.Ledgers
}

// newDatabases wires one backing over what this node was given.
//
// THE GROUP TABLE IS BUILT HERE AND NOT ON FIRST USE, because building it costs
// nothing — it opens no group, which is the promise instance/ledger keeps — and
// because a table built lazily would have to be built again after Close, on a
// node whose groups are gone.
func newDatabases(n *Instance) (*databaseBacking, error) {
	b := &databaseBacking{}

	ledgers, err := ledger.New(n.raftGroup, ledger.Budget)
	if err != nil {
		return nil, fmt.Errorf("node: %s: %w", Database, err)
	}
	b.ledgers = ledgers

	holdings, err := nodeDatabase.NewHoldings(
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

var _ nodeDatabase.Stores = databaseStores{}

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

// openDatabase starts this node's database service, when it runs one.
//
// THE ENGINES ARE THIS NODE'S AND THE DECLARATIONS ARE THE REGISTER'S, which is
// the split the service was built for: it opens what a project declared, and it
// reads what a project declared rather than being told.
func (n *Instance) openDatabase(ctx context.Context) error {
	if !n.cfg.runs(Database) {
		return nil
	}

	// Both consensus resolutions, from the replicated store and the raft opener; a node
	// missing one refuses what it cannot keep, so a raft ledger is never merged.

	// The group is opened on first use, not here: raft finds peers over a swarm not yet
	// up, and a group opened now would abort its election. One store table for the
	// engine and holdings, so a destroyed store cannot stay warm in another table.
	stores, err := newDatabases(n)
	if err != nil {
		return err
	}
	engine, err := kvengine.New(stores.holdings.Entries)
	if err != nil {
		return fmt.Errorf("node: %s: %w", Database, err)
	}
	registry := database.NewRegistry()
	if err := registry.Register(ctx, engine); err != nil {
		return fmt.Errorf("node: %s: %w", Database, err)
	}

	// WHAT THIS NODE CAN ACTUALLY KEEP, which is a narrower question than what
	// the service implements. Both resolutions are served there; raft needs a
	// group to be opened in, and a node composed without one must refuse a
	// ledger BY NAME rather than answer "unavailable" about it forever.
	kept := []string{database.CRDT, database.Raft}

	svc, err := databaseService.New(n.Host(),
		databaseService.WithEngines(registry),
		databaseService.WithConsensus(kept...),
		databaseService.WithRegister(n.registerSource()),
		// WHAT THIS NODE ACTUALLY HOLDS, which is a different question from what
		// it can open: a node answers "do you hold this" from its disk, and is
		// the only thing that can let go of a copy.
		databaseService.WithHoldings(stores.holdings),

		// THE FLEET AS THIS NODE SEES IT, asked each sweep rather than taken
		// once: a snapshot at construction would place every database on the
		// fleet as it was when the process started. A node running no discovery
		// sees no fleet and moves nothing, which is the honest answer for a
		// node that cannot tell who else is there.
		databaseService.WithMembers(func() []placement.Member {
			if n.discovery == nil {
				return nil
			}
			return n.discovery.Placement()
		}),
	)
	if err != nil {
		return err
	}

	n.database = svc
	// THE GROUPS THIS NODE OPENED GO WITH THE SERVICE THAT OPENED THEM. A raft
	// cluster left running after the node stopped keeps a protocol handler, a
	// heartbeat and a log open on a peer nobody is using any more.
	n.closers = append(n.closers, stores)
	n.closers = append(n.closers, svc)
	return nil
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
