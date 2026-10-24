package raft

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	ds "github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2/raftv2connect"
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
	node1 := newMockNode(t)
	node2 := newMockNode(t)

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
	node := newMockNode(t)
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
	node := newMockNode(t)
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

// splitPair is two nodes that each bootstrapped a group of their own before
// they could see each other, then were connected: a split brain, each side
// holding a write the other never saw. The bootstrap timeout leaves a yielding
// node's discovery time to find the other side.
func splitPair(t *testing.T, namespace string) (a, b Cluster) {
	t.Helper()
	nodeA := newMockNode(t)
	nodeB := newMockNode(t)

	opts := func() []Option {
		return []Option{WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(time.Second), WithSnapshotRoot(t.TempDir())}
	}
	a, err := New(nodeA, namespace, opts()...)
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })
	b, err = New(nodeB, namespace, opts()...)
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })

	waitLeader(t, a, 5*time.Second)
	waitLeader(t, b, 5*time.Second)
	require.NoError(t, a.Set("from-a", []byte("a"), time.Second))
	require.NoError(t, b.Set("from-b", []byte("b"), time.Second))

	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, nodeB.Peer().Connect(t.Context(), peer.AddrInfo{ID: nodeA.ID(), Addrs: nodeA.Peer().Addrs()}))
	a.(*cluster).tracker.addPeer(nodeB.ID())
	b.(*cluster).tracker.addPeer(nodeA.ID())
	return a, b
}

// winnerOf is the side of a split that negotiateWinner keeps.
func winnerOf(a, b Cluster) (winner, loser *cluster) {
	ca, cb := a.(*cluster), b.(*cluster)
	if negotiateWinner(ca.healer.localClusterInfo(), cb.healer.localClusterInfo()) == ca.node.ID().String() {
		return ca, cb
	}
	return cb, ca
}

// holdsEverything reports whether cl holds both sides' writes.
func holdsEverything(cl Cluster) bool {
	va, okA := cl.Get("from-a")
	vb, okB := cl.Get("from-b")
	return okA && okB && string(va) == "a" && string(vb) == "b"
}

// TestHealing_ALoserRejoinsTheGroupItYieldedTo: the winner of a split merges
// the loser's writes and tells it to yield, and the loser wipes its state and
// joins the winner's group.
func TestHealing_ALoserRejoinsTheGroupItYieldedTo(t *testing.T) {
	a, b := splitPair(t, "heal-rejoin")
	winner, loser := winnerOf(a, b)

	winner.healer.executeMerge(t.Context(), loser.node.ID())

	require.Eventually(t, func() bool {
		members, err := winner.Members()
		return err == nil && len(members) == 2
	}, 20*time.Second, 50*time.Millisecond, "the loser never joined the winner's group")
	require.Eventually(t, func() bool {
		return holdsEverything(a) && holdsEverything(b)
	}, 10*time.Second, 50*time.Millisecond, "the healed group does not hold both sides' writes")
	leaderOf(t, a, b)
}

// TestHealing_AYieldWaitsForTheRaftItReplaces: a yield shut its raft down
// without waiting for it, so an apply still in flight landed after the wipe,
// in state the yielding node had just thrown away.
func TestHealing_AYieldWaitsForTheRaftItReplaces(t *testing.T) {
	node := newMockNode(t)
	namespace := "yield-waits"
	held := newHeldStore(node.Store(), ds.NewKey(path.Join(RaftStoragePrefix, namespace, "data", "held")))
	defer held.letGo()

	c, err := New(storeNode{Node: node, store: held}, namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	defer c.Close()
	waitLeader(t, c, 5*time.Second)

	setDone := make(chan error, 1)
	go func() { setDone <- c.Set("held", []byte("v"), 5*time.Second) }()
	<-held.entered

	yielded := make(chan struct{})
	go func() {
		c.(*cluster).healer.yieldAndRejoin(t.Context(), "")
		close(yielded)
	}()
	select {
	case <-yielded:
		t.Fatal("the yield went on while the raft it replaces was still applying")
	case <-time.After(200 * time.Millisecond):
	}

	held.letGo()
	<-yielded
	<-setDone
	_, found := c.Get("held")
	assert.False(t, found, "an apply of the replaced raft outlived the wipe")
}

// heldWipe holds the first delete under prefix until it is let go, so a test
// can stop a yield inside its wipe.
type heldWipe struct {
	ds.Batching
	prefix  string
	entered chan struct{}
	release chan struct{}
	in, out sync.Once
}

func (s *heldWipe) Delete(ctx context.Context, key ds.Key) error {
	if strings.HasPrefix(key.String(), s.prefix) {
		s.in.Do(func() { close(s.entered) })
		<-s.release
	}
	return s.Batching.Delete(ctx, key)
}

func (s *heldWipe) letGo() { s.out.Do(func() { close(s.release) }) }

// TestCluster_CloseWaitsForAYieldInProgress: Close did not wait for a yield,
// which rebuilt a raft after Close returned, one nothing would ever shut down
// and which ran past the close of the store under it.
func TestCluster_CloseWaitsForAYieldInProgress(t *testing.T) {
	node := newMockNode(t)
	namespace := "close-during-yield"
	wipe := &heldWipe{
		Batching: node.Store(),
		prefix:   path.Join(RaftStoragePrefix, namespace) + "/",
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	defer wipe.letGo()

	c, err := New(storeNode{Node: node, store: wipe}, namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	defer c.Close()
	waitLeader(t, c, 5*time.Second)
	require.NoError(t, c.Set("k", []byte("v"), time.Second))

	cl := c.(*cluster)
	yielded := make(chan struct{})
	go func() {
		cl.healer.yieldAndRejoin(t.Context(), "")
		close(yielded)
	}()
	<-wipe.entered

	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned while a yield was rebuilding the raft")
	case <-time.After(200 * time.Millisecond):
	}

	wipe.letGo()
	require.NoError(t, <-closed)
	<-yielded
	assert.Equal(t, raft.Shutdown, cl.raft.State(), "a raft the yield built is still running after Close")
}

// probeEvery runs the healers a test starts at interval, for the test.
func probeEvery(t *testing.T, interval time.Duration) {
	before := HealingProbeInterval
	HealingProbeInterval = interval
	t.Cleanup(func() { HealingProbeInterval = before })
}

// TestHealing_ASplitHealsByItself: each side's healer finds the other, the
// side negotiateWinner keeps merges the other's writes, and the other yields
// and joins it, with nothing but the healers driving it.
func TestHealing_ASplitHealsByItself(t *testing.T) {
	probeEvery(t, 50*time.Millisecond)
	a, b := splitPair(t, "heal-by-itself")

	require.Eventually(t, func() bool {
		ofA, errA := a.Members()
		ofB, errB := b.Members()
		return errA == nil && errB == nil && len(ofA) == 2 && len(ofB) == 2
	}, 20*time.Second, 50*time.Millisecond, "the split never healed")
	require.Eventually(t, func() bool {
		return holdsEverything(a) && holdsEverything(b)
	}, 10*time.Second, 50*time.Millisecond, "the healed group does not hold both sides' writes")
	leaderOf(t, a, b)
}

// formedPair is a group of two that formed the way a fleet does, with no
// member forced to bootstrap: a bootstrapped alone, then b found it and joined.
func formedPair(t *testing.T, namespace string) (a, b Cluster) {
	t.Helper()
	nodeA := newMockNode(t)
	nodeB := newMockNode(t)

	a, err := New(nodeA, namespace,
		WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(time.Second), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })
	waitLeader(t, a, 5*time.Second)

	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, nodeB.Peer().Connect(t.Context(), peer.AddrInfo{ID: nodeA.ID(), Addrs: nodeA.Peer().Addrs()}))
	b, err = New(nodeB, namespace,
		WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(2*time.Second), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })
	require.Eventually(t, func() bool {
		members, err := a.Members()
		return err == nil && len(members) == 2
	}, 10*time.Second, 20*time.Millisecond, "b never joined a")
	return a, b
}

// TestHealing_AnOrphanIsAbsorbed: a member removed from its group keeps
// running with no leader, and the group's healer finds it and has it wipe its
// state and join again.
func TestHealing_AnOrphanIsAbsorbed(t *testing.T) {
	probeEvery(t, 50*time.Millisecond)
	a, b := formedPair(t, "heal-orphan")

	removed, left := removeTheLeader(t, a, b)
	left.(*cluster).tracker.addPeer(idOf(removed))
	require.NoError(t, onLeader(t, []Cluster{left}, func(leader Cluster) error {
		return leader.Set("after-removal", []byte("v"), time.Second)
	}))

	require.Eventually(t, func() bool {
		members, err := left.Members()
		return err == nil && len(members) == 2
	}, 20*time.Second, 50*time.Millisecond, "the orphan was never absorbed")
	require.Eventually(t, func() bool {
		value, found := removed.Get("after-removal")
		return found && string(value) == "v"
	}, 10*time.Second, 50*time.Millisecond, "the absorbed member does not hold the group's writes")
	leaderOf(t, a, b)
}

// TestHealing_AGroupWithoutQuorumYieldsToTheGroupAheadOfIt: a member left
// alone by its group's other member has no leader, and once it can see a
// group of the same size that has committed more, its healer yields and the
// member joins that group.
func TestHealing_AGroupWithoutQuorumYieldsToTheGroupAheadOfIt(t *testing.T) {
	probeEvery(t, 50*time.Millisecond)
	before := SplitBrainDetectionCycles
	SplitBrainDetectionCycles = 1
	t.Cleanup(func() { SplitBrainDetectionCycles = before })

	namespace := "heal-quorum-lost"
	stranded, gone := formedPair(t, namespace)
	ahead, other := formedPair(t, namespace)
	group := []Cluster{ahead, other}
	for i := range 10 {
		require.NoError(t, onLeader(t, group, func(leader Cluster) error {
			return leader.Set(fmt.Sprintf("ahead-%d", i), []byte("v"), time.Second)
		}))
	}

	require.NoError(t, gone.Close())
	require.NoError(t, nodeOfCluster(stranded).Peer().Connect(t.Context(),
		peer.AddrInfo{ID: idOf(ahead), Addrs: nodeOfCluster(ahead).Peer().Addrs()}))
	stranded.(*cluster).tracker.addPeer(idOf(ahead))

	require.Eventually(t, func() bool {
		members, err := ahead.Members()
		if err != nil {
			return false
		}
		for _, m := range members {
			if m.ID == idOf(stranded) {
				return true
			}
		}
		return false
	}, 20*time.Second, 50*time.Millisecond, "the stranded member never joined the group ahead of it")
	require.Eventually(t, func() bool {
		value, found := stranded.Get("ahead-9")
		return found && string(value) == "v"
	}, 10*time.Second, 50*time.Millisecond, "the stranded member does not hold the group's writes")
}

func nodeOfCluster(cl Cluster) taupeer.Node { return cl.(*cluster).node }

// TestHealing_AVoteFromANonMemberIsReported: the healer sees every vote and
// pre-vote raft receives; one from a peer outside the configuration is
// reported, and one from a member, from nobody or from a name that is not a
// peer is not.
func TestHealing_AVoteFromANonMemberIsReported(t *testing.T) {
	node := newMockNode(t)
	c, err := New(node, "foreign-votes", WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	defer c.Close()
	waitLeader(t, c, 5*time.Second)

	// A healer of its own, so nothing else drains what it reports.
	h := newHealer(c.(*cluster))
	stranger := newMockNode(t).ID()
	vote := func(from string) raft.Observation {
		return raft.Observation{Data: raft.RequestVoteRequest{RPCHeader: raft.RPCHeader{ID: []byte(from)}}}
	}
	preVote := func(from string) raft.Observation {
		return raft.Observation{Data: raft.RequestPreVoteRequest{RPCHeader: raft.RPCHeader{ID: []byte(from)}}}
	}

	for _, ignored := range []raft.Observation{
		vote(node.ID().String()),
		preVote(""),
		vote("not-a-peer"),
		{Data: raft.LeaderObservation{}},
	} {
		h.processVoteObservation(ignored)
	}
	select {
	case pid := <-h.foreignVoteCh:
		t.Fatalf("reported %s, which is not a foreign voter", pid)
	default:
	}

	h.processVoteObservation(preVote(stranger.String()))
	h.processVoteObservation(vote(stranger.String()))
	for range 2 {
		select {
		case pid := <-h.foreignVoteCh:
			assert.Equal(t, stranger, pid)
		default:
			t.Fatal("a vote from a peer outside the configuration was not reported")
		}
	}

	// Reporting never blocks raft: a full channel drops the report.
	for range cap(h.foreignVoteCh) + 1 {
		h.notifyForeignVote(stranger)
	}
	assert.Len(t, h.foreignVoteCh, cap(h.foreignVoteCh))
}

// TestHealing_AForeignVoterIsTrackedAndAbsorbedByTheLeader: a follower only
// remembers a foreign voter, so that its own probes can find it; a leader
// takes it in at once.
func TestHealing_AForeignVoterIsTrackedAndAbsorbedByTheLeader(t *testing.T) {
	a, b := formedPair(t, "foreign-voter")
	stranger := newMockNode(t).ID()

	leader := leaderOf(t, a, b)
	follower := a
	if leader == a {
		follower = b
	}

	follower.(*cluster).healer.handleForeignVote(t.Context(), stranger)
	assert.Contains(t, follower.(*cluster).tracker.allPeers(), stranger)

	// The stranger answers nothing, so the leader's absorb gives up after
	// trying, and the leader still leads a group without it.
	leader.(*cluster).healer.handleForeignVote(t.Context(), stranger)
	assert.Contains(t, leader.(*cluster).tracker.allPeers(), stranger)
	members, err := leader.Members()
	require.NoError(t, err)
	assert.Len(t, members, 2)

	require.NoError(t, a.Close())
	a.(*cluster).healer.handleForeignVote(t.Context(), stranger)
}

// TestHealing_AMergeNeedsTheLeadAndTheOtherSide: a member that does not lead
// merges nothing, and neither does a leader that cannot read the other side;
// either leaves its group as it was.
func TestHealing_AMergeNeedsTheLeadAndTheOtherSide(t *testing.T) {
	a, b := formedPair(t, "merge-preconditions")
	stranger := newMockNode(t).ID()
	require.NoError(t, onLeader(t, []Cluster{a, b}, func(leader Cluster) error {
		return leader.Set("k", []byte("v"), time.Second)
	}))
	require.Eventually(t, func() bool {
		_, onA := a.Get("k")
		_, onB := b.Get("k")
		return onA && onB
	}, 5*time.Second, 20*time.Millisecond, "the write never reached both members")

	leader := leaderOf(t, a, b)
	follower := a
	if leader == a {
		follower = b
	}
	follower.(*cluster).healer.executeMerge(t.Context(), idOf(leader))
	leader.(*cluster).healer.executeMerge(t.Context(), stranger)

	for _, cl := range []Cluster{a, b} {
		members, err := cl.Members()
		require.NoError(t, err)
		assert.Len(t, members, 2)
		assert.Equal(t, []string{"k"}, cl.Keys(""))
	}
}

// exportingPeer stands for the other side of a split: it hands over one write
// of its own and counts the healAcks it is sent, each of which would have it
// wipe that write.
type exportingPeer struct {
	raftv2connect.UnimplementedRaftServiceHandler
	state    []byte
	healAcks atomic.Int32
}

func (p *exportingPeer) ExportFSM(context.Context, *connect.Request[raftv2.ExportFSMRequest]) (*connect.Response[raftv2.ExportFSMResponse], error) {
	return connect.NewResponse(&raftv2.ExportFSMResponse{FsmState: p.state, Clock: 1}), nil
}

func (p *exportingPeer) HealAck(context.Context, *connect.Request[raftv2.HealAckRequest]) (*connect.Response[raftv2.HealAckResponse], error) {
	p.healAcks.Add(1)
	return connect.NewResponse(&raftv2.HealAckResponse{}), nil
}

// TestHealing_AMergeTheStoreRefusesTellsNobodyToYield: Apply reports what the
// state machine refused in its response and not in its error, so a merge the
// winner's store refused was still followed by the healAck that has the other
// side wipe the only copy of what it held, whether it was the loser of a split
// or an orphan.
func TestHealing_AMergeTheStoreRefusesTellsNobodyToYield(t *testing.T) {
	const namespace = "merge-refused"
	store := newFaultStore()
	winnerNode := newMockNode(t)
	w, err := New(storeNode{Node: winnerNode, store: store}, namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	defer w.Close()
	waitLeader(t, w, 5*time.Second)

	state, err := cbor.Marshal(map[string]CRDTEntry{
		"theirs": {Value: []byte("v"), Timestamp: 1, WallClock: time.Now().UnixNano()},
	})
	require.NoError(t, err)
	other := &exportingPeer{state: state}
	otherNode := newMockNode(t)
	route, handler := raftv2connect.NewRaftServiceHandler(other)
	mux := http.NewServeMux()
	mux.Handle(route, handler)
	srv, err := rpc.Listen(t.Context(), otherNode.Peer(), protocol.ID(Protocol(namespace)), mux)
	require.NoError(t, err)
	defer srv.Close()

	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, winnerNode.Peer().Connect(t.Context(), peer.AddrInfo{ID: otherNode.ID(), Addrs: otherNode.Peer().Addrs()}))
	winner := w.(*cluster)
	winner.tracker.addPeer(otherNode.ID())
	require.Equal(t, []peer.ID{otherNode.ID()}, winner.healer.findForeignPeers())
	store.failPut(path.Join(RaftStoragePrefix, namespace, "data", "theirs"), errNoSpace)

	winner.healer.executeMerge(t.Context(), otherNode.ID())
	assert.Zero(t, other.healAcks.Load(), "the loser was told to yield a write the merge never landed")

	winner.healer.absorbOrphan(t.Context(), otherNode.ID())
	assert.Zero(t, other.healAcks.Load(), "the orphan was told to yield a write the merge never landed")

	_, found, err := w.TryGet("theirs")
	require.NoError(t, err)
	assert.False(t, found)
}
