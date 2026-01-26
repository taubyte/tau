package raft

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig("/raft/test")

	if cfg.namespace != "/raft/test" {
		t.Errorf("expected namespace /raft/test, got %s", cfg.namespace)
	}

	if cfg.timeoutPreset != PresetRegional {
		t.Errorf("expected preset regional, got %s", cfg.timeoutPreset)
	}

	if cfg.discoveryConfig.DiscoveryInterval != 30*time.Second {
		t.Errorf("expected discovery interval 30s, got %v", cfg.discoveryConfig.DiscoveryInterval)
	}

	if cfg.discoveryConfig.MinPeers != 0 {
		t.Errorf("expected min peers 0, got %d", cfg.discoveryConfig.MinPeers)
	}
}

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
			cfg := defaultConfig("/raft/test")
			cfg.timeoutPreset = tt.preset
			cfg.timeoutConfig = presetConfigs[tt.preset]

			result := cfg.getTimeoutConfig()

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
	cfg := defaultConfig("/raft/test")
	cfg.timeoutConfig = TimeoutConfig{
		HeartbeatTimeout:   100 * time.Millisecond,
		ElectionTimeout:    200 * time.Millisecond,
		CommitTimeout:      50 * time.Millisecond,
		LeaderLeaseTimeout: 50 * time.Millisecond,
		SnapshotInterval:   1 * time.Minute,
		SnapshotThreshold:  1000,
	}

	result := cfg.getTimeoutConfig()

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
	// Test with unknown preset
	cfg := &config{
		timeoutPreset: TimeoutPreset("unknown"),
		timeoutConfig: TimeoutConfig{}, // Zero values
	}

	result := cfg.getTimeoutConfig()

	// Should fallback to regional
	if result.HeartbeatTimeout != 5*time.Second {
		t.Errorf("expected regional fallback heartbeat 5s, got %v", result.HeartbeatTimeout)
	}
}

func TestGetTimeoutConfig_CustomOverridesPreset(t *testing.T) {
	cfg := defaultConfig("/raft/test")
	cfg.timeoutPreset = PresetGlobal
	cfg.timeoutConfig = TimeoutConfig{
		HeartbeatTimeout:   999 * time.Millisecond,
		ElectionTimeout:    888 * time.Millisecond,
		CommitTimeout:      777 * time.Millisecond,
		LeaderLeaseTimeout: 666 * time.Millisecond,
	}

	result := cfg.getTimeoutConfig()

	// Custom values should be used
	if result.HeartbeatTimeout != 999*time.Millisecond {
		t.Errorf("expected custom heartbeat, got %v", result.HeartbeatTimeout)
	}
}
