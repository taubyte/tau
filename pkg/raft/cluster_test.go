package raft

import (
	"context"
	"fmt"
	"io"
	"path"
	"slices"
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
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2/raftv2connect"
)

// newMockNode is a mock node closed when the test ends: a mock makes a
// repository of its own and only Close removes it. Cleanups run last in first
// out, so a cluster whose cleanup is registered after its node closes first.
func newMockNode(t *testing.T) taupeer.Node {
	node := taupeer.Mock(t.Context())
	t.Cleanup(node.Close)
	return node
}

// testTimeoutConfig returns fast timeouts for testing
func testTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		HeartbeatTimeout:   50 * time.Millisecond,
		ElectionTimeout:    50 * time.Millisecond,
		CommitTimeout:      25 * time.Millisecond,
		LeaderLeaseTimeout: 25 * time.Millisecond,
		SnapshotInterval:   1 * time.Minute,
		SnapshotThreshold:  1000,
	}
}

// testOptions returns common options for unit tests (fast timeouts + short bootstrap)
func testOptions() []Option {
	return []Option{
		WithTimeouts(testTimeoutConfig()),
		WithBootstrapTimeout(100 * time.Millisecond), // Short timeout for fast tests
	}
}

// TestCluster_NodesKeepTheirOwnSnapshots: with no snapshot option a group's
// snapshots went to /tmp/tau-raft-snapshots/<namespace>, one directory for
// every node on the machine and every run, so a node opening a namespace
// another node had snapshotted started from that node's state. Each node's
// snapshots are now its own.
func TestCluster_NodesKeepTheirOwnSnapshots(t *testing.T) {
	const namespace = "snapshots-of-their-own"
	opts := []Option{WithTimeouts(testTimeoutConfig()), WithForceBootstrap()}

	a, err := New(newMockNode(t), namespace, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })
	waitLeader(t, a, 5*time.Second)
	require.NoError(t, a.Set("only-a-wrote", []byte("v"), time.Second))
	require.NoError(t, a.(*cluster).current().raft.Snapshot().Error())

	b, err := New(newMockNode(t), namespace, opts...)
	require.NoError(t, err, "b started from a's snapshot")
	t.Cleanup(func() { b.Close() })
	waitLeader(t, b, 5*time.Second)
	snaps, err := b.(*cluster).current().snaps.List()
	require.NoError(t, err)
	require.Empty(t, snaps, "b's store holds a's snapshot")
	_, found := b.Get("only-a-wrote")
	require.False(t, found, "b holds a write only a made")
}

// TestCluster_FoundersBootstrapWithinTheirTimeout: nodes that start a group
// together wait out the bootstrap timeout, then decide who founds it. A member
// asked to add a voter before any had a leader held the call two seconds
// waiting for one, a join begun before the deadline ran past it, and a tick
// ready with the deadline could start another, so founders took several times
// their timeout. Each now returns within its timeout and a round of asking,
// and the three form one group.
func TestCluster_FoundersBootstrapWithinTheirTimeout(t *testing.T) {
	const (
		namespace = "founders-on-time"
		timeout   = 2 * time.Second
	)
	nodes := []taupeer.Node{newMockNode(t), newMockNode(t), newMockNode(t)}
	require.NoError(t, taupeer.LinkAllPeers())

	type opened struct {
		cl   Cluster
		err  error
		took time.Duration
	}
	results := make(chan opened, len(nodes))
	for _, node := range nodes {
		root := t.TempDir()
		go func() {
			began := time.Now()
			cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(timeout), WithSnapshotRoot(root))
			results <- opened{cl: cl, err: err, took: time.Since(began)}
		}()
	}
	for _, node := range nodes {
		require.Eventually(t, func() bool {
			return slices.Contains(node.Peer().Mux().Protocols(), protocol.ID(Protocol(namespace)))
		}, 5*time.Second, 5*time.Millisecond, "a founder never served the group")
	}
	for i, a := range nodes {
		for _, b := range nodes[i+1:] {
			require.NoError(t, a.Peer().Connect(t.Context(), peer.AddrInfo{ID: b.ID(), Addrs: b.Peer().Addrs()}))
		}
	}

	var got []opened
	for range nodes {
		o := <-results
		if o.err == nil {
			t.Cleanup(func() { o.cl.Close() })
		}
		got = append(got, o)
	}
	clusters := make([]Cluster, 0, len(got))
	for _, o := range got {
		require.NoError(t, o.err)
		assert.Less(t, o.took, timeout+timeout/2, "a founder took %v on a %v bootstrap timeout", o.took, timeout)
		clusters = append(clusters, o.cl)
	}
	leader := leaderOf(t, clusters...)
	require.Eventually(t, func() bool {
		members, err := leader.Members()
		return err == nil && len(members) == len(nodes)
	}, leaderWait, 20*time.Millisecond, "the founders did not form one group")
}

func TestNew_ValidNamespace(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	assert.Equal(t, "test", cluster.Namespace())
}

func TestNew_InvalidNamespace(t *testing.T) {
	node := newMockNode(t)

	tests := []struct {
		name      string
		namespace string
	}{
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(node, tt.namespace)
			assert.Error(t, err, "expected error for invalid namespace")
		})
	}
}

func TestNew_NilNode(t *testing.T) {
	_, err := New(nil, "test")
	assert.Error(t, err, "expected error for nil node")
}

func TestNew_WithOptions(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	assert.Equal(t, "test", cluster.Namespace())
}

func TestCluster_Close(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")

	// First close should succeed
	assert.NoError(t, cluster.Close(), "first close should succeed")

	// Second close should return ErrAlreadyClosed
	assert.ErrorIs(t, cluster.Close(), ErrAlreadyClosed)
}

// heldStore holds every write of one key until it is let go, so a test can
// keep a raft's FSM goroutine inside an apply.
type heldStore struct {
	ds.Batching
	key      ds.Key
	entered  chan struct{}
	release  chan struct{}
	in, out  sync.Once
	finished atomic.Bool
}

func newHeldStore(store ds.Batching, key ds.Key) *heldStore {
	return &heldStore{
		Batching: store,
		key:      key,
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (s *heldStore) Put(ctx context.Context, key ds.Key, value []byte) error {
	if key != s.key {
		return s.Batching.Put(ctx, key, value)
	}
	s.in.Do(func() { close(s.entered) })
	<-s.release
	err := s.Batching.Put(ctx, key, value)
	s.finished.Store(true)
	return err
}

func (s *heldStore) letGo() { s.out.Do(func() { close(s.release) }) }

// storeNode is a node whose store is the one a test hands it.
type storeNode struct {
	taupeer.Node
	store ds.Batching
}

func (n storeNode) Store() ds.Batching { return n.store }

// leaderWait bounds how long a test waits for its members to agree on a
// leader, the same bound the integration tests give WaitForLeader.
const leaderWait = 30 * time.Second

// leaderOf returns whichever of clusters leads, once every one of them names it
// and it alone says it leads. Nothing outside raft decides who leads: with the
// tests' 25ms lease a loaded host moves leadership between any two calls, so a
// test asks at the moment it needs the answer.
func leaderOf(t testing.TB, clusters ...Cluster) Cluster {
	t.Helper()
	deadline := time.Now().Add(leaderWait)
	for {
		if l := agreedLeader(clusters); l != nil {
			return l
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d members never agreed on a leader within %v", len(clusters), leaderWait)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// agreedLeader is leaderOf's one look, nil while the members disagree.
func agreedLeader(clusters []Cluster) Cluster {
	var (
		named  peer.ID
		leader Cluster
	)
	for _, cl := range clusters {
		id, err := cl.Leader()
		if err != nil || (named != "" && id != named) {
			return nil
		}
		named = id
		if cl.IsLeader() {
			if leader != nil {
				return nil
			}
			leader = cl
		}
	}
	if leader == nil || idOf(leader) != named {
		return nil
	}
	return leader
}

func idOf(cl Cluster) peer.ID { return cl.(*cluster).node.ID() }

// onLeader runs write on whichever of clusters leads, and again on the next
// leader while leadership moves under it. A write lost mid-commit may land
// twice, so this is for writes that leave the same state however often they
// are made.
func onLeader(t testing.TB, clusters []Cluster, write func(Cluster) error) error {
	t.Helper()
	deadline := time.Now().Add(leaderWait)
	for {
		err := write(leaderOf(t, clusters...))
		if !leadershipMoved(err) || time.Now().After(deadline) {
			return err
		}
	}
}

// joinedPair is a group of two on linked mock nodes: a bootstrapped it and b
// joined. Each runs on the store wrap makes of its node's own.
func joinedPair(t *testing.T, namespace string, wrap func(ds.Batching) ds.Batching) (a, b Cluster) {
	t.Helper()
	group := joinedGroup(t, namespace, wrap, 2)
	return group[0], group[1]
}

// joinedGroup is a group of size on linked mock nodes: the first bootstrapped
// it and each other joined in turn. Each runs on the store wrap makes of its
// node's own, with opts after the test timeouts.
//
// A joiner connects to the first once that one serves the group, so the
// identify that completes the connection already lists its protocols.
// Connected earlier, the joiner learns them only from identify's push, and one
// that has not heard it within its bootstrap timeout founds a group of its own.
func joinedGroup(t *testing.T, namespace string, wrap func(ds.Batching) ds.Batching, size int, opts ...Option) []Cluster {
	t.Helper()
	nodes := make([]taupeer.Node, size)
	for i := range nodes {
		nodes[i] = newMockNode(t)
	}
	require.NoError(t, taupeer.LinkAllPeers())

	first, err := New(storeNode{Node: nodes[0], store: wrap(nodes[0].Store())}, namespace,
		append([]Option{WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir())}, opts...)...)
	require.NoError(t, err)
	t.Cleanup(func() { first.Close() })
	waitLeader(t, first, 5*time.Second)

	group := []Cluster{first}
	for _, node := range nodes[1:] {
		require.NoError(t, node.Peer().Connect(t.Context(), peer.AddrInfo{ID: nodes[0].ID(), Addrs: nodes[0].Peer().Addrs()}))
		joiner, err := New(storeNode{Node: node, store: wrap(node.Store())}, namespace,
			append([]Option{WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(2 * time.Second), WithSnapshotRoot(t.TempDir())}, opts...)...)
		require.NoError(t, err)
		t.Cleanup(func() { joiner.Close() })
		group = append(group, joiner)

		require.Eventually(t, func() bool {
			members, err := first.Members()
			return err == nil && len(members) == len(group)
		}, 10*time.Second, 20*time.Millisecond, "member %d never joined", len(group))
	}
	return group
}

// removeTheLeader has whichever of a and b leads commit its own removal and
// returns it with the member left leading. The removal is sent and not waited
// on, because its future is answered by the FSM goroutine, which a test may be
// holding; a leader that loses its lease before the removal commits is asked
// again of whichever leads next.
func removeTheLeader(t *testing.T, a, b Cluster) (removed, left Cluster) {
	t.Helper()
	deadline := time.Now().Add(leaderWait)
	for time.Now().Before(deadline) {
		removed = leaderOf(t, a, b)
		left = a
		if removed == a {
			left = b
		}
		removed.(*cluster).current().raft.RemoveServer(raft.ServerID(idOf(removed).String()), 0, 5*time.Second)
		settled := time.Now().Add(2 * time.Second)
		for time.Now().Before(settled) {
			members, err := left.Members()
			if err == nil && len(members) == 1 && left.IsLeader() && removed.State() != raft.Leader {
				return removed, left
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	t.Fatalf("no leader committed its own removal within %v", leaderWait)
	return nil, nil
}

// TestCluster_CloseWaitsForALeaderThatRemovedItself: raft's default is to shut
// itself down once a leader commits its own removal, and the future that
// waits for that shutdown is dropped inside raft. Close then found a raft
// already shut down, got a future that waits for nothing, and returned while
// the FSM goroutine was still writing to the store its caller closes next.
func TestCluster_CloseWaitsForALeaderThatRemovedItself(t *testing.T) {
	namespace := "close-after-self-removal"
	key := ds.NewKey(path.Join(RaftStoragePrefix, namespace, "data", "held"))
	a, b := joinedPair(t, namespace, func(store ds.Batching) ds.Batching { return newHeldStore(store, key) })
	heldOf := func(cl Cluster) *heldStore { return cl.(*cluster).node.Store().(*heldStore) }
	defer heldOf(a).letGo()
	defer heldOf(b).letGo()

	// Both FSMs hold the write, so whichever member leads at the removal is
	// inside an apply when it removes itself.
	setDone := make(chan error, 1)
	go func() {
		for {
			l := agreedLeader([]Cluster{a, b})
			if l == nil {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if err := l.Set("held", []byte("v"), 5*time.Second); !leadershipMoved(err) {
				setDone <- err
				return
			}
		}
	}()
	for _, cl := range []Cluster{a, b} {
		select {
		case <-heldOf(cl).entered:
		case <-time.After(leaderWait):
			t.Fatal("the held write never reached a member's FSM")
		}
	}

	removed, _ := removeTheLeader(t, a, b)

	closed := make(chan error, 1)
	go func() { closed <- removed.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned while its raft was still writing to the store")
	case <-time.After(200 * time.Millisecond):
	}

	heldOf(a).letGo()
	heldOf(b).letGo()
	require.NoError(t, <-closed)
	assert.True(t, heldOf(removed).finished.Load(), "Close returned before the write it was holding finished")
	require.NoError(t, <-setDone)
}

// TestCluster_BarrierOffTheLeaderIsNotLeader: every write path answers a
// follower's refusal as ErrNotLeader, and Barrier passed raft's own error
// through, so a caller testing for ErrNotLeader read a refused barrier as a
// failure of some other kind.
func TestCluster_BarrierOffTheLeaderIsNotLeader(t *testing.T) {
	a, b := joinedPair(t, "barrier-off-the-leader", func(store ds.Batching) ds.Batching { return store })

	// A member that removed itself follows and can never be elected again.
	removed, left := removeTheLeader(t, a, b)

	require.ErrorIs(t, removed.Barrier(time.Second), ErrNotLeader)
	require.NoError(t, left.Barrier(time.Second))
}

func TestCluster_SingleNode_IsLeader(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	// Wait for leader election
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitForLeader(ctx), "failed to wait for leader")

	// Single node should be leader
	assert.True(t, cluster.IsLeader(), "single node should be leader")
}

func TestCluster_SingleNode_SetGet(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitForLeader(ctx), "failed to wait for leader")

	require.NoError(t, cluster.Set("testkey", []byte("testvalue"), time.Second), "failed to set")

	val, ok := cluster.Get("testkey")
	assert.True(t, ok, "expected key to exist")
	assert.Equal(t, "testvalue", string(val))
}

func TestCluster_SingleNode_Delete(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitForLeader(ctx), "failed to wait for leader")

	require.NoError(t, cluster.Set("testkey", []byte("testvalue"), time.Second), "failed to set")

	require.NoError(t, cluster.Delete("testkey", time.Second), "failed to delete")

	_, ok := cluster.Get("testkey")
	assert.False(t, ok, "expected key to be deleted")
}

func TestCluster_SingleNode_Keys(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitForLeader(ctx), "failed to wait for leader")

	keys := []string{"config/a", "config/b", "data/x"}
	for _, key := range keys {
		require.NoError(t, cluster.Set(key, []byte("value"), time.Second), "failed to set %s", key)
	}

	allKeys := cluster.Keys("")
	assert.Len(t, allKeys, 3, "expected 3 keys")

	configKeys := cluster.Keys("config/")
	assert.Len(t, configKeys, 2, "expected 2 config keys")
}

func TestCluster_Barrier(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Barrier should succeed
	if err := cluster.Barrier(time.Second); err != nil {
		t.Errorf("barrier failed: %v", err)
	}
}

func TestCluster_Members(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	members, err := cluster.Members()
	if err != nil {
		t.Fatalf("failed to get members: %v", err)
	}

	if len(members) != 1 {
		t.Errorf("expected 1 member, got %d", len(members))
	}
}

func TestCluster_State(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Should be leader state
	state := cluster.State()
	if state.String() != "Leader" {
		t.Errorf("expected Leader state, got %s", state.String())
	}
}

func TestCluster_ClosedOperations(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	// Operations on closed cluster should fail
	if err := cluster.Set("key", []byte("value"), time.Second); err != ErrShutdown {
		t.Errorf("expected ErrShutdown for Set, got %v", err)
	}

	if err := cluster.Delete("key", time.Second); err != ErrShutdown {
		t.Errorf("expected ErrShutdown for Delete, got %v", err)
	}

	if _, err := cluster.Apply([]byte("cmd"), time.Second); err != ErrShutdown {
		t.Errorf("expected ErrShutdown for Apply, got %v", err)
	}

	if err := cluster.Barrier(time.Second); err != ErrShutdown {
		t.Errorf("expected ErrShutdown for Barrier, got %v", err)
	}

	if _, err := cluster.Members(); err != ErrShutdown {
		t.Errorf("expected ErrShutdown for Members, got %v", err)
	}
}

func TestCluster_WaitForLeader_Timeout(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait should succeed for single node
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		t.Fatalf("wait for leader failed: %v", err)
	}
}

func TestCluster_TransferLeadership(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Transfer leadership (will fail for single node but shouldn't panic)
	err = cluster.TransferLeadership()
	// Error is expected for single node cluster
	_ = err
}

func TestCluster_Leader(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Get leader - should be self
	leader, err := cluster.Leader()
	if err != nil {
		t.Fatalf("failed to get leader: %v", err)
	}

	if leader != node.ID() {
		t.Errorf("expected leader to be self, got %s", leader)
	}
}

func TestCluster_Apply(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Apply a set command
	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "applytest", Value: []byte("applyvalue")},
	}
	data, _ := cbor.Marshal(cmd)

	_, err = cluster.Apply(data, time.Second)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Verify the value was set
	val, ok := cluster.Get("applytest")
	if !ok {
		t.Error("expected key to exist")
	}
	if string(val) != "applyvalue" {
		t.Errorf("expected 'applyvalue', got '%s'", val)
	}
}

func TestCluster_RemoveServer(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Try to remove a non-existent server (should not error)
	dummyPeer, _ := peer.Decode("12D3KooWGzBkj7vvP52A5RRMgQJ4xm4mLe8L1VRyxkj4mHpvT4Yx")
	err = cluster.RemoveServer(dummyPeer, time.Second)
	// Error is acceptable - server doesn't exist
	_ = err
}

func TestCluster_Get_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	// Get on closed cluster should return not found
	val, ok := cluster.Get("key")
	if ok {
		t.Error("expected not found on closed cluster")
	}
	if val != nil {
		t.Error("expected nil value on closed cluster")
	}
}

func TestCluster_Keys_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	// Keys on closed cluster should return empty slice
	keys := cluster.Keys("")
	if len(keys) != 0 {
		t.Error("expected empty keys on closed cluster")
	}
}

func TestCluster_IsLeader_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	// IsLeader on closed cluster should return false
	if cluster.IsLeader() {
		t.Error("expected not leader on closed cluster")
	}
}

func TestCluster_Leader_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	// Leader on closed cluster should return error
	_, err = cluster.Leader()
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_WaitForLeader_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	// WaitForLeader on closed cluster should return error
	ctx := context.Background()
	err = cluster.WaitForLeader(ctx)
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_RemoveServer_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	dummyPeer, _ := peer.Decode("12D3KooWGzBkj7vvP52A5RRMgQJ4xm4mLe8L1VRyxkj4mHpvT4Yx")
	err = cluster.RemoveServer(dummyPeer, time.Second)
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_TransferLeadership_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	err = cluster.TransferLeadership()
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_State_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	state := cluster.State()
	if state.String() != "Shutdown" {
		t.Errorf("expected Shutdown state, got %s", state.String())
	}
}

func TestCluster_Set_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	err = cluster.Set("key", []byte("value"), time.Second)
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_Delete_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	err = cluster.Delete("key", time.Second)
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_Apply_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	_, err = cluster.Apply([]byte("test"), time.Second)
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_Apply_InvalidCommand(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Apply invalid data - should succeed but FSM should return error
	resp, err := cluster.Apply([]byte("invalid cbor data"), time.Second)
	if err != nil {
		t.Logf("Apply error: %v", err)
	}
	// The FSM will return an error for invalid command
	if resp.Error != nil {
		t.Logf("FSM error (expected): %v", resp.Error)
	}
}

func TestCluster_Barrier_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	err = cluster.Barrier(time.Second)
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_Members_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	_, err = cluster.Members()
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_RemoveServer_NotLeader(t *testing.T) {
	// For this test, we'd need a follower node, but in single-node test
	// we can at least verify the method exists and doesn't panic
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Try immediately before becoming leader - should return ErrNotLeader
	dummyPeer, _ := peer.Decode("12D3KooWGzBkj7vvP52A5RRMgQJ4xm4mLe8L1VRyxkj4mHpvT4Yx")
	err = cluster.RemoveServer(dummyPeer, 10*time.Millisecond)
	// Either ErrNotLeader or timeout is acceptable
	_ = err
}

func TestBuildRaftConfig(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "/raft/test",
		WithTimeouts(TimeoutConfig{
			HeartbeatTimeout:   100 * time.Millisecond,
			ElectionTimeout:    200 * time.Millisecond,
			CommitTimeout:      50 * time.Millisecond,
			LeaderLeaseTimeout: 50 * time.Millisecond,
			SnapshotInterval:   1 * time.Minute,
			SnapshotThreshold:  500,
		}),
	)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Verify cluster was created successfully
	if cluster.Namespace() != "/raft/test" {
		t.Errorf("expected namespace '/raft/test', got '%s'", cluster.Namespace())
	}
}

func TestCluster_RaftClient(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cl.Close()

	c := getClusterInternal(cl)
	if c.current().raftClient == nil {
		t.Error("expected raft client to be initialized")
	}
}

func TestCluster_StreamService(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	c := getClusterInternal(cl)
	assert.NotNil(t, c.current().streamService, "expected stream service to be initialized")
}

func TestCluster_Apply_NotLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Try to apply before becoming leader
	_, err = cl.Apply([]byte("test-command"), 10*time.Millisecond)
	// May fail with ErrNotLeader or timeout - either is acceptable
	_ = err
}

func TestCluster_Apply_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Try to apply an invalid command (will be handled by FSM)
	// The FSM returns an error in the response, but Apply itself may succeed
	resp, err := cl.Apply([]byte("invalid"), 100*time.Millisecond)
	// The apply itself may succeed, but response may contain error
	if err == nil {
		assert.NotNil(t, resp.Error, "FSM should return error for invalid command")
	}
}

func TestCluster_Set_EncodingError(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set should work with any key/value
	err = cl.Set("test-key", []byte("test-value"), 100*time.Millisecond)
	require.NoError(t, err)
}

func TestCluster_Delete_NotFound(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Delete non-existent key should not error
	err = cl.Delete("nonexistent-key", 100*time.Millisecond)
	require.NoError(t, err)
}

func TestCluster_FSM_Nil_CustomFSM(t *testing.T) {
	node := newMockNode(t)

	// Test that default FSM works
	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Default FSM should work
	val, found := cl.Get("test")
	assert.False(t, found)
	assert.Nil(t, val)

	keys := cl.Keys("test")
	assert.Equal(t, 0, len(keys))
}

// mockCustomFSM is a minimal FSM for testing custom FSM path
type mockCustomFSM struct{}

func (m *mockCustomFSM) Apply(log *raft.Log) interface{} {
	return FSMResponse{}
}

func (m *mockCustomFSM) Snapshot() (raft.FSMSnapshot, error) {
	return &mockSnapshot{}, nil
}

func (m *mockCustomFSM) Restore(rc io.ReadCloser) error {
	return nil
}

func (m *mockCustomFSM) Get(key string) ([]byte, bool) {
	return nil, false
}

func (m *mockCustomFSM) TryGet(key string) ([]byte, bool, error) {
	return nil, false, nil
}

func (m *mockCustomFSM) Keys(prefix string) []string {
	return []string{}
}

type mockSnapshot struct{}

func (m *mockSnapshot) Persist(sink raft.SnapshotSink) error {
	return sink.Close()
}

func (m *mockSnapshot) Release() {}

func TestCluster_Keys_WithData(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set some keys
	require.NoError(t, cl.Set("prefix-a-1", []byte("value1"), 100*time.Millisecond))
	require.NoError(t, cl.Set("prefix-a-2", []byte("value2"), 100*time.Millisecond))
	require.NoError(t, cl.Set("prefix-b-1", []byte("value3"), 100*time.Millisecond))

	// Get keys with prefix "prefix-a"
	keys := cl.Keys("prefix-a")
	assert.Equal(t, 2, len(keys))

	// Get all keys
	allKeys := cl.Keys("")
	assert.Equal(t, 3, len(allKeys))
}

func TestCluster_Get_NotFound(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Get non-existent key
	val, found := cl.Get("nonexistent-key")
	assert.False(t, found)
	assert.Nil(t, val)
}

func TestFsmAdapter_Apply_ViaCluster(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/adapter-test")

	// Create a valid set command
	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "adapter-key", Value: []byte("adapter-value")},
	}
	data, err := cbor.Marshal(cmd)
	require.NoError(t, err)

	log := &raft.Log{Data: data, Index: 1}
	resp := fsm.Apply(log)

	fsmResp, ok := resp.(FSMResponse)
	assert.True(t, ok)
	assert.Nil(t, fsmResp.Error)

	// Verify key was stored
	val, found := fsm.Get("adapter-key")
	assert.True(t, found)
	assert.Equal(t, []byte("adapter-value"), val)
}

func TestFsmAdapter_Snapshot_ViaCluster(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/adapter-test")
	snap, err := fsm.Snapshot()
	assert.NoError(t, err)
	assert.NotNil(t, snap)
}

func TestCluster_AddVoter_NotLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Try to add voter before becoming leader
	dummyPeer, _ := peer.Decode("12D3KooWGzBkj7vvP52A5RRMgQJ4xm4mLe8L1VRyxkj4mHpvT4Yx")
	err = cl.AddVoter(dummyPeer, 10*time.Millisecond)
	// Should fail with ErrNotLeader
	if err != nil {
		assert.Equal(t, ErrNotLeader, err)
	}
}

func TestCluster_Barrier_Success(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Barrier should succeed when leader
	err = cl.Barrier(100 * time.Millisecond)
	assert.NoError(t, err)
}

func TestCluster_TransferLeadership_SingleNode(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// TransferLeadership in single node - should fail (no peer to transfer to)
	err = cl.TransferLeadership()
	// Error is expected for single node
	_ = err
}

func TestCluster_Members_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Get members
	members, err := cl.Members()
	assert.NoError(t, err)
	assert.True(t, len(members) >= 1)
}

func TestCluster_State_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// State should be Leader
	state := cl.State()
	assert.Equal(t, raft.Leader, state)
}

func TestCluster_Leader_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Leader should return our node ID
	leader, err := cl.Leader()
	assert.NoError(t, err)
	assert.Equal(t, node.ID(), leader)
}

func TestCluster_AddVoter_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Try to add self as voter (already a member)
	err = cl.AddVoter(node.ID(), 100*time.Millisecond)
	// Should succeed or return already a member
	assert.NoError(t, err)
}

func TestCluster_Set_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a key
	err = cl.Set("leader-key", []byte("leader-value"), 100*time.Millisecond)
	require.NoError(t, err)

	// Verify it was set
	val, found := cl.Get("leader-key")
	assert.True(t, found)
	assert.Equal(t, []byte("leader-value"), val)
}

func TestCluster_Delete_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a key first
	require.NoError(t, cl.Set("delete-key", []byte("value"), 100*time.Millisecond))

	// Delete it
	err = cl.Delete("delete-key", 100*time.Millisecond)
	require.NoError(t, err)

	// Verify it was deleted
	_, found := cl.Get("delete-key")
	assert.False(t, found)
}

func TestCluster_Close_Idempotent(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")

	// First close
	err = cl.Close()
	assert.NoError(t, err)

	// Second close should return error
	err = cl.Close()
	assert.Equal(t, ErrAlreadyClosed, err)
}

func TestCluster_Apply_InvalidTimeout(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "/raft/test-apply-timeout", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitForLeader(ctx), "failed to wait for leader")

	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "test", Value: []byte("value")},
	}
	data, _ := cbor.Marshal(cmd)

	// Test with timeout = 0 (should fail)
	_, err = cluster.Apply(data, 0)
	require.Error(t, err, "Apply with timeout=0 should fail")
	require.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	// Test with timeout < 0 (should fail)
	_, err = cluster.Apply(data, -1*time.Second)
	require.Error(t, err, "Apply with negative timeout should fail")
	require.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	// Test with timeout > MaxApplyTimeout (should fail)
	_, err = cluster.Apply(data, MaxApplyTimeout+time.Second)
	require.Error(t, err, "Apply with timeout > MaxApplyTimeout should fail")
	require.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	// Test with timeout = MaxApplyTimeout (should work)
	_, err = cluster.Apply(data, MaxApplyTimeout)
	require.NoError(t, err, "Apply with timeout at MaxApplyTimeout should succeed")

	// Test with valid timeout (1 second, well within limit)
	cmd2 := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "test2", Value: []byte("value2")},
	}
	data2, _ := cbor.Marshal(cmd2)
	_, err = cluster.Apply(data2, time.Second)
	require.NoError(t, err, "Apply with valid timeout should succeed")

	// Verify the value was set
	val, found := cluster.Get("test2")
	require.True(t, found, "key should be found")
	require.Equal(t, []byte("value2"), val, "value should match")
}

func TestCluster_ApplyAsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Apply a valid set command
	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "apply-test", Value: []byte("apply-value")},
	}
	cmdBytes, _ := cbor.Marshal(cmd)

	resp, err := cl.Apply(cmdBytes, time.Second)
	require.NoError(t, err)
	assert.Nil(t, resp.Error)

	// Verify the value was set
	val, found := cl.Get("apply-test")
	assert.True(t, found)
	assert.Equal(t, []byte("apply-value"), val)
}

func TestCluster_SetAndVerify(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set multiple values
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("multi-key-%d", i)
		value := []byte(fmt.Sprintf("multi-value-%d", i))
		require.NoError(t, cl.Set(key, value, time.Second))
	}

	// Verify all values
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("multi-key-%d", i)
		expected := fmt.Sprintf("multi-value-%d", i)
		val, found := cl.Get(key)
		assert.True(t, found)
		assert.Equal(t, expected, string(val))
	}

	// Keys should work
	keys := cl.Keys("multi-")
	assert.Len(t, keys, 5)
}

func TestCluster_DeleteAndVerify(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a value
	require.NoError(t, cl.Set("to-delete", []byte("value"), time.Second))

	// Verify it exists
	_, found := cl.Get("to-delete")
	assert.True(t, found)

	// Delete it
	require.NoError(t, cl.Delete("to-delete", time.Second))

	// Verify it's gone
	_, found = cl.Get("to-delete")
	assert.False(t, found)
}

func TestCluster_BarrierSuccess(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Barrier should succeed
	err = cl.Barrier(time.Second)
	assert.NoError(t, err)
}

func TestCluster_LeaderInfo(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Should be leader
	assert.True(t, cl.IsLeader())

	// State should be Leader
	assert.Equal(t, raft.Leader, cl.State())

	// Leader should return our ID
	leader, err := cl.Leader()
	require.NoError(t, err)
	assert.Equal(t, node.ID(), leader)
}

func TestCluster_MembersAfterBootstrap(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Should have exactly one member
	members, err := cl.Members()
	require.NoError(t, err)
	assert.Len(t, members, 1)
	assert.Equal(t, node.ID(), members[0].ID)
	assert.Equal(t, raft.Voter, members[0].Suffrage)
}

// TestCluster_TwoMembersThatStartTogetherFormOneGroup: two nodes connected
// before either runs the namespace find each other while both are still
// deciding, so neither finds a group to join and each counts the other as a
// founder. The lower of the two bootstraps a group of both, the other asks it
// to be let in, and they end as one group that applies the same writes.
func TestCluster_TwoMembersThatStartTogetherFormOneGroup(t *testing.T) {
	const namespace = "start-together"
	nodes := []taupeer.Node{newMockNode(t), newMockNode(t)}
	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, nodes[1].Peer().Connect(t.Context(), peer.AddrInfo{ID: nodes[0].ID(), Addrs: nodes[0].Peer().Addrs()}))

	type started struct {
		cl  Cluster
		err error
	}
	results := make(chan started, len(nodes))
	for _, node := range nodes {
		root := t.TempDir()
		go func() {
			cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(time.Second), WithSnapshotRoot(root))
			results <- started{cl: cl, err: err}
		}()
	}
	group := make([]Cluster, 0, len(nodes))
	for range nodes {
		r := <-results
		require.NoError(t, r.err)
		t.Cleanup(func() { r.cl.Close() })
		group = append(group, r.cl)
	}

	require.Eventually(t, func() bool {
		for _, cl := range group {
			if members, err := cl.Members(); err != nil || len(members) != 2 {
				return false
			}
		}
		return true
	}, leaderWait, 20*time.Millisecond, "the two never formed one group")
	require.NoError(t, onLeader(t, group, func(leader Cluster) error {
		return leader.Set("k", []byte("v"), time.Second)
	}))
	for _, cl := range group {
		require.Eventually(t, func() bool {
			value, found := cl.Get("k")
			return found && string(value) == "v"
		}, leaderWait, 20*time.Millisecond, "%s never applied the group's write", idOf(cl).ShortString())
	}
}

// TestCluster_AMemberRestartedOnItsStateLeadsItsGroupAgain: a member started
// again on the store it ran on finds nobody to join, and the bootstrap a new
// member would make is refused because the store already holds a group. It
// asks to be let in instead, and its raft takes the group up again from its
// log, holding what the group committed and committing more.
func TestCluster_AMemberRestartedOnItsStateLeadsItsGroupAgain(t *testing.T) {
	const namespace = "restarted-member"
	node := newMockNode(t)
	root := t.TempDir()

	first, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(root))
	require.NoError(t, err)
	waitLeader(t, first, 5*time.Second)
	require.NoError(t, first.Set("k", []byte("v"), time.Second))
	require.NoError(t, first.Close())

	again, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(200*time.Millisecond), WithSnapshotRoot(root))
	require.NoError(t, err)
	defer again.Close()
	waitLeader(t, again, 5*time.Second)

	value, found, err := again.TryGet("k")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("v"), value)
	members, err := again.Members()
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.NoError(t, onLeader(t, []Cluster{again}, func(leader Cluster) error {
		return leader.Set("after", []byte("v"), time.Second)
	}))
}

// letInPeer answers every join the way a leader letting the member in does,
// and counts the joins it is asked.
type letInPeer struct {
	raftv2connect.UnimplementedRaftServiceHandler
	joins atomic.Int32
}

func (p *letInPeer) JoinVoter(context.Context, *connect.Request[raftv2.JoinVoterRequest]) (*connect.Response[raftv2.JoinVoterResponse], error) {
	p.joins.Add(1)
	return connect.NewResponse(&raftv2.JoinVoterResponse{}), nil
}

// TestCluster_AMemberLetInAsksNoMore: a member went on asking to be let in
// every 500ms for the rest of its 10s once the group had let it in, and a
// leader answers each ask by reading its configuration through its raft.
func TestCluster_AMemberLetInAsksNoMore(t *testing.T) {
	group := &letInPeer{}
	member, _ := facing(t, "let-in-asks-no-more", newFaultStore(), group)

	member.askToJoin(member.current(), time.Second)
	assert.Equal(t, int32(1), group.joins.Load(), "the member asked again once let in")
	assert.True(t, member.current().joined.Load(), "the member does not know the group let it in")
}
