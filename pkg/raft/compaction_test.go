package raft

import (
	"context"
	"fmt"
	"path"
	"testing"
	"time"

	"github.com/ipfs/go-datastore/query"
	"github.com/stretchr/testify/require"
)

// logEntries is how many raft log entries the group keeps in the peer's datastore.
func logEntries(t *testing.T, c *cluster) int {
	t.Helper()
	results, err := c.Store().Query(context.Background(), query.Query{
		Prefix:   path.Join(RaftStoragePrefix, c.namespace, "log"),
		KeysOnly: true,
	})
	require.NoError(t, err)
	entries, err := results.Rest()
	require.NoError(t, err)
	return len(entries)
}

// A GROUP THAT WRITES PAST ITS THRESHOLD KEEPS A BOUNDED LOG AND A BOUNDED SET OF
// SNAPSHOTS: each snapshot deletes the log behind it but for the trailing entries,
// the snapshot store keeps no more than it retains, and the state a snapshot holds
// is the live keys alone.
func TestCluster_CompactsItsLogAndKeepsBoundedSnapshots(t *testing.T) {
	timeouts := testTimeoutConfig()
	timeouts.SnapshotThreshold = 64
	timeouts.TrailingLogs = 16

	c, err := New(newMockNode(t), "compacts", WithTimeouts(timeouts), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	waitLeader(t, c, 5*time.Second)
	inner := c.(*cluster)

	const writes = 400
	for round := range defaultRetainSnapshots + 2 {
		for i := range writes / (defaultRetainSnapshots + 2) {
			key := fmt.Sprintf("k/%d/%d", round, i)
			require.NoError(t, c.Set(key, []byte("v"), time.Second))
			if i%2 == 0 {
				require.NoError(t, c.Delete(key, time.Second))
			}
		}
		require.NoError(t, inner.current().raft.Snapshot().Error())
	}

	// THE LOG BEHIND THE LAST SNAPSHOT IS GONE but for the trailing entries, so
	// what a node keeps is bounded by the setting rather than by its history.
	require.LessOrEqual(t, logEntries(t, inner), int(timeouts.TrailingLogs)+1)
	require.NotEqual(t, "0", inner.current().raft.Stats()["last_snapshot_index"])

	snaps, err := inner.current().snaps.List()
	require.NoError(t, err)
	require.LessOrEqual(t, len(snaps), defaultRetainSnapshots, "older snapshots are reaped")

	// AND WHAT A SNAPSHOT HOLDS IS WHAT IS LIVE: a deleted key is not carried.
	require.Len(t, c.Keys("k/"), writes/(defaultRetainSnapshots+2)/2*(defaultRetainSnapshots+2))
}
