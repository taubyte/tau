package raft

import "time"

var (
	// BootstrapCheckInterval is the interval at which we check if we can join an existing cluster
	BootstrapCheckInterval = 200 * time.Millisecond

	// VoterJoinTimeout is the timeout for requesting to join as a voter
	VoterJoinTimeout = 5 * time.Second

	// LateJoinerTimeout is the timeout for late joiners to request voter status
	LateJoinerTimeout = 10 * time.Second

	// SplitBrainDetectionCycles is consecutive failed election cycles before split-brain handling.
	SplitBrainDetectionCycles = 3

	// HealingProbeInterval is the minimum interval between leader probes for foreign clusters.
	HealingProbeInterval = 30 * time.Second

	// HealingMergeTimeout bounds export/merge/ack during healing.
	HealingMergeTimeout = 60 * time.Second

	// MaxWallClockDrift is the warn threshold for CRDT WallClock skew (sync NTP).
	MaxWallClockDrift = 500 * time.Millisecond
)
