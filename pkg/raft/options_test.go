package raft

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func newTestCluster() *cluster {
	return &cluster{
		namespace:          "/raft/test",
		timeoutConfig:      DefaultTimeoutConfig,
		forceBootstrap:     false,
		bootstrapTimeout:   10 * time.Second,
		bootstrapThreshold: 0.8,
	}
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
	assert.Assert(t, !c.forceBootstrap)
	assert.Equal(t, c.bootstrapTimeout, 10*time.Second)
	assert.Equal(t, c.timeoutConfig.HeartbeatTimeout, DefaultTimeoutConfig.HeartbeatTimeout)
	assert.Equal(t, c.timeoutConfig.ElectionTimeout, DefaultTimeoutConfig.ElectionTimeout)
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

	t.Run("falls_back_to_default", func(t *testing.T) {
		c := newTestCluster()
		c.timeoutConfig = TimeoutConfig{}

		result := c.getTimeoutConfig()
		assert.Equal(t, result.HeartbeatTimeout, 15*time.Second)
	})
}
