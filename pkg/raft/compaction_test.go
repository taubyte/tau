package raft

import (
	"context"
	"fmt"
	"path"
	"testing"
	"time"

	"github.com/ipfs/go-datastore/query"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/peer/peertest"
)

// logEntries is how many raft log entries the group keeps in the peer's datastore.
func logEntries(t *testing.T, c *cluster) int {
	t.Helper()
	results, err := c.Store().Query(context.Background(), query.Query{
		Prefix:   path.Join(RaftStoragePrefix, c.namespace, "engine", "log"),
		KeysOnly: true,
	})
	require.NoError(t, err)
	entries, err := results.Rest()
	require.NoError(t, err)
	return len(entries)
}

// A GROUP THAT WRITES PAST ITS THRESHOLD KEEPS A BOUNDED LOG: each snapshot deletes
// the log behind it but for the trailing entries, and the state a snapshot holds is
// the live keys alone.
func TestCluster_CompactsItsLogBehindItsSnapshot(t *testing.T) {
	mn := peertest.Net(t)
	timeouts := testTimeoutConfig()
	timeouts.SnapshotThreshold = 64
	timeouts.TrailingLogs = 16

	c, err := New(newMockNode(t, mn), "compacts", WithTimeouts(timeouts), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	waitLeader(t, c, 5*time.Second)
	inner := c.(*cluster)

	const rounds, writes = 5, 80
	for round := range rounds {
		for i := range writes {
			key := fmt.Sprintf("k/%d/%d", round, i)
			require.NoError(t, c.Set(key, []byte("v"), time.Second))
			if i%2 == 0 {
				require.NoError(t, c.Delete(key, time.Second))
			}
		}
	}
	require.NoError(t, inner.current().raft.Snapshot())

	// THE LOG BEHIND THE SNAPSHOT IS GONE but for the trailing entries, so what a
	// node keeps is bounded by the setting rather than by its history.
	require.LessOrEqual(t, logEntries(t, inner), int(timeouts.TrailingLogs)+1)
	snap, err := inner.current().raft.log.Snapshot()
	require.NoError(t, err)
	require.NotZero(t, snap.GetMetadata().GetIndex())

	// AND WHAT A SNAPSHOT HOLDS IS WHAT IS LIVE: a deleted key is not carried.
	require.Len(t, keysOf(t, c, "k/"), rounds*writes/2)
}
