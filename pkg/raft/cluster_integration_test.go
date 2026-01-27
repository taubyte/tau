package raft

import (
	"context"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	"github.com/libp2p/go-libp2p/core/network"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/keypair"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"gotest.tools/v3/assert"
)

func waitForConnected(t *testing.T, node taupeer.Node, peerID peercore.ID, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			state := node.Peer().Network().Connectedness(peerID)
			if state == network.Connected {
				return nil
			}
		}
	}
}

// newTestNode creates a real libp2p node for testing
func newTestNode(t *testing.T, bootstrapPeers ...peercore.AddrInfo) taupeer.Node {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	dir := t.TempDir()

	node, err := taupeer.New(
		ctx,
		dir,
		keypair.NewRaw(),
		nil,                              // swarm key
		[]string{"/ip4/127.0.0.1/tcp/0"}, // port 0 for dynamic allocation
		nil,                              // swarm announce
		true,                             // notPublic
		false,                            // don't bootstrap to default peers
	)
	assert.NilError(t, err, "failed to create node")

	t.Cleanup(func() {
		node.Close()
	})

	// Wait for node to be ready
	err = node.WaitForSwarm(5 * time.Second)
	assert.NilError(t, err, "failed to wait for swarm")

	// If bootstrap peers provided, add them to peering
	for _, peer := range bootstrapPeers {
		node.Peering().AddPeer(peer)
	}

	return node
}

func waitForMember(t *testing.T, cl Cluster, memberID peercore.ID, suffrage raft.ServerSuffrage, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			members, err := cl.Members()
			if err != nil {
				continue
			}
			for _, member := range members {
				if member.ID == memberID && member.Suffrage == suffrage {
					return nil
				}
			}
		}
	}
}

func waitForMemberAbsent(t *testing.T, cl Cluster, memberID peercore.ID, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			members, err := cl.Members()
			if err != nil {
				continue
			}
			found := false
			for _, member := range members {
				if member.ID == memberID {
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}
	}
}

func TestCluster_MultiNode_LeaderElection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create first node (will be bootstrap)
	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	// Create additional nodes that connect to node1
	node2 := newTestNode(t, node1Info)
	node3 := newTestNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "multi-node-test"

	// First node bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	// Wait for cluster1 to become leader
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	// Other nodes do NOT bootstrap - they will be added by the leader
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	cluster3, err := New(node3, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster3")
	defer cluster3.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")
	err = waitForMember(t, cluster1, node3.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node3 failed to join as voter")

	// Wait for all clusters to see the leader
	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	clusters := []Cluster{cluster1, cluster2, cluster3}

	for i, cl := range clusters {
		err := cl.WaitForLeader(ctx2)
		assert.NilError(t, err, "cluster%d failed to wait for leader", i+1)
	}

	// Verify all see the same leader
	leader1, err := cluster1.Leader()
	assert.NilError(t, err, "failed to get leader from cluster1")

	for i, cl := range clusters[1:] {
		leader, err := cl.Leader()
		assert.NilError(t, err, "cluster%d failed to get leader", i+2)
		assert.Equal(t, leader, leader1, "all clusters should agree on leader")
	}

	t.Logf("Leader: %s", leader1)

	// Set a value on the leader
	err = cluster1.Set("test-key", []byte("test-value"), 5*time.Second)
	assert.NilError(t, err, "failed to set on leader")

	// Wait for replication
	time.Sleep(1 * time.Second)

	// Verify value is replicated to followers
	for i, cl := range clusters[1:] {
		val, found := cl.Get("test-key")
		assert.Assert(t, found, "cluster%d: key not found after replication", i+2)
		assert.Equal(t, string(val), "test-value", "cluster%d: wrong value", i+2)
	}
}

func TestCluster_MultiNode_SendToNonLeader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "forward-test"

	// First node bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	// Second node does NOT bootstrap
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	// Wait for cluster2 to see leader
	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	// cluster2 should be follower
	assert.Assert(t, !cluster2.IsLeader(), "cluster2 should be follower")

	t.Logf("Sending Set to follower: %s", node2.ID())

	// Try to set on follower directly - should return ErrNotLeader
	err = cluster2.Set("follower-key", []byte("follower-value"), 5*time.Second)
	assert.ErrorIs(t, err, ErrNotLeader, "expected ErrNotLeader when calling Set on follower")

	// Use the client to send to the follower's stream service
	// This should forward to the leader
	client, err := NewClient(node1, namespace, nil)
	assert.NilError(t, err, "failed to create client")
	defer client.Close()

	err = client.Set("client-key", []byte("client-value"), 5*time.Second, node2.ID())
	assert.NilError(t, err, "client.Set to follower should forward to leader")

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify value was set (read from follower)
	val, found := cluster2.Get("client-key")
	assert.Assert(t, found, "key should exist after forwarded set")
	assert.Equal(t, string(val), "client-value")
}

func TestCluster_AutoBootstrapThenJoiners(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	namespace := "autobootstrap-joiners"

	// Start node1 alone; it should auto-bootstrap after 1s with no peers.
	node1 := newTestNode(t)

	start := time.Now()
	cluster1, err := New(
		node1,
		namespace,
		WithTimeouts(testTimeoutConfig()),
		WithBootstrapTimeout(1*time.Second),
	)
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader after auto-bootstrap")
	assert.Assert(t, time.Since(start) >= 900*time.Millisecond, "auto-bootstrap should wait ~1s")

	// Start joiners after leader is established.
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}
	node2 := newTestNode(t, node1Info)
	node3 := newTestNode(t, node1Info)

	// Ensure they can find/connect to node1.
	err = waitForConnected(t, node2, node1.ID(), 10*time.Second)
	assert.NilError(t, err, "node2 failed to connect to node1")
	err = waitForConnected(t, node3, node1.ID(), 10*time.Second)
	assert.NilError(t, err, "node3 failed to connect to node1")

	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(200*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	cluster3, err := New(node3, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(200*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster3")
	defer cluster3.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")
	err = waitForMember(t, cluster1, node3.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node3 failed to join as voter")

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	for i, cl := range []Cluster{cluster1, cluster2, cluster3} {
		err := cl.WaitForLeader(ctx2)
		assert.NilError(t, err, "cluster%d failed to wait for leader", i+1)
	}

	// Joiners should see the same leader.
	leader1, err := cluster1.Leader()
	assert.NilError(t, err, "failed to get leader from cluster1")
	leader2, err := cluster2.Leader()
	assert.NilError(t, err, "failed to get leader from cluster2")
	leader3, err := cluster3.Leader()
	assert.NilError(t, err, "failed to get leader from cluster3")

	assert.Equal(t, leader1, leader2, "cluster2 should agree on leader")
	assert.Equal(t, leader1, leader3, "cluster3 should agree on leader")
}

func TestCluster_MultiNode_DiscoverPeers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "discover-test"

	// First node bootstraps as the leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	// Wait for cluster1 to become leader
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be the leader")

	// Second node does NOT bootstrap - it will be added by the leader
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	// Wait for cluster2 to find the leader
	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	// Verify both clusters see the same leader
	leader1, err := cluster1.Leader()
	assert.NilError(t, err, "cluster1 failed to get leader")

	leader2, err := cluster2.Leader()
	assert.NilError(t, err, "cluster2 failed to get leader")

	assert.Equal(t, leader1, leader2, "both clusters should agree on leader")

	t.Logf("Both clusters agree on leader: %s", leader1)
}

func TestCluster_MultiNode_StreamServiceForwarding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "stream-forward-test"

	// First node bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	// Second node does NOT bootstrap
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	leaderCluster := cluster1.(*cluster)
	followerCluster := cluster2.(*cluster)

	t.Logf("Leader: %s", leaderCluster.node.ID())
	t.Logf("Follower: %s", followerCluster.node.ID())

	// Create a client
	client, err := NewClient(node1, namespace, nil)
	assert.NilError(t, err, "failed to create client")
	defer client.Close()

	// Send Set to follower - should be forwarded to leader
	err = client.Set("forwarded-key", []byte("forwarded-value"), 5*time.Second, followerCluster.node.ID())
	assert.NilError(t, err, "Set to follower should forward to leader")

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify value was set on leader
	val, found := leaderCluster.Get("forwarded-key")
	assert.Assert(t, found, "key should exist on leader after forwarded set")
	assert.Equal(t, string(val), "forwarded-value")

	// Also verify on follower
	val, found = followerCluster.Get("forwarded-key")
	assert.Assert(t, found, "key should be replicated to follower")
	assert.Equal(t, string(val), "forwarded-value")

	// Test Get from follower
	val, found, err = client.Get("forwarded-key", 0, followerCluster.node.ID())
	assert.NilError(t, err, "Get from follower should work")
	assert.Assert(t, found, "key should be found via client")
	assert.Equal(t, string(val), "forwarded-value")

	// Test Keys from follower
	keys, err := client.Keys("forwarded", followerCluster.node.ID())
	assert.NilError(t, err, "Keys from follower should work")
	assert.Assert(t, len(keys) >= 1, "should find at least one key with prefix")

	// Test Delete on follower (should forward)
	err = client.Delete("forwarded-key", 5*time.Second, followerCluster.node.ID())
	assert.NilError(t, err, "Delete on follower should forward to leader")

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify key is deleted
	_, found = leaderCluster.Get("forwarded-key")
	assert.Assert(t, !found, "key should be deleted after forwarded delete")
}

func TestCluster_MultiNode_ClientOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "client-ops-test"

	// First node bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	// Second node does NOT bootstrap
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	// Test Set directly via cluster (local operations)
	err = cluster1.Set("key1", []byte("value1"), 5*time.Second)
	assert.NilError(t, err, "Set to leader should succeed")

	err = cluster1.Set("key2", []byte("value2"), 5*time.Second)
	assert.NilError(t, err, "Set second key should succeed")

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Test Get via cluster
	val, found := cluster1.Get("key1")
	assert.Assert(t, found, "key1 should be found")
	assert.Equal(t, string(val), "value1")

	// Test Keys via cluster
	keys := cluster1.Keys("key")
	assert.Equal(t, len(keys), 2, "should find 2 keys with prefix 'key'")

	// Create client from node2 to test remote operations
	client, err := NewClient(node2, namespace, nil)
	assert.NilError(t, err, "failed to create client")
	defer client.Close()

	// Test Get via client to leader
	val, found, err = client.Get("key1", 0, node1.ID())
	assert.NilError(t, err, "Get via client should succeed")
	assert.Assert(t, found, "key1 should be found via client")
	assert.Equal(t, string(val), "value1")

	// Test Keys via client to leader
	keys, err = client.Keys("key", node1.ID())
	assert.NilError(t, err, "Keys via client should succeed")
	assert.Equal(t, len(keys), 2, "should find 2 keys via client")

	// Test Delete via cluster (local operation)
	err = cluster1.Delete("key1", 5*time.Second)
	assert.NilError(t, err, "Delete should succeed")

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify deletion via client
	_, found, err = client.Get("key1", 0, node1.ID())
	assert.NilError(t, err, "Get after delete should succeed")
	assert.Assert(t, !found, "key1 should not be found after delete")

	// key2 should still exist
	val, found, err = client.Get("key2", 0, node1.ID())
	assert.NilError(t, err, "Get key2 should succeed")
	assert.Assert(t, found, "key2 should still exist")
	assert.Equal(t, string(val), "value2")
}

func TestCluster_MultiNode_ClientGetWithBarrier(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "barrier-test"

	// First node bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	// Second node joins
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	// Create client from node2 to test remote operations with barrier
	client, err := NewClient(node2, namespace, nil)
	assert.NilError(t, err, "failed to create client")
	defer client.Close()

	// Set a value via leader
	err = cluster1.Set("barrier-test-key", []byte("barrier-test-value"), 5*time.Second)
	assert.NilError(t, err, "Set to leader should succeed")

	// Test Get without barrier - may return stale data on follower
	val, found, err := client.Get("barrier-test-key", 0, node1.ID())
	assert.NilError(t, err, "Get without barrier should succeed")
	// Note: Without barrier, might not see the value immediately due to replication lag

	// Test Get with barrier - should ensure consistency
	barrierNs := int64(2 * time.Second.Nanoseconds())
	val, found, err = client.Get("barrier-test-key", barrierNs, node1.ID())
	assert.NilError(t, err, "Get with barrier should succeed")
	assert.Assert(t, found, "key should be found with barrier")
	assert.Equal(t, string(val), "barrier-test-value", "value should match")

	// Set another value
	err = cluster1.Set("barrier-test-key2", []byte("barrier-test-value2"), 5*time.Second)
	assert.NilError(t, err, "Set second key should succeed")

	// Get with barrier should see the new value
	val, found, err = client.Get("barrier-test-key2", barrierNs, node1.ID())
	assert.NilError(t, err, "Get with barrier should succeed")
	assert.Assert(t, found, "key2 should be found with barrier")
	assert.Equal(t, string(val), "barrier-test-value2", "value2 should match")

	// Test Get with barrier at max timeout
	maxBarrierNs := int64(MaxGetHandlerBarrierTimeout.Nanoseconds())
	val, found, err = client.Get("barrier-test-key", maxBarrierNs, node1.ID())
	assert.NilError(t, err, "Get with max barrier should succeed")
	assert.Assert(t, found, "key should be found with max barrier")
	assert.Equal(t, string(val), "barrier-test-value", "value should match")

	// Test Get with invalid barrier (negative) - should fail before network call
	_, _, err = client.Get("barrier-test-key", -1, node1.ID())
	assert.Assert(t, err != nil, "Get with negative barrier should fail")
	assert.ErrorIs(t, err, ErrInvalidBarrier, "should return ErrInvalidBarrier")

	// Test Get with invalid barrier (exceeds max) - should fail before network call
	exceedsMaxBarrierNs := int64((MaxGetHandlerBarrierTimeout + time.Second).Nanoseconds())
	_, _, err = client.Get("barrier-test-key", exceedsMaxBarrierNs, node1.ID())
	assert.Assert(t, err != nil, "Get with barrier exceeding max should fail")
	assert.ErrorIs(t, err, ErrInvalidBarrier, "should return ErrInvalidBarrier")
}

func TestCluster_MultiNode_Apply_InvalidTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "apply-timeout-test"

	// First node bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	// Second node joins
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	// Prepare a command
	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "apply-timeout-key", Value: []byte("apply-timeout-value")},
	}
	data, _ := cbor.Marshal(cmd)

	// Test Apply with timeout = 0 on leader (should fail)
	_, err = cluster1.Apply(data, 0)
	assert.Assert(t, err != nil, "Apply with timeout=0 should fail")
	assert.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	// Test Apply with timeout < 0 on leader (should fail)
	_, err = cluster1.Apply(data, -1*time.Second)
	assert.Assert(t, err != nil, "Apply with negative timeout should fail")
	assert.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	// Test Apply with timeout > MaxApplyTimeout on leader (should fail)
	_, err = cluster1.Apply(data, MaxApplyTimeout+time.Second)
	assert.Assert(t, err != nil, "Apply with timeout > MaxApplyTimeout should fail")
	assert.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	// Test Apply with valid timeout on leader (should work)
	cmd2 := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "apply-timeout-key2", Value: []byte("apply-timeout-value2")},
	}
	data2, _ := cbor.Marshal(cmd2)
	_, err = cluster1.Apply(data2, time.Second)
	assert.NilError(t, err, "Apply with valid timeout should succeed")

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify the value was replicated to follower
	val, found := cluster2.Get("apply-timeout-key2")
	assert.Assert(t, found, "key should be found on follower")
	assert.Equal(t, string(val), "apply-timeout-value2", "value should match")

	// Test Apply with timeout = MaxApplyTimeout on leader (should work)
	cmd3 := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "apply-timeout-key3", Value: []byte("apply-timeout-value3")},
	}
	data3, _ := cbor.Marshal(cmd3)
	_, err = cluster1.Apply(data3, MaxApplyTimeout)
	assert.NilError(t, err, "Apply with timeout at MaxApplyTimeout should succeed")

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify the value was replicated
	val, found = cluster2.Get("apply-timeout-key3")
	assert.Assert(t, found, "key3 should be found on follower")
	assert.Equal(t, string(val), "apply-timeout-value3", "value3 should match")
}

// TestCluster_MultiCluster_SameNode tests that a single node can participate
// in multiple clusters with different namespaces without any collision
func TestCluster_MultiCluster_SameNode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create a single node that will join multiple clusters
	node := newTestNode(t)

	namespace1 := "cluster-alpha"
	namespace2 := "cluster-beta"
	namespace3 := "cluster-gamma"

	// Create three separate clusters on the SAME node with different namespaces
	cluster1, err := New(node, namespace1, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	cluster2, err := New(node, namespace2, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	cluster3, err := New(node, namespace3, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster3")
	defer cluster3.Close()

	// Wait for all clusters to elect leaders
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	assert.NilError(t, cluster1.WaitForLeader(ctx), "cluster1 failed to elect leader")
	assert.NilError(t, cluster2.WaitForLeader(ctx), "cluster2 failed to elect leader")
	assert.NilError(t, cluster3.WaitForLeader(ctx), "cluster3 failed to elect leader")

	// Each cluster should be leader (single-node clusters)
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")
	assert.Assert(t, cluster2.IsLeader(), "cluster2 should be leader")
	assert.Assert(t, cluster3.IsLeader(), "cluster3 should be leader")

	// Set different data in each cluster
	err = cluster1.Set("shared-key", []byte("cluster1-value"), 5*time.Second)
	assert.NilError(t, err, "cluster1 Set failed")

	err = cluster2.Set("shared-key", []byte("cluster2-value"), 5*time.Second)
	assert.NilError(t, err, "cluster2 Set failed")

	err = cluster3.Set("shared-key", []byte("cluster3-value"), 5*time.Second)
	assert.NilError(t, err, "cluster3 Set failed")

	// Verify each cluster has its own isolated data
	val1, found1 := cluster1.Get("shared-key")
	assert.Assert(t, found1, "cluster1 should have the key")
	assert.Equal(t, string(val1), "cluster1-value", "cluster1 has wrong value")

	val2, found2 := cluster2.Get("shared-key")
	assert.Assert(t, found2, "cluster2 should have the key")
	assert.Equal(t, string(val2), "cluster2-value", "cluster2 has wrong value")

	val3, found3 := cluster3.Get("shared-key")
	assert.Assert(t, found3, "cluster3 should have the key")
	assert.Equal(t, string(val3), "cluster3-value", "cluster3 has wrong value")

	// Set unique keys in each cluster
	err = cluster1.Set("alpha-only", []byte("alpha"), 5*time.Second)
	assert.NilError(t, err)

	err = cluster2.Set("beta-only", []byte("beta"), 5*time.Second)
	assert.NilError(t, err)

	err = cluster3.Set("gamma-only", []byte("gamma"), 5*time.Second)
	assert.NilError(t, err)

	// Verify keys don't leak between clusters
	_, found := cluster1.Get("beta-only")
	assert.Assert(t, !found, "cluster1 should NOT have beta-only key")

	_, found = cluster2.Get("alpha-only")
	assert.Assert(t, !found, "cluster2 should NOT have alpha-only key")

	_, found = cluster3.Get("alpha-only")
	assert.Assert(t, !found, "cluster3 should NOT have alpha-only key")

	// Delete in one cluster shouldn't affect others
	err = cluster1.Delete("shared-key", 5*time.Second)
	assert.NilError(t, err)

	_, found = cluster1.Get("shared-key")
	assert.Assert(t, !found, "cluster1 should NOT have shared-key after delete")

	// Other clusters should still have their data
	val2, found2 = cluster2.Get("shared-key")
	assert.Assert(t, found2, "cluster2 should still have shared-key")
	assert.Equal(t, string(val2), "cluster2-value")

	val3, found3 = cluster3.Get("shared-key")
	assert.Assert(t, found3, "cluster3 should still have shared-key")
	assert.Equal(t, string(val3), "cluster3-value")

	t.Logf("Successfully tested 3 isolated clusters on same node")
}

// TestCluster_MultiCluster_MultiNode tests multiple nodes each participating
// in multiple clusters with no collision
func TestCluster_MultiCluster_MultiNode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create two nodes
	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespaceA := "service-A"
	namespaceB := "service-B"

	// Create cluster A on both nodes
	clusterA1, err := New(node1, namespaceA, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create clusterA1")
	defer clusterA1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	assert.NilError(t, clusterA1.WaitForLeader(ctx), "clusterA1 failed to elect leader")

	clusterA2, err := New(node2, namespaceA, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create clusterA2")
	defer clusterA2.Close()

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()
	err = waitForMember(t, clusterA1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join cluster A as voter")
	assert.NilError(t, clusterA2.WaitForLeader(ctx2), "clusterA2 failed to wait for leader")

	// Create cluster B on both nodes (independent from cluster A)
	clusterB1, err := New(node1, namespaceB, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create clusterB1")
	defer clusterB1.Close()

	ctx3, cancel3 := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel3()
	assert.NilError(t, clusterB1.WaitForLeader(ctx3), "clusterB1 failed to elect leader")

	clusterB2, err := New(node2, namespaceB, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create clusterB2")
	defer clusterB2.Close()

	ctx4, cancel4 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel4()
	err = waitForMember(t, clusterB1, node2.ID(), raft.Voter, 30*time.Second)
	assert.NilError(t, err, "node2 failed to join cluster B as voter")
	assert.NilError(t, clusterB2.WaitForLeader(ctx4), "clusterB2 failed to wait for leader")

	// Set data in cluster A
	err = clusterA1.Set("service-data", []byte("from-service-A"), 5*time.Second)
	assert.NilError(t, err, "clusterA1 Set failed")

	// Set data in cluster B
	err = clusterB1.Set("service-data", []byte("from-service-B"), 5*time.Second)
	assert.NilError(t, err, "clusterB1 Set failed")

	// Wait for replication
	time.Sleep(1 * time.Second)

	// Verify cluster A data is isolated
	valA1, foundA1 := clusterA1.Get("service-data")
	assert.Assert(t, foundA1, "clusterA1 should have service-data")
	assert.Equal(t, string(valA1), "from-service-A")

	valA2, foundA2 := clusterA2.Get("service-data")
	assert.Assert(t, foundA2, "clusterA2 should have replicated service-data")
	assert.Equal(t, string(valA2), "from-service-A")

	// Verify cluster B data is isolated
	valB1, foundB1 := clusterB1.Get("service-data")
	assert.Assert(t, foundB1, "clusterB1 should have service-data")
	assert.Equal(t, string(valB1), "from-service-B")

	valB2, foundB2 := clusterB2.Get("service-data")
	assert.Assert(t, foundB2, "clusterB2 should have replicated service-data")
	assert.Equal(t, string(valB2), "from-service-B")

	// Verify clusters are truly independent
	members_A, _ := clusterA1.Members()
	members_B, _ := clusterB1.Members()

	t.Logf("Cluster A members: %d", len(members_A))
	t.Logf("Cluster B members: %d", len(members_B))

	assert.Equal(t, len(members_A), 2, "Cluster A should have 2 members")
	assert.Equal(t, len(members_B), 2, "Cluster B should have 2 members")

	t.Logf("Successfully tested multi-node multi-cluster isolation")
}

func TestCluster_RejoinAfterLeave(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}
	node2 := newTestNode(t, node1Info)

	time.Sleep(2 * time.Second)

	namespace := "rejoin-test"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	assert.NilError(t, cluster1.WaitForLeader(ctx), "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	// Remove node2 and close its cluster (leave).
	assert.NilError(t, cluster1.RemoveServer(node2.ID(), 10*time.Second), "failed to remove node2")
	assert.NilError(t, waitForMemberAbsent(t, cluster1, node2.ID(), 20*time.Second), "node2 still present after removal")
	assert.NilError(t, cluster2.Close(), "failed to close cluster2")

	// Join again with the same node while cluster1 is running.
	cluster2b, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to recreate cluster2")
	defer cluster2b.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to rejoin as voter")
}

// TestCluster_DiscoveryBasedBootstrap tests the discovery-based peer convergence
// where nodes discover each other and bootstrap together
func TestCluster_DiscoveryBasedBootstrap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create nodes that know about each other via peering
	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)
	node2Info := peercore.AddrInfo{ID: node2.ID(), Addrs: node2.Peer().Addrs()}

	// Also add node2 to node1's peering for bidirectional peering
	node1.Peering().AddPeer(node2Info)

	// Wait for peering to establish connections (peering handles connection automatically)
	err := waitForConnected(t, node1, node2.ID(), 30*time.Second)
	assert.NilError(t, err, "node1 did not connect to node2")
	err = waitForConnected(t, node2, node1.ID(), 30*time.Second)
	assert.NilError(t, err, "node2 did not connect to node1")

	// Verify nodes are actually connected
	node1Peers := node1.Peer().Network().Peers()
	node2Peers := node2.Peer().Network().Peers()
	t.Logf("Node1 connected peers: %d", len(node1Peers))
	t.Logf("Node2 connected peers: %d", len(node2Peers))

	assert.Assert(t, len(node1Peers) > 0, "node1 should have connected peers")
	assert.Assert(t, len(node2Peers) > 0, "node2 should have connected peers")

	namespace := "discovery-bootstrap-test"

	// Use a bootstrap timeout that allows discovery and exchange to work
	bootstrapTimeout := 3 * time.Second

	// Start clusters concurrently - they should discover each other
	var cluster1, cluster2 Cluster
	var err1, err2 error

	done := make(chan struct{})

	go func() {
		cluster1, err1 = New(node1, namespace,
			WithTimeouts(testTimeoutConfig()),
			WithBootstrapTimeout(bootstrapTimeout),
		)
		done <- struct{}{}
	}()

	go func() {
		cluster2, err2 = New(node2, namespace,
			WithTimeouts(testTimeoutConfig()),
			WithBootstrapTimeout(bootstrapTimeout),
		)
		done <- struct{}{}
	}()

	// Wait for both to complete
	<-done
	<-done

	if cluster1 != nil {
		defer cluster1.Close()
	}
	if cluster2 != nil {
		defer cluster2.Close()
	}

	assert.NilError(t, err1, "failed to create cluster1")
	assert.NilError(t, err2, "failed to create cluster2")

	// Wait for a leader to emerge
	leaderCtx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// At least one should become leader
	err = cluster1.WaitForLeader(leaderCtx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")

	err = cluster2.WaitForLeader(leaderCtx)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	// Both should see the same leader
	leader1, err := cluster1.Leader()
	assert.NilError(t, err, "failed to get leader from cluster1")

	leader2, err := cluster2.Leader()
	assert.NilError(t, err, "failed to get leader from cluster2")

	assert.Equal(t, leader1, leader2, "both nodes should agree on leader")

	t.Logf("Leader elected: %s", leader1)
	t.Logf("Node1 is leader: %v", cluster1.IsLeader())
	t.Logf("Node2 is leader: %v", cluster2.IsLeader())

	// Exactly one should be leader
	assert.Assert(t, cluster1.IsLeader() != cluster2.IsLeader(),
		"exactly one node should be leader")
}

// TestCluster_ThreeNodes_SimultaneousStart tests that 3 nodes starting at exactly
// the same time can properly bootstrap and elect a single leader
func TestCluster_ThreeNodes_SimultaneousStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create three nodes that will know about each other
	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)
	node2Info := peercore.AddrInfo{ID: node2.ID(), Addrs: node2.Peer().Addrs()}

	node3 := newTestNode(t, node1Info, node2Info)
	node3Info := peercore.AddrInfo{ID: node3.ID(), Addrs: node3.Peer().Addrs()}

	// Add all peers to each other for full mesh peering
	node1.Peering().AddPeer(node2Info)
	node1.Peering().AddPeer(node3Info)
	node2.Peering().AddPeer(node3Info)

	// Give peering service time to start establishing connections
	time.Sleep(2 * time.Second)

	// Wait for all connections to be established
	err := waitForConnected(t, node1, node2.ID(), 30*time.Second)
	assert.NilError(t, err, "node1 failed to connect to node2")
	err = waitForConnected(t, node1, node3.ID(), 30*time.Second)
	assert.NilError(t, err, "node1 failed to connect to node3")
	err = waitForConnected(t, node2, node3.ID(), 30*time.Second)
	assert.NilError(t, err, "node2 failed to connect to node3")

	namespace := "three-node-simultaneous"
	bootstrapTimeout := 3 * time.Second

	var cluster1, cluster2, cluster3 Cluster
	var err1, err2, err3 error
	done := make(chan struct{}, 3)

	// Start all three clusters simultaneously
	go func() {
		cluster1, err1 = New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(bootstrapTimeout))
		done <- struct{}{}
	}()
	go func() {
		cluster2, err2 = New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(bootstrapTimeout))
		done <- struct{}{}
	}()
	go func() {
		cluster3, err3 = New(node3, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(bootstrapTimeout))
		done <- struct{}{}
	}()

	// Wait for all to complete initialization
	for i := 0; i < 3; i++ {
		<-done
	}

	if cluster1 != nil {
		defer cluster1.Close()
	}
	if cluster2 != nil {
		defer cluster2.Close()
	}
	if cluster3 != nil {
		defer cluster3.Close()
	}

	assert.NilError(t, err1, "failed to create cluster1")
	assert.NilError(t, err2, "failed to create cluster2")
	assert.NilError(t, err3, "failed to create cluster3")

	// Wait for leader election on all nodes
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	err = cluster2.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster2 failed to wait for leader")
	err = cluster3.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster3 failed to wait for leader")

	// All nodes must see the same leader
	leader1, err := cluster1.Leader()
	assert.NilError(t, err, "cluster1 failed to get leader")
	leader2, err := cluster2.Leader()
	assert.NilError(t, err, "cluster2 failed to get leader")
	leader3, err := cluster3.Leader()
	assert.NilError(t, err, "cluster3 failed to get leader")

	assert.Equal(t, leader1, leader2, "cluster1 and cluster2 must agree on leader")
	assert.Equal(t, leader2, leader3, "cluster2 and cluster3 must agree on leader")

	// Count leaders - exactly one node must be leader
	leaderCount := 0
	if cluster1.IsLeader() {
		leaderCount++
	}
	if cluster2.IsLeader() {
		leaderCount++
	}
	if cluster3.IsLeader() {
		leaderCount++
	}
	assert.Equal(t, leaderCount, 1, "exactly one node must be leader")

	t.Logf("Leader elected among 3 simultaneous nodes: %s", leader1)
}

// TestCluster_LeaderCrashAndFailover tests that a cluster survives leader crash
// and elects a new leader, with data remaining accessible
func TestCluster_LeaderCrashAndFailover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create three nodes with full mesh connectivity
	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)
	node2Info := peercore.AddrInfo{ID: node2.ID(), Addrs: node2.Peer().Addrs()}

	node3 := newTestNode(t, node1Info, node2Info)
	node3Info := peercore.AddrInfo{ID: node3.ID(), Addrs: node3.Peer().Addrs()}

	// Establish full mesh: all nodes know about all other nodes
	node1.Peering().AddPeer(node2Info)
	node1.Peering().AddPeer(node3Info)
	node2.Peering().AddPeer(node3Info)

	// Give peering service time to start establishing connections
	time.Sleep(2 * time.Second)

	// Wait for all connections to be established
	err := waitForConnected(t, node1, node2.ID(), 30*time.Second)
	assert.NilError(t, err, "node1 failed to connect to node2")
	err = waitForConnected(t, node1, node3.ID(), 30*time.Second)
	assert.NilError(t, err, "node1 failed to connect to node3")
	err = waitForConnected(t, node2, node3.ID(), 30*time.Second)
	assert.NilError(t, err, "node2 failed to connect to node3")

	namespace := "leader-crash-failover"

	// Node1 bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 must be leader after force bootstrap")

	// Add followers
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	cluster3, err := New(node3, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster3")
	defer cluster3.Close()

	// Wait for all nodes to join
	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")
	err = waitForMember(t, cluster1, node3.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node3 failed to join as voter")

	// Verify all followers see the leader
	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()
	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to see leader")
	err = cluster3.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster3 failed to see leader")

	// Set data on leader BEFORE crash
	err = cluster1.Set("before-crash", []byte("important-data"), 5*time.Second)
	assert.NilError(t, err, "failed to set data before crash")

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify data is replicated to followers
	val, found := cluster2.Get("before-crash")
	assert.Assert(t, found, "data must be replicated to cluster2 before crash")
	assert.Equal(t, string(val), "important-data", "cluster2 data mismatch before crash")

	val, found = cluster3.Get("before-crash")
	assert.Assert(t, found, "data must be replicated to cluster3 before crash")
	assert.Equal(t, string(val), "important-data", "cluster3 data mismatch before crash")

	t.Log("Crashing leader (cluster1)...")

	// Crash the leader
	err = cluster1.Close()
	assert.NilError(t, err, "failed to close cluster1")

	// Wait for new leader election
	ctx3, cancel3 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel3()

	// Both remaining nodes should eventually see a new leader
	err = cluster2.WaitForLeader(ctx3)
	assert.NilError(t, err, "cluster2 must find new leader after crash")
	err = cluster3.WaitForLeader(ctx3)
	assert.NilError(t, err, "cluster3 must find new leader after crash")

	// Get new leader - should be either node2 or node3
	newLeader, err := cluster2.Leader()
	assert.NilError(t, err, "failed to get new leader from cluster2")

	// New leader must NOT be the crashed node
	assert.Assert(t, newLeader != node1.ID(), "new leader must not be the crashed node")

	// Both nodes must agree on the new leader
	newLeader3, err := cluster3.Leader()
	assert.NilError(t, err, "failed to get new leader from cluster3")
	assert.Equal(t, newLeader, newLeader3, "cluster2 and cluster3 must agree on new leader")

	// Data must still be accessible after leader crash
	val, found = cluster2.Get("before-crash")
	assert.Assert(t, found, "data must persist after leader crash (cluster2)")
	assert.Equal(t, string(val), "important-data", "data integrity failure after crash (cluster2)")

	val, found = cluster3.Get("before-crash")
	assert.Assert(t, found, "data must persist after leader crash (cluster3)")
	assert.Equal(t, string(val), "important-data", "data integrity failure after crash (cluster3)")

	// Exactly one of the remaining nodes must be leader
	leaderCount := 0
	if cluster2.IsLeader() {
		leaderCount++
	}
	if cluster3.IsLeader() {
		leaderCount++
	}
	assert.Equal(t, leaderCount, 1, "exactly one remaining node must be leader")

	t.Logf("New leader after crash: %s", newLeader)
}

// TestCluster_NodeRebootWithDataPersistence verifies that when a follower reboots,
// it catches up with the cluster and has access to all data.
// Uses a 3-node cluster so quorum is maintained when one node reboots.
func TestCluster_NodeRebootWithDataPersistence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create 3 nodes with full mesh connectivity (needed to maintain quorum during reboot)
	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)
	node2Info := peercore.AddrInfo{ID: node2.ID(), Addrs: node2.Peer().Addrs()}

	node3 := newTestNode(t, node1Info, node2Info)
	node3Info := peercore.AddrInfo{ID: node3.ID(), Addrs: node3.Peer().Addrs()}

	// Full mesh peering
	node1.Peering().AddPeer(node2Info)
	node1.Peering().AddPeer(node3Info)
	node2.Peering().AddPeer(node3Info)

	// Give peering service time to start establishing connections
	time.Sleep(2 * time.Second)

	// Wait for all connections
	err := waitForConnected(t, node1, node2.ID(), 30*time.Second)
	assert.NilError(t, err, "node1 failed to connect to node2")
	err = waitForConnected(t, node1, node3.ID(), 30*time.Second)
	assert.NilError(t, err, "node1 failed to connect to node3")
	err = waitForConnected(t, node2, node3.ID(), 30*time.Second)
	assert.NilError(t, err, "node2 failed to connect to node3")

	namespace := "node-reboot-persistence"

	// Node1 bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 must be leader")

	// Node2 and Node3 join
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")

	cluster3, err := New(node3, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster3")
	defer cluster3.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")
	err = waitForMember(t, cluster1, node3.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node3 failed to join as voter")

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()
	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to see leader")
	err = cluster3.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster3 failed to see leader")

	// Set data BEFORE reboot
	err = cluster1.Set("key-before-reboot", []byte("value-before"), 5*time.Second)
	assert.NilError(t, err, "failed to set key-before-reboot")

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify data on follower before reboot
	val, found := cluster2.Get("key-before-reboot")
	assert.Assert(t, found, "key-before-reboot must exist on cluster2 before reboot")
	assert.Equal(t, string(val), "value-before", "value mismatch before reboot")

	t.Log("Rebooting follower (cluster2)...")

	// Simulate reboot: close cluster2
	err = cluster2.Close()
	assert.NilError(t, err, "failed to close cluster2")

	// Set more data while follower is down (quorum maintained via node1 + node3)
	err = cluster1.Set("key-during-reboot", []byte("value-during"), 5*time.Second)
	assert.NilError(t, err, "failed to set key-during-reboot (should work with quorum from node1+node3)")

	// Wait for replication to node3
	time.Sleep(500 * time.Millisecond)

	// Verify data is on node3 while node2 is down
	val, found = cluster3.Get("key-during-reboot")
	assert.Assert(t, found, "key-during-reboot must be replicated to node3")
	assert.Equal(t, string(val), "value-during")

	// Recreate cluster2 on the same node (simulating reboot)
	cluster2b, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to recreate cluster2 after reboot")
	defer cluster2b.Close()

	// Wait for node2 to rejoin
	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to rejoin as voter after reboot")

	ctx3, cancel3 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel3()
	err = cluster2b.WaitForLeader(ctx3)
	assert.NilError(t, err, "cluster2b failed to see leader after reboot")

	// Wait for replication to catch up
	time.Sleep(1 * time.Second)

	// Verify data set BEFORE reboot is accessible
	val, found = cluster2b.Get("key-before-reboot")
	assert.Assert(t, found, "key-before-reboot must exist after reboot")
	assert.Equal(t, string(val), "value-before", "value mismatch for key-before-reboot after reboot")

	// Verify data set DURING reboot (while node was down) is replicated
	val, found = cluster2b.Get("key-during-reboot")
	assert.Assert(t, found, "key-during-reboot must be replicated after rejoin")
	assert.Equal(t, string(val), "value-during", "value mismatch for key-during-reboot after reboot")

	// Set data AFTER reboot to verify full functionality
	err = cluster1.Set("key-after-reboot", []byte("value-after"), 5*time.Second)
	assert.NilError(t, err, "failed to set key-after-reboot")

	time.Sleep(500 * time.Millisecond)

	val, found = cluster2b.Get("key-after-reboot")
	assert.Assert(t, found, "key-after-reboot must be replicated")
	assert.Equal(t, string(val), "value-after", "value mismatch for key-after-reboot")

	t.Log("Node reboot with data persistence test passed")
}

// TestCluster_StaggeredConcurrentStartup tests nodes starting at random offsets
// within a short window (simulating real network conditions)
func TestCluster_StaggeredConcurrentStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create three nodes
	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)
	node2Info := peercore.AddrInfo{ID: node2.ID(), Addrs: node2.Peer().Addrs()}

	node3 := newTestNode(t, node1Info, node2Info)
	node3Info := peercore.AddrInfo{ID: node3.ID(), Addrs: node3.Peer().Addrs()}

	// Full mesh peering
	node1.Peering().AddPeer(node2Info)
	node1.Peering().AddPeer(node3Info)
	node2.Peering().AddPeer(node3Info)

	// Give peering service time to start establishing connections
	time.Sleep(2 * time.Second)

	// Wait for connections with longer timeout
	err := waitForConnected(t, node1, node2.ID(), 30*time.Second)
	assert.NilError(t, err, "node1 failed to connect to node2")
	err = waitForConnected(t, node1, node3.ID(), 30*time.Second)
	assert.NilError(t, err, "node1 failed to connect to node3")
	err = waitForConnected(t, node2, node3.ID(), 30*time.Second)
	assert.NilError(t, err, "node2 failed to connect to node3")

	namespace := "staggered-startup"
	bootstrapTimeout := 3 * time.Second

	var cluster1, cluster2, cluster3 Cluster
	var err1, err2, err3 error
	done := make(chan struct{}, 3)

	// Start nodes with staggered delays (0ms, 200ms, 500ms)
	go func() {
		cluster1, err1 = New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(bootstrapTimeout))
		done <- struct{}{}
	}()

	go func() {
		time.Sleep(200 * time.Millisecond) // Staggered start
		cluster2, err2 = New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(bootstrapTimeout))
		done <- struct{}{}
	}()

	go func() {
		time.Sleep(500 * time.Millisecond) // More staggered
		cluster3, err3 = New(node3, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(bootstrapTimeout))
		done <- struct{}{}
	}()

	// Wait for all
	for i := 0; i < 3; i++ {
		<-done
	}

	if cluster1 != nil {
		defer cluster1.Close()
	}
	if cluster2 != nil {
		defer cluster2.Close()
	}
	if cluster3 != nil {
		defer cluster3.Close()
	}

	assert.NilError(t, err1, "failed to create cluster1")
	assert.NilError(t, err2, "failed to create cluster2")
	assert.NilError(t, err3, "failed to create cluster3")

	// Wait for leader on all
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	err = cluster2.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster2 failed to wait for leader")
	err = cluster3.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster3 failed to wait for leader")

	// All must agree on leader
	leader1, err := cluster1.Leader()
	assert.NilError(t, err, "cluster1 failed to get leader")
	leader2, err := cluster2.Leader()
	assert.NilError(t, err, "cluster2 failed to get leader")
	leader3, err := cluster3.Leader()
	assert.NilError(t, err, "cluster3 failed to get leader")

	assert.Equal(t, leader1, leader2, "cluster1 and cluster2 must agree on leader")
	assert.Equal(t, leader2, leader3, "cluster2 and cluster3 must agree on leader")

	// Exactly one leader
	leaderCount := 0
	if cluster1.IsLeader() {
		leaderCount++
	}
	if cluster2.IsLeader() {
		leaderCount++
	}
	if cluster3.IsLeader() {
		leaderCount++
	}
	assert.Equal(t, leaderCount, 1, "exactly one node must be leader")

	// Verify cluster is functional - write and read
	var leaderCluster Cluster
	if cluster1.IsLeader() {
		leaderCluster = cluster1
	} else if cluster2.IsLeader() {
		leaderCluster = cluster2
	} else {
		leaderCluster = cluster3
	}

	err = leaderCluster.Set("staggered-key", []byte("staggered-value"), 5*time.Second)
	assert.NilError(t, err, "failed to set on leader")

	time.Sleep(500 * time.Millisecond)

	// All nodes must see the data
	val, found := cluster1.Get("staggered-key")
	assert.Assert(t, found, "cluster1 must see staggered-key")
	assert.Equal(t, string(val), "staggered-value")

	val, found = cluster2.Get("staggered-key")
	assert.Assert(t, found, "cluster2 must see staggered-key")
	assert.Equal(t, string(val), "staggered-value")

	val, found = cluster3.Get("staggered-key")
	assert.Assert(t, found, "cluster3 must see staggered-key")
	assert.Equal(t, string(val), "staggered-value")

	t.Logf("Staggered startup test passed, leader: %s", leader1)
}

// TestCluster_LeaderRebootAndRejoin tests the scenario where the leader crashes,
// a new leader is elected, and then the old leader comes back and rejoins as follower
func TestCluster_LeaderRebootAndRejoin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create 3 nodes with full mesh connectivity
	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newTestNode(t, node1Info)
	node2Info := peercore.AddrInfo{ID: node2.ID(), Addrs: node2.Peer().Addrs()}

	node3 := newTestNode(t, node1Info, node2Info)
	node3Info := peercore.AddrInfo{ID: node3.ID(), Addrs: node3.Peer().Addrs()}

	// Establish full mesh peering
	node1.Peering().AddPeer(node2Info)
	node1.Peering().AddPeer(node3Info)
	node2.Peering().AddPeer(node3Info)

	// Give peering service time to start establishing connections
	time.Sleep(2 * time.Second)

	// Wait for all connections
	err := waitForConnected(t, node1, node2.ID(), 30*time.Second)
	assert.NilError(t, err, "node1 failed to connect to node2")
	err = waitForConnected(t, node1, node3.ID(), 30*time.Second)
	assert.NilError(t, err, "node1 failed to connect to node3")
	err = waitForConnected(t, node2, node3.ID(), 30*time.Second)
	assert.NilError(t, err, "node2 failed to connect to node3")

	namespace := "leader-reboot-rejoin"

	// Node1 bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 must be leader")

	// Add followers
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	cluster3, err := New(node3, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster3")
	defer cluster3.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join")
	err = waitForMember(t, cluster1, node3.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node3 failed to join")

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()
	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to see leader")
	err = cluster3.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster3 failed to see leader")

	// Set data before leader crash
	err = cluster1.Set("before-leader-crash", []byte("original-value"), 5*time.Second)
	assert.NilError(t, err, "failed to set before-leader-crash")
	time.Sleep(500 * time.Millisecond)

	t.Log("Crashing original leader (cluster1)...")

	// Crash the leader
	err = cluster1.Close()
	assert.NilError(t, err, "failed to close cluster1")

	// Wait for new leader
	ctx3, cancel3 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel3()
	err = cluster2.WaitForLeader(ctx3)
	assert.NilError(t, err, "cluster2 must find new leader")
	err = cluster3.WaitForLeader(ctx3)
	assert.NilError(t, err, "cluster3 must find new leader")

	// Identify the new leader
	newLeader, err := cluster2.Leader()
	assert.NilError(t, err, "failed to get new leader")
	assert.Assert(t, newLeader != node1.ID(), "new leader must not be crashed node")

	// Set data while old leader is down
	var newLeaderCluster Cluster
	if cluster2.IsLeader() {
		newLeaderCluster = cluster2
	} else {
		newLeaderCluster = cluster3
	}

	err = newLeaderCluster.Set("during-leader-down", []byte("new-leader-value"), 5*time.Second)
	assert.NilError(t, err, "new leader failed to set data")
	time.Sleep(500 * time.Millisecond)

	t.Log("Rebooting old leader (cluster1)...")

	// Old leader comes back
	cluster1b, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to recreate cluster1")
	defer cluster1b.Close()

	// Wait for old leader to rejoin
	err = waitForMember(t, newLeaderCluster, node1.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node1 failed to rejoin after reboot")

	ctx4, cancel4 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel4()
	err = cluster1b.WaitForLeader(ctx4)
	assert.NilError(t, err, "cluster1b failed to see leader")

	// Old leader must now be a follower
	assert.Assert(t, !cluster1b.IsLeader(), "old leader must rejoin as follower")

	// Verify old leader agrees on current leader
	leader1b, err := cluster1b.Leader()
	assert.NilError(t, err, "cluster1b failed to get leader")
	assert.Equal(t, leader1b, newLeader, "old leader must agree on current leader")

	// Wait for replication
	time.Sleep(1 * time.Second)

	// Old leader must have all data
	val, found := cluster1b.Get("before-leader-crash")
	assert.Assert(t, found, "old leader must have data from before crash")
	assert.Equal(t, string(val), "original-value")

	val, found = cluster1b.Get("during-leader-down")
	assert.Assert(t, found, "old leader must have data set during its downtime")
	assert.Equal(t, string(val), "new-leader-value")

	// Set data after old leader rejoins
	err = newLeaderCluster.Set("after-rejoin", []byte("post-rejoin-value"), 5*time.Second)
	assert.NilError(t, err, "failed to set after-rejoin")
	time.Sleep(500 * time.Millisecond)

	val, found = cluster1b.Get("after-rejoin")
	assert.Assert(t, found, "old leader must receive new data after rejoin")
	assert.Equal(t, string(val), "post-rejoin-value")

	t.Logf("Leader reboot and rejoin test passed. New leader: %s, Old leader rejoined as follower: %s", newLeader, node1.ID())
}
