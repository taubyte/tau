package raft

import (
	"context"
	"errors"
	"path"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	taupeer "github.com/taubyte/tau/p2p/peer"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
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
	fsm := newKVFSM(t.Context(), node.Store(), path.Join(RaftStoragePrefix, namespace))
	g, err := e.open(namespace, groupConfig{timeouts: timeouts, machine: fsm, store: node.Store()})
	require.NoError(t, err)
	t.Cleanup(func() { g.Close() })
	return g
}

// bound binds groups to founder's formation, as members that asked it for an
// offer do.
func bound(t *testing.T, founder *group, groups ...*group) {
	t.Helper()
	for _, g := range groups {
		if g != founder {
			require.NoError(t, g.Bind(founder.Formation()))
		}
	}
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
	v, _ := g.fsm.Get(key)
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
	fsm := newKVFSM(t.Context(), node.Store(), path.Join(RaftStoragePrefix, "one"))
	g, err := e.open("one", groupConfig{timeouts: quickTimeouts, machine: fsm, store: node.Store()})
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

	_, err = e.open("one", groupConfig{timeouts: quickTimeouts, machine: fsm, store: node.Store()})
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
	bound(t, groups[0], groups...)
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
	bound(t, leader, fourth)
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
	require.Eventually(t, func() bool {
		compacted, err := first.log.FirstIndex()
		return err == nil && compacted > 2
	}, 5*time.Second, 5*time.Millisecond, "the leader compacted its log")

	second := onEngine(t, nodes[1], "snap", compacting)
	bound(t, first, second)
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

// A SECOND GROUP UNDER THE SAME NAME shows itself as a message of another
// formation, which raft never sees: what the healer watches for.
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

// A GROUP WHOSE STATE CANNOT BE WRITTEN STALLS, rather than run on what it did not
// keep, and comes back once the store takes its writes again.
func TestAGroupThatCannotWriteStallsUntilItCan(t *testing.T) {
	mock := taupeer.Mock(t.Context())
	t.Cleanup(mock.Close)
	node := failingStore{Node: mock, broken: &atomic.Bool{}}
	g := onEngine(t, node, "broken", quickTimeouts)
	require.NoError(t, g.Bootstrap([]peer.ID{node.ID()}))
	leading(t, g)

	node.broken.Store(true)
	cmd, err := encodeSetCommand("k", []byte("v"))
	require.NoError(t, err)
	_, err = g.Propose(cmd, 300*time.Millisecond)
	require.ErrorIs(t, err, ErrApplyTimeout, "nothing applies while its state cannot be written")

	node.broken.Store(false)
	require.Eventually(t, func() bool { return held(g, "k") == "v" }, 5*time.Second, 5*time.Millisecond,
		"the write it held applied once the store came back")
	set(t, g, "after", "v")
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
	bound(t, groups[0], groups...)
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

// A FOLLOWER WHOSE ENGINE STOPPED AND STARTED AGAIN hears its leader again: the
// streams the old engine read are reset, so the leader dials the new one rather
// than writing on into an engine that is gone.
func TestAFollowerWhoseEngineRestartsHearsItsLeaderAgain(t *testing.T) {
	nodes := linked(t, 3)
	ids := []peer.ID{nodes[0].ID(), nodes[1].ID(), nodes[2].ID()}
	open := func(node taupeer.Node) (*engine, *group) {
		e := engineFor(node)
		fsm := newKVFSM(t.Context(), node.Store(), path.Join(RaftStoragePrefix, "restart"))
		g, err := e.open("restart", groupConfig{timeouts: quickTimeouts, machine: fsm, store: node.Store()})
		require.NoError(t, err)
		return e, g
	}
	running := make([]*engine, 3)
	groups := make([]*group, 3)
	for i, node := range nodes {
		running[i], groups[i] = open(node)
	}
	t.Cleanup(func() {
		for i := range groups {
			groups[i].Close()
			running[i].release()
		}
	})
	require.NoError(t, groups[0].Bootstrap(ids))
	bound(t, groups[0], groups...)
	leader := leading(t, groups...)
	set(t, leader, "before", "v")

	f := slices.IndexFunc(groups, func(g *group) bool { return g != leader })
	require.Eventually(t, func() bool { return held(groups[f], "before") == "v" }, 5*time.Second, 5*time.Millisecond)
	require.NoError(t, groups[f].Close())
	running[f].release()
	engines.Lock()
	_, stillRunning := engines.on[nodes[f].ID()]
	engines.Unlock()
	require.False(t, stillRunning, "its engine stopped with its last group")

	set(t, leader, "while", "v")
	running[f], groups[f] = open(nodes[f])
	require.NotEqual(t, leader, groups[f])
	require.Eventually(t, func() bool { return held(groups[f], "while") == "v" }, 10*time.Second, 5*time.Millisecond,
		"the restarted follower never heard from its leader")
	_, known := groups[f].Leader()
	require.True(t, known)
}

// A GROUP KEEPS ITS LOG IN ITS OWN NODE'S STORE, even on a host whose engine another
// node value started with a store of its own.
func TestAGroupKeepsItsLogInItsOwnStore(t *testing.T) {
	node := taupeer.Mock(t.Context())
	t.Cleanup(node.Close)
	first := onEngine(t, node, "first", quickTimeouts)
	require.NoError(t, first.Bootstrap([]peer.ID{node.ID()}))
	leading(t, first)

	own := newFaultStore()
	other := storeNode{Node: node, store: own}
	second := onEngine(t, other, "second", quickTimeouts)
	require.NoError(t, second.Bootstrap([]peer.ID{node.ID()}))
	leading(t, second)
	set(t, second, "k", "v")

	key := datastore.NewKey(path.Join(RaftStoragePrefix, "second", "engine", "hardstate"))
	has, err := own.Has(t.Context(), key)
	require.NoError(t, err)
	require.True(t, has, "the group's log is in its own node's store")
	has, err = node.Store().Has(t.Context(), key)
	require.NoError(t, err)
	require.False(t, has, "and not in the store of the node that started the engine")
}

// refusingMachine is the KV machine with snapshots and restores that fail on
// demand.
type refusingMachine struct {
	FSM
	snapshots, restores atomic.Bool
}

var errMachine = errors.New("machine refused")

func (m *refusingMachine) Snapshot() ([]byte, error) {
	if m.snapshots.Load() {
		return nil, errMachine
	}
	return m.FSM.Snapshot()
}

func (m *refusingMachine) Restore(data []byte) error {
	if m.restores.Load() {
		return errMachine
	}
	return m.FSM.Restore(data)
}

// A MACHINE THAT CANNOT BE SNAPSHOTTED OR RESTORED says so: a snapshot asked for is
// refused by name, one the threshold calls for is put off with the group still
// writing, and a group whose kept snapshot cannot be restored is not opened.
func TestAMachineThatCannotSnapshotOrRestoreSaysSo(t *testing.T) {
	node := taupeer.Mock(t.Context())
	t.Cleanup(node.Close)
	often := TimeoutConfig{HeartbeatTimeout: 50 * time.Millisecond, SnapshotThreshold: 2, TrailingLogs: 1}
	open := func(m FSM) (*group, error) {
		e := engineFor(node)
		t.Cleanup(e.release)
		return e.open("machine", groupConfig{timeouts: often, machine: m, store: node.Store()})
	}

	m := &refusingMachine{FSM: newKVFSM(t.Context(), node.Store(), path.Join(RaftStoragePrefix, "machine"))}
	g, err := open(m)
	require.NoError(t, err)
	require.NoError(t, g.Bootstrap([]peer.ID{node.ID()}))
	leading(t, g)
	set(t, g, "a", "1")

	m.snapshots.Store(true)
	set(t, g, "b", "2")
	set(t, g, "c", "3")
	require.ErrorIs(t, g.Snapshot(), errMachine)
	set(t, g, "d", "4")

	m.snapshots.Store(false)
	require.NoError(t, g.Snapshot())
	require.NoError(t, g.Close())
	require.ErrorIs(t, g.Snapshot(), ErrShutdown)

	m.restores.Store(true)
	_, err = open(m)
	require.ErrorIs(t, err, errMachine)
	m.restores.Store(false)
	again, err := open(m)
	require.NoError(t, err)
	t.Cleanup(func() { again.Close() })
	require.Equal(t, "4", held(again, "d"))
}

// A SNAPSHOT THAT DOES NOT READ refuses the group rather than start it on a machine
// it could not restore.
func TestAGroupWhoseSnapshotDoesNotReadIsNotOpened(t *testing.T) {
	node := taupeer.Mock(t.Context())
	t.Cleanup(node.Close)
	raw, err := proto.Marshal(&pb.Snapshot{
		Data:     []byte{0xff},
		Metadata: &pb.SnapshotMetadata{Index: proto.Uint64(3), Term: proto.Uint64(1), ConfState: &pb.ConfState{Voters: []uint64{raftID(node.ID())}}},
	})
	require.NoError(t, err)
	require.NoError(t, node.Store().Put(t.Context(), datastore.NewKey(path.Join(RaftStoragePrefix, "garbled", "engine", "snapshot")), raw))

	e := engineFor(node)
	t.Cleanup(e.release)
	fsm := newKVFSM(t.Context(), node.Store(), path.Join(RaftStoragePrefix, "garbled"))
	_, err = e.open("garbled", groupConfig{timeouts: quickTimeouts, machine: fsm, store: node.Store()})
	require.ErrorContains(t, err, "reading its snapshot")
}

// heartbeat is a frame holding one heartbeat at term to g's member, from a member
// of formation.
func heartbeat(t *testing.T, g *group, from peer.ID, term uint64, formation []byte) *raftv2.Frame {
	t.Helper()
	raw, err := proto.Marshal(&pb.Message{
		Type: pb.MessageType_MsgHeartbeat.Enum(),
		To:   proto.Uint64(g.e.self),
		From: proto.Uint64(raftID(from)),
		Term: proto.Uint64(term),
	})
	require.NoError(t, err)
	return &raftv2.Frame{Envelopes: []*raftv2.Envelope{{Group: g.namespace, Message: raw, Formation: formation}}}
}

// RAFT NEVER SEES A MESSAGE OF ANOTHER FORMATION, nor any before its group is bound
// to one: two formations can hold different entries at the same term and index,
// which raft cannot tell apart. A heartbeat at a later term would otherwise move
// the group to it.
func TestAGroupTakesNoMessageOfAFormationItIsNotBoundTo(t *testing.T) {
	nodes := linked(t, 2)
	g := onEngine(t, nodes[0], "fenced", quickTimeouts)
	watch := &voteWatch{}
	g.Observe(watch)
	ours, theirs := newFormation(), newFormation()

	g.e.received(nodes[1].ID(), heartbeat(t, g, nodes[1].ID(), 50, ours))
	require.Never(t, func() bool { return g.Term() >= 50 }, 200*time.Millisecond, 5*time.Millisecond,
		"a group bound to no formation took a message")

	require.NoError(t, g.Bind(ours))
	g.e.received(nodes[1].ID(), heartbeat(t, g, nodes[1].ID(), 60, theirs))
	require.Never(t, func() bool { return g.Term() >= 60 }, 200*time.Millisecond, 5*time.Millisecond,
		"a group took a message of another formation")
	require.Eventually(t, func() bool { f, _ := watch.heard(); return f > 0 }, time.Second, 5*time.Millisecond,
		"the other formation was not reported")

	g.e.received(nodes[1].ID(), heartbeat(t, g, nodes[1].ID(), 70, ours))
	require.Eventually(t, func() bool { return g.Term() == 70 }, time.Second, 5*time.Millisecond,
		"a group did not take a message of its own formation")
}

// A BINDING IS KEPT: a group reopened is bound where it was, refuses another
// formation, and bootstraps none of its own.
func TestAGroupKeepsItsFormation(t *testing.T) {
	node := taupeer.Mock(t.Context())
	t.Cleanup(node.Close)
	ours := newFormation()

	first := onEngine(t, node, "kept", quickTimeouts)
	require.Nil(t, first.Formation())
	require.True(t, first.Pristine())
	require.Error(t, first.Bind(ours[:4]), "a formation of the wrong size was taken")
	require.NoError(t, first.Bind(ours))
	require.NoError(t, first.Bind(ours), "binding again where it is bound is refused")
	require.NoError(t, first.Close())

	again := onEngine(t, node, "kept", quickTimeouts)
	require.Equal(t, ours, again.Formation())
	require.ErrorIs(t, again.Bind(newFormation()), ErrBoundElsewhere)
	require.ErrorIs(t, again.Bootstrap([]peer.ID{node.ID()}), ErrCantBootstrap)
	require.True(t, again.Pristine(), "a group bound to a formation it holds nothing of is not pristine")
}

// losingReads is a wire that loses the first frame holding a read sent to a
// leader, as one the link could not deliver.
type losingReads struct {
	transport
	lost atomic.Bool
}

func (w *losingReads) send(to peer.ID, frame *raftv2.Frame) {
	for _, env := range frame.GetEnvelopes() {
		msg := &pb.Message{}
		if proto.Unmarshal(env.GetMessage(), msg) == nil && msg.GetType() == pb.MessageType_MsgReadIndex && w.lost.CompareAndSwap(false, true) {
			return
		}
	}
	w.transport.send(to, frame)
}

// rewire has the loop send through wire, which sends through the one it had.
type rewire struct {
	wire *losingReads
	done chan struct{}
}

func (a rewire) run(e *engine) {
	a.wire.transport = e.wire
	e.wire = a.wire
	close(a.done)
}

// A FOLLOWER'S READ WHOSE FRAME WAS LOST IS ASKED AGAIN: raft sends it once, and a
// barrier would otherwise wait out its time for an answer nobody will send.
func TestAFollowersReadThatWasLostIsAskedAgain(t *testing.T) {
	nodes := linked(t, 2)
	ids := []peer.ID{nodes[0].ID(), nodes[1].ID()}
	groups := []*group{onEngine(t, nodes[0], "lost-read", quickTimeouts), onEngine(t, nodes[1], "lost-read", quickTimeouts)}
	require.NoError(t, groups[0].Bootstrap(ids))
	bound(t, groups[0], groups...)
	leader := leading(t, groups...)
	set(t, leader, "k", "v")
	follower := groups[0]
	if follower == leader {
		follower = groups[1]
	}

	wire := &losingReads{}
	a := rewire{wire: wire, done: make(chan struct{})}
	require.NoError(t, follower.e.do(a))
	<-a.done

	require.NoError(t, follower.Barrier(2*time.Second))
	require.True(t, wire.lost.Load(), "no read was lost, so nothing was proven")
}
