package database

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/database"
)

// The copies of a database this node keeps, the half below the engines.

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

// Stores is the store one database's entries live in. Two methods because serving
// keeps a store for the node's life, while releasing opens one only to destroy it (a
// restarted node holds bytes it never opened), with no closer registered.
type Stores interface {
	Store(name string) (database.Store, error)
	Release(ctx context.Context, name string) error
}

// Disk is what this node holds, answered without opening anything: opening
// subscribes, so a node answering by opening would become a holder. Optional, since
// some factories keep no bytes.
type Disk interface {
	Holding(ctx context.Context, path string) (bool, error)
	Paths(ctx context.Context, under string, depth int) ([]string, error)
}

// Ledgers is where a `consensus: raft` database's entries are ordered; a node with no
// way to open a group refuses.
type Ledgers interface {
	Store(spec database.Spec) (database.Store, error)
}

// Copies is the copies of a database this node keeps: where each one's entries
// go, what it has on disk, and letting go of one. Construct it with NewCopies.
type Copies struct {
	stores  Stores
	ledgers Ledgers
	disk    Disk

	mu   sync.Mutex
	open map[string]database.Store
}

var (
	_ Holdings        = (*Copies)(nil)
	_ database.Stores = (*Copies)(nil).Entries
)

// NewCopies wires the adapter. disk may be nil (the factory cannot say what it
// holds): it then holds nothing as far as peers know. stores and ledgers may not be
// nil.
func NewCopies(stores Stores, ledgers Ledgers, disk Disk) (*Copies, error) {
	if stores == nil {
		return nil, errors.New("database: nowhere to keep a database's entries")
	}
	if ledgers == nil {
		return nil, errors.New("database: no answer for a raft database, not even a refusal")
	}
	return &Copies{stores: stores, ledgers: ledgers, disk: disk}, nil
}

// Entries is the provider every resident engine on this node is handed: the
// store ONE database's entries live in.
//
// The store is kept, so two opens of one database share it and a node carries as
// many as it HOLDS rather than as many as exist.
func (h *Copies) Entries(_ context.Context, spec database.Spec) (database.Store, error) {
	// Which store is the database's own `consensus` declaration. A ledger is a store too,
	// so pkg/database's kv engine serves both unchanged.
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

func (h *Copies) Held(ctx context.Context, spec database.Spec) bool {
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
func (h *Copies) Destroy(ctx context.Context, spec database.Spec) error {
	name, err := Name(spec)
	if err != nil {
		return err
	}

	h.mu.Lock()
	delete(h.open, name)
	h.mu.Unlock()

	return h.stores.Release(ctx, name)
}

func (h *Copies) OnDisk(ctx context.Context) []database.Spec {
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
