package raft

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func newTestCluster() *cluster {
	return &cluster{
		namespace:          "/raft/test",
		timeoutPreset:      PresetRegional,
		timeoutConfig:      presetConfigs[PresetRegional],
		forceBootstrap:     false,
		bootstrapTimeout:   10 * time.Second,
		bootstrapThreshold: 0.8,
	}
}

func TestWithTimeoutPreset(t *testing.T) {
	t.Run("local_preset", func(t *testing.T) {
		c := newTestCluster()
		WithTimeoutPreset(PresetLocal)(c)

		assert.Equal(t, c.timeoutPreset, PresetLocal)
		assert.Equal(t, c.timeoutConfig.HeartbeatTimeout, 1*time.Second)
	})

	t.Run("regional_preset", func(t *testing.T) {
		c := newTestCluster()
		WithTimeoutPreset(PresetRegional)(c)

		assert.Equal(t, c.timeoutPreset, PresetRegional)
		assert.Equal(t, c.timeoutConfig.HeartbeatTimeout, 5*time.Second)
	})

	t.Run("global_preset", func(t *testing.T) {
		c := newTestCluster()
		WithTimeoutPreset(PresetGlobal)(c)

		assert.Equal(t, c.timeoutPreset, PresetGlobal)
		assert.Equal(t, c.timeoutConfig.HeartbeatTimeout, 15*time.Second)
	})

	t.Run("invalid_preset", func(t *testing.T) {
		c := newTestCluster()
		originalTimeout := c.timeoutConfig.HeartbeatTimeout
		WithTimeoutPreset("invalid")(c)

		// Should keep original config
		assert.Equal(t, c.timeoutConfig.HeartbeatTimeout, originalTimeout)
	})
}

func TestWithTimeouts(t *testing.T) {
	c := newTestCluster()
	customConfig := TimeoutConfig{
		HeartbeatTimeout:   100 * time.Millisecond,
		ElectionTimeout:    200 * time.Millisecond,
		CommitTimeout:      50 * time.Millisecond,
		LeaderLeaseTimeout: 75 * time.Millisecond,
		SnapshotInterval:   1 * time.Minute,
		SnapshotThreshold:  100,
	}

	WithTimeouts(customConfig)(c)

	assert.Equal(t, c.timeoutConfig.HeartbeatTimeout, 100*time.Millisecond)
	assert.Equal(t, c.timeoutConfig.ElectionTimeout, 200*time.Millisecond)
	assert.Equal(t, c.timeoutConfig.CommitTimeout, 50*time.Millisecond)
	assert.Equal(t, c.timeoutConfig.LeaderLeaseTimeout, 75*time.Millisecond)
	assert.Equal(t, c.timeoutConfig.SnapshotInterval, 1*time.Minute)
	assert.Equal(t, c.timeoutConfig.SnapshotThreshold, uint64(100))
}

func TestWithForceBootstrap(t *testing.T) {
	c := newTestCluster()
	assert.Assert(t, !c.forceBootstrap)

	WithForceBootstrap()(c)

	assert.Assert(t, c.forceBootstrap)
}

func TestWithBootstrapTimeout(t *testing.T) {
	c := newTestCluster()
	WithBootstrapTimeout(30 * time.Second)(c)

	assert.Equal(t, c.bootstrapTimeout, 30*time.Second)
}

func TestDefaultConfig_Values(t *testing.T) {
	c := newTestCluster()

	assert.Equal(t, c.namespace, "/raft/test")
	assert.Equal(t, c.timeoutPreset, PresetRegional)
	assert.Assert(t, !c.forceBootstrap)
	assert.Equal(t, c.bootstrapTimeout, 10*time.Second)
}

func TestGetTimeoutConfig(t *testing.T) {
	t.Run("uses_explicit_config", func(t *testing.T) {
		c := newTestCluster()
		c.timeoutConfig = TimeoutConfig{
			HeartbeatTimeout: 123 * time.Millisecond,
		}

		result := c.getTimeoutConfig()
		assert.Equal(t, result.HeartbeatTimeout, 123*time.Millisecond)
	})

	t.Run("falls_back_to_preset", func(t *testing.T) {
		c := newTestCluster()
		c.timeoutConfig = TimeoutConfig{} // Zero value
		c.timeoutPreset = PresetGlobal

		result := c.getTimeoutConfig()
		assert.Equal(t, result.HeartbeatTimeout, 15*time.Second) // Global preset
	})

	t.Run("falls_back_to_regional", func(t *testing.T) {
		c := newTestCluster()
		c.timeoutConfig = TimeoutConfig{} // Zero value
		c.timeoutPreset = "unknown"       // Invalid preset

		result := c.getTimeoutConfig()
		assert.Equal(t, result.HeartbeatTimeout, 5*time.Second) // Regional fallback
	})
}
