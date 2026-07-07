//go:build raft_integration

package raft

import (
	"context"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// fastSingleNode timeouts: quick election so single-node bootstrap settles fast.
var fastSingleNode = TimeoutConfig{
	HeartbeatTimeout:   50 * time.Millisecond,
	ElectionTimeout:    50 * time.Millisecond,
	CommitTimeout:      25 * time.Millisecond,
	LeaderLeaseTimeout: 25 * time.Millisecond,
	SnapshotInterval:   time.Minute,
	SnapshotThreshold:  1000,
}

// bootstrapAndSnapshot brings up a single-node leader on snapDir, commits one
// entry, and forces a snapshot so snapDir holds this node's Raft configuration.
func bootstrapAndSnapshot(t *testing.T, snapDir, namespace string) {
	t.Helper()
	node := newTestNode(t)
	c, err := New(node, namespace,
		WithForceBootstrap(),
		WithSnapshotDir(snapDir),
		WithTimeouts(fastSingleNode),
	)
	assert.NilError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	assert.NilError(t, c.WaitForLeader(ctx))
	assert.NilError(t, c.Set("k", []byte("v"), 2*time.Second))

	// Force a snapshot: snapDir now carries a configuration listing this node.
	assert.NilError(t, c.(*cluster).raft.Snapshot().Error())
	assert.NilError(t, c.Close())
}

// TestSharedSnapshotDir_StaleIdentity_Wedges reproduces #431. Two nodes with
// different identities share one snapshot dir (as dream did via the global /tmp
// default). The second node restores the first's snapshot — whose configuration
// does not include it — enters follower state "not part of stable configuration"
// and can never elect a leader.
func TestSharedSnapshotDir_StaleIdentity_Wedges(t *testing.T) {
	shared := t.TempDir()
	const ns = "main"

	bootstrapAndSnapshot(t, shared, ns)

	// Second node, fresh identity, SAME snapshot dir.
	nodeB := newTestNode(t)
	cB, err := New(nodeB, ns,
		WithBootstrapTimeout(500*time.Millisecond),
		WithSnapshotDir(shared),
		WithTimeouts(fastSingleNode),
	)
	// New may return nil (then WaitForLeader hangs) or an error; either way node
	// B must not reach a healthy single-node leader.
	if err == nil {
		defer cB.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		err = cB.WaitForLeader(ctx)
	}
	assert.Assert(t, err != nil,
		"#431: node B inherited node A's snapshot and should not be able to elect a leader")
}

// TestScopedSnapshotDir_FreshIdentity_Bootstraps is the fix guard: when the
// snapshot dir is scoped per root (SnapshotDir / NewRaftCluster), a fresh node in
// its own dir bootstraps cleanly regardless of a prior node's leftover snapshot.
func TestScopedSnapshotDir_FreshIdentity_Bootstraps(t *testing.T) {
	const ns = "main"

	// A prior run leaves a snapshot under its own root.
	bootstrapAndSnapshot(t, SnapshotDir(t.TempDir(), "shapeA", ns), ns)

	// New run: its own root → its own snapshot dir → no contamination.
	nodeB := newTestNode(t)
	cB, err := New(nodeB, ns,
		WithForceBootstrap(),
		WithSnapshotDir(SnapshotDir(t.TempDir(), "shapeB", ns)),
		WithTimeouts(fastSingleNode),
	)
	assert.NilError(t, err)
	defer cB.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	assert.NilError(t, cB.WaitForLeader(ctx),
		"fresh node in its own snapshot dir must elect a leader")
}
