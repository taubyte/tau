package raft

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2/raftv2connect"
)

// covering is a receipt for everything m's formation holds, which a test hands a
// yield it means to see made.
func covering(m *member) receipt {
	return receipt{formation: m.raft.Formation(), cut: math.MaxUint64}
}

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
	cl1.current().tracker.addPeer(node2.ID())
	cl2.current().tracker.addPeer(node1.ID())

	info1 := cl1.current().healer.Load().localClusterInfo()
	info2 := cl2.current().healer.Load().localClusterInfo()
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

	winner.current().healer.Load().executeMerge(t.Context(), loser.ID())

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
	ourState, err := cl.current().fsm.ExportState()
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
		healAckCh: make(chan healAck, 1),
		foreignCh: make(chan peer.ID, 16),
	}

	h.signalHealAck(node.ID(), receipt{})

	select {
	case ack := <-h.healAckCh:
		assert.Equal(t, node.ID(), ack.from)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("healAck signal was not received")
	}

	h.signalHealAck(node.ID(), receipt{})
	h.signalHealAck(node.ID(), receipt{})

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
		return []Option{WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(time.Second)}
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
	require.NoError(t, nodeB.Host().Connect(t.Context(), peer.AddrInfo{ID: nodeA.ID(), Addrs: nodeA.Host().Addrs()}))
	a.(*cluster).current().tracker.addPeer(nodeB.ID())
	b.(*cluster).current().tracker.addPeer(nodeA.ID())
	return a, b
}

func winnerOf(a, b Cluster) (winner, loser *cluster) {
	ca, cb := a.(*cluster), b.(*cluster)
	if negotiateWinner(ca.current().healer.Load().localClusterInfo(), cb.current().healer.Load().localClusterInfo()) == ca.ID().String() {
		return ca, cb
	}
	return cb, ca
}

func holdsEverything(cl Cluster) bool {
	va, okA := cl.Get("from-a")
	vb, okB := cl.Get("from-b")
	return okA && okB && string(va) == "a" && string(vb) == "b"
}

func TestHealing_ALoserRejoinsTheGroupItYieldedTo(t *testing.T) {
	a, b := splitPair(t, "heal-rejoin")
	winner, loser := winnerOf(a, b)

	winner.current().healer.Load().executeMerge(t.Context(), loser.ID())

	require.Eventually(t, func() bool {
		members, err := winner.Members()
		return err == nil && len(members) == 2
	}, 20*time.Second, 50*time.Millisecond, "the loser never joined the winner's group")
	require.Eventually(t, func() bool {
		return holdsEverything(a) && holdsEverything(b)
	}, 10*time.Second, 50*time.Millisecond, "the healed group does not hold both sides' writes")
	leaderOf(t, a, b)
}

func TestHealing_AYieldWaitsForTheRaftItReplaces(t *testing.T) {
	node := newMockNode(t)
	namespace := "yield-waits"
	held := newHeldStore(node.Store(), ds.NewKey(path.Join(RaftStoragePrefix, namespace, "data", "held")))
	defer held.letGo()

	c, err := New(storeNode{Node: node, store: held}, namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	defer c.Close()
	waitLeader(t, c, 5*time.Second)

	setDone := make(chan error, 1)
	go func() { setDone <- c.Set("held", []byte("v"), 5*time.Second) }()
	<-held.entered

	yielded := make(chan struct{})
	go func() {
		c.(*cluster).current().healer.Load().yieldAndRejoin(t.Context(), "", covering(c.(*cluster).current()))
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

func TestHealing_AJoinEndsWithTheMemberAYieldTakesDown(t *testing.T) {
	c, err := New(newMockNode(t), "yield-ends-join",
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	defer c.Close()
	waitLeader(t, c, 5*time.Second)

	cl := c.(*cluster)
	m := cl.current()
	start := time.Now()
	asked := make(chan struct{})
	go func() {
		cl.askToJoin(m, time.Second)
		close(asked)
	}()
	cl.current().healer.Load().yieldAndRejoin(t.Context(), "", covering(cl.current()))
	<-asked
	assert.Less(t, time.Since(start), 10*time.Second, "the ask of the member taken down ran its whole course")
}

func TestHealing_AYieldRebuildsTheMemberItsReadersRead(t *testing.T) {
	const namespace = "yield-under-readers"
	c, err := New(newMockNode(t), namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	defer c.Close()
	waitLeader(t, c, 5*time.Second)

	caller := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())
	connected(t, caller, c)
	cli, err := newInternalClient(caller, namespace, nil, quickRetry)
	require.NoError(t, err)
	defer cli.Close()

	stop := make(chan struct{})
	var readers sync.WaitGroup
	read := func(f func()) {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					f()
				}
			}
		}()
	}
	read(func() {
		c.Members()
		c.Leader()
		c.State()
	})
	read(func() {
		c.Set("k", []byte("v"), 50*time.Millisecond)
		c.Get("k")
		c.Keys("")
		c.Barrier(50 * time.Millisecond)
	})
	read(func() {
		cli.ClusterInfo(idOf(c))
		cli.ExportFSM(idOf(c), 0)
		cli.ExchangePeers(time.Now(), nil, idOf(c))
	})

	for range 3 {
		c.(*cluster).current().healer.Load().yieldAndRejoin(t.Context(), "", covering(c.(*cluster).current()))
		waitLeader(t, c, 5*time.Second)
	}
	close(stop)
	readers.Wait()
}

// holdingPeer serves the group's protocol and holds every exchange of peers it
// is asked for until the asker gives up, closing entered on the first.
type holdingPeer struct {
	raftv2connect.UnimplementedRaftServiceHandler
	entered chan struct{}
	once    sync.Once
}

func (p *holdingPeer) ExchangePeers(ctx context.Context, _ *connect.Request[raftv2.ExchangePeersRequest]) (*connect.Response[raftv2.ExchangePeersResponse], error) {
	p.once.Do(func() { close(p.entered) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestHealing_AnExchangeAYieldCutsShortKeepsToItsMembersClient(t *testing.T) {
	const namespace = "yield-mid-exchange"
	held := &holdingPeer{entered: make(chan struct{})}
	node := newMockNode(t)
	for range 2 {
		other := newMockNode(t)
		route, handler := raftv2connect.NewRaftServiceHandler(held)
		mux := http.NewServeMux()
		mux.Handle(route, handler)
		srv, err := rpc.Listen(t.Context(), other.Host(), protocol.ID(Protocol(namespace)), mux)
		require.NoError(t, err)
		t.Cleanup(func() { srv.Close() })
		require.NoError(t, taupeer.LinkAllPeers())
		require.NoError(t, node.Host().Connect(t.Context(), peer.AddrInfo{ID: other.ID(), Addrs: other.Host().Addrs()}))
	}

	c, err := New(node, namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	defer c.Close()

	// The tracker's first round exchanges with both, and the first holds it.
	<-held.entered
	c.(*cluster).current().healer.Load().yieldAndRejoin(t.Context(), "", covering(c.(*cluster).current()))
}

// heldWipe holds the first delete under prefix until it is let go, so a test
// can stop a yield inside its wipe. A wipe deletes in a batch, so the batch is
// where it is held.
type heldWipe struct {
	ds.Batching
	prefix  string
	entered chan struct{}
	release chan struct{}
	in, out sync.Once
}

func (s *heldWipe) Batch(ctx context.Context) (ds.Batch, error) {
	b, err := s.Batching.Batch(ctx)
	if err != nil {
		return nil, err
	}
	return heldBatch{Batch: b, wipe: s}, nil
}

type heldBatch struct {
	ds.Batch
	wipe *heldWipe
}

func (b heldBatch) Delete(ctx context.Context, key ds.Key) error {
	if strings.HasPrefix(key.String(), b.wipe.prefix) {
		b.wipe.in.Do(func() { close(b.wipe.entered) })
		<-b.wipe.release
	}
	return b.Batch.Delete(ctx, key)
}

func (s *heldWipe) letGo() { s.out.Do(func() { close(s.release) }) }

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
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	defer c.Close()
	waitLeader(t, c, 5*time.Second)
	require.NoError(t, c.Set("k", []byte("v"), time.Second))

	cl := c.(*cluster)
	yielded := make(chan struct{})
	go func() {
		cl.current().healer.Load().yieldAndRejoin(t.Context(), "", covering(cl.current()))
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
	assert.Equal(t, Shutdown, cl.current().raft.State(), "a raft the yield built is still running after Close")
}

func probeEvery(t *testing.T, interval time.Duration) {
	before := HealingProbeInterval
	HealingProbeInterval = interval
	t.Cleanup(func() { HealingProbeInterval = before })
}

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

// TWO GROUPS FORMED APART BECOME ONE AS SOON AS THEY ARE CONNECTED, with nothing
// handing either tracker the other: a member that formed before its connections were
// up hears of the other group from identify, not from its next discovery pass.
func TestHealing_GroupsFormedApartBecomeOneOnceConnected(t *testing.T) {
	nodeA, nodeB := newMockNode(t), newMockNode(t)
	opts := func() []Option {
		return []Option{WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(time.Second)}
	}
	a, err := New(nodeA, "formed-apart", opts()...)
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })
	b, err := New(nodeB, "formed-apart", opts()...)
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })
	waitLeader(t, a, 5*time.Second)
	waitLeader(t, b, 5*time.Second)
	require.NoError(t, a.Set("from-a", []byte("a"), time.Second))
	require.NoError(t, b.Set("from-b", []byte("b"), time.Second))

	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, nodeB.Host().Connect(t.Context(), peer.AddrInfo{ID: nodeA.ID(), Addrs: nodeA.Host().Addrs()}))

	require.Eventually(t, func() bool {
		ofA, errA := a.Members()
		ofB, errB := b.Members()
		return errA == nil && errB == nil && len(ofA) == 2 && len(ofB) == 2
	}, 5*time.Second, 50*time.Millisecond, "connected groups stayed apart past a settled discovery interval would")
	require.Eventually(t, func() bool {
		return holdsEverything(a) && holdsEverything(b)
	}, 5*time.Second, 50*time.Millisecond, "the group they became does not hold both sides' writes")
}

func TestHealing_ASettingChangedAfterNewIsNotReadByItsHealer(t *testing.T) {
	c, err := New(newMockNode(t), "heal-settings",
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	defer c.Close()
	waitLeader(t, c, 5*time.Second)

	probeEvery(t, 50*time.Millisecond)
	before := SplitBrainDetectionCycles
	SplitBrainDetectionCycles = 1
	t.Cleanup(func() { SplitBrainDetectionCycles = before })
}

// formedPair is a group of two that formed the way a fleet does, with no
// member forced to bootstrap: a bootstrapped alone, then b found it and joined.
func formedPair(t *testing.T, namespace string) (a, b Cluster) {
	t.Helper()
	nodeA := newMockNode(t)
	nodeB := newMockNode(t)

	a, err := New(nodeA, namespace,
		WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })
	waitLeader(t, a, 5*time.Second)

	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, nodeB.Host().Connect(t.Context(), peer.AddrInfo{ID: nodeA.ID(), Addrs: nodeA.Host().Addrs()}))
	b, err = New(nodeB, namespace,
		WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(2*time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })
	require.Eventually(t, func() bool {
		members, err := a.Members()
		return err == nil && len(members) == 2
	}, 10*time.Second, 20*time.Millisecond, "b never joined a")
	return a, b
}

// A MEMBER TOLD TO WIPE IS REMOVED FIRST: it rejoins under the same id, and a leader
// still holding its old progress would send it a commit past the log it wiped,
// which etcd's raft answers by panicking.
func TestHealing_AMemberToldToWipeIsRemovedFirstAndRejoins(t *testing.T) {
	a, b := formedPair(t, "heal-member-wipes")
	leader := leaderOf(t, a, b)
	member := b
	if leader == b {
		member = a
	}
	require.Eventually(t, setThrough(leader, "kept"), 5*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool { _, ok := member.Get("kept"); return ok }, 5*time.Second, 20*time.Millisecond)

	leader.(*cluster).current().healer.Load().absorbOrphan(t.Context(), member.(*cluster).ID())

	require.Eventually(t, func() bool {
		ofLeader, err := leader.Members()
		ofMember, errM := member.Members()
		return err == nil && errM == nil && len(ofLeader) == 2 && len(ofMember) == 2
	}, 10*time.Second, 50*time.Millisecond, "the wiped member did not rejoin")
	require.Eventually(t, setThrough(leader, "after"), 5*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		_, kept := member.Get("kept")
		_, after := member.Get("after")
		return kept && after
	}, 10*time.Second, 50*time.Millisecond, "the rejoined member does not hold the group's state")
}

// setThrough is a write of key through c, which leads.
func setThrough(c Cluster, key string) func() bool {
	return func() bool { return c.Set(key, []byte("v"), time.Second) == nil }
}

// The joiner of a formed pair spends its one discovery probe before it serves,
// and identify can leave it unlisted for good, so only a probe from the healer
// finds it.
func TestHealing_AnOrphanThePeerstoreDoesNotListIsAbsorbed(t *testing.T) {
	probeEvery(t, 50*time.Millisecond)
	namespace := "heal-orphan-unlisted"
	a, b := formedPair(t, namespace)

	removed, left := removeTheLeader(t, a, b)
	tracker := left.(*cluster).current().tracker
	tracker.addPeer(idOf(removed))
	tracker.probeDue(idOf(removed))
	require.NoError(t, left.(*cluster).Host().Peerstore().RemoveProtocols(idOf(removed), protocol.ID(Protocol(namespace))))

	require.Eventually(t, func() bool {
		members, err := left.Members()
		return err == nil && len(members) == 2
	}, 20*time.Second, 50*time.Millisecond, "the orphan was never absorbed")
	leaderOf(t, a, b)
}

func TestHealing_AnOrphanIsAbsorbed(t *testing.T) {
	probeEvery(t, 50*time.Millisecond)
	a, b := formedPair(t, "heal-orphan")

	removed, left := removeTheLeader(t, a, b)
	left.(*cluster).current().tracker.addPeer(idOf(removed))
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

// A split needs two leaderless cycles: the group ahead runs 25ms leases, which
// under load leave it leaderless for longer than one 50ms cycle.
func TestHealing_AGroupWithoutQuorumYieldsToTheGroupAheadOfIt(t *testing.T) {
	probeEvery(t, time.Second)
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
	require.NoError(t, nodeOfCluster(stranded).Host().Connect(t.Context(),
		peer.AddrInfo{ID: idOf(ahead), Addrs: nodeOfCluster(ahead).Host().Addrs()}))
	stranded.(*cluster).current().tracker.addPeer(idOf(ahead))

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

func nodeOfCluster(cl Cluster) taupeer.Node { return cl.(*cluster).Node }

func TestHealing_AVoteFromANonMemberIsReported(t *testing.T) {
	node := newMockNode(t)
	c, err := New(node, "foreign-votes", WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	defer c.Close()
	waitLeader(t, c, 5*time.Second)

	// A healer of its own, so nothing else drains what it reports.
	h := newHealer(c.(*cluster), c.(*cluster).current())
	stranger := newMockNode(t).ID()

	h.foreignVote(node.ID())
	select {
	case pid := <-h.foreignCh:
		t.Fatalf("reported %s, which is this node", pid)
	default:
	}

	h.foreignVote(stranger)
	select {
	case pid := <-h.foreignCh:
		assert.Equal(t, stranger, pid)
	default:
		t.Fatal("a vote from a peer outside the configuration was not reported")
	}

	// Reporting never blocks raft: a full channel drops the report.
	for range cap(h.foreignCh) + 1 {
		h.notifyForeign(stranger)
	}
	assert.Len(t, h.foreignCh, cap(h.foreignCh))
}

// A CHANGE OF LEADER the engine reports is a cycle that had one, whoever leads by
// the time the healer looks.
func TestHealing_ALeaderChangeCountsForTheCycleItHappenedIn(t *testing.T) {
	c := &cluster{}
	c.closed.Store(true) // knows no leader
	h := &healer{cluster: c}
	cycleStart := time.Now()
	assert.False(t, h.hadLeaderSince(cycleStart), "a cycle with no leader had one")

	h.leaderChanged("")
	assert.True(t, h.hadLeaderSince(cycleStart), "a change of leader went uncounted")
	assert.False(t, h.hadLeaderSince(time.Now().Add(time.Second)), "a change counted for a later cycle")
}

func TestHealing_AForeignVoterIsTrackedAndAbsorbedByTheLeader(t *testing.T) {
	a, b := formedPair(t, "foreign-voter")
	stranger := newMockNode(t).ID()

	leader := leaderOf(t, a, b)
	follower := a
	if leader == a {
		follower = b
	}

	follower.(*cluster).current().healer.Load().meetForeign(t.Context(), stranger)
	assert.Contains(t, follower.(*cluster).current().tracker.allPeers(), stranger)

	// The stranger answers nothing, so the leader's absorb gives up after
	// trying, and the leader still leads a group without it.
	leader.(*cluster).current().healer.Load().meetForeign(t.Context(), stranger)
	assert.Contains(t, leader.(*cluster).current().tracker.allPeers(), stranger)
	members, err := leader.Members()
	require.NoError(t, err)
	assert.Len(t, members, 2)

	require.NoError(t, a.Close())
	a.(*cluster).current().healer.Load().meetForeign(t.Context(), stranger)
}

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
	follower.(*cluster).current().healer.Load().executeMerge(t.Context(), idOf(leader))
	leader.(*cluster).current().healer.Load().executeMerge(t.Context(), stranger)

	for _, cl := range []Cluster{a, b} {
		members, err := cl.Members()
		require.NoError(t, err)
		assert.Len(t, members, 2)
		assert.Equal(t, []string{"k"}, cl.Keys(""))
	}
}

// exportingPeer stands for the other side of a split: it hands over one write
// of its own, or refuses to when refuse is set, and counts the healAcks it is
// sent, each of which would have it wipe that write.
type exportingPeer struct {
	raftv2connect.UnimplementedRaftServiceHandler
	state    map[string]CRDTEntry
	refuse   error
	healAcks atomic.Int32
	// members is the configuration it says it holds, which is who a merge of it
	// tells to yield.
	members []string
}

func (p *exportingPeer) ClusterInfo(context.Context, *connect.Request[raftv2.ClusterInfoRequest]) (*connect.Response[raftv2.ClusterInfoResponse], error) {
	return connect.NewResponse(&raftv2.ClusterInfoResponse{MemberCount: int64(len(p.members)), Members: p.members}), nil
}

func (p *exportingPeer) ExportFSM(context.Context, *connect.Request[raftv2.ExportFSMRequest]) (*connect.Response[raftv2.ExportFSMResponse], error) {
	if p.refuse != nil {
		return nil, p.refuse
	}
	return connect.NewResponse(&raftv2.ExportFSMResponse{Entries: entriesProto(p.state), Clock: 1}), nil
}

func (p *exportingPeer) HealAck(context.Context, *connect.Request[raftv2.HealAckRequest]) (*connect.Response[raftv2.HealAckResponse], error) {
	p.healAcks.Add(1)
	return connect.NewResponse(&raftv2.HealAckResponse{}), nil
}

// facing is a group of one running on store, connected to and tracking a node
// that answers the group's protocol with other, the way the other side of a
// split does.
func facing(t *testing.T, namespace string, store ds.Batching, other raftv2connect.RaftServiceHandler) (*cluster, peer.ID) {
	t.Helper()
	winnerNode := newMockNode(t)
	w, err := New(storeNode{Node: winnerNode, store: store}, namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { w.Close() })
	waitLeader(t, w, 5*time.Second)

	otherNode := newMockNode(t)
	route, handler := raftv2connect.NewRaftServiceHandler(other)
	mux := http.NewServeMux()
	mux.Handle(route, handler)
	srv, err := rpc.Listen(t.Context(), otherNode.Host(), protocol.ID(Protocol(namespace)), mux)
	require.NoError(t, err)
	t.Cleanup(func() { srv.Close() })

	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, winnerNode.Host().Connect(t.Context(), peer.AddrInfo{ID: otherNode.ID(), Addrs: otherNode.Host().Addrs()}))
	winner := w.(*cluster)
	winner.current().tracker.addPeer(otherNode.ID())
	require.Equal(t, []peer.ID{otherNode.ID()}, winner.current().healer.Load().findForeignPeers(false))
	return winner, otherNode.ID()
}

func oneWriteOfTheirs(t *testing.T) map[string]CRDTEntry {
	t.Helper()
	return map[string]CRDTEntry{
		"theirs": {Value: []byte("v"), Timestamp: 1, WallClock: time.Now().UnixNano()},
	}
}

func TestHealing_AMergeTheStoreRefusesTellsNobodyToYield(t *testing.T) {
	const namespace = "merge-refused"
	store := newFaultStore()
	other := &exportingPeer{state: oneWriteOfTheirs(t)}
	winner, otherID := facing(t, namespace, store, other)
	other.members = []string{otherID.String()}
	theirs := path.Join(RaftStoragePrefix, namespace, "data", "theirs")
	store.failPut(theirs, errNoSpace)

	winner.current().healer.Load().executeMerge(t.Context(), otherID)
	assert.Zero(t, other.healAcks.Load(), "the loser was told to yield a write the merge never landed")

	winner.current().healer.Load().absorbOrphan(t.Context(), otherID)
	assert.Zero(t, other.healAcks.Load(), "the orphan was told to yield a write the merge never landed")

	_, found, err := winner.TryGet("theirs")
	require.NoError(t, err)
	assert.False(t, found)

	// THE SAME MERGE ONCE THE STORE TAKES IT lands and tells the loser to yield:
	// the refusals above were the store's, not a merge that never ran.
	store.failPut(theirs, nil)
	winner.current().healer.Load().executeMerge(t.Context(), otherID)
	assert.Equal(t, int32(1), other.healAcks.Load(), "a merge that landed did not tell its loser to yield")
	_, found, err = winner.TryGet("theirs")
	require.NoError(t, err)
	assert.True(t, found)
}

func TestHealing_AnOrphanWhoseStateIsNotTakenIsNotToldToYield(t *testing.T) {
	t.Run("its export fails", func(t *testing.T) {
		other := &exportingPeer{refuse: connect.NewError(connect.CodeUnavailable, errDisk)}
		winner, orphan := facing(t, "orphan-export-refused", newFaultStore(), other)

		winner.current().healer.Load().absorbOrphan(t.Context(), orphan)
		assert.Zero(t, other.healAcks.Load(), "an orphan whose state was never had was told to yield")
	})

	t.Run("the leader cannot read its own state", func(t *testing.T) {
		const namespace = "orphan-own-state-unreadable"
		store := newFaultStore()
		other := &exportingPeer{state: oneWriteOfTheirs(t)}
		winner, orphan := facing(t, namespace, store, other)
		store.failScan(path.Join(RaftStoragePrefix, namespace, "data"), errDisk)

		winner.current().healer.Load().absorbOrphan(t.Context(), orphan)
		assert.Zero(t, other.healAcks.Load(), "an orphan whose state was never merged was told to yield")
	})
}

// The default probe interval keeps the orphan's own healer quiet.
func TestHealing_AnOrphansWritesAreMergedBeforeItYields(t *testing.T) {
	const namespace = "orphan-writes"
	a, b := formedPair(t, namespace)
	require.NoError(t, onLeader(t, []Cluster{a, b}, func(leader Cluster) error {
		return leader.Set("held", []byte("v"), time.Second)
	}))
	require.Eventually(t, func() bool {
		_, onA := a.Get("held")
		_, onB := b.Get("held")
		return onA && onB
	}, 5*time.Second, 20*time.Millisecond, "the write never reached both members")

	gone := leaderOf(t, a, b)
	orphan := a
	if gone == a {
		orphan = b
	}
	require.NoError(t, gone.Close())
	require.Eventually(t, func() bool {
		_, err := orphan.Leader()
		return errors.Is(err, ErrNoLeader)
	}, leaderWait, 10*time.Millisecond, "the member left alone kept naming a leader")

	groupNode := newMockNode(t)
	group, err := New(groupNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { group.Close() })
	waitLeader(t, group, 5*time.Second)
	require.NoError(t, taupeer.LinkAllPeers())
	connected(t, groupNode, orphan)

	group.(*cluster).current().healer.Load().absorbOrphan(t.Context(), idOf(orphan))

	value, found, err := group.TryGet("held")
	require.NoError(t, err)
	require.True(t, found, "the orphan was told to yield a write the group never took")
	require.Equal(t, []byte("v"), value)
}

// joiningPeer answers the group's protocol the way a node still joining it
// does: in no configuration and knowing no leader. It counts the exports it is
// asked for and the healAcks it is sent.
type joiningPeer struct {
	raftv2connect.UnimplementedRaftServiceHandler
	exports  atomic.Int32
	healAcks atomic.Int32
}

func (p *joiningPeer) ClusterInfo(context.Context, *connect.Request[raftv2.ClusterInfoRequest]) (*connect.Response[raftv2.ClusterInfoResponse], error) {
	return connect.NewResponse(&raftv2.ClusterInfoResponse{}), nil
}

func (p *joiningPeer) ExportFSM(context.Context, *connect.Request[raftv2.ExportFSMRequest]) (*connect.Response[raftv2.ExportFSMResponse], error) {
	p.exports.Add(1)
	return connect.NewResponse(&raftv2.ExportFSMResponse{}), nil
}

func (p *joiningPeer) HealAck(context.Context, *connect.Request[raftv2.HealAckRequest]) (*connect.Response[raftv2.HealAckResponse], error) {
	p.healAcks.Add(1)
	return connect.NewResponse(&raftv2.HealAckResponse{}), nil
}

func TestHealing_AJoinerIsNotTakenForAnOrphan(t *testing.T) {
	joiner := &joiningPeer{}
	leader, _ := facing(t, "heal-joiner", newFaultStore(), joiner)

	leader.current().healer.Load().probeAndMergeAsLeader(t.Context())
	assert.Zero(t, joiner.healAcks.Load(), "a joiner was told to yield")
	assert.Zero(t, joiner.exports.Load(), "a joiner was absorbed as an orphan")
}

func TestHealing_AHealAckFromTheMembersOwnLeaderIsIgnored(t *testing.T) {
	a, b := formedPair(t, "heal-ack-own-leader")
	require.NoError(t, onLeader(t, []Cluster{a, b}, func(leader Cluster) error {
		return leader.Set("held", []byte("v"), time.Second)
	}))
	leader := leaderOf(t, a, b)
	member := a.(*cluster)
	if leader == a {
		member = b.(*cluster)
	}
	require.Eventually(t, func() bool {
		led, err := member.Leader()
		_, held := member.Get("held")
		return err == nil && led == idOf(leader) && held
	}, leaderWait, 10*time.Millisecond, "the member never followed its leader")
	before, healer := member.current().raft, member.current().healer.Load()

	healer.healAcked(t.Context(), healAck{from: idOf(leader)})
	assert.False(t, healer.superseded.Load(), "the member yielded to its own leader")
	assert.Same(t, before, member.current().raft, "the member's raft was replaced")
	_, held := member.Get("held")
	assert.True(t, held, "the member wiped what its group holds")
}

func TestHealing_AWipeFinishesWhateverCancelsItsContext(t *testing.T) {
	node := newMockNode(t)
	namespace := "heal-wipe-cancelled"
	cl, err := New(node, namespace, healingTestOptions()...)
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)
	require.NoError(t, cl.Set("held", []byte("v"), time.Second))

	c := cl.(*cluster)
	m := c.current()
	m.takeDown()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m.healer.Load().wipeRaftState(ctx)

	results, err := node.Store().Query(t.Context(), query.Query{Prefix: path.Join(RaftStoragePrefix, namespace), KeysOnly: true})
	require.NoError(t, err)
	left, err := results.Rest()
	require.NoError(t, err)
	assert.Empty(t, left, "a wipe under a cancelled context left raft state behind")
}

func TestHealing_ACycleWithALeaderInItIsNotSplit(t *testing.T) {
	a, b := formedPair(t, "heal-cycle-with-leader")
	stranded := a.(*cluster)

	// The cycle begins when the member knows a leader in it: the member the
	// pair formed around can lose its quorum to the one just added, slow to
	// answer, before the pair is handed back.
	var cycleStart time.Time
	require.Eventually(t, func() bool {
		cycleStart = time.Now()
		_, err := stranded.Leader()
		return err == nil
	}, leaderWait, 10*time.Millisecond, "the member never knew a leader")
	require.NoError(t, b.Close())
	require.Eventually(t, func() bool {
		_, err := stranded.Leader()
		return errors.Is(err, ErrNoLeader)
	}, leaderWait, 10*time.Millisecond, "the member left alone kept naming a leader")

	require.Eventually(t, func() bool { return stranded.current().healer.Load().hadLeaderSince(cycleStart) },
		5*time.Second, 10*time.Millisecond, "a cycle the member began with a leader counted as leaderless")
	assert.False(t, stranded.current().healer.Load().hadLeaderSince(time.Now()), "a cycle with no leader in it did not count")
}

// A GROUP WHOSE FOUNDER DIES BEFORE ANYONE ELSE HOLDS ITS CONFIGURATION FORMS
// AGAIN among the rest, with nobody to restart anything. The others joined empty
// and raft lets no member campaign before it applies a configuration, so the
// founder's bootstrap alone leaves them unable to ever elect.
func TestHealing_AGroupWhoseFounderDiesBeforeItReplicatesFormsAgain(t *testing.T) {
	const namespace = "founder-dies"
	probeEvery(t, 50*time.Millisecond)
	nodes := []taupeer.Node{newMockNode(t), newMockNode(t), newMockNode(t)}
	slices.SortFunc(nodes, func(a, b taupeer.Node) int { return strings.Compare(a.ID().String(), b.ID().String()) })
	connectAllOf(t, nodes...)

	opened := make(chan *cluster, len(nodes))
	failed := make(chan error, len(nodes))
	for i, node := range nodes {
		timeouts := testTimeoutConfig()
		if i == 0 {
			// It never campaigns before it is closed, so it replicates nothing.
			timeouts.HeartbeatTimeout = time.Minute
		}
		go func() {
			cl, err := New(node, namespace, WithTimeouts(timeouts), WithBootstrapTimeout(time.Second))
			if err != nil {
				failed <- err
				return
			}
			if node == nodes[0] {
				cl.Close()
			}
			opened <- cl.(*cluster)
		}()
	}
	var rest []Cluster
	for range nodes {
		select {
		case cl := <-opened:
			if cl.Host().ID() == nodes[0].ID() {
				members, err := cl.current().members()
				require.NoError(t, err)
				require.Len(t, members, len(nodes), "the lowest founder did not bootstrap the group")
				continue
			}
			t.Cleanup(func() { cl.Close() })
			rest = append(rest, cl)
		case err := <-failed:
			t.Fatal(err)
		}
	}

	require.Eventually(t, func() bool {
		for _, cl := range rest {
			if members, err := cl.Members(); err != nil || len(members) != len(rest) {
				return false
			}
		}
		return true
	}, 30*time.Second, 50*time.Millisecond, "the rest never formed a group of their own")
	leader := leaderOf(t, rest...)
	require.NoError(t, leader.Set("after", []byte("v"), 5*time.Second))
}

// A MEMBER HOLDING WHAT ITS GROUP COMMITTED KEEPS IT when its quorum is gone and an
// empty newcomer appears: the newcomer has no leader either, and wiping for it
// would throw away the only copy of what the group committed.
func TestHealing_AMemberThatLostItsQuorumKeepsItsStateForAnEmptyNewcomer(t *testing.T) {
	const namespace = "quorum-lost-keeps"
	probeEvery(t, 50*time.Millisecond)
	group := joinedGroup(t, namespace, sameStore, 3)
	leader := leaderOf(t, group...)
	require.NoError(t, leader.Set("kept", []byte("v"), 5*time.Second))

	survivor := group[0].(*cluster)
	if Cluster(survivor) == leader {
		survivor = group[1].(*cluster)
	}
	require.Eventually(t, func() bool { _, ok := survivor.Get("kept"); return ok }, 5*time.Second, 10*time.Millisecond)
	for _, cl := range group {
		if cl != Cluster(survivor) {
			require.NoError(t, cl.Close())
		}
	}
	require.Eventually(t, func() bool { _, err := survivor.Leader(); return err != nil }, leaderWait, 10*time.Millisecond)
	before := survivor.current()

	node := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())
	connected(t, node, survivor)
	newcomer, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { newcomer.Close() })

	require.Never(t, func() bool {
		_, ok := survivor.Get("kept")
		return !ok || survivor.current() != before
	}, 3*time.Second, 20*time.Millisecond, "the survivor wiped what its group committed for an empty newcomer")
	require.False(t, survivor.current().raft.Pristine())
}

// A MERGE TELLS ONLY THE GROUP IT MERGED TO YIELD: a third group in sight, whose
// state nobody took, keeps it.
func TestHealing_AMergeTellsOnlyTheMergedGroupToYield(t *testing.T) {
	const namespace = "heal-only-merged"
	a, b := splitPair(t, namespace)
	winner, loser := winnerOf(a, b)

	node := newMockNode(t)
	third, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { third.Close() })
	waitLeader(t, third, 5*time.Second)
	require.NoError(t, third.Set("from-third", []byte("c"), time.Second))
	require.NoError(t, taupeer.LinkAllPeers())
	connected(t, node, winner)
	winner.current().tracker.addPeer(node.ID())
	before := third.(*cluster).current()

	winner.current().healer.Load().executeMerge(t.Context(), loser.ID())
	require.Eventually(t, func() bool {
		members, err := winner.Members()
		return err == nil && len(members) == 2
	}, 20*time.Second, 50*time.Millisecond, "the loser never joined the winner's group")
	require.Never(t, func() bool {
		_, ok := third.Get("from-third")
		return !ok || third.(*cluster).current() != before
	}, time.Second, 20*time.Millisecond, "a group the merge never took was told to yield")
}

// A MEMBER WHOSE REBUILD FAILS AFTER A YIELD IS REBUILT once it can be, with
// nobody to restart it: the yield took its old raft down, and a failed rebuild
// left it no raft and no healer.
func TestHealing_AMemberWhoseRebuildFailsIsRebuiltOnceItCanBe(t *testing.T) {
	const namespace = "rebuild-fails"
	store := newFaultStore()
	node := storeNode{Node: newMockNode(t), store: store}
	cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)
	c := cl.(*cluster)

	snapshot := path.Join(RaftStoragePrefix, namespace, "engine", "snapshot")
	store.failGet(snapshot, errNoSpace)
	c.current().healer.Load().yieldAndRejoin(t.Context(), "", covering(c.current()))
	require.Equal(t, Shutdown, c.current().raft.State(), "the rebuild did not fail")

	store.failGet(snapshot, nil)
	require.Eventually(t, func() bool { return c.current().raft.State() == Leader }, 10*time.Second, 20*time.Millisecond,
		"the member was never rebuilt")
	require.NoError(t, cl.Set("after", []byte("v"), 5*time.Second))
}

// winningPeer answers the group's protocol as the leader of a formation that wins
// every comparison, and never merges anyone.
type winningPeer struct {
	raftv2connect.UnimplementedRaftServiceHandler
	id peer.ID
}

func (p *winningPeer) ClusterInfo(context.Context, *connect.Request[raftv2.ClusterInfoRequest]) (*connect.Response[raftv2.ClusterInfoResponse], error) {
	return connect.NewResponse(&raftv2.ClusterInfoResponse{
		Leader:      p.id.String(),
		MemberCount: 1,
		NodeId:      p.id.String(),
		Formation:   make([]byte, formationSize),
		Members:     []string{p.id.String()},
	}), nil
}

// A MEMBER HOLDING STATE THAT LOSES TO ANOTHER GROUP WAITS TO BE TAKEN: yielding
// before the winner merged it would wipe what the winner never had.
func TestHealing_ALoserHoldingStateWaitsToBeTaken(t *testing.T) {
	const namespace = "loser-waits"
	probeEvery(t, 50*time.Millisecond)
	group := joinedGroup(t, namespace, sameStore, 3)
	leader := leaderOf(t, group...)
	require.NoError(t, leader.Set("kept", []byte("v"), 5*time.Second))
	survivor := group[0].(*cluster)
	if Cluster(survivor) == leader {
		survivor = group[1].(*cluster)
	}
	require.Eventually(t, func() bool { _, ok := survivor.Get("kept"); return ok }, 5*time.Second, 10*time.Millisecond)
	for _, cl := range group {
		if cl != Cluster(survivor) {
			require.NoError(t, cl.Close())
		}
	}

	node := newMockNode(t)
	route, handler := raftv2connect.NewRaftServiceHandler(&winningPeer{id: node.ID()})
	mux := http.NewServeMux()
	mux.Handle(route, handler)
	srv, err := rpc.Listen(t.Context(), node.Host(), protocol.ID(Protocol(namespace)), mux)
	require.NoError(t, err)
	t.Cleanup(func() { srv.Close() })
	require.NoError(t, taupeer.LinkAllPeers())
	connected(t, node, survivor)
	survivor.current().tracker.addPeer(node.ID())
	before := survivor.current()

	require.Never(t, func() bool {
		_, ok := survivor.Get("kept")
		return !ok || survivor.current() != before
	}, 3*time.Second, 20*time.Millisecond, "the loser yielded before anyone took its state")
}

// A YIELD THAT NO LONGER HOLDS ONCE THE RAFT IS DOWN WIPES NOTHING: a receipt that
// stops short of what the member committed, or a member that holds something
// after all, is rebuilt from what it kept.
func TestHealing_AYieldThatNoLongerHoldsWipesNothing(t *testing.T) {
	cl, err := New(newMockNode(t), "yield-guarded", WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)
	require.NoError(t, cl.Set("kept", []byte("v"), time.Second))
	c := cl.(*cluster)
	formation := c.current().raft.Formation()

	for name, guard := range map[string]yieldGuard{
		"a receipt that stops short":     receipt{formation: formation, cut: c.current().raft.CommitIndex() - 1},
		"a receipt of another":           receipt{formation: newFormation(), cut: math.MaxUint64},
		"holding nothing while it holds": holdingNothing{},
	} {
		t.Run(name, func(t *testing.T) {
			before := c.current()
			before.healer.Load().yieldAndRejoin(t.Context(), "", guard)
			require.NotSame(t, before, c.current(), "the member was not rebuilt")
			waitLeader(t, cl, 5*time.Second)
			value, found := cl.Get("kept")
			require.True(t, found, "the member wiped what it held")
			require.Equal(t, "v", string(value))
			require.Equal(t, formation, c.current().raft.Formation())
		})
	}
}

// A REBUILD THAT KEEPS FAILING is tried again, ever less often, until it lands,
// and stops being tried once the cluster closes.
func TestHealing_ARebuildThatKeepsFailingIsTriedUntilItLands(t *testing.T) {
	const namespace = "rebuild-keeps-failing"
	store := newFaultStore()
	node := storeNode{Node: newMockNode(t), store: store}
	cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)
	c := cl.(*cluster)

	snapshot := path.Join(RaftStoragePrefix, namespace, "engine", "snapshot")
	store.failGet(snapshot, errNoSpace)
	c.current().healer.Load().yieldAndRejoin(t.Context(), "", covering(c.current()))
	require.Never(t, func() bool { return c.current().raft.State() == Leader }, 800*time.Millisecond, 20*time.Millisecond,
		"a rebuild the store refused landed")
	store.failGet(snapshot, nil)
	require.Eventually(t, func() bool { return c.current().raft.State() == Leader }, 10*time.Second, 20*time.Millisecond,
		"the member was never rebuilt")

	store.failGet(snapshot, errNoSpace)
	c.current().healer.Load().yieldAndRejoin(t.Context(), "", covering(c.current()))
	require.NoError(t, cl.Close())
	store.failGet(snapshot, nil)
	require.Never(t, func() bool { return c.current().raft.State() == Leader }, 500*time.Millisecond, 20*time.Millisecond,
		"a closed cluster was rebuilt")
}

// soloPair is two groups of one, each on its own node and each holding a write,
// connected but with neither tracking the other, ordered winner first.
func soloPair(t *testing.T, namespace string) (winner, loser *cluster) {
	t.Helper()
	var solos []*cluster
	for _, key := range []string{"from-a", "from-b"} {
		cl, err := New(newMockNode(t), namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
		require.NoError(t, err)
		t.Cleanup(func() { cl.Close() })
		waitLeader(t, cl, 5*time.Second)
		require.NoError(t, cl.Set(key, []byte(key[len(key)-1:]), time.Second))
		solos = append(solos, cl.(*cluster))
	}
	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, solos[0].Host().Connect(t.Context(), peer.AddrInfo{ID: solos[1].ID(), Addrs: solos[1].Host().Addrs()}))
	winner, loser = winnerOf(solos[0], solos[1])
	return winner, loser
}

// A LEADER THAT LOSES TO ANOTHER MAKES ITSELF KNOWN TO IT: the winner may not
// track it, and would take it only whenever its discovery came round.
func TestHealing_ALosingLeaderMakesItselfKnownToTheWinner(t *testing.T) {
	probeEvery(t, 50*time.Millisecond)
	winner, loser := soloPair(t, "loser-announces")
	loser.current().tracker.addPeer(winner.ID())

	require.Eventually(t, func() bool {
		members, err := winner.Members()
		return err == nil && len(members) == 2
	}, 10*time.Second, 50*time.Millisecond, "the winner never took the leader that lost to it")
	require.Eventually(t, func() bool { return holdsEverything(winner) && holdsEverything(loser) },
		10*time.Second, 50*time.Millisecond, "the healed group does not hold both sides' writes")
}

// A NODE MADE TO FORM ITS GROUP ALONE JOINS THE GROUP IT YIELDED TO: it was the
// first of its group once, and forming another would be absorbed again, forever.
func TestHealing_AForceBootstrappedLoserJoinsTheGroupItYieldedTo(t *testing.T) {
	winner, loser := soloPair(t, "forced-loser-joins")
	winner.current().tracker.addPeer(loser.ID())

	winner.current().healer.Load().executeMerge(t.Context(), loser.ID())
	require.Eventually(t, func() bool {
		members, err := winner.Members()
		return err == nil && len(members) == 2
	}, 10*time.Second, 50*time.Millisecond, "the loser formed a group of its own rather than join the winner's")
	require.Eventually(t, func() bool { return holdsEverything(loser) }, 10*time.Second, 50*time.Millisecond)
}

// TWO LEADERS HEARING EACH OTHER AT ONCE NEVER EACH TAKE THE OTHER: both would
// yield on the other's receipt, and wipe what each had just merged.
func TestHealing_TwoLeadersHearingEachOtherAtOnceDoNotBothYield(t *testing.T) {
	winner, loser := soloPair(t, "heard-at-once")
	done := make(chan struct{}, 2)
	for _, pair := range [][2]*cluster{{winner, loser}, {loser, winner}} {
		go func() {
			pair[0].current().healer.Load().meetForeign(t.Context(), pair[1].ID())
			done <- struct{}{}
		}()
	}
	<-done
	<-done
	require.Eventually(t, func() bool {
		members, err := winner.Members()
		return err == nil && len(members) == 2
	}, 10*time.Second, 50*time.Millisecond, "the two leaders never became one group")
	require.Eventually(t, func() bool { return holdsEverything(winner) && holdsEverything(loser) },
		10*time.Second, 50*time.Millisecond, "a write was wiped by two leaders taking each other")
}

// A MEMBER LISTED IN THE WINNER'S CONFIGURATION BUT BOUND TO ANOTHER FORMATION IS
// TAKEN with that formation: listing it does not make it ours.
func TestHealing_AListedMemberOfAnotherFormationIsTaken(t *testing.T) {
	const namespace = "listed-elsewhere"
	probeEvery(t, 50*time.Millisecond)
	// OURS IS A FORMATION IN THE LOWER HALF, and the other one above it, so ours
	// takes it: drawn at random, either is redrawn until it is.
	var leader *cluster
	for range 10 {
		ours := joinedGroup(t, namespace, sameStore, 2)
		candidate := leaderOf(t, ours...).(*cluster)
		if candidate.current().raft.Formation()[0] < 0x80 {
			leader = candidate
			break
		}
		for _, cl := range ours {
			require.NoError(t, cl.Close())
		}
	}
	require.NotNil(t, leader, "no formation fell in the lower half in ten draws")
	require.NoError(t, leader.Set("from-a", []byte("a"), time.Second))

	var other *cluster
	for range 20 {
		cl, err := New(newMockNode(t), namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
		require.NoError(t, err)
		t.Cleanup(func() { cl.Close() })
		waitLeader(t, cl, 5*time.Second)
		if bytes.Compare(leader.current().raft.Formation(), cl.(*cluster).current().raft.Formation()) < 0 {
			other = cl.(*cluster)
			break
		}
		require.NoError(t, cl.Close())
	}
	require.NotNil(t, other, "no formation came after ours in twenty draws")
	require.NoError(t, other.Set("from-b", []byte("b"), time.Second))
	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, leader.Host().Connect(t.Context(), peer.AddrInfo{ID: other.ID(), Addrs: other.Host().Addrs()}))
	require.NoError(t, leader.AddVoter(other.ID(), 5*time.Second))
	leader.current().tracker.addPeer(other.ID())

	require.Eventually(t, func() bool {
		return bytes.Equal(other.current().raft.Formation(), leader.current().raft.Formation())
	}, 15*time.Second, 50*time.Millisecond, "the listed member of another formation was never taken")
	require.Eventually(t, func() bool { return holdsEverything(leader) && holdsEverything(other) },
		10*time.Second, 50*time.Millisecond, "the merged group does not hold both formations' writes")
}

// TWO LEADERS WITH THE SAME STATE HEARING EACH OTHER AT ONCE do not both yield: a
// merge of what one already holds commits nothing, so each receipt would cover
// its receiver, and both would wipe the only copies there are.
func TestHealing_TwoLeadersWithTheSameStateDoNotBothYield(t *testing.T) {
	const namespace = "same-state-at-once"
	var solos []*cluster
	for range 2 {
		cl, err := New(newMockNode(t), namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
		require.NoError(t, err)
		t.Cleanup(func() { cl.Close() })
		waitLeader(t, cl, 5*time.Second)
		solos = append(solos, cl.(*cluster))
	}
	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, solos[0].Host().Connect(t.Context(), peer.AddrInfo{ID: solos[1].ID(), Addrs: solos[1].Host().Addrs()}))
	before := []*member{solos[0].current(), solos[1].current()}

	done := make(chan struct{}, 2)
	for i := range solos {
		go func() {
			solos[i].current().healer.Load().meetForeign(t.Context(), solos[1-i].ID())
			done <- struct{}{}
		}()
	}
	<-done
	<-done
	require.Never(t, func() bool { return solos[0].current() != before[0] && solos[1].current() != before[1] },
		2*time.Second, 20*time.Millisecond, "both leaders yielded to each other")
}

// A WIPE THE STORE REFUSES TAKES NOTHING: what the member held goes all at once or
// not at all, so a log is never gone while the data it committed stays.
func TestHealing_AWipeTheStoreRefusesTakesNothing(t *testing.T) {
	const namespace = "wipe-refused"
	store := newFaultStore()
	cl, err := New(storeNode{Node: newMockNode(t), store: store}, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)
	require.NoError(t, cl.Set("kept", []byte("v"), time.Second))
	c := cl.(*cluster)
	formation := c.current().raft.Formation()

	store.failStaging(path.Join(RaftStoragePrefix, namespace, "data", "kept"), errNoSpace)
	c.current().healer.Load().yieldAndRejoin(t.Context(), "", covering(c.current()))
	waitLeader(t, cl, 5*time.Second)
	require.Equal(t, formation, c.current().raft.Formation(), "the wipe took the log and left the data")
	value, found := cl.Get("kept")
	require.True(t, found)
	require.Equal(t, "v", string(value))
}

// A FOUNDER WHOSE OTHER FOUNDER NEVER CAME FORMS AGAIN, alone if need be: its log
// holds only who it named, which leaving loses nothing of.
func TestHealing_AFounderLeftWithOnlyItsConfigurationFormsAgain(t *testing.T) {
	const namespace = "founder-left-alone"
	probeEvery(t, 50*time.Millisecond)
	node := newMockNode(t)
	cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithBootstrapTimeout(time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	c := cl.(*cluster)
	waitLeader(t, cl, 5*time.Second)
	require.NoError(t, c.current().raft.AddVoter(strangerID(t), 5*time.Second))
	require.Eventually(t, func() bool { _, err := cl.Leader(); return err != nil }, leaderWait, 10*time.Millisecond)
	formed := c.current().raft.Formation()
	require.False(t, c.current().raft.Pristine(), "the founder holds no configuration")

	require.Eventually(t, func() bool {
		return c.IsLeader() && !bytes.Equal(formed, c.current().raft.Formation())
	}, 15*time.Second, 50*time.Millisecond, "the founder left with only its configuration never formed again")
}
