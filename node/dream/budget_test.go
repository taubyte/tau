//go:build dreaming

package dream_test

import "time"

// What a test in this package is allowed to wait for a raft group, priced from
// the profile dream opens its groups with (node/dream/init.go: dreamTimeouts,
// dreamBootstrapTimeout).
//
// THESE ARE HERE SO THE NUMBERS CANNOT DRIFT APART FROM THE PROFILE. Before the
// profile existed every one of these budgets was a guess at
// raft.DefaultTimeoutConfig plus margin — 240s in the ledger tests, 90s in the
// queue and workflow ones — and a guess that large stops describing anything:
// it cannot tell a group that is forming from one that has stalled, and it
// makes a stall cost four minutes to report.

const (
	// formingBudget is one raft group going from nothing to a leader in a dream
	// universe: discovery, the bootstrap window, the founder's bootstrap and
	// the first election.
	//
	// THERE IS NO CEILING HERE TO BE A MULTIPLE OF, and that is a property of
	// pkg/raft rather than a gap in the measurement. Forming is
	//
	//	<window> + <founder's join attempt> + <first election> + <poll>
	//
	// and only the last two are bounded. The window is dreamBootstrapTimeout
	// exactly (5.000s measured) ONLY when the node never sees a peer, which is
	// a single-node universe. With peers, every 200ms tick spends a round in
	// tryJoinExistingCluster (cluster.go:212). A round costs ~2.2s — its own
	// context is 2s (cluster.go:448) and the JoinVoter inside it is answered by
	// a leaderless target only after THAT node's 2s WaitForLeader
	// (stream.go:448), so the two coincide — but rounds of 6.2s and 10.2s were
	// also measured, because the JoinVoter call is not bounded by the loop's
	// context at all. The window's own context is read between rounds and never
	// during one, and once it has expired both cases of that select are ready,
	// so Go picks uniformly at random and the loop runs on with probability 1/2
	// per round. The founder's own attempt (cluster.go:240) is the same loop
	// again.
	//
	// SO THIS IS MEASURED, AND THE MARGIN IS A TAIL AND NOT A BOUND. Twenty
	// group openings across three universes on the build server at load 7,
	// raft-cluster logging on:
	//
	//	window, no peers                     5.000s exactly, every time
	//	window, with peers                   5.0s - 23.2s, median 9.0s
	//	bootstrap decision to bootstrapped   2.2s - 8.2s
	//	first election                       2s - 4s (1x-2x of dreamHeartbeat)
	//	a database group, open to bootstrap  10.8s, 13.2s, 25.2s
	//
	// The slowest of those is ~30s by the time the election and this loop's own
	// 1s poll are counted, so 60s IS 2x THE WORST ACTUALLY SEEN, and going past
	// it means running about a dozen more ~2.2s rounds than the worst run
	// needed, each one a coin flip. No run of the two ledger tests on this
	// profile has come near it — more than a dozen runs across four sessions.
	//
	// AND A BIGGER BUDGET WOULD BUY ALMOST NOTHING. Past the worst observed
	// forming, every extra round halves an already small tail while the cost of
	// the budget — how long a genuinely stalled group takes to report — grows
	// with every second. That, not margin, is the argument for 60 over 240.
	formingBudget = 60 * time.Second

	// electionBudget is a group that ALREADY EXISTS choosing a new leader,
	// which is the cheaper half: nothing bootstraps, nothing joins, and the
	// survivors are already in the configuration.
	//
	// MEASURED 3.95s close-to-leader when the closed voter was the leader: the
	// survivors logged a heartbeat timeout 1.94s after the close and took two
	// rounds to resolve it. Ceiling: a 2-4s HeartbeatTimeout before a follower
	// stands, at most one 4s ElectionTimeout if the round splits, and 2s of
	// poll granularity — 10s. 30s is 3x.
	electionBudget = 30 * time.Second
)
