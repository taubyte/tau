package node

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/core/kvdb"
	nodeDatabase "github.com/taubyte/tau/node/database"
	"github.com/taubyte/tau/node/ledger"
	"github.com/taubyte/tau/pkg/database"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/placement"
	databaseService "github.com/taubyte/tau/services/database"
)

// database.go names what this node's databases are made of.
//
// The adapters are node/database: the four data models the wasm tier opens, and
// the stores their entries go into. What is left here is the part only the root
// can do — reach the database factory an operator gave this node, name a store,
// register what it opened with the closers, reach the raft opener, reach the
// vault, and hand the composer's own registry over — none of which a database has
// an opinion about.
//
// AND STARTING THE DATABASE SERVICE, which is the other half of this file: a node
// that runs one serves its project's databases from there, and a node that does
// not falls back to these engines. See kvBacking for how much of that cutover has
// happened.

// databaseBacking is one consumer's databases on this node: the connections it
// opens, the stores their entries go into, and the groups a ledger is ordered in.
//
// ONE PER CONSUMER AND NOT ONE FOR THE NODE, which is what it already was and is
// worth saying out loud: the wasm tier's fallback engines and the database
// service's own engines are built separately and so each keeps its own store
// table. Nothing crosses between them today because the service serves one cell
// of the grid — tau's own kv — and the adapter refuses a ledger through these
// engines by name before a store is ever asked for. It is a seam to watch rather
// than a bug.
type databaseBacking struct {
	*nodeDatabase.Databases

	holdings *nodeDatabase.Holdings
	ledgers  *ledger.Ledgers
}

// newDatabases wires one backing over what this node was given.
//
// THE GROUP TABLE IS BUILT HERE AND NOT ON FIRST USE, because building it costs
// nothing — it opens no group, which is the promise node/ledger keeps — and
// because a table built lazily would have to be built again after Close, on a
// node whose groups are gone.
func newDatabases(n *Node) (*databaseBacking, error) {
	b := &databaseBacking{}

	if n.opts.raft != nil {
		ledgers, err := ledger.New(n.opts.raft, ledger.Budget)
		if err != nil {
			return nil, fmt.Errorf("node: %s: %w", Database, err)
		}
		b.ledgers = ledgers
	}

	holdings, err := nodeDatabase.NewHoldings(
		databaseStores{node: n},
		databaseLedgers{ledgers: b.ledgers},
		// THE DISK-LEVEL SURFACE IS ASSERTED rather than required of
		// kvdb.Factory: holding is a fact about a NODE's bytes, and the shared
		// interface is also implemented by handles that keep none.
		databaseDisk(n.opts.databases),
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

// databaseSecrets reads the secret a connection's credentials were declared as.
//
// ONE VAULT PER READ AND NOT THE NODE'S, which is what this did before the
// adapter left and is kept deliberately: n.vault is built with the wasm tier, and
// a node running the database service alone has none — so sharing it would make
// whether a credential can be resolved depend on which other services this node
// happens to run.
type databaseSecrets struct{ node *Node }

var _ nodeDatabase.Secrets = databaseSecrets{}

func (s databaseSecrets) Secret(ctx context.Context, project, environment, name string) (string, error) {
	vault := newVault(s.node)
	defer vault.Close()
	return vault.Secret(ctx, project, environment, name)
}

// databaseStores opens the store one database's entries live in, on the first
// request that reaches that database — the same lazy seam node/cache.go,
// node/storage.go and node/volume.go keep, with the name as its argument because
// a node carries one store per database rather than one for all of them.
type databaseStores struct{ node *Node }

var _ nodeDatabase.Stores = databaseStores{}

func (s databaseStores) Store(name string) (database.Store, error) {
	if s.node.opts.databases == nil {
		return nil, ErrNoCacheStore
	}
	db, err := s.node.opts.databases.New(logger, name, rebroadcastSeconds)
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
	if s.node.opts.databases == nil {
		return ErrNoCacheStore
	}
	db, err := s.node.opts.databases.New(logger, name, rebroadcastSeconds)
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

// databaseLedgers is the root's answer for `consensus: raft`.
//
// THE REFUSAL IS HERE AND NOT IN THE ADAPTER, because ErrNoRaft is the one
// sentinel every consumer of consensus on this node shares and names none of
// them — see config.go.
type databaseLedgers struct{ ledgers *ledger.Ledgers }

var _ nodeDatabase.Ledgers = databaseLedgers{}

func (l databaseLedgers) Store(spec database.Spec) (database.Store, error) {
	if l.ledgers == nil {
		return nil, ErrNoRaft
	}
	return l.ledgers.Store(spec)
}

// databaseDisk is the factory's own disk-level surface, or nil when it has none.
func databaseDisk(factory kvdb.Factory) nodeDatabase.Disk {
	disk, ok := factory.(nodeDatabase.Disk)
	if !ok {
		return nil
	}
	return disk
}

// openDatabase starts this node's database service, when it runs one.
//
// THE ENGINES ARE THIS NODE'S AND THE DECLARATIONS ARE THE REGISTER'S, which is
// the split the service was built for: it opens what a project declared, and it
// reads what a project declared rather than being told.
func (n *Node) openDatabase(ctx context.Context) error {
	if !n.cfg.runs(Database) {
		return nil
	}

	// BOTH HALVES OF THE CONSENSUS AXIS, from the two things this node already
	// has: the replicated CRDT store every other v2 store is built on, and the
	// raft cluster the queues and crons run in. A node missing either serves
	// the other and refuses what it cannot keep, which is the point — a ledger
	// that asked for raft must not be merged.
	if n.opts.databases == nil {
		return fmt.Errorf("node: %s: %w", Database, ErrNoCacheStore)
	}

	// THE GROUP IS OPENED ON FIRST USE AND NOT HERE. A node builds its services
	// before it starts them, and raft finds its peers over a swarm that is not
	// up yet — a group opened at construction sits with no known peers, aborts
	// its election, and answers every write with "no leader". node/queue.go
	// opens its own in the background for the same reason.
	//
	// ONE STORE TABLE FOR THE NODE, shared by the engine that serves this
	// node's databases and the holdings that let go of them. Two tables is how
	// a destroyed store stays warm and serving: the eviction lands in one of
	// them and every later open comes out of the other.
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
	kept := []string{database.CRDT}
	if n.opts.raft != nil {
		kept = append(kept, database.Raft)
	}

	svc, err := databaseService.New(n.opts.host,
		databaseService.WithEngines(registry),
		databaseService.WithConsensus(kept...),
		databaseService.WithRegister(n.registerSource()),
		// WHAT THIS NODE ACTUALLY HOLDS, which is a different question from what
		// it can open: a node answers "do you hold this" from its disk, and is
		// the only thing that can let go of a copy.
		databaseService.WithHoldings(stores.holdings),
		databaseService.WithRegion(n.cfg.Region),

		// THE FLEET AS THIS NODE SEES IT, asked each sweep rather than taken
		// once: a snapshot at construction would place every database on the
		// fleet as it was when the process started. A node running no discovery
		// sees no fleet and moves nothing, which is the honest answer for a
		// node that cannot tell who else is there.
		databaseService.WithMembers(func() map[string][]placement.Member {
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

// kvBacking is what the wasm tier opens a kv through.
//
// THE SERVICE WHEN THERE IS ONE, and this node's own engines otherwise. The
// second half is what v1 did everywhere and is kept only for a node that runs
// no database service and can reach none — a single-process test, a composition
// that predates the service. A node that runs the service takes Local(), which
// is the absence of a hop rather than a shortcut.
//
// REACHING ONE IN THE FLEET IS NOT HERE YET. A node with no database service of
// its own still falls back, so the data plane is still the worker's on that
// node — that is the remaining half of the cutover, and it is stated rather
// than papered over.
func (n *Node) kvBacking(own wasmtier.KVs) wasmtier.KVs {
	if n.database != nil {
		return n.database.Local()
	}
	return own
}
