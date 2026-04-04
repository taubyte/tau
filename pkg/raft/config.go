package raft

import "time"

// TimeoutConfig allows fine-grained control over Raft timing
type TimeoutConfig struct {
	// HeartbeatTimeout specifies the time in follower state without
	// a leader before we attempt an election
	HeartbeatTimeout time.Duration

	// ElectionTimeout specifies the time in candidate state without
	// leader contact before we attempt another election
	ElectionTimeout time.Duration

	// CommitTimeout controls the time without an Apply operation
	// before heartbeat is sent to ensure timely commit
	CommitTimeout time.Duration

	// LeaderLeaseTimeout is used to control how long the "lease"
	// lasts for leader without being able to contact quorum
	LeaderLeaseTimeout time.Duration

	// SnapshotInterval controls how often we check if we should
	// perform a snapshot
	SnapshotInterval time.Duration

	// SnapshotThreshold controls how many outstanding logs there
	// must be before we perform a snapshot
	SnapshotThreshold uint64
}

// DefaultTimeoutConfig is the default timeout configuration, tuned for
// worldwide distributed clusters.
var DefaultTimeoutConfig = TimeoutConfig{
	HeartbeatTimeout:   15 * time.Second,
	ElectionTimeout:    30 * time.Second,
	CommitTimeout:      15 * time.Second,
	LeaderLeaseTimeout: 15 * time.Second,
	SnapshotInterval:   10 * time.Minute,
	SnapshotThreshold:  32768,
}
