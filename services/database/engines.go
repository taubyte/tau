package database

import (
	"context"
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/database"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	"github.com/taubyte/tau/pkg/raft"
)

// engines.go builds tau's own kv, once per consensus.
//
// BOTH ARE THE SAME ENGINE OVER A DIFFERENT STORE. pkg/database's kv engine is
// generic over a four-method store and does the per-database prefixing itself,
// so "raft instead of crdt" is which store it is handed — and every other
// property of a kv database is unchanged by construction rather than by two
// implementations that have to agree.
//
// THEY ARE TWO REGISTRY ENTRIES because the registry is keyed by (type, engine)
// and one node serves both at once: a project's sessions resolve by merge while
// its ledger does not. declarations.go is where an author's `consensus` becomes
// the engine name, and this is the only other file that knows those names.

// applyTimeout bounds one raft round trip. It is the cost the vocabulary names
// when it says a raft write "costs a round trip", and it is here rather than a
// caller's because a caller that could set it could set it to zero.
const applyTimeout = 10 * time.Second

// crdtEngine is tau's own kv over the replicated CRDT store: any node writes,
// concurrent writes merge.
type crdtEngine struct{ *kvengine.Engine }

func (crdtEngine) Name() string { return servedConsensus["crdt"] }

// raftEngine is tau's own kv over a raft group: the leader writes, and nothing
// resolves by merge because nothing is allowed to conflict.
type raftEngine struct{ *kvengine.Engine }

func (raftEngine) Name() string { return servedConsensus["raft"] }

// Engines builds the registry a database node serves from.
//
// A NODE CAN SERVE ONE, THE OTHER OR BOTH, and which it serves is which stores
// the composer has: a node with no raft cluster cannot keep a raft database,
// and says so by not registering the engine rather than by accepting one and
// merging its writes.
func Engines(ctx context.Context, crdt kvengine.Store, cluster func() (raft.Cluster, error)) (*database.Registry, error) {
	registry := database.NewRegistry()

	if crdt != nil {
		engine, err := kvengine.New(crdt)
		if err != nil {
			return nil, err
		}
		if err := registry.Register(ctx, crdtEngine{engine}); err != nil {
			return nil, fmt.Errorf("database: registering the crdt kv: %w", err)
		}
	}

	if cluster != nil {
		engine, err := kvengine.New(&raftStore{open: cluster, timeout: applyTimeout})
		if err != nil {
			return nil, err
		}
		if err := registry.Register(ctx, raftEngine{engine}); err != nil {
			return nil, fmt.Errorf("database: registering the raft kv: %w", err)
		}
	}

	return registry, nil
}
