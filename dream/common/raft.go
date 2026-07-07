package common

import (
	"time"

	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/raft"
)

// DreamRaftTimeoutConfig is the timeout config used for raft clusters in dream
// (short timeouts so single-node bootstrap and leader election complete quickly).
var DreamRaftTimeoutConfig = raft.TimeoutConfig{
	HeartbeatTimeout:   50 * time.Millisecond,
	ElectionTimeout:    50 * time.Millisecond,
	CommitTimeout:      25 * time.Millisecond,
	LeaderLeaseTimeout: 25 * time.Millisecond,
	SnapshotInterval:   time.Minute,
	SnapshotThreshold:  1000,
}

// NewRaftCluster creates a raft cluster with options suitable for dream:
// bootstrap timeout so a single node auto-bootstraps when no peers are found,
// and short timeouts for fast leader election.
//
// root/shape scope the snapshot directory under the universe's root
// (raft.SnapshotDir). Without this the snapshot store defaults to a shared
// global /tmp path that survives universe teardown, so a later universe (fresh
// deterministic identity, different root) would restore a stale snapshot whose
// configuration doesn't contain it — never elect a leader — and wedge the
// service (issue #431).
func NewRaftCluster(node peer.Node, clusterName, root, shape string) (raft.Cluster, error) {
	if clusterName == "" {
		clusterName = "main"
	}
	return raft.New(node, clusterName,
		raft.WithBootstrapTimeout(1*time.Second),
		raft.WithTimeouts(DreamRaftTimeoutConfig),
		raft.WithSnapshotDir(raft.SnapshotDir(root, shape, clusterName)),
	)
}
