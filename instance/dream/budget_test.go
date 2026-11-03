//go:build dreaming

package dream_test

import "time"

// What a test here may wait for a raft group, priced from dream's profile
// (instance/dream/init.go: dreamTimeouts, dreamBootstrapTimeout), so the numbers stay
// tied to it.

const (
	// formingBudget is one raft group going from nothing to a leader: discovery, the
	// bootstrap window, the founder's bootstrap and the first election. The window
	// is unbounded with peers (pkg/raft's join loop may run extra ~2.2s rounds), so
	// this is measured: across twenty openings the slowest was about 30s, and 60s is
	// twice that. A larger budget only delays reporting a stall.
	formingBudget = 60 * time.Second

	// electionBudget is an existing group choosing a new leader: measured 3.95s, at
	// most a 2-4s heartbeat timeout, one 4s election and 2s of polling, so 10s; 30s is
	// 3x.
	electionBudget = 30 * time.Second
)
