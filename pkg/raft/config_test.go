package raft

import (
	"testing"
	"time"
)

func TestDefaultTimeoutConfig(t *testing.T) {
	if DefaultTimeoutConfig.HeartbeatTimeout != 15*time.Second {
		t.Errorf("expected heartbeat 15s, got %v", DefaultTimeoutConfig.HeartbeatTimeout)
	}
	if DefaultTimeoutConfig.ElectionTimeout != 30*time.Second {
		t.Errorf("expected election 30s, got %v", DefaultTimeoutConfig.ElectionTimeout)
	}
	if DefaultTimeoutConfig.CommitTimeout != 15*time.Second {
		t.Errorf("expected commit 15s, got %v", DefaultTimeoutConfig.CommitTimeout)
	}
	if DefaultTimeoutConfig.LeaderLeaseTimeout != 15*time.Second {
		t.Errorf("expected leader lease 15s, got %v", DefaultTimeoutConfig.LeaderLeaseTimeout)
	}
	if DefaultTimeoutConfig.SnapshotInterval != 10*time.Minute {
		t.Errorf("expected snapshot interval 10m, got %v", DefaultTimeoutConfig.SnapshotInterval)
	}
	if DefaultTimeoutConfig.SnapshotThreshold != 32768 {
		t.Errorf("expected snapshot threshold 32768, got %d", DefaultTimeoutConfig.SnapshotThreshold)
	}
}

func TestGetTimeoutConfig_Custom(t *testing.T) {
	c := &cluster{
		timeoutConfig: TimeoutConfig{
			HeartbeatTimeout:   100 * time.Millisecond,
			ElectionTimeout:    200 * time.Millisecond,
			CommitTimeout:      50 * time.Millisecond,
			LeaderLeaseTimeout: 50 * time.Millisecond,
			SnapshotInterval:   1 * time.Minute,
			SnapshotThreshold:  1000,
		},
	}

	result := c.getTimeoutConfig()

	if result.HeartbeatTimeout != 100*time.Millisecond {
		t.Errorf("expected custom heartbeat 100ms, got %v", result.HeartbeatTimeout)
	}
	if result.ElectionTimeout != 200*time.Millisecond {
		t.Errorf("expected custom election 200ms, got %v", result.ElectionTimeout)
	}
}

func TestGetTimeoutConfig_Fallback(t *testing.T) {
	c := &cluster{
		timeoutConfig: TimeoutConfig{},
	}

	result := c.getTimeoutConfig()

	if result.HeartbeatTimeout != 15*time.Second {
		t.Errorf("expected default heartbeat 15s, got %v", result.HeartbeatTimeout)
	}
	if result.ElectionTimeout != 30*time.Second {
		t.Errorf("expected default election 30s, got %v", result.ElectionTimeout)
	}
}

func TestGetTimeoutConfig_CustomOverridesDefault(t *testing.T) {
	c := &cluster{
		timeoutConfig: TimeoutConfig{
			HeartbeatTimeout:   999 * time.Millisecond,
			ElectionTimeout:    888 * time.Millisecond,
			CommitTimeout:      777 * time.Millisecond,
			LeaderLeaseTimeout: 666 * time.Millisecond,
		},
	}

	result := c.getTimeoutConfig()

	if result.HeartbeatTimeout != 999*time.Millisecond {
		t.Errorf("expected custom heartbeat, got %v", result.HeartbeatTimeout)
	}
}
