package raft

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	taupeer "github.com/taubyte/tau/p2p/peer"
)

func TestQuorumLossRecoveryIsManualOrADuration(t *testing.T) {
	var timing Timing
	require.NoError(t, yaml.Unmarshal([]byte("quorum_loss_recovery: manual\n"), &timing))
	require.Equal(t, Manual, timing.QuorumLossRecovery)
	require.Empty(t, timing.Options())

	require.NoError(t, yaml.Unmarshal([]byte("quorum_loss_recovery: 10m\n"), &timing))
	require.Equal(t, QuorumLossRecovery(10*time.Minute), timing.QuorumLossRecovery)
	require.Len(t, timing.Options(), 1)
	c := &cluster{}
	require.NoError(t, timing.Options()[0](c))
	require.Equal(t, 10*time.Minute, c.quorumLossAfter)

	require.Error(t, yaml.Unmarshal([]byte("quorum_loss_recovery: soon\n"), &timing))
	require.Error(t, yaml.Unmarshal([]byte("quorum_loss_recovery: -1m\n"), &timing))
	require.Error(t, WithQuorumLossRecovery(0)(c))

	for _, r := range []QuorumLossRecovery{Manual, QuorumLossRecovery(90 * time.Second)} {
		raw, err := yaml.Marshal(Timing{QuorumLossRecovery: r})
		require.NoError(t, err)
		var back Timing
		require.NoError(t, yaml.Unmarshal(raw, &back))
		require.Equal(t, r, back.QuorumLossRecovery)
	}
}

// survivorsOf closes all but keep of group's members, once each holds key, and
// returns those kept once none of them knows a leader.
func survivorsOf(t *testing.T, group []Cluster, key string, keep int) []*cluster {
	t.Helper()
	for _, cl := range group {
		require.Eventually(t, func() bool { _, ok := cl.Get(key); return ok }, 5*time.Second, 10*time.Millisecond)
	}
	var kept []*cluster
	for _, cl := range group {
		if len(kept) < keep {
			kept = append(kept, cl.(*cluster))
			continue
		}
		require.NoError(t, cl.Close())
	}
	for _, s := range kept {
		require.Eventually(t, func() bool { _, err := s.Leader(); return err != nil }, leaderWait, 10*time.Millisecond,
			"a survivor kept a leader with its quorum gone")
	}
	return kept
}

// A GROUP THAT LOST ITS QUORUM FOR GOOD LEADS AGAIN WHEN TOLD TO, with nobody to
// restart it: the member that remains forms a new group carrying what it holds.
func TestHealing_AGroupThatLostItsQuorumRecoversWhenToldTo(t *testing.T) {
	probeEvery(t, 50*time.Millisecond)
	group := joinedGroup(t, "quorum-recovers", sameStore, 3, WithQuorumLossRecovery(500*time.Millisecond))
	require.NoError(t, leaderOf(t, group...).Set("kept", []byte("v"), 5*time.Second))
	survivor := survivorsOf(t, group, "kept", 1)[0]
	formed := survivor.current().raft.Formation()

	require.Eventually(t, func() bool { return survivor.IsLeader() }, 20*time.Second, 50*time.Millisecond,
		"the survivor never led again")
	require.NotEqual(t, formed, survivor.current().raft.Formation(), "the survivor leads the formation that lost its quorum")
	require.Eventually(t, func() bool { _, ok := survivor.Get("kept"); return ok && !survivor.hasArchive() },
		5*time.Second, 20*time.Millisecond, "the new group does not hold what the old one committed")
	require.NoError(t, survivor.Set("after", []byte("v"), 5*time.Second))
}

// A GROUP LEFT TO MANUAL RECOVERY WAITS for its members however long they are
// gone.
func TestHealing_AGroupLeftToManualRecoveryWaits(t *testing.T) {
	probeEvery(t, 50*time.Millisecond)
	group := joinedGroup(t, "quorum-waits", sameStore, 3)
	require.NoError(t, leaderOf(t, group...).Set("kept", []byte("v"), 5*time.Second))
	survivor := survivorsOf(t, group, "kept", 1)[0]
	formed := survivor.current().raft.Formation()

	require.Never(t, func() bool { return survivor.IsLeader() }, 2*time.Second, 50*time.Millisecond,
		"a group left to manual recovery formed again")
	require.Equal(t, formed, survivor.current().raft.Formation())
	_, ok := survivor.Get("kept")
	require.True(t, ok)
}

// THE MEMBERS THAT REMAIN END UP IN ONE GROUP: the lowest forms it, and takes the
// others with their state.
func TestHealing_TheMembersThatRemainOfALostQuorumEndUpInOneGroup(t *testing.T) {
	probeEvery(t, 50*time.Millisecond)
	group := joinedGroup(t, "quorum-two-remain", sameStore, 5, WithQuorumLossRecovery(500*time.Millisecond))
	require.NoError(t, leaderOf(t, group...).Set("kept", []byte("v"), 5*time.Second))
	survivors := survivorsOf(t, group, "kept", 2)
	formed := survivors[0].current().raft.Formation()

	require.Eventually(t, func() bool {
		for _, s := range survivors {
			members, err := s.Members()
			if err != nil || len(members) != 2 || bytes.Equal(formed, s.current().raft.Formation()) {
				return false
			}
		}
		return true
	}, 30*time.Second, 50*time.Millisecond, "the members that remain never formed one group")
	leader := leaderOf(t, survivors[0], survivors[1])
	require.NoError(t, leader.Set("after", []byte("v"), 5*time.Second))
	for _, s := range survivors {
		require.Eventually(t, func() bool {
			_, kept := s.Get("kept")
			_, after := s.Get("after")
			return kept && after
		}, 5*time.Second, 20*time.Millisecond)
	}
}

// A MEMBER THAT STOPPED WHILE FORMING ITS NEW GROUP, holding nothing but the state
// it carried, forms it when it starts again and takes that state into it, even
// with another group in sight: joining that one, it would lead nothing to merge
// its archive into.
func TestHealing_AMemberThatStoppedWhileFormingFormsWithWhatItCarried(t *testing.T) {
	const namespace = "quorum-archive-replayed"
	other, err := New(newMockNode(t), namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { other.Close() })
	waitLeader(t, other, 5*time.Second)
	node := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())
	connected(t, node, other)
	data, err := encodeMergeCommand(map[string]CRDTEntry{
		"carried": {Value: []byte("v"), Timestamp: 1, WallClock: time.Now().UnixNano()},
	})
	require.NoError(t, err)
	c := &cluster{Node: node, namespace: namespace, ctx: t.Context()}
	require.NoError(t, node.Store().Put(t.Context(), c.archiveKey(), data))

	cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)
	require.Eventually(t, func() bool {
		value, found := cl.Get("carried")
		return found && string(value) == "v" && !cl.(*cluster).hasArchive()
	}, 5*time.Second, 20*time.Millisecond, "the state it carried never reached its group")
}
