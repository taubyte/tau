package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"

	"github.com/taubyte/tau/pkg/kvdb"
)

// config.go declares what this service is built from, and it declares the
// three things service-conventions.md separates rather than one bag: CONFIG is
// what an operator wrote down; THE NODE is passed in explicitly; RESOURCES are
// narrow interfaces injected by whatever composes the node.
//
// EVERY INTERFACE HERE IS DECLARED BY THE CONSUMER AND IS AS NARROW AS THE USE.
// In particular there is no peer.Node anywhere in this package. peer.Node
// bundles a host, a DHT, a datastore, a DAG and pubsub, and a test that wanted
// any one of them got a DHT bootstrap and an on-disk datastore with it. What
// this half of the service actually needs is a key-value store with an ordered
// query, a way to ask whether a block is here, and a catalog of which buckets
// exist. Those are three interfaces, and every one of them has an in-memory
// implementation in this package's tests.

// Config is what an operator declared. It is a plain immutable value: no
// setters, no live objects, nothing that had to be constructed, and it
// survives a round trip through yaml.
//
// REPLICATION POLICY IS THE PLATFORM'S, NOT A BUCKET'S, and the vocabulary
// says so: the cache kind carries "NO MECHANISM AND NO REPLICA COUNT. How
// copies are kept is the platform's to change." So Replicas and Barrier are
// operator config and there is no per-bucket override anywhere in this
// package.
type Config struct {
	// Replicas is R: how many nodes the repair loop drives a bucket's index
	// and an object's bytes towards. Default 3.
	Replicas int

	// Barrier is K: how many acknowledgements a write waits for before it
	// returns. Default 2.
	//
	// K IS NEVER INFERRED. A single-node cloud writes Barrier: 1 in operator
	// config; the service cannot tell "the fleet is one node" from "I am
	// partitioned from the other two", so it refuses to guess. That is owner
	// decision O-5's default, and this is where it bites: a K that adapted to
	// the membership view would ack a write during a partition that no second
	// node ever saw, which is exactly v1's shipped lie.
	Barrier int

	// MaxSkew is how far ahead of local time a remote stamp may be and still
	// advance this node's clock. Default 5m. See clock.go.
	MaxSkew time.Duration

	// GraceGC is how long a tombstone must have been OBSERVED HERE before the
	// blocks it releases may be swept. Default 24h. It is timed from local
	// observation, never from the stamp — see gc.go.
	GraceGC time.Duration

	// Liveness and Stabilize are the two membership timescales
	// (pkg/v2/membership). Defaults 5s and 60s.
	Liveness  time.Duration
	Stabilize time.Duration
}

// withDefaults fills the zero fields and refuses a combination that cannot be
// correct. It is called once, by whatever constructs the service, and every
// consumer in this package reads the result rather than re-deriving a default.
func (c Config) withDefaults() (Config, error) {
	if c.Replicas == 0 {
		c.Replicas = 3
	}
	if c.Barrier == 0 {
		c.Barrier = 2
	}
	if c.MaxSkew == 0 {
		c.MaxSkew = DefaultMaxSkew
	}
	if c.GraceGC == 0 {
		c.GraceGC = 24 * time.Hour
	}
	if c.Liveness == 0 {
		c.Liveness = 5 * time.Second
	}
	if c.Stabilize == 0 {
		c.Stabilize = 60 * time.Second
	}

	if c.Replicas < 1 {
		return Config{}, fmt.Errorf("storage: replicas %d: a target below one places nothing", c.Replicas)
	}
	if c.Barrier < 1 {
		return Config{}, fmt.Errorf("storage: barrier %d: a write that waits for no acknowledgement is v1's shipped lie", c.Barrier)
	}
	if c.Barrier > c.Replicas {
		return Config{}, fmt.Errorf("storage: barrier %d exceeds replicas %d: every write would fail", c.Barrier, c.Replicas)
	}
	if c.GraceGC <= 0 || c.MaxSkew <= 0 {
		return Config{}, errors.New("storage: grace and skew budgets must be positive")
	}
	if c.Stabilize < c.Liveness {
		return Config{}, fmt.Errorf("storage: stabilize %s is shorter than liveness %s: placement would move faster than acknowledgement", c.Stabilize, c.Liveness)
	}
	return c, nil
}

// R is the replica target.
func (c Config) R() int { return c.Replicas }

// K is the acknowledgement barrier.
func (c Config) K() int { return c.Barrier }

// Datastore is the slice of go-datastore this service's index needs, declared
// here so nothing in this package can reach sideways out of the shard it was
// handed.
//
// THERE IS NO Close. A shard's lifetime belongs to shard.go, which opened it;
// a door that can close a store two other readers are using is a door this
// package does not need. THERE IS NO AllKeysChan either, and that absence is
// gc.go's: the node's blockstore is shared with every other consumer, and a
// sweep that enumerated it would be enumerating kvdb's own Merkle-DAG blocks.
//
// *kvdb.Datastore satisfies it, which is compile-verified below rather than
// asserted. That matters because THIS SERVICE OWNS ITS OWN STORE CONSTRUCTION
// deliberately: Compact, ReclaimCompacted, Repair and InternalStats are
// methods on *kvdb.Datastore and are NOT on core/kvdb.KVDB, so pushing this
// service back behind that interface in a later refactor silently stops
// compaction — which is not a performance regression but a growth in a new
// replica's catch-up cost with the cloud's LIFETIME rather than with its state.
type Datastore interface {
	Get(ctx context.Context, key ds.Key) ([]byte, error)
	Has(ctx context.Context, key ds.Key) (bool, error)
	Put(ctx context.Context, key ds.Key, value []byte) error
	Delete(ctx context.Context, key ds.Key) error
	Query(ctx context.Context, q query.Query) (query.Results, error)
	Sync(ctx context.Context, prefix ds.Key) error
}

var _ Datastore = (*kvdb.Datastore)(nil)

// Catalog is which buckets this cloud declares. It is the compiled config,
// reached through an interface because THE CONFIG SERVICE DOES NOT EXIST:
// services/build/backend/doc.go names the same gap for itself — "no tns schema
// to publish a compiled config to". Declaring the dependency makes it explicit
// rather than blocking, and it is what shard.go asks "does this shard hold
// anything" so a three-node dev cloud with five buckets opens five topics and
// not two hundred and fifty-six.
type Catalog interface {
	// Buckets lists every bucket instance the cloud declares. A storage node
	// reads it to decide which shards exist; it is never a per-request call.
	Buckets(ctx context.Context) ([]Address, error)
}

// Blocks is the byte plane as the index half of this service sees it: enough
// to ask whether bytes are here and to release them, and nothing that could
// fetch from the network. A DAG walk that could leave the node is a sweep that
// blocks on a peer, and a sweep that blocks on a peer is a sweep that holds a
// deletion decision open across a partition.
//
// The byte plane's own file (blocks.go, a later round) implements this over
// *peer.DAGService. It is an interface here because gc.go is written against
// it and gc.go must be testable with no libp2p host, no bitswap and no DHT.
type Blocks interface {
	// Has reports whether a block is in this node's blockstore.
	Has(ctx context.Context, c cid.Cid) (bool, error)

	// Links returns a block's direct links WITHOUT leaving the node. A cid
	// this node does not hold is an error, never a network fetch.
	Links(ctx context.Context, c cid.Cid) ([]cid.Cid, error)

	// Delete removes one block. The sweep calls it once per unmarked block in
	// this service's OWN block index — never over the whole blockstore.
	Delete(ctx context.Context, c cid.Cid) error
}
