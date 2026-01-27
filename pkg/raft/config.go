package raft

import "time"

// TimeoutPreset defines timeout configurations for different deployment scenarios
type TimeoutPreset string

const (
	// PresetLocal for same-datacenter deployments (low latency)
	PresetLocal TimeoutPreset = "local"

	// PresetRegional for multi-region within same continent
	PresetRegional TimeoutPreset = "regional"

	// PresetGlobal for worldwide distributed clusters
	PresetGlobal TimeoutPreset = "global"
)

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

// presetConfigs maps presets to their timeout configurations
var presetConfigs = map[TimeoutPreset]TimeoutConfig{
	PresetLocal: {
		HeartbeatTimeout:   1 * time.Second,
		ElectionTimeout:    1 * time.Second,
		CommitTimeout:      500 * time.Millisecond,
		LeaderLeaseTimeout: 500 * time.Millisecond,
		SnapshotInterval:   2 * time.Minute,
		SnapshotThreshold:  8192,
	},
	PresetRegional: {
		HeartbeatTimeout:   5 * time.Second,
		ElectionTimeout:    10 * time.Second,
		CommitTimeout:      5 * time.Second,
		LeaderLeaseTimeout: 5 * time.Second,
		SnapshotInterval:   5 * time.Minute,
		SnapshotThreshold:  16384,
	},
	PresetGlobal: {
		HeartbeatTimeout:   15 * time.Second,
		ElectionTimeout:    30 * time.Second,
		CommitTimeout:      15 * time.Second,
		LeaderLeaseTimeout: 15 * time.Second,
		SnapshotInterval:   10 * time.Minute,
		SnapshotThreshold:  32768,
	},
}
