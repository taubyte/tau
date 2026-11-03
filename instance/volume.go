package instance

import (
	"fmt"
	"time"

	"github.com/taubyte/tau/instance/volume"
	"github.com/taubyte/tau/pkg/invoke/container"
)

// The directories are instance/volume's; the root reaches the root directory, the asset
// store and the factory, names the database, and registers it to close.

// volumeDatabase is the one database's name, for cacheDatabase's reason.
const volumeDatabase = "volumes"

// checkpointTick is how often this node asks which volumes are due.
//
// IT IS FINER THAN ANY INTERVAL AN AUTHOR CAN DECLARE, because each volume has
// its own and what a tick decides is which of them have come round. How fine is
// this node's decision rather than the adapter's, like the name of the database
// above it.
const checkpointTick = 5 * time.Second

// disksOrNil is this node's volume backing, or nil when it has no root to put
// one in. CONSTRUCTION ONLY, like the other backings.
//
// A NIL RETURN IS THE STATEMENT. A tier handed nothing refuses a workload that
// declares a mount, which is the only unserved capability that would lose data
// rather than fail.
//
// THE ASSET STORE MAY BE ABSENT and the adapter is built anyway: a node with a
// disk and no DAG mounts what it holds and says so on every checkpoint, which is
// the adapter's own sentence to make and not this one's.
func (n *Instance) disksOrNil() (container.Volumes, error) {
	if n.opts.root == "" {
		return nil, nil
	}
	if n.disks == nil {
		disks, err := volume.New(n.opts.root, n.opts.assets, volumeStores{node: n}, logger)
		if err != nil {
			return nil, err
		}
		n.disks = disks
		n.closers = append(n.closers, disks)
	}
	return n.disks, nil
}

// volumeStores opens that database for instance/volume, on the first checkpoint that
// needs it.
type volumeStores struct{ node *Instance }

var _ volume.Stores = volumeStores{}

func (s volumeStores) Store() (volume.Rows, error) {
	if s.node.opts.databases == nil {
		return nil, ErrNoCacheStore
	}
	db, err := s.node.opts.databases.New(logger, volumeDatabase, rebroadcastSeconds)
	if err != nil {
		return nil, fmt.Errorf("node: opening the volumes database: %w", err)
	}
	// THROUGH addCloser AND NOT AN APPEND, because this runs on the request
	// path: a first mount and another adapter's first request would otherwise
	// append to one slice at once.
	s.node.addCloser(closerFunc(db.Close))
	return cacheStore{db}, nil
}
