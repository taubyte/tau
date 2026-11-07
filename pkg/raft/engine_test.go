package raft

import (
	"context"
	"errors"
	"path"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	taupeer "github.com/taubyte/tau/p2p/peer"
)

// quickTimeouts is raft timed for nodes one process apart: a tick every 5ms.
var quickTimeouts = TimeoutConfig{HeartbeatTimeout: 50 * time.Millisecond, SnapshotThreshold: 1024, TrailingLogs: 64}

func linked(t *testing.T, n int) []taupeer.Node {
	t.Helper()
	nodes := make([]taupeer.Node, n)
	for i := range nodes {
		nodes[i] = taupeer.Mock(t.Context())
		t.Cleanup(nodes[i].Close)
	}
	connectAllOf(t, nodes...)
	return nodes
}

// onEngine is namespace's group on node's engine, over the KV machine.
func onEngine(t *testing.T, node taupeer.Node, namespace string, timeouts TimeoutConfig) *group {
	t.Helper()
	e := engineFor(node)
	t.Cleanup(e.release)
	fsm := newKVFSM(t.Context(), node.Store(), path.Join(RaftStoragePrefix, namespace)).(machine)
	g, err := e.open(namespace, groupConfig{timeouts: timeouts, machine: fsm})
	require.NoError(t, err)
	t.Cleanup(func() { g.Close() })
	return g
}

func set(t *testing.T, g *group, key, value string) {
	t.Helper()
	cmd, err := encodeSetCommand(key, []byte(value))
	require.NoError(t, err)
	resp, err := g.Propose(cmd, 5*time.Second)
	require.NoError(t, err)
	require.NoError(t, resp.Error)
}

func held(g *group, key string) string {
	v, _ := g.fsm.(FSM).Get(key)
	return string(v)
}

func leading(t *testing.T, groups ...*group) *group {
	t.Helper()
	var leader *group
	require.Eventually(t, func() bool {
		for _, g := range groups {
			if g.State() == Leader {
				leader = g
				return true
			}
		}
		return false
	}, 10*time.Second, 5*time.Millisecond, "no group elected a leader")
	return leader
}

func TestAGroupOfOneElectsItselfAppliesAndKeepsItsLog(t *testing.T) {
	node := taupeer.Mock(t.Context())
	t.Cleanup(node.Close)

	e := engineFor(node)
	fsm := newKVFSM(t.Context(), node.Store(), path.Join(RaftStoragePrefix, "one")).(machine)
	g, err := e.open("one", groupConfig{timeouts: quickTimeouts, machine: fsm})
	require.NoError(t, err)
	require.NoError(t, g.Bootstrap([]peer.ID{node.ID()}))
	require.ErrorIs(t, g.Bootstrap([]peer.ID{node.ID()}), ErrCantBootstrap, "a log is bootstrapped once")
	leading(t, g)

	set(t, g, "k", "v")
	require.Equal(t, "v", held(g, "k"))
	require.NoError(t, g.Barrier(5*time.Second))
	leader, ok := g.Leader()
	require.True(t, ok)
	require.Equal(t, node.ID(), leader)
	require.Equal(t, []Member{{ID: node.ID(), Voter: true}}, g.Members())

	_, err = e.open("one", groupConfig{timeouts: quickTimeouts, machine: fsm})
	require.ErrorIs(t, err, ErrGroupOpen)

	// A RESTART reads back the log it kept, and leads again.
	require.NoError(t, g.Close())
	e.release()
	again := onEngine(t, node, "one", quickTimeouts)
	leading(t, again)
	set(t, again, "k2", "v2")
	require.Equal(t, "v", held(again, "k"))
	require.Equal(t, "v2", held(again, "k2"))

	cmd, err := encodeSetCommand("late", []byte("x"))
	require.NoError(t, err)
	require.NoError(t, again.Close())
	_, err = again.Propose(cmd, time.Second)
	require.ErrorIs(t, err, ErrShutdown, "a closed group takes nothing")
}

func TestThreeNodesReplicateAndChangeMembers(t *testing.T) {
	nodes := linked(t, 4)
	ids := []peer.ID{nodes[0].ID(), nodes[1].ID(), nodes[2].ID()}
	groups := []*group{
		onEngine(t, nodes[0], "three", quickTimeouts),
		onEngine(t, nodes[1], "three", quickTimeouts),
		onEngine(t, nodes[2], "three", quickTimeouts),
	}
	// ONE FOUNDER BOOTSTRAPS, naming the others, who start empty and learn the
	// configuration from the leader: how pkg/raft's discovery forms a group.
	require.NoError(t, groups[0].Bootstrap(ids))
	leader := leading(t, groups...)

	set(t, leader, "a", "1")
	for _, g := range groups {
		require.Eventually(t, func() bool { return held(g, "a") == "1" }, 5*time.Second, 5*time.Millisecond)
	}

	var follower *group
	for _, g := range groups {
		if g != leader {
			follower = g
		}
	}
	cmd, err := encodeSetCommand("b", []byte("2"))
	require.NoError(t, err)
	_, err = follower.Propose(cmd, time.Second)
	require.ErrorIs(t, err, ErrNotLeader, "a follower refuses a write by name")
	require.NoError(t, follower.Barrier(5*time.Second), "a follower's barrier is the leader's read index")
	require.ErrorIs(t, follower.AddVoter(nodes[3].ID(), time.Second), ErrNotLeader)

	// A FOURTH NODE joins empty, is added by the leader, and catches up.
	fourth := onEngine(t, nodes[3], "three", quickTimeouts)
	require.NoError(t, leader.AddVoter(nodes[3].ID(), 5*time.Second))
	require.Eventually(t, func() bool { return held(fourth, "a") == "1" }, 5*time.Second, 5*time.Millisecond)
	require.Len(t, leader.Members(), 4)

	require.NoError(t, leader.RemoveServer(nodes[3].ID(), 5*time.Second))
	require.Len(t, leader.Members(), 3)
	require.Greater(t, leader.CommitIndex(), uint64(0))
	require.GreaterOrEqual(t, leader.LastIndex(), leader.CommitIndex())
	require.Greater(t, leader.Term(), uint64(0))

	// LEADERSHIP MOVES on request.
	require.NoError(t, leader.TransferLeadership(""))
	require.NotEqual(t, Leader, leader.State(), "it returns once leadership has moved")
	require.ErrorIs(t, leader.TransferLeadership(""), ErrNotLeader)
}

func TestANewcomerCatchesUpFromASnapshot(t *testing.T) {
	nodes := linked(t, 2)
	compacting := TimeoutConfig{HeartbeatTimeout: 50 * time.Millisecond, SnapshotThreshold: 5, TrailingLogs: 1}
	first := onEngine(t, nodes[0], "snap", compacting)
	require.NoError(t, first.Bootstrap([]peer.ID{nodes[0].ID()}))
	leading(t, first)
	for i := range 20 {
		set(t, first, "k", string(rune('a'+i)))
	}
	compacted, err := first.log.FirstIndex()
	require.NoError(t, err)
	require.Greater(t, compacted, uint64(2), "the leader compacted its log")

	second := onEngine(t, nodes[1], "snap", compacting)
	require.NoError(t, first.AddVoter(nodes[1].ID(), 5*time.Second))
	require.Eventually(t, func() bool { return held(second, "k") == string(rune('a'+19)) }, 5*time.Second, 5*time.Millisecond,
		"the newcomer was sent a snapshot, the log before it being gone")
}

// voteWatch records what a group's observers hear.
type voteWatch struct {
	mu      sync.Mutex
	foreign []peer.ID
	leaders []peer.ID
}

func (w *voteWatch) leaderChanged(p peer.ID) {
	w.mu.Lock()
	w.leaders = append(w.leaders, p)
	w.mu.Unlock()
}

func (w *voteWatch) foreignVote(p peer.ID) {
	w.mu.Lock()
	w.foreign = append(w.foreign, p)
	w.mu.Unlock()
}

func (w *voteWatch) heard() (foreign, leaders int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.foreign), len(w.leaders)
}

// A SECOND GROUP UNDER THE SAME NAME shows itself as a vote asked by a node the
// first does not hold: what the healer watches for.
func TestAVoteFromOutsideTheGroupIsHeard(t *testing.T) {
	nodes := linked(t, 2)
	lone := onEngine(t, nodes[1], "split", quickTimeouts)
	require.NoError(t, lone.Bootstrap([]peer.ID{nodes[1].ID()}))
	watch := &voteWatch{}
	lone.Observe(watch)
	leading(t, lone)

	other := onEngine(t, nodes[0], "split", quickTimeouts)
	require.NoError(t, other.Bootstrap([]peer.ID{nodes[0].ID(), nodes[1].ID()}))
	require.Eventually(t, func() bool { f, _ := watch.heard(); return f > 0 }, 5*time.Second, 5*time.Millisecond)
	_, leaders := watch.heard()
	require.Positive(t, leaders, "the lone group heard itself elected")
}

// A WRITE NOBODY CAN COMMIT ends with the group: a quorum of two with one member
// unreachable commits nothing, and closing the group answers the write.
func TestClosingAGroupAnswersWhatWaitsOnIt(t *testing.T) {
	nodes := linked(t, 1)
	ghost := taupeer.Mock(t.Context())
	ghostID := ghost.ID()
	ghost.Close()

	g := onEngine(t, nodes[0], "stuck", quickTimeouts)
	require.NoError(t, g.Bootstrap([]peer.ID{nodes[0].ID(), ghostID}))

	// No leader can be elected without the ghost, so a barrier says so by name.
	require.ErrorIs(t, g.Barrier(200*time.Millisecond), ErrNoLeader)

	cmd, err := encodeSetCommand("k", []byte("v"))
	require.NoError(t, err)
	_, err = g.Propose(cmd, time.Second)
	require.ErrorIs(t, err, ErrNotLeader, "a candidate takes no write")

	ch := make(chan FSMResponse, 1)
	require.NoError(t, g.await(7, ch))
	require.NoError(t, g.Close())
	_, open := <-ch
	require.False(t, open, "closing answers what waited")
	require.ErrorIs(t, g.failedOr(ErrNoLeader), ErrShutdown)
	require.ErrorIs(t, g.await(8, ch), ErrShutdown)
	require.ErrorIs(t, g.Barrier(time.Second), ErrShutdown)
	require.Equal(t, Shutdown, g.State())
}

// WIPED, a group starts again as on a node that never ran it.
func TestAWipedGroupStartsAgain(t *testing.T) {
	node := taupeer.Mock(t.Context())
	t.Cleanup(node.Close)
	g := onEngine(t, node, "wiped", quickTimeouts)
	require.NoError(t, g.Bootstrap([]peer.ID{node.ID()}))
	leading(t, g)
	set(t, g, "k", "v")

	require.NoError(t, g.Close())
	require.NoError(t, g.Wipe(t.Context()))
	again := onEngine(t, node, "wiped", quickTimeouts)
	require.NoError(t, again.Bootstrap([]peer.ID{node.ID()}), "nothing kept, so it bootstraps again")
	leading(t, again)
}

// failingStore is a node whose datastore stops taking batches once told to.
type failingStore struct {
	taupeer.Node
	broken *atomic.Bool
}

func (f failingStore) Store() datastore.Batching {
	return refusingBatches{Batching: f.Node.Store(), broken: f.broken}
}

type refusingBatches struct {
	datastore.Batching
	broken *atomic.Bool
}

func (r refusingBatches) Batch(ctx context.Context) (datastore.Batch, error) {
	if r.broken.Load() {
		return nil, errors.New("disk gone")
	}
	return r.Batching.Batch(ctx)
}

// A GROUP WHOSE STATE CANNOT BE WRITTEN STOPS, rather than run on what it did not
// keep: what waits on it hears why.
func TestAGroupThatCannotWriteStops(t *testing.T) {
	mock := taupeer.Mock(t.Context())
	t.Cleanup(mock.Close)
	node := failingStore{Node: mock, broken: &atomic.Bool{}}
	g := onEngine(t, node, "broken", quickTimeouts)
	require.NoError(t, g.Bootstrap([]peer.ID{node.ID()}))
	leading(t, g)

	node.broken.Store(true)
	cmd, err := encodeSetCommand("k", []byte("v"))
	require.NoError(t, err)
	_, err = g.Propose(cmd, 5*time.Second)
	require.ErrorContains(t, err, "disk gone")
	require.Equal(t, Shutdown, g.State())
}

func TestEtcdsLogIsTaus(t *testing.T) {
	l := etcdLogger{}
	l.Debug("d")
	l.Debugf("%s", "d")
	l.Info("i")
	l.Infof("%s", "i")
	l.Warning("w")
	l.Warningf("%s", "w")
	l.Error("e")
	l.Errorf("%s", "e")
	require.Panics(t, func() { l.Fatal("f") })
	require.Panics(t, func() { l.Fatalf("%s", "f") })
	require.Panics(t, func() { l.Panic("p") })
	require.Panics(t, func() { l.Panicf("%s", "p") })
}

func TestALoneLeaderHasNobodyToHandLeadershipTo(t *testing.T) {
	node := taupeer.Mock(t.Context())
	t.Cleanup(node.Close)
	g := onEngine(t, node, "lone", quickTimeouts)
	require.NoError(t, g.Bootstrap([]peer.ID{node.ID()}))
	leading(t, g)
	require.ErrorIs(t, g.TransferLeadership(""), ErrNoLeader)
}

// A WRITE THAT CANNOT COMMIT IN ITS TIME says so: its leader lost the quorum it
// needs, and either still leads in the term it was asked or has stepped down.
func TestAWriteThatCannotCommitInTimeSaysWhy(t *testing.T) {
	nodes := linked(t, 3)
	ids := []peer.ID{nodes[0].ID(), nodes[1].ID(), nodes[2].ID()}
	groups := []*group{
		onEngine(t, nodes[0], "quorum", quickTimeouts),
		onEngine(t, nodes[1], "quorum", quickTimeouts),
		onEngine(t, nodes[2], "quorum", quickTimeouts),
	}
	require.NoError(t, groups[0].Bootstrap(ids))
	leader := leading(t, groups...)
	for _, g := range groups {
		if g != leader {
			require.NoError(t, g.Close())
		}
	}
	cmd, err := encodeSetCommand("k", []byte("v"))
	require.NoError(t, err)
	_, err = leader.Propose(cmd, 20*time.Millisecond)
	if err != nil && !errors.Is(err, ErrNotLeader) {
		require.True(t, errors.Is(err, ErrApplyTimeout) || errors.Is(err, ErrLeadershipLost), "got %v", err)
	}
}
