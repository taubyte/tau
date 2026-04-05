package raft

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	taupeer "github.com/taubyte/tau/p2p/peer"
)

// healingTestOptions uses fast timeouts and force-bootstrap so each mock node is its own single-node cluster.
func healingTestOptions() []Option {
	return []Option{
		WithTimeouts(TimeoutConfig{
			HeartbeatTimeout:   100 * time.Millisecond,
			ElectionTimeout:    100 * time.Millisecond,
			CommitTimeout:      50 * time.Millisecond,
			LeaderLeaseTimeout: 50 * time.Millisecond,
			SnapshotInterval:   1 * time.Minute,
			SnapshotThreshold:  1000,
		}),
		WithForceBootstrap(),
		WithBootstrapTimeout(50 * time.Millisecond),
	}
}

func waitLeader(t *testing.T, c Cluster, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	require.NoError(t, c.WaitForLeader(ctx), "timed out waiting for leader")
}

func TestHealing_TwoSplitClusters_MergesFSM(t *testing.T) {
	node1 := taupeer.Mock(t.Context())
	node2 := taupeer.Mock(t.Context())

	require.NoError(t, taupeer.LinkAllPeers())

	opts := healingTestOptions()

	c1, err := New(node1, "heal-merge", opts...)
	require.NoError(t, err)
	defer c1.Close()

	c2, err := New(node2, "heal-merge", opts...)
	require.NoError(t, err)
	defer c2.Close()

	waitLeader(t, c1, 3*time.Second)
	waitLeader(t, c2, 3*time.Second)

	assert.True(t, c1.IsLeader())
	assert.True(t, c2.IsLeader())

	require.NoError(t, c1.Set("from-c1", []byte("value1"), time.Second))
	require.NoError(t, c2.Set("from-c2", []byte("value2"), time.Second))

	require.NoError(t, c1.Set("shared", []byte("c1-version"), time.Second))
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, c2.Set("shared", []byte("c2-version"), time.Second))

	require.NoError(t, taupeer.LinkAllPeers())

	cl1 := c1.(*cluster)
	cl2 := c2.(*cluster)
	cl1.tracker.addPeer(node2.ID())
	cl2.tracker.addPeer(node1.ID())

	info1 := cl1.healer.localClusterInfo()
	info2 := cl2.healer.localClusterInfo()
	winnerID := negotiateWinner(info1, info2)

	var winner *cluster
	if winnerID == info1.NodeID {
		winner = cl1
	} else {
		winner = cl2
	}
	loser := cl2
	if winner == cl2 {
		loser = cl1
	}

	winner.healer.executeMerge(t.Context(), loser.node.ID())

	v, ok := winner.Get("from-c1")
	assert.True(t, ok)
	assert.Equal(t, "value1", string(v))

	v, ok = winner.Get("from-c2")
	assert.True(t, ok)
	assert.Equal(t, "value2", string(v))

	v, ok = winner.Get("shared")
	assert.True(t, ok)
	assert.Equal(t, "c2-version", string(v))
}

func TestMergeCRDTDelta(t *testing.T) {
	our := map[string]CRDTEntry{
		"keep": {Value: []byte("a"), Timestamp: 5},
		"lose": {Value: []byte("old"), Timestamp: 1},
	}
	foreign := map[string]CRDTEntry{
		"keep": {Value: []byte("b"), Timestamp: 3},
		"lose": {Value: []byte("new"), Timestamp: 10},
		"new":  {Value: []byte("x"), Timestamp: 1},
	}
	delta := mergeCRDTDelta(our, foreign)

	_, hasKeep := delta["keep"]
	assert.False(t, hasKeep, "foreign keep loses on Lamport")

	assert.Equal(t, []byte("new"), delta["lose"].Value)
	assert.Equal(t, []byte("x"), delta["new"].Value)
}

func TestHealing_MergeApply(t *testing.T) {
	node := taupeer.Mock(t.Context())
	require.NoError(t, taupeer.LinkAllPeers())

	c, err := New(node, "merge-apply", healingTestOptions()...)
	require.NoError(t, err)
	defer c.Close()

	waitLeader(t, c, 3*time.Second)

	require.NoError(t, c.Set("keep-local", []byte("local"), time.Second))
	require.NoError(t, c.Set("will-merge", []byte("local"), time.Second))
	require.NoError(t, c.Set("another", []byte("local"), time.Second))

	cl := c.(*cluster)
	ourState, err := cl.fsm.ExportState()
	require.NoError(t, err)

	foreignState := map[string]CRDTEntry{
		"keep-local": {Value: []byte("foreign"), Timestamp: 0, WallClock: 0},
		"will-merge": {Value: []byte("foreign"), Timestamp: 999, WallClock: time.Now().UnixNano()},
		"new-key":    {Value: []byte("brand-new"), Timestamp: 5, WallClock: time.Now().UnixNano()},
	}
	delta := mergeCRDTDelta(ourState, foreignState)

	mergeData, err := encodeMergeCommand(delta)
	require.NoError(t, err)
	_, err = c.Apply(mergeData, 5*time.Second)
	require.NoError(t, err)

	v, ok := c.Get("keep-local")
	assert.True(t, ok)
	assert.Equal(t, "local", string(v))

	v, ok = c.Get("will-merge")
	assert.True(t, ok)
	assert.Equal(t, "foreign", string(v))

	v, ok = c.Get("new-key")
	assert.True(t, ok)
	assert.Equal(t, "brand-new", string(v))
}

func TestHealing_HealAck_Signal(t *testing.T) {
	node := taupeer.Mock(t.Context())
	require.NoError(t, taupeer.LinkAllPeers())

	h := &healer{
		healAckCh:     make(chan peer.ID, 1),
		foreignVoteCh: make(chan peer.ID, 16),
	}

	h.signalHealAck(node.ID())

	select {
	case from := <-h.healAckCh:
		assert.Equal(t, node.ID(), from)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("healAck signal was not received")
	}

	h.signalHealAck(node.ID())
	h.signalHealAck(node.ID())

	select {
	case <-h.healAckCh:
	default:
		t.Fatal("expected buffered signal")
	}

	select {
	case <-h.healAckCh:
		t.Fatal("buffer should be 1, second signal should be dropped")
	default:
	}
}
