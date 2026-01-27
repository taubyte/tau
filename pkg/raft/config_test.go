package raft

import (
	"testing"
	"time"
)

func TestGetTimeoutConfig_Presets(t *testing.T) {
	tests := []struct {
		name              string
		preset            TimeoutPreset
		expectedHeartbeat time.Duration
		expectedElection  time.Duration
	}{
		{
			name:              "local preset",
			preset:            PresetLocal,
			expectedHeartbeat: 1 * time.Second,
			expectedElection:  1 * time.Second,
		},
		{
			name:              "regional preset",
			preset:            PresetRegional,
			expectedHeartbeat: 5 * time.Second,
			expectedElection:  10 * time.Second,
		},
		{
			name:              "global preset",
			preset:            PresetGlobal,
			expectedHeartbeat: 15 * time.Second,
			expectedElection:  30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &cluster{
				timeoutPreset: tt.preset,
				timeoutConfig: presetConfigs[tt.preset],
			}

			result := c.getTimeoutConfig()

			if result.HeartbeatTimeout != tt.expectedHeartbeat {
				t.Errorf("expected heartbeat %v, got %v", tt.expectedHeartbeat, result.HeartbeatTimeout)
			}
			if result.ElectionTimeout != tt.expectedElection {
				t.Errorf("expected election %v, got %v", tt.expectedElection, result.ElectionTimeout)
			}
		})
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

func TestPresetConfigs_AllPresetsExist(t *testing.T) {
	presets := []TimeoutPreset{PresetLocal, PresetRegional, PresetGlobal}

	for _, preset := range presets {
		cfg, ok := presetConfigs[preset]
		if !ok {
			t.Errorf("preset %s not found in presetConfigs", preset)
			continue
		}
		if cfg.HeartbeatTimeout == 0 {
			t.Errorf("preset %s has zero heartbeat timeout", preset)
		}
		if cfg.ElectionTimeout == 0 {
			t.Errorf("preset %s has zero election timeout", preset)
		}
	}
}

func TestGetTimeoutConfig_Fallback(t *testing.T) {
	c := &cluster{
		timeoutPreset: TimeoutPreset("unknown"),
		timeoutConfig: TimeoutConfig{}, // Zero values
	}

	result := c.getTimeoutConfig()

	// Should fallback to regional
	if result.HeartbeatTimeout != 5*time.Second {
		t.Errorf("expected regional fallback heartbeat 5s, got %v", result.HeartbeatTimeout)
	}
}

func TestGetTimeoutConfig_CustomOverridesPreset(t *testing.T) {
	c := &cluster{
		timeoutPreset: PresetGlobal,
		timeoutConfig: TimeoutConfig{
			HeartbeatTimeout:   999 * time.Millisecond,
			ElectionTimeout:    888 * time.Millisecond,
			CommitTimeout:      777 * time.Millisecond,
			LeaderLeaseTimeout: 666 * time.Millisecond,
		},
	}

	result := c.getTimeoutConfig()

	// Custom values should be used
	if result.HeartbeatTimeout != 999*time.Millisecond {
		t.Errorf("expected custom heartbeat, got %v", result.HeartbeatTimeout)
	}
}
