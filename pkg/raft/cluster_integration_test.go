package raft

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/keypair"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"gotest.tools/v3/assert"
)

func waitForConnected(t *testing.T, node Node, peerID peercore.ID, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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

// newRealNode creates a real libp2p node for testing
func newRealNode(t *testing.T, bootstrapPeers ...peercore.AddrInfo) Node {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	dir := t.TempDir()
	port := 12000 + rand.Intn(20000)

	node, err := taupeer.New(
		ctx,
		dir,
		keypair.NewRaw(),
		nil, // swarm key
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port)},
		nil,   // swarm announce
		true,  // notPublic
		false, // don't bootstrap to default peers
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

func TestCluster_MultiNode_LeaderElection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create first node (will be bootstrap)
	node1 := newRealNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	// Create additional nodes that connect to node1
	node2 := newRealNode(t, node1Info)
	node3 := newRealNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "/raft/multi-node-test"

	// First node bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	// Wait for cluster1 to become leader
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	// Other nodes do NOT bootstrap - they will be added by the leader
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(100*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	cluster3, err := New(node3, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(100*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster3")
	defer cluster3.Close()

	// Leader adds nodes as voters
	err = cluster1.AddVoter(node2.ID(), 10*time.Second)
	assert.NilError(t, err, "failed to add node2 as voter")

	err = cluster1.AddVoter(node3.ID(), 10*time.Second)
	assert.NilError(t, err, "failed to add node3 as voter")

	// Wait for all clusters to see the leader
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
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

	node1 := newRealNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newRealNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "/raft/forward-test"

	// First node bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	// Second node does NOT bootstrap
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(100*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	// Leader adds node2 as voter
	err = cluster1.AddVoter(node2.ID(), 10*time.Second)
	assert.NilError(t, err, "failed to add node2 as voter")

	// Wait for cluster2 to see leader
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()

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
	client, err := NewClient(node1, namespace)
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

func TestCluster_MultiNode_DiscoverPeers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	node1 := newRealNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newRealNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "/raft/discover-test"

	// First node bootstraps as the leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	// Wait for cluster1 to become leader
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be the leader")

	// Second node does NOT bootstrap - it will be added by the leader
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(100*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	// Leader adds node2 as a voter
	err = cluster1.AddVoter(node2.ID(), 10*time.Second)
	assert.NilError(t, err, "failed to add node2 as voter")

	// Wait for cluster2 to find the leader
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()

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

	node1 := newRealNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newRealNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "/raft/stream-forward-test"

	// First node bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	// Second node does NOT bootstrap
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(100*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	// Leader adds node2
	err = cluster1.AddVoter(node2.ID(), 10*time.Second)
	assert.NilError(t, err, "failed to add node2 as voter")

	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	leaderCluster := cluster1.(*cluster)
	followerCluster := cluster2.(*cluster)

	t.Logf("Leader: %s", leaderCluster.node.ID())
	t.Logf("Follower: %s", followerCluster.node.ID())

	// Create a client
	client, err := NewClient(node1, namespace)
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
	val, found, err = client.Get("forwarded-key", followerCluster.node.ID())
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

	node1 := newRealNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newRealNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "/raft/client-ops-test"

	// First node bootstraps as leader
	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	// Second node does NOT bootstrap
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(100*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	// Leader adds node2
	err = cluster1.AddVoter(node2.ID(), 10*time.Second)
	assert.NilError(t, err, "failed to add node2 as voter")

	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()

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
	client, err := NewClient(node2, namespace)
	assert.NilError(t, err, "failed to create client")
	defer client.Close()

	// Test Get via client to leader
	val, found, err = client.Get("key1", node1.ID())
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
	_, found, err = client.Get("key1", node1.ID())
	assert.NilError(t, err, "Get after delete should succeed")
	assert.Assert(t, !found, "key1 should not be found after delete")

	// key2 should still exist
	val, found, err = client.Get("key2", node1.ID())
	assert.NilError(t, err, "Get key2 should succeed")
	assert.Assert(t, found, "key2 should still exist")
	assert.Equal(t, string(val), "value2")
}

// TestCluster_MultiCluster_SameNode tests that a single node can participate
// in multiple clusters with different namespaces without any collision
func TestCluster_MultiCluster_SameNode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create a single node that will join multiple clusters
	node := newRealNode(t)

	namespace1 := "/raft/cluster-alpha"
	namespace2 := "/raft/cluster-beta"
	namespace3 := "/raft/cluster-gamma"

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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	node1 := newRealNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newRealNode(t, node1Info)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespaceA := "/raft/service-A"
	namespaceB := "/raft/service-B"

	// Create cluster A on both nodes
	clusterA1, err := New(node1, namespaceA, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create clusterA1")
	defer clusterA1.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	assert.NilError(t, clusterA1.WaitForLeader(ctx), "clusterA1 failed to elect leader")

	clusterA2, err := New(node2, namespaceA, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(100*time.Millisecond))
	assert.NilError(t, err, "failed to create clusterA2")
	defer clusterA2.Close()

	// Add node2 to cluster A
	err = clusterA1.AddVoter(node2.ID(), 10*time.Second)
	assert.NilError(t, err, "failed to add node2 to cluster A")

	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	assert.NilError(t, clusterA2.WaitForLeader(ctx2), "clusterA2 failed to wait for leader")

	// Create cluster B on both nodes (independent from cluster A)
	clusterB1, err := New(node1, namespaceB, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create clusterB1")
	defer clusterB1.Close()

	ctx3, cancel3 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel3()
	assert.NilError(t, clusterB1.WaitForLeader(ctx3), "clusterB1 failed to elect leader")

	clusterB2, err := New(node2, namespaceB, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(100*time.Millisecond))
	assert.NilError(t, err, "failed to create clusterB2")
	defer clusterB2.Close()

	// Add node2 to cluster B
	err = clusterB1.AddVoter(node2.ID(), 10*time.Second)
	assert.NilError(t, err, "failed to add node2 to cluster B")

	ctx4, cancel4 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel4()
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

// TestCluster_DiscoveryBasedBootstrap tests the discovery-based peer convergence
// where nodes discover each other and bootstrap together
func TestCluster_DiscoveryBasedBootstrap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create nodes that know about each other via peering
	node1 := newRealNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}

	node2 := newRealNode(t, node1Info)
	node2Info := peercore.AddrInfo{ID: node2.ID(), Addrs: node2.Peer().Addrs()}

	// Also add node2 to node1's peering for bidirectional peering
	node1.Peering().AddPeer(node2Info)

	// Wait for peering to establish connections (peering handles connection automatically)
	err := waitForConnected(t, node1, node2.ID(), 10*time.Second)
	assert.NilError(t, err, "node1 did not connect to node2")
	err = waitForConnected(t, node2, node1.ID(), 10*time.Second)
	assert.NilError(t, err, "node2 did not connect to node1")

	// Verify nodes are actually connected
	node1Peers := node1.Peer().Network().Peers()
	node2Peers := node2.Peer().Network().Peers()
	t.Logf("Node1 connected peers: %d", len(node1Peers))
	t.Logf("Node2 connected peers: %d", len(node2Peers))

	assert.Assert(t, len(node1Peers) > 0, "node1 should have connected peers")
	assert.Assert(t, len(node2Peers) > 0, "node2 should have connected peers")

	namespace := "/raft/discovery-bootstrap-test"

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
	leaderCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
