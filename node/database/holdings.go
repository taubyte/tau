package database

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/database"
	databaseService "github.com/taubyte/tau/services/database"
)

// holdings.go is the copies of a database this node keeps. The package doc says
// why it is not in the root; what is here is the half below the engines.

// Prefix is what every database's own store is named under.
//
// SEPARATE FROM THE CACHES', because they are different kinds with different
// lifetimes: a cache evicts and a database does not, and one CRDT holding both
// would make a cache's churn the database's replication traffic.
const Prefix = "databases"

// Name is the one name a database is known by here — what addresses it, what
// names its topic, and what a release evicts by, so the three agree by
// construction rather than by three expressions that happen to match.
//
// Spec.Prefix already names a database uniquely and already refuses a separator
// in any segment, which is exactly what a topic name needs.
func Name(spec database.Spec) (string, error) {
	prefix, err := spec.Prefix()
	if err != nil {
		return "", err
	}
	return Prefix + "/" + strings.TrimSuffix(prefix, "/"), nil
}

// Stores is the store one database's entries live in.
//
// NOT A kvdb.Factory, whose New also takes a logger and a rebroadcast interval —
// a node's decisions about a store, which a database holds no opinion about —
// and whose result has to be registered with the closers the root keeps.
//
// TWO METHODS BECAUSE THERE ARE TWO REASONS TO REACH A STORE, and they differ in
// what happens to it afterwards. Serving a database keeps its store for the life
// of the node. Releasing a copy opens it for exactly as long as the release takes
// — NOT OPEN IS NOT ABSENT, a restarted node holds bytes it has never opened —
// and a store that is about to be destroyed is not one to register a closer for.
//
// BY NAME AND NOT BY SPEC, because the name is this package's: see [Name].
type Stores interface {
	Store(name string) (database.Store, error)
	Release(ctx context.Context, name string) error
}

// Disk is what this node holds, answered WITHOUT opening anything.
//
// THE WITHOUT IS THE POINT. Opening subscribes, so a node that answered "do you
// hold this?" by opening would become a holder because it was asked — and would
// then answer a digest that looks converged, authorising a drop by a node whose
// replacement it never was.
//
// TWO METHODS AND NOT A kvdb.Factory for Stores' reason, and it is OPTIONAL for
// one of its own: holding is a fact about a NODE's bytes, and the factory
// interface a composer satisfies is also satisfied by handles that keep none.
type Disk interface {
	Holding(ctx context.Context, path string) (bool, error)
	Paths(ctx context.Context, under string, depth int) ([]string, error)
}

// Ledgers is where a `consensus: raft` database's entries are ordered.
//
// ONE METHOD AND NOT node/ledger, which also holds the table of groups and
// closes them — a lifetime only the root can keep. What is asked here is the one
// question: which store keeps this database's entries. A node composed with no
// way to open a group answers it by refusing, which is the root's sentence to
// make because every consumer of consensus on a node shares it.
type Ledgers interface {
	Store(spec database.Spec) (database.Store, error)
}

// Holdings is the copies of a database this node keeps: where each one's entries
// go, what it has on disk, and letting go of one. Construct it with NewHoldings.
type Holdings struct {
	stores  Stores
	ledgers Ledgers
	disk    Disk

	mu   sync.Mutex
	open map[string]database.Store
}

var (
	_ databaseService.Holdings = (*Holdings)(nil)
	_ database.Stores          = (*Holdings)(nil).Entries
)

// NewHoldings wires the adapter.
//
// disk MAY BE NIL, and that is a composer's factory that cannot be asked what it
// has: it holds nothing, as far as anybody else is concerned. Answering "yes" on
// a guess would let a peer drop the last copy.
//
// stores and ledgers may not be nil. Without somewhere to put entries there is
// no database at all, and a nil ledgers would make `consensus: raft` a panic
// instead of the refusal by name it has to be.
func NewHoldings(stores Stores, ledgers Ledgers, disk Disk) (*Holdings, error) {
	if stores == nil {
		return nil, errors.New("database: nowhere to keep a database's entries")
	}
	if ledgers == nil {
		return nil, errors.New("database: no answer for a raft database, not even a refusal")
	}
	return &Holdings{stores: stores, ledgers: ledgers, disk: disk}, nil
}

// Entries is the provider every resident engine on this node is handed: the
// store ONE database's entries live in.
//
// The store is kept, so two opens of one database share it and a node carries as
// many as it HOLDS rather than as many as exist.
func (h *Holdings) Entries(_ context.Context, spec database.Spec) (database.Store, error) {
	// WHICH KIND OF STORE IS THE DATABASE'S OWN DECLARATION AND NOT THIS NODE'S
	// CHOICE. `consensus` is the axis the vocabulary declares beside the model,
	// and it reaches here on the spec rather than as a second engine, because
	// the schema declares ONE engine for tau's own.
	//
	// A LEDGER IS A STORE TOO, which is the whole of the difference between the
	// two resolutions: pkg/database's kv engine is generic over four methods and
	// does the per-database prefixing itself, so every other property of a kv
	// database is unchanged by construction rather than by a second
	// implementation agreeing with the first.
	if spec.Consensus == database.Raft {
		return h.ledgers.Store(spec)
	}

	name, err := Name(spec)
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if held, open := h.open[name]; open {
		return held, nil
	}
	store, err := h.stores.Store(name)
	if err != nil {
		return nil, err
	}
	if h.open == nil {
		h.open = map[string]database.Store{}
	}
	h.open[name] = store
	return store, nil
}

func (h *Holdings) Held(ctx context.Context, spec database.Spec) bool {
	name, err := Name(spec)
	if err != nil {
		return false
	}
	if h.disk == nil {
		return false
	}
	held, err := h.disk.Holding(ctx, name)
	return err == nil && held
}

// Destroy drops this node's copy, and drops it from the table first: see the
// package doc for what serving from a destroyed store did.
func (h *Holdings) Destroy(ctx context.Context, spec database.Spec) error {
	name, err := Name(spec)
	if err != nil {
		return err
	}

	h.mu.Lock()
	delete(h.open, name)
	h.mu.Unlock()

	return h.stores.Release(ctx, name)
}

func (h *Holdings) OnDisk(ctx context.Context) []database.Spec {
	if h.disk == nil {
		return nil
	}

	// FOUR SEGMENTS BELOW THE PREFIX, which is what Spec.Prefix writes: project,
	// environment, model, name. Everything under that is the CRDT's own
	// structure and is none of this function's business.
	paths, err := h.disk.Paths(ctx, Prefix, 4)
	if err != nil {
		return nil
	}

	out := make([]database.Spec, 0, len(paths))
	for _, path := range paths {
		parts := strings.Split(strings.TrimPrefix(path, Prefix+"/"), "/")
		if len(parts) != 4 {
			continue
		}
		out = append(out, database.Spec{
			Project: parts[0], Environment: parts[1],
			Type: database.Type(parts[2]), Name: parts[3],
		})
	}
	return out
}
