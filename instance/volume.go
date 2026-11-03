package instance

import (
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/invoke/container"
	"github.com/taubyte/tau/services/worker/volume"
)

// The directories are services/worker/volume's; the root reaches the root directory, the asset
// store and its databases, names the database, and registers it to close.

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
func (n *Instance) disksOrNil() (container.Volumes, error) {
	if n.cfg.Root == "" {
		return nil, nil
	}
	if n.disks == nil {
		disks, err := volume.New(n.cfg.Root, volume.Checkpoints(n.store), volumeStores{node: n}, logger)
		if err != nil {
			return nil, err
		}
		n.disks = disks
		n.closers = append(n.closers, disks)
	}
	return n.disks, nil
}

// volumeStores opens that database for services/worker/volume, on the first checkpoint that
// needs it.
type volumeStores struct{ node *Instance }

var _ volume.Stores = volumeStores{}

func (s volumeStores) Store() (volume.Rows, error) {
	db, err := s.node.kvdb(volumeDatabase)
	if err != nil {
		return nil, fmt.Errorf("node: opening the volumes database: %w", err)
	}
	// THROUGH addCloser AND NOT AN APPEND, because this runs on the request
	// path: a first mount and another adapter's first request would otherwise
	// append to one slice at once.
	s.node.addCloser(closerFunc(db.Close))
	return cacheStore{db}, nil
}
