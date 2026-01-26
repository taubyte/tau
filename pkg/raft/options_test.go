package raft

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestWithTimeoutPreset(t *testing.T) {
	t.Run("local_preset", func(t *testing.T) {
		cfg := defaultConfig("/raft/test")
		WithTimeoutPreset(PresetLocal)(cfg)

		assert.Equal(t, cfg.timeoutPreset, PresetLocal)
		assert.Equal(t, cfg.timeoutConfig.HeartbeatTimeout, 1*time.Second)
	})

	t.Run("regional_preset", func(t *testing.T) {
		cfg := defaultConfig("/raft/test")
		WithTimeoutPreset(PresetRegional)(cfg)

		assert.Equal(t, cfg.timeoutPreset, PresetRegional)
		assert.Equal(t, cfg.timeoutConfig.HeartbeatTimeout, 5*time.Second)
	})

	t.Run("global_preset", func(t *testing.T) {
		cfg := defaultConfig("/raft/test")
		WithTimeoutPreset(PresetGlobal)(cfg)

		assert.Equal(t, cfg.timeoutPreset, PresetGlobal)
		assert.Equal(t, cfg.timeoutConfig.HeartbeatTimeout, 15*time.Second)
	})

	t.Run("invalid_preset", func(t *testing.T) {
		cfg := defaultConfig("/raft/test")
		originalTimeout := cfg.timeoutConfig.HeartbeatTimeout
		WithTimeoutPreset("invalid")(cfg)

		// Should keep original config
		assert.Equal(t, cfg.timeoutConfig.HeartbeatTimeout, originalTimeout)
	})
}

func TestWithTimeouts(t *testing.T) {
	cfg := defaultConfig("/raft/test")
	customConfig := TimeoutConfig{
		HeartbeatTimeout:   100 * time.Millisecond,
		ElectionTimeout:    200 * time.Millisecond,
		CommitTimeout:      50 * time.Millisecond,
		LeaderLeaseTimeout: 75 * time.Millisecond,
		SnapshotInterval:   1 * time.Minute,
		SnapshotThreshold:  100,
	}

	WithTimeouts(customConfig)(cfg)

	assert.Equal(t, cfg.timeoutConfig.HeartbeatTimeout, 100*time.Millisecond)
	assert.Equal(t, cfg.timeoutConfig.ElectionTimeout, 200*time.Millisecond)
	assert.Equal(t, cfg.timeoutConfig.CommitTimeout, 50*time.Millisecond)
	assert.Equal(t, cfg.timeoutConfig.LeaderLeaseTimeout, 75*time.Millisecond)
	assert.Equal(t, cfg.timeoutConfig.SnapshotInterval, 1*time.Minute)
	assert.Equal(t, cfg.timeoutConfig.SnapshotThreshold, uint64(100))
}

func TestWithDiscoveryInterval(t *testing.T) {
	cfg := defaultConfig("/raft/test")
	WithDiscoveryInterval(5 * time.Second)(cfg)

	assert.Equal(t, cfg.discoveryConfig.DiscoveryInterval, 5*time.Second)
}

func TestWithFSM(t *testing.T) {
	cfg := defaultConfig("/raft/test")

	// Create a mock FSM
	mockFSM := &kvFSM{}
	WithFSM(mockFSM)(cfg)

	assert.Assert(t, cfg.customFSM != nil)
}

func TestWithForceBootstrap(t *testing.T) {
	cfg := defaultConfig("/raft/test")
	assert.Assert(t, !cfg.forceBootstrap)

	WithForceBootstrap()(cfg)

	assert.Assert(t, cfg.forceBootstrap)
}

func TestWithBootstrapTimeout(t *testing.T) {
	cfg := defaultConfig("/raft/test")
	WithBootstrapTimeout(30 * time.Second)(cfg)

	assert.Equal(t, cfg.bootstrapTimeout, 30*time.Second)
}

func TestDefaultConfig_Values(t *testing.T) {
	cfg := defaultConfig("/raft/test")

	assert.Equal(t, cfg.namespace, "/raft/test")
	assert.Equal(t, cfg.timeoutPreset, PresetRegional)
	assert.Assert(t, !cfg.forceBootstrap)
	assert.Equal(t, cfg.bootstrapTimeout, 10*time.Second)
}

func TestGetTimeoutConfig(t *testing.T) {
	t.Run("uses_explicit_config", func(t *testing.T) {
		cfg := defaultConfig("/raft/test")
		cfg.timeoutConfig = TimeoutConfig{
			HeartbeatTimeout: 123 * time.Millisecond,
		}

		result := cfg.getTimeoutConfig()
		assert.Equal(t, result.HeartbeatTimeout, 123*time.Millisecond)
	})

	t.Run("falls_back_to_preset", func(t *testing.T) {
		cfg := defaultConfig("/raft/test")
		cfg.timeoutConfig = TimeoutConfig{} // Zero value
		cfg.timeoutPreset = PresetGlobal

		result := cfg.getTimeoutConfig()
		assert.Equal(t, result.HeartbeatTimeout, 15*time.Second) // Global preset
	})

	t.Run("falls_back_to_regional", func(t *testing.T) {
		cfg := defaultConfig("/raft/test")
		cfg.timeoutConfig = TimeoutConfig{} // Zero value
		cfg.timeoutPreset = "unknown"       // Invalid preset

		result := cfg.getTimeoutConfig()
		assert.Equal(t, result.HeartbeatTimeout, 5*time.Second) // Regional fallback
	})
}
