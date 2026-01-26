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

// DiscoveryConfig allows tuning discovery behavior
type DiscoveryConfig struct {
	// DiscoveryInterval is how often to search for new peers
	// Default: 30s
	DiscoveryInterval time.Duration
}

// config holds the internal configuration for a cluster
type config struct {
	namespace       string
	timeoutPreset   TimeoutPreset
	timeoutConfig   TimeoutConfig
	discoveryConfig DiscoveryConfig
	customFSM       FSM

	// Bootstrap behavior:
	// - forceBootstrap=true: bootstrap immediately as single-node cluster (skip discovery)
	// - forceBootstrap=false (default): discover peers first, auto-bootstrap only if none found
	forceBootstrap     bool
	bootstrapTimeout   time.Duration // Total discovery time
	bootstrapThreshold float64       // Fraction of timeout for founding members (0.0-1.0)
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

// defaultConfig returns the default configuration
func defaultConfig(namespace string) *config {
	return &config{
		namespace:     namespace,
		timeoutPreset: PresetRegional,
		timeoutConfig: presetConfigs[PresetRegional],
		discoveryConfig: DiscoveryConfig{
			DiscoveryInterval: 30 * time.Second,
		},
		forceBootstrap:     false,            // Default: discover first, auto-bootstrap if no peers
		bootstrapTimeout:   10 * time.Second, // Total discovery time
		bootstrapThreshold: 0.8,              // 80% = founding members, after = late joiners
	}
}

// getTimeoutConfig returns the effective timeout configuration
func (c *config) getTimeoutConfig() TimeoutConfig {
	if c.timeoutConfig.HeartbeatTimeout > 0 {
		return c.timeoutConfig
	}
	if cfg, ok := presetConfigs[c.timeoutPreset]; ok {
		return cfg
	}
	return presetConfigs[PresetRegional]
}
