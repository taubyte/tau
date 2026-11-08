package raft

import (
	"bytes"
	"context"
	"path"
	"sync"
	"testing"
	"time"

	ds "github.com/ipfs/go-datastore"
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

func TestManualRecoveryIsWrittenAsManual(t *testing.T) {
	written, err := Manual.MarshalYAML()
	require.NoError(t, err)
	require.Equal(t, "manual", written)
}

// AN ARCHIVE THE STORE CANNOT READ, MERGE OR LET GO OF IS KEPT, and replayed once
// it can be: what a member carried out of a lost formation is its only copy.
func TestHealing_AnArchiveTheStoreRefusesIsReplayedOnceItCan(t *testing.T) {
	const namespace = "archive-refused"
	store := newFaultStore()
	node := storeNode{Node: newMockNode(t), store: store}
	data, err := encodeMergeCommand(map[string]CRDTEntry{
		"carried": {Value: []byte("v"), Timestamp: 1, WallClock: time.Now().UnixNano()},
	})
	require.NoError(t, err)
	c := &cluster{Node: node, namespace: namespace, ctx: t.Context()}
	archive := c.archiveKey().String()
	require.NoError(t, store.Put(t.Context(), c.archiveKey(), data))
	carried := path.Join(RaftStoragePrefix, namespace, "data", "carried")
	store.failGet(archive, errDisk)
	store.failPut(carried, errNoSpace)
	store.failDelete(archive, errNoSpace)

	cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)
	held := func() bool { _, found := cl.Get("carried"); return found }

	require.Never(t, held, 300*time.Millisecond, 20*time.Millisecond, "an archive the store could not read was merged")
	store.failGet(archive, nil)
	require.Never(t, held, 300*time.Millisecond, 20*time.Millisecond, "an archive the store could not merge was merged")
	require.True(t, cl.(*cluster).hasArchive())

	store.failPut(carried, nil)
	require.Eventually(t, held, 5*time.Second, 20*time.Millisecond, "the archive was never merged once it could be")
	require.Never(t, func() bool { return !cl.(*cluster).hasArchive() }, 300*time.Millisecond, 20*time.Millisecond,
		"an archive the store could not let go of was let go of")

	store.failDelete(archive, nil)
	require.Eventually(t, func() bool { return !cl.(*cluster).hasArchive() }, 5*time.Second, 20*time.Millisecond,
		"the archive was never let go of once it could be")
}

// A SURVIVOR THAT CANNOT ARCHIVE WHAT IT HOLDS WIPES NOTHING, and forms its new
// group once it can.
func TestHealing_ASurvivorThatCannotArchiveWipesNothing(t *testing.T) {
	const namespace = "archive-not-written"
	probeEvery(t, 50*time.Millisecond)
	var stores []*faultStore
	wrap := func(ds.Batching) ds.Batching {
		s := newFaultStore()
		stores = append(stores, s)
		return s
	}
	group := joinedGroup(t, namespace, wrap, 3, WithQuorumLossRecovery(300*time.Millisecond))
	require.NoError(t, leaderOf(t, group...).Set("kept", []byte("v"), 5*time.Second))
	archive := path.Join(RaftStoragePrefix, namespace, "archive")
	stores[0].failPut(archive, errNoSpace)
	survivor := survivorsOf(t, group, "kept", 1)[0]
	formed, before := survivor.current().raft.Formation(), survivor.current()

	require.Never(t, func() bool { return survivor.current() != before }, 1500*time.Millisecond, 20*time.Millisecond,
		"a survivor that could not archive what it holds wiped it")
	_, kept := survivor.Get("kept")
	require.True(t, kept)

	stores[0].failPut(archive, nil)
	require.Eventually(t, func() bool { return survivor.IsLeader() && !bytes.Equal(formed, survivor.current().raft.Formation()) },
		20*time.Second, 50*time.Millisecond, "the survivor never formed its new group once it could archive")
	require.Eventually(t, func() bool { _, ok := survivor.Get("kept"); return ok }, 5*time.Second, 20*time.Millisecond)
}

// A GROUP CARRYING AN ARCHIVE TAKES NO OUTSIDE WRITE UNTIL IT IS IN: one taken
// before would carry a clock behind the archive's, and lose to it on replay.
func TestHealing_AGroupCarryingAnArchiveTakesNoWriteUntilItIsIn(t *testing.T) {
	const namespace = "archive-gates-writes"
	store := newFaultStore()
	node := storeNode{Node: newMockNode(t), store: store}
	data, err := encodeMergeCommand(map[string]CRDTEntry{
		"carried": {Value: []byte("old"), Timestamp: 100, WallClock: time.Now().UnixNano()},
	})
	require.NoError(t, err)
	c := &cluster{Node: node, namespace: namespace, ctx: t.Context()}
	require.NoError(t, store.Put(t.Context(), c.archiveKey(), data))
	carried := path.Join(RaftStoragePrefix, namespace, "data", "carried")
	store.failPut(carried, errNoSpace)

	cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)
	require.ErrorIs(t, cl.Set("carried", []byte("new"), time.Second), ErrFrozen)

	store.failPut(carried, nil)
	require.Eventually(t, func() bool { return !cl.(*cluster).hasArchive() }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, cl.Set("carried", []byte("new"), time.Second))
	value, found := cl.Get("carried")
	require.True(t, found)
	require.Equal(t, "new", string(value), "a write after the replay lost to the archive")
}

// rearchiving is a store on which another archive is written the moment a replay
// looks again whether the one it merged is still there: a recovery since.
type rearchiving struct {
	ds.Batching
	key ds.Key

	mu    sync.Mutex
	next  []byte
	reads int
}

func (s *rearchiving) Get(ctx context.Context, key ds.Key) ([]byte, error) {
	if key == s.key {
		s.mu.Lock()
		s.reads++
		again := s.reads == 2
		s.mu.Unlock()
		if again {
			if err := s.Batching.Put(ctx, key, s.next); err != nil {
				return nil, err
			}
		}
	}
	return s.Batching.Get(ctx, key)
}

// A REPLAY LEAVES AN ARCHIVE WRITTEN SINCE: it lets go only of the one it merged.
func TestHealing_AReplayLeavesAnArchiveWrittenSince(t *testing.T) {
	const namespace = "archive-written-since"
	first, err := encodeMergeCommand(map[string]CRDTEntry{"a": {Value: []byte("v"), Timestamp: 1}})
	require.NoError(t, err)
	next, err := encodeMergeCommand(map[string]CRDTEntry{"b": {Value: []byte("v"), Timestamp: 1}})
	require.NoError(t, err)
	node := newMockNode(t)
	archive := ds.NewKey(path.Join(RaftStoragePrefix, namespace, "archive"))
	store := &rearchiving{Batching: node.Store(), key: archive, next: next}
	cl, err := New(storeNode{Node: node, store: store}, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)
	c := cl.(*cluster)
	require.Equal(t, archive, c.archiveKey())
	require.NoError(t, store.Put(t.Context(), archive, first))

	c.current().healer.Load().replayArchive(t.Context())
	kept, err := node.Store().Get(t.Context(), archive)
	require.NoError(t, err, "the replay let go of an archive written since")
	require.Equal(t, next, kept)
	_, merged := cl.Get("a")
	require.True(t, merged, "the replay never merged the archive it read")
}
