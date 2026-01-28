package raft

import "time"

var (
	// BootstrapCheckInterval is the interval at which we check if we can join an existing cluster
	BootstrapCheckInterval = 200 * time.Millisecond

	// VoterJoinTimeout is the timeout for requesting to join as a voter
	VoterJoinTimeout = 5 * time.Second

	// LateJoinerTimeout is the timeout for late joiners to request voter status
	LateJoinerTimeout = 10 * time.Second
)
