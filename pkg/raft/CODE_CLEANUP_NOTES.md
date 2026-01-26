# Raft package cleanup notes

This file lists potential leftovers, unused pieces, or over-engineered areas
observed in `pkg/raft`. It is not a correctness review.

## Likely leftovers / unused

- Unused discovery helpers and settings:
  - `discoverExistingPeers` is defined but never called.
  - `WithMinPeers` / `WithDiscoveryTimeout` populate `config.minPeers` and
    `discoveryConfig.*`, but nothing reads those values at runtime.
  - `DiscoveryConfig.MinPeers` / `DiscoveryConfig.DiscoveryTimeout` are never
    referenced outside config/tests/docs.
  - `config.minPeers` duplicates `discoveryConfig.MinPeers` and is unused.

```1:120:pkg/raft/options.go
// WithMinPeers waits for N peers before starting consensus
// Default: 0 (start immediately)
func WithMinPeers(n int) Option {
	return func(c *config) {
		c.minPeers = n
		c.discoveryConfig.MinPeers = n
	}
}

// WithDiscoveryTimeout sets max time to wait for MinPeers
func WithDiscoveryTimeout(d time.Duration) Option {
	return func(c *config) {
		c.discoveryConfig.DiscoveryTimeout = d
	}
}
```

```61:126:pkg/raft/config.go
type config struct {
	namespace       string
	timeoutPreset   TimeoutPreset
	timeoutConfig   TimeoutConfig
	discoveryConfig DiscoveryConfig
	minPeers        int
	customFSM       FSM
	// ...
}
```

```229:261:pkg/raft/cluster.go
// discoverExistingPeers searches for existing cluster members (used by discoverPeers goroutine)
func (c *cluster) discoverExistingPeers() ([]peer.ID, error) {
	// ... existing code ...
}
```

- Unused error values:
  - `ErrTimeout` and `ErrKeyNotFound` are defined but not referenced by the code
    in this package (only tests/docs mention them).

```1:30:pkg/raft/errors.go
var (
	// ErrTimeout is returned when an operation times out
	ErrTimeout = errors.New("operation timeout")
	// ErrKeyNotFound is returned when a key doesn't exist
	ErrKeyNotFound = errors.New("key not found")
)
```

- Unused struct fields:
  - `Client.namespace` is stored but never read.
  - `namespaceAddrProvider.h` is stored but never read.

```13:32:pkg/raft/client.go
type Client struct {
	*streamClient.Client
	namespace string
}
```

```187:195:pkg/raft/transport.go
type namespaceAddrProvider struct {
	h host.Host
}
```

- Test artifacts in the package directory:
  - `coverage.out` and `coverage.html` look like generated coverage outputs.
    If these are not meant to be versioned, they can be removed or moved.

## Potential over-engineering / questionable complexity

- Custom snapshot store vs. built-in options:
  - `fileSnapshotStore` re-implements a filesystem snapshot store. Hashicorp
    Raft already ships a `raft.FileSnapshotStore`. If the custom store is not
    needed for CBOR metadata or the specific retention behavior here, this might
    be extra maintenance surface.

```231:448:pkg/raft/storage.go
// fileSnapshotStore implements raft.SnapshotStore using the filesystem
type fileSnapshotStore struct {
	dir    string
	retain int
	mu     sync.Mutex
}
// ... custom Create/List/Open and sink implementation ...
```

- Bootstrap peer exchange complexity:
  - `peerTracker` + exchange protocol adds a time-based “founding members”
    concept (`bootstrapThreshold`) on top of discovery. If this logic is not
    crucial in practice, it is a large amount of coordination surface that could
    be simplified to “discover peers, then bootstrap if none found”.
  - Note that `bootstrapThreshold` is not configurable via options, which makes
    the field look more like a constant than a true configuration knob.

```149:199:pkg/raft/cluster.go
// handleBootstrap implements autonomous bootstrap with time-based threshold:
// 1. If forceBootstrap is set, bootstrap immediately as single-node
// 2. Otherwise, discover peers and exchange lists for bootstrapTimeout
// 3. Peers discovered before threshold (80%) = founding members → bootstrap together
// 4. Peers discovered after threshold = late joiners → wait for leader to add them
func (c *cluster) handleBootstrap(raftConfig *raft.Config, transport raft.Transport) error {
	// ... existing code ...
}
```

```12:139:pkg/raft/discovery.go
// peerTracker tracks discovered peers and their discovery times
type peerTracker struct {
	// ... existing code ...
}
```
