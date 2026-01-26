package raft

import "time"

// Option configures optional cluster behavior
type Option func(*config)

// WithTimeoutPreset sets a predefined timeout configuration
// Default: PresetRegional
func WithTimeoutPreset(preset TimeoutPreset) Option {
	return func(c *config) {
		c.timeoutPreset = preset
		if cfg, ok := presetConfigs[preset]; ok {
			c.timeoutConfig = cfg
		}
	}
}

// WithTimeouts sets custom timeout configuration
func WithTimeouts(cfg TimeoutConfig) Option {
	return func(c *config) {
		c.timeoutConfig = cfg
	}
}

// WithMinPeers waits for N peers before starting consensus
// Default: 0 (start immediately)
func WithMinPeers(n int) Option {
	return func(c *config) {
		c.minPeers = n
		c.discoveryConfig.MinPeers = n
	}
}

// WithDiscoveryInterval sets how often to search for new peers
func WithDiscoveryInterval(d time.Duration) Option {
	return func(c *config) {
		c.discoveryConfig.DiscoveryInterval = d
	}
}

// WithDiscoveryTimeout sets max time to wait for MinPeers
func WithDiscoveryTimeout(d time.Duration) Option {
	return func(c *config) {
		c.discoveryConfig.DiscoveryTimeout = d
	}
}

// WithFSM provides a custom FSM implementation (advanced)
// Default: built-in key-value FSM
func WithFSM(fsm FSM) Option {
	return func(c *config) {
		c.customFSM = fsm
	}
}

// WithForceBootstrap forces immediate bootstrap as a single-node cluster,
// skipping peer discovery entirely.
// Default: false (discover peers first, auto-bootstrap only if none found)
// Use this only when you KNOW this should be the first node in a new cluster.
func WithForceBootstrap() Option {
	return func(c *config) {
		c.forceBootstrap = true
	}
}

// WithBootstrapTimeout sets how long to wait for peers before auto-bootstrapping.
// If no peers are discovered within this timeout, the node will bootstrap itself.
// Default: 10s
func WithBootstrapTimeout(d time.Duration) Option {
	return func(c *config) {
		c.bootstrapTimeout = d
	}
}
