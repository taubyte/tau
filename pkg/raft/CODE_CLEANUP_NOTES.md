# Raft package cleanup notes

This file lists potential over-engineered areas observed in `pkg/raft`. It is
not a correctness review.

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
