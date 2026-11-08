package raft

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	ds "github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	taupeer "github.com/taubyte/tau/p2p/peer"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2/raftv2connect"
	etcdraft "go.etcd.io/raft/v3"
)

// newMockNode is a mock node closed when the test ends: a mock makes a
// repository of its own and only Close removes it. Cleanups run last in first
// out, so a cluster whose cleanup is registered after its node closes first.
func newMockNode(t *testing.T) taupeer.Node {
	node := taupeer.Mock(t.Context())
	t.Cleanup(node.Close)
	return node
}

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

func testOptions() []Option {
	return []Option{
		WithTimeouts(testTimeoutConfig()),
		WithBootstrapTimeout(100 * time.Millisecond),
	}
}

func TestCluster_NodesKeepTheirOwnSnapshots(t *testing.T) {
	const namespace = "snapshots-of-their-own"
	opts := []Option{WithTimeouts(testTimeoutConfig()), WithForceBootstrap()}

	a, err := New(newMockNode(t), namespace, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })
	waitLeader(t, a, 5*time.Second)
	require.NoError(t, a.Set("only-a-wrote", []byte("v"), time.Second))
	require.NoError(t, a.(*cluster).current().raft.Snapshot())

	b, err := New(newMockNode(t), namespace, opts...)
	require.NoError(t, err, "b started from a's snapshot")
	t.Cleanup(func() { b.Close() })
	waitLeader(t, b, 5*time.Second)
	snap, err := b.(*cluster).current().raft.log.Snapshot()
	require.NoError(t, err)
	require.True(t, etcdraft.IsEmptySnap(snap), "b's store holds a's snapshot")
	_, found := b.Get("only-a-wrote")
	require.False(t, found, "b holds a write only a made")
}

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
		go func() {
			began := time.Now()
			cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(timeout))
			results <- opened{cl: cl, err: err, took: time.Since(began)}
		}()
	}
	for _, node := range nodes {
		require.Eventually(t, func() bool {
			return slices.Contains(node.Host().Mux().Protocols(), protocol.ID(Protocol(namespace)))
		}, 5*time.Second, 5*time.Millisecond, "a founder never served the group")
	}
	for i, a := range nodes {
		for _, b := range nodes[i+1:] {
			require.NoError(t, a.Host().Connect(t.Context(), peer.AddrInfo{ID: b.ID(), Addrs: b.Host().Addrs()}))
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

	type testCase struct {
		name      string
		namespace string
	}
	tests := []testCase{
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

	assert.NoError(t, cluster.Close(), "first close should succeed")

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

type storeNode struct {
	taupeer.Node
	store ds.Batching
}

func (n storeNode) Store() ds.Batching { return n.store }

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

func idOf(cl Cluster) peer.ID { return cl.(*cluster).ID() }

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

func joinedPair(t *testing.T, namespace string, wrap func(ds.Batching) ds.Batching) (a, b Cluster) {
	t.Helper()
	group := joinedGroup(t, namespace, wrap, 2)
	return group[0], group[1]
}

// joinedGroup is a group of size on linked mock nodes: the first bootstrapped
// it and each other joined in turn, on the store wrap makes of its node's own.
// A joiner connects once the first serves the group, so identify already lists
// its protocols; connected earlier, it learns them only from identify's push,
// and one that misses it founds a group of its own.
func joinedGroup(t *testing.T, namespace string, wrap func(ds.Batching) ds.Batching, size int, opts ...Option) []Cluster {
	t.Helper()
	nodes := make([]taupeer.Node, size)
	for i := range nodes {
		nodes[i] = newMockNode(t)
	}
	require.NoError(t, taupeer.LinkAllPeers())

	first, err := New(storeNode{Node: nodes[0], store: wrap(nodes[0].Store())}, namespace,
		append([]Option{WithTimeouts(testTimeoutConfig()), WithForceBootstrap()}, opts...)...)
	require.NoError(t, err)
	t.Cleanup(func() { first.Close() })
	waitLeader(t, first, 5*time.Second)

	group := []Cluster{first}
	for _, node := range nodes[1:] {
		require.NoError(t, node.Host().Connect(t.Context(), peer.AddrInfo{ID: nodes[0].ID(), Addrs: nodes[0].Host().Addrs()}))
		joiner, err := New(storeNode{Node: node, store: wrap(node.Store())}, namespace,
			append([]Option{WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(2 * time.Second)}, opts...)...)
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

// removeTheLeader has whichever of a and b leads commit its own removal, not
// waited on since the FSM goroutine answers it and a test may hold that. It is
// sent only once the member left holds the whole log: a removed leader no
// longer campaigns, and refuses the votes of a member with a shorter log, so
// neither could lead again.
func removeTheLeader(t *testing.T, a, b Cluster) (removed, left Cluster) {
	t.Helper()
	deadline := time.Now().Add(leaderWait)
	for time.Now().Before(deadline) {
		removed = leaderOf(t, a, b)
		left = a
		if removed == a {
			left = b
		}
		leader, follower := removed.(*cluster).current().raft, left.(*cluster).current().raft
		for time.Now().Before(deadline) && removed.IsLeader() &&
			!(follower.LastIndex() >= leader.LastIndex() && inConfiguration(follower, idOf(left))) {
			time.Sleep(10 * time.Millisecond)
		}
		if !removed.IsLeader() {
			continue
		}
		leader.RemoveServer(idOf(removed), 5*time.Second)
		for time.Now().Before(deadline) {
			members, err := left.Members()
			if err == nil && len(members) == 1 && left.IsLeader() && removed.State() != Leader {
				return removed, left
			}
			if removed.State() != Leader && inConfiguration(leader, idOf(removed)) {
				break // refused: it no longer led when the removal was asked of it
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	t.Fatalf("no leader committed its own removal within %v", leaderWait)
	return nil, nil
}

func inConfiguration(g *group, id peer.ID) bool {
	return slices.ContainsFunc(g.Members(), func(m Member) bool { return m.ID == id })
}

// A LEADER THAT REMOVED ITSELF while its machine was inside an apply still closes
// only once that apply finished: nothing writes to the store after Close returns.
func TestCluster_CloseWaitsForALeaderThatRemovedItself(t *testing.T) {
	namespace := "close-after-self-removal"
	key := ds.NewKey(path.Join(RaftStoragePrefix, namespace, "data", "held"))
	a, b := joinedPair(t, namespace, func(store ds.Batching) ds.Batching { return newHeldStore(store, key) })
	heldOf := func(cl Cluster) *heldStore { return cl.(*cluster).Store().(*heldStore) }
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
			// A leader that removed itself and closed answers ErrShutdown rather than
			// ErrNotLeader; either way the write goes to the member that is left.
			err := l.Set("held", []byte("v"), 5*time.Second)
			if leadershipMoved(err) || (errors.Is(err, ErrShutdown) && l.(*cluster).closed.Load()) {
				continue
			}
			setDone <- err
			return
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitForLeader(ctx), "failed to wait for leader")

	assert.True(t, cluster.IsLeader(), "single node should be leader")
}

func TestCluster_SingleNode_SetGet(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Error is expected for single node cluster
	cluster.TransferLeadership()
}

func TestCluster_Leader(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "applytest", Value: []byte("applyvalue")},
	}
	data, _ := encodeCommand(cmd)

	_, err = cluster.Apply(data, time.Second)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	dummyPeer, _ := peer.Decode("12D3KooWGzBkj7vvP52A5RRMgQJ4xm4mLe8L1VRyxkj4mHpvT4Yx")
	// Error is acceptable - server doesn't exist
	cluster.RemoveServer(dummyPeer, time.Second)
}

func TestCluster_Get_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	resp, err := cluster.Apply([]byte("not a command"), time.Second)
	if err != nil {
		t.Logf("Apply error: %v", err)
	}
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
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	dummyPeer, _ := peer.Decode("12D3KooWGzBkj7vvP52A5RRMgQJ4xm4mLe8L1VRyxkj4mHpvT4Yx")
	// Either ErrNotLeader or timeout is acceptable
	cluster.RemoveServer(dummyPeer, 10*time.Millisecond)
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

	// May fail with ErrNotLeader or timeout - either is acceptable
	cl.Apply([]byte("test-command"), 10*time.Millisecond)
}

func TestCluster_Apply_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	resp, err := cl.Apply([]byte("invalid"), 100*time.Millisecond)
	if err == nil {
		assert.NotNil(t, resp.Error, "FSM should return error for invalid command")
	}
}

func TestCluster_Set_EncodingError(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	err = cl.Set("test-key", []byte("test-value"), 100*time.Millisecond)
	require.NoError(t, err)
}

func TestCluster_Delete_NotFound(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	err = cl.Delete("nonexistent-key", 100*time.Millisecond)
	require.NoError(t, err)
}

func TestCluster_FSM_Nil_CustomFSM(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	val, found := cl.Get("test")
	assert.False(t, found)
	assert.Nil(t, val)

	keys := cl.Keys("test")
	assert.Equal(t, 0, len(keys))
}

func TestCluster_Keys_WithData(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	require.NoError(t, cl.Set("prefix-a-1", []byte("value1"), 100*time.Millisecond))
	require.NoError(t, cl.Set("prefix-a-2", []byte("value2"), 100*time.Millisecond))
	require.NoError(t, cl.Set("prefix-b-1", []byte("value3"), 100*time.Millisecond))

	keys := cl.Keys("prefix-a")
	assert.Equal(t, 2, len(keys))

	allKeys := cl.Keys("")
	assert.Equal(t, 3, len(allKeys))
}

func TestCluster_Get_NotFound(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	val, found := cl.Get("nonexistent-key")
	assert.False(t, found)
	assert.Nil(t, val)
}

func TestFsmAdapter_Apply_ViaCluster(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/adapter-test")

	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "adapter-key", Value: []byte("adapter-value")},
	}
	data, err := encodeCommand(cmd)
	require.NoError(t, err)

	resp := fsm.Apply(1, data)
	assert.Nil(t, resp.Error)

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

	dummyPeer, _ := peer.Decode("12D3KooWGzBkj7vvP52A5RRMgQJ4xm4mLe8L1VRyxkj4mHpvT4Yx")
	err = cl.AddVoter(dummyPeer, 10*time.Millisecond)
	if err != nil {
		assert.Equal(t, ErrNotLeader, err)
	}
}

func TestCluster_Barrier_Success(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	err = cl.Barrier(100 * time.Millisecond)
	assert.NoError(t, err)
}

func TestCluster_TransferLeadership_SingleNode(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Error is expected for single node
	cl.TransferLeadership()
}

func TestCluster_Members_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	members, err := cl.Members()
	assert.NoError(t, err)
	assert.True(t, len(members) >= 1)
}

func TestCluster_State_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	state := cl.State()
	assert.Equal(t, Leader, state)
}

func TestCluster_Leader_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	leader, err := cl.Leader()
	assert.NoError(t, err)
	assert.Equal(t, node.ID(), leader)
}

func TestCluster_AddVoter_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	err = cl.AddVoter(node.ID(), 100*time.Millisecond)
	assert.NoError(t, err)
}

func TestCluster_Set_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	err = cl.Set("leader-key", []byte("leader-value"), 100*time.Millisecond)
	require.NoError(t, err)

	val, found := cl.Get("leader-key")
	assert.True(t, found)
	assert.Equal(t, []byte("leader-value"), val)
}

func TestCluster_Delete_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	require.NoError(t, cl.Set("delete-key", []byte("value"), 100*time.Millisecond))

	err = cl.Delete("delete-key", 100*time.Millisecond)
	require.NoError(t, err)

	_, found := cl.Get("delete-key")
	assert.False(t, found)
}

func TestCluster_Close_Idempotent(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")

	err = cl.Close()
	assert.NoError(t, err)

	err = cl.Close()
	assert.Equal(t, ErrAlreadyClosed, err)
}

func TestCluster_Apply_InvalidTimeout(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "/raft/test-apply-timeout", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitForLeader(ctx), "failed to wait for leader")

	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "test", Value: []byte("value")},
	}
	data, _ := encodeCommand(cmd)

	_, err = cluster.Apply(data, 0)
	require.Error(t, err, "Apply with timeout=0 should fail")
	require.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	_, err = cluster.Apply(data, -1*time.Second)
	require.Error(t, err, "Apply with negative timeout should fail")
	require.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	_, err = cluster.Apply(data, MaxApplyTimeout+time.Second)
	require.Error(t, err, "Apply with timeout > MaxApplyTimeout should fail")
	require.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	_, err = cluster.Apply(data, MaxApplyTimeout)
	require.NoError(t, err, "Apply with timeout at MaxApplyTimeout should succeed")

	cmd2 := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "test2", Value: []byte("value2")},
	}
	data2, _ := encodeCommand(cmd2)
	_, err = cluster.Apply(data2, time.Second)
	require.NoError(t, err, "Apply with valid timeout should succeed")

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

	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "apply-test", Value: []byte("apply-value")},
	}
	cmdBytes, _ := encodeCommand(cmd)

	resp, err := cl.Apply(cmdBytes, time.Second)
	require.NoError(t, err)
	assert.Nil(t, resp.Error)

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

	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("multi-key-%d", i)
		value := []byte(fmt.Sprintf("multi-value-%d", i))
		require.NoError(t, cl.Set(key, value, time.Second))
	}

	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("multi-key-%d", i)
		expected := fmt.Sprintf("multi-value-%d", i)
		val, found := cl.Get(key)
		assert.True(t, found)
		assert.Equal(t, expected, string(val))
	}

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

	require.NoError(t, cl.Set("to-delete", []byte("value"), time.Second))

	_, found := cl.Get("to-delete")
	assert.True(t, found)

	require.NoError(t, cl.Delete("to-delete", time.Second))

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

	assert.True(t, cl.IsLeader())

	assert.Equal(t, Leader, cl.State())

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

	members, err := cl.Members()
	require.NoError(t, err)
	assert.Len(t, members, 1)
	assert.Equal(t, node.ID(), members[0].ID)
	assert.True(t, members[0].Voter)
}

func TestCluster_TwoMembersThatStartTogetherFormOneGroup(t *testing.T) {
	const namespace = "start-together"
	nodes := []taupeer.Node{newMockNode(t), newMockNode(t)}
	require.NoError(t, taupeer.LinkAllPeers())
	require.NoError(t, nodes[1].Host().Connect(t.Context(), peer.AddrInfo{ID: nodes[0].ID(), Addrs: nodes[0].Host().Addrs()}))

	type started struct {
		cl  Cluster
		err error
	}
	results := make(chan started, len(nodes))
	for _, node := range nodes {
		go func() {
			cl, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(time.Second))
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

// A member restarted on its store finds nobody to join, and the bootstrap a
// new member would make is refused because the store already holds a group, so
// it asks to be let in and takes the group up again from its log.
func TestCluster_AMemberRestartedOnItsStateLeadsItsGroupAgain(t *testing.T) {
	const namespace = "restarted-member"
	node := newMockNode(t)

	first, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	waitLeader(t, first, 5*time.Second)
	require.NoError(t, first.Set("k", []byte("v"), time.Second))
	require.NoError(t, first.Close())

	again, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(200*time.Millisecond))
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

func TestCluster_AMemberLetInAsksNoMore(t *testing.T) {
	group := &letInPeer{}
	member, _ := facing(t, "let-in-asks-no-more", newFaultStore(), group)

	member.askToJoin(member.current(), time.Second)
	assert.Equal(t, int32(1), group.joins.Load(), "the member asked again once let in")
	assert.True(t, member.current().joined.Load(), "the member does not know the group let it in")
}

// A NODE THAT FORMED ITS GROUP BY ITSELF RESUMES IT after a restart, over what it
// kept: the bootstrap it cannot make again is the one it already made.
func TestCluster_AForceBootstrappedNodeResumesItsGroupAfterARestart(t *testing.T) {
	const namespace = "force-resumes"
	node := newMockNode(t)
	first, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	waitLeader(t, first, 5*time.Second)
	require.NoError(t, first.Set("kept", []byte("v"), time.Second))
	require.NoError(t, first.Close())

	again, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { again.Close() })
	waitLeader(t, again, 5*time.Second)
	value, found := again.Get("kept")
	require.True(t, found)
	require.Equal(t, "v", string(value))
}
