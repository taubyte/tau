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

// An operator configures four per-node durations. R and K are constants every node
// reaches identically, each capped by the storage nodes discovery finds. Every interface here is declared by the consumer and narrow,
// and each has an in-memory test implementation; only host.go and bytes.go see the
// instance's peer, to build them from it.

// Config is what an operator declared: a plain value that round-trips through yaml.
// Replication policy is the platform's, not a bucket's or a node's.
type Config struct {
	// MaxSkew is how far ahead of local time a remote stamp may be and still
	// advance this node's clock. Default 5m. See clock.go.
	MaxSkew time.Duration `yaml:"max_skew"`

	// GraceGC is how long a tombstone must have been OBSERVED HERE before the
	// blocks it releases may be swept. Default 24h. It is timed from local
	// observation, never from the stamp — see gc.go.
	GraceGC time.Duration `yaml:"grace_gc"`

	// Liveness and Stabilize are the two membership timescales
	// (pkg/membership). Defaults 5s and 60s.
	Liveness  time.Duration `yaml:"liveness"`
	Stabilize time.Duration `yaml:"stabilize"`
}

// withDefaults fills the zero fields and refuses a combination that cannot be
// correct. Called once by the constructor.
func (c Config) withDefaults() (Config, error) {
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

	if c.GraceGC <= 0 || c.MaxSkew <= 0 {
		return Config{}, errors.New("storage: grace and skew budgets must be positive")
	}
	if c.Stabilize < c.Liveness {
		return Config{}, fmt.Errorf("storage: stabilize %s is shorter than liveness %s: placement would move faster than acknowledgement", c.Stabilize, c.Liveness)
	}
	return c, nil
}

// R and K are the region's, read from cm (cm/settings), each capped by the holders
// placement names over the storage nodes discovery finds.

// Datastore is the slice of go-datastore this service's index needs. No Close (a
// shard's lifetime is shard.go's) and no AllKeysChan (the blockstore is shared with
// kvdb's own DAG blocks). *kvdb.Datastore satisfies it; this service builds its own
// store because it needs prefix queries, which core/kvdb.Instance lacks.
type Datastore interface {
	Get(ctx context.Context, key ds.Key) ([]byte, error)
	Has(ctx context.Context, key ds.Key) (bool, error)
	Put(ctx context.Context, key ds.Key, value []byte) error
	Delete(ctx context.Context, key ds.Key) error
	Query(ctx context.Context, q query.Query) (query.Results, error)
	Sync(ctx context.Context, prefix ds.Key) error
}

var _ Datastore = (*kvdb.Datastore)(nil)

// Catalog is which buckets this cloud declares, from the compiled config. An
// interface so a node supplying none still runs; nothing supplies one yet. shard.go
// asks it whether a shard holds anything, so a small cloud opens few topics.
type Catalog interface {
	// Buckets lists every bucket instance the cloud declares. A storage node
	// reads it to decide which shards exist; it is never a per-request call.
	Buckets(ctx context.Context) ([]Address, error)
}

// catalogRecorder is a Catalog that learns of a bucket instance from its first write.
type catalogRecorder interface {
	Record(ctx context.Context, a Address) error
}

// Blocks is the byte plane as the index half sees it: has, local links, delete, and
// nothing that fetches from the network, so a sweep never holds a deletion decision
// open across a partition. To be implemented over *peer.DAGService.
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
