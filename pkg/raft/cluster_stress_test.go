//go:build stress

package raft

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"github.com/libp2p/go-libp2p/core/network"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/keypair"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"gotest.tools/v3/assert"
)

const stressTestLogPath = "/tmp/stresstest.log"

var (
	logFile     *os.File
	logFileOnce sync.Once
	logMu       sync.Mutex
)

// stressLog writes a log entry to /tmp/stresstest.log
func stressLog(format string, args ...interface{}) {
	logFileOnce.Do(func() {
		var err error
		logFile, err = os.OpenFile(stressTestLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			// If we can't open the log file, just return silently
			panic(err)
		}
	})
	if logFile == nil {
		return
	}

	logMu.Lock()
	defer logMu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(logFile, "[%s] %s\n", timestamp, msg)
	logFile.Sync()
}

// stressTimeoutConfig returns slightly more relaxed timeouts for stress scenarios
func stressTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		HeartbeatTimeout:   100 * time.Millisecond,
		ElectionTimeout:    100 * time.Millisecond,
		CommitTimeout:      50 * time.Millisecond,
		LeaderLeaseTimeout: 50 * time.Millisecond,
		SnapshotInterval:   1 * time.Minute,
		SnapshotThreshold:  1000,
	}
}

// bootstrapTimeoutForNodes returns appropriate bootstrap timeout based on node count
// More nodes = more time needed for peer discovery to converge
func bootstrapTimeoutForNodes(nodeCount int) time.Duration {
	// Base: 1 second per node, minimum 3 seconds, maximum 15 seconds
	timeout := time.Duration(nodeCount) * time.Second
	if timeout < 3*time.Second {
		timeout = 3 * time.Second
	}
	if timeout > 15*time.Second {
		timeout = 15 * time.Second
	}
	return timeout
}

// createStressNodes creates N connected nodes with full mesh peering
// NOTE: Nodes must be manually closed - do not use t.Cleanup() as it closes them
// while clusters are still active, causing "swarm closed" errors
func createStressNodes(t *testing.T, count int) []taupeer.Node {
	stressLog("createStressNodes: Starting creation of %d nodes", count)
	nodes := make([]taupeer.Node, count)
	nodeInfos := make([]peercore.AddrInfo, count)

	// Create all nodes first
	for i := 0; i < count; i++ {
		ctx, cancel := context.WithCancel(t.Context())
		// Don't use t.Cleanup for cancel - we'll manage it manually
		_ = cancel // Will be cleaned up when nodes are closed

		dir := t.TempDir()
		start := time.Now()
		node, err := taupeer.New(
			ctx,
			dir,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		if err != nil {
			stressLog("createStressNodes: ERROR creating node %d: %v", i, err)
			assert.NilError(t, err, "failed to create node %d", i)
		}
		// Don't use t.Cleanup - nodes will be closed manually after clusters

		err = node.WaitForSwarm(5 * time.Second)
		if err != nil {
			stressLog("createStressNodes: ERROR waiting for swarm node %d: %v", i, err)
			assert.NilError(t, err, "failed to wait for swarm for node %d", i)
		}

		nodes[i] = node
		nodeInfos[i] = peercore.AddrInfo{ID: node.ID(), Addrs: node.Peer().Addrs()}
		stressLog("createStressNodes: Node %d created: %s (took %v)", i, node.ID().String(), time.Since(start))
	}

	// Establish full mesh peering
	stressLog("createStressNodes: Establishing full mesh peering")
	for i := 0; i < count; i++ {
		for j := 0; j < count; j++ {
			if i != j {
				nodes[i].Peering().AddPeer(nodeInfos[j])
			}
		}
	}
	stressLog("createStressNodes: Completed creation of %d nodes", count)
	return nodes
}

// waitAllConnected waits for all nodes to establish mesh connectivity
func waitAllConnected(t *testing.T, nodes []taupeer.Node, timeout time.Duration) error {
	stressLog("waitAllConnected: Waiting for %d nodes to establish mesh connectivity (timeout: %v)", len(nodes), timeout)
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	start := time.Now()
	checkCount := 0
	for {
		select {
		case <-ctx.Done():
			stressLog("waitAllConnected: TIMEOUT after %v - not all nodes connected", time.Since(start))
			return ctx.Err()
		case <-ticker.C:
			checkCount++
			allConnected := true
			connectedCount := 0
			totalConnections := len(nodes) * (len(nodes) - 1)
			for i := 0; i < len(nodes); i++ {
				for j := 0; j < len(nodes); j++ {
					if i != j {
						state := nodes[i].Peer().Network().Connectedness(nodes[j].ID())
						if state == network.Connected {
							connectedCount++
						} else {
							allConnected = false
						}
					}
				}
			}
			if checkCount%10 == 0 {
				stressLog("waitAllConnected: Check %d: %d/%d connections established", checkCount, connectedCount, totalConnections)
			}
			if allConnected {
				stressLog("waitAllConnected: SUCCESS - all %d nodes connected (took %v)", len(nodes), time.Since(start))
				return nil
			}
		}
	}
}

// verifyLeaderElected verifies exactly one leader exists and all nodes agree
func verifyLeaderElected(t *testing.T, clusters []Cluster, timeout time.Duration) (peercore.ID, error) {
	stressLog("verifyLeaderElected: Verifying leader election for %d clusters (timeout: %v)", len(clusters), timeout)
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	start := time.Now()
	// Wait for all clusters to see a leader
	for i, cl := range clusters {
		err := cl.WaitForLeader(ctx)
		if err != nil {
			stressLog("verifyLeaderElected: ERROR - cluster %d failed to wait for leader: %v", i, err)
			return "", fmt.Errorf("cluster %d failed to wait for leader: %w", i, err)
		}
		stressLog("verifyLeaderElected: Cluster %d sees a leader", i)
	}

	// Count leaders
	leaderCount := 0
	var leaderID peercore.ID
	leaderIndices := []int{}
	for i, cl := range clusters {
		if cl.IsLeader() {
			leaderCount++
			leaderIndices = append(leaderIndices, i)
			var err error
			leaderID, err = cl.Leader()
			if err != nil {
				stressLog("verifyLeaderElected: ERROR - cluster %d is leader but failed to get leader ID: %v", i, err)
				return "", fmt.Errorf("cluster %d is leader but failed to get leader ID: %w", i, err)
			}
		}
	}

	stressLog("verifyLeaderElected: Found %d leaders (indices: %v)", leaderCount, leaderIndices)
	if leaderCount != 1 {
		stressLog("verifyLeaderElected: ERROR - expected exactly 1 leader, found %d", leaderCount)
		return "", fmt.Errorf("expected exactly 1 leader, found %d", leaderCount)
	}

	// Verify all clusters agree on the leader
	for i, cl := range clusters {
		leader, err := cl.Leader()
		if err != nil {
			stressLog("verifyLeaderElected: ERROR - cluster %d failed to get leader: %v", i, err)
			return "", fmt.Errorf("cluster %d failed to get leader: %w", i, err)
		}
		if leader != leaderID {
			stressLog("verifyLeaderElected: ERROR - cluster %d disagrees on leader: got %s, expected %s", i, leader, leaderID)
			return "", fmt.Errorf("cluster %d disagrees on leader: got %s, expected %s", i, leader, leaderID)
		}
	}

	stressLog("verifyLeaderElected: SUCCESS - Leader %s elected, all %d clusters agree (took %v)", leaderID.String(), len(clusters), time.Since(start))
	return leaderID, nil
}

// getLeaderID returns the elected leader's peer ID
func getLeaderID(clusters []Cluster) (peercore.ID, error) {
	for _, cl := range clusters {
		if cl.IsLeader() {
			return cl.Leader()
		}
	}
	// If no leader found, try to get from any cluster
	if len(clusters) > 0 {
		return clusters[0].Leader()
	}
	return "", fmt.Errorf("no clusters provided")
}

// verifyAllVoters verifies all nodes are voters in the cluster
func verifyAllVoters(t *testing.T, clusters []Cluster, nodeIDs []peercore.ID, timeout time.Duration) error {
	stressLog("verifyAllVoters: Verifying all %d nodes are voters (timeout: %v)", len(nodeIDs), timeout)
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	start := time.Now()
	checkCount := 0
	for {
		select {
		case <-ctx.Done():
			stressLog("verifyAllVoters: TIMEOUT after %v - not all nodes are voters", time.Since(start))
			return ctx.Err()
		case <-ticker.C:
			checkCount++
			// Check from the leader's perspective
			var leaderCluster Cluster
			for _, cl := range clusters {
				if cl.IsLeader() {
					leaderCluster = cl
					break
				}
			}
			if leaderCluster == nil {
				if checkCount%10 == 0 {
					stressLog("verifyAllVoters: Check %d: No leader found yet", checkCount)
				}
				continue
			}

			members, err := leaderCluster.Members()
			if err != nil {
				if checkCount%10 == 0 {
					stressLog("verifyAllVoters: Check %d: Failed to get members: %v", checkCount, err)
				}
				continue
			}

			// Check all node IDs are present as voters
			allPresent := true
			missingNodes := []string{}
			for _, nodeID := range nodeIDs {
				found := false
				for _, member := range members {
					if member.ID == nodeID && member.Suffrage == raft.Voter {
						found = true
						break
					}
				}
				if !found {
					allPresent = false
					missingNodes = append(missingNodes, nodeID.String())
				}
			}

			if checkCount%10 == 0 {
				stressLog("verifyAllVoters: Check %d: %d/%d members present, missing: %v", checkCount, len(members), len(nodeIDs), missingNodes)
			}

			if allPresent && len(members) == len(nodeIDs) {
				stressLog("verifyAllVoters: SUCCESS - all %d nodes are voters (took %v)", len(nodeIDs), time.Since(start))
				return nil
			}
		}
	}
}

// verifyRaftStates verifies that exactly one node is Leader and all others are Followers
func verifyRaftStates(t *testing.T, clusters []Cluster) error {
	stressLog("verifyRaftStates: Verifying Raft states for %d clusters", len(clusters))
	leaderCount := 0
	followerCount := 0
	otherStates := make(map[string]int)

	for i, cl := range clusters {
		state := cl.State()
		stateStr := state.String()
		switch state {
		case raft.Leader:
			leaderCount++
		case raft.Follower:
			followerCount++
		default:
			otherStates[stateStr]++
			stressLog("verifyRaftStates: WARNING - cluster %d in unexpected state: %s", i, stateStr)
		}
	}

	if leaderCount != 1 {
		return fmt.Errorf("expected exactly 1 leader, found %d (followers: %d, other: %v)", leaderCount, followerCount, otherStates)
	}

	expectedFollowers := len(clusters) - 1
	if followerCount != expectedFollowers {
		return fmt.Errorf("expected %d followers, found %d (leader: %d, other: %v)", expectedFollowers, followerCount, leaderCount, otherStates)
	}

	stressLog("verifyRaftStates: SUCCESS - 1 Leader, %d Followers", followerCount)
	return nil
}

// verifyConfigurationConsistency verifies all nodes see the same cluster configuration
func verifyConfigurationConsistency(t *testing.T, clusters []Cluster) error {
	stressLog("verifyConfigurationConsistency: Verifying configuration consistency across %d clusters", len(clusters))
	if len(clusters) == 0 {
		return fmt.Errorf("no clusters provided")
	}

	// Get configuration from all clusters
	configs := make([][]Member, len(clusters))
	for i, cl := range clusters {
		members, err := cl.Members()
		if err != nil {
			return fmt.Errorf("cluster %d failed to get members: %w", i, err)
		}
		configs[i] = members
	}

	// Compare all configurations with the first one
	baseConfig := configs[0]
	baseConfigMap := make(map[string]Member)
	for _, m := range baseConfig {
		baseConfigMap[m.ID.String()] = m
	}

	for i := 1; i < len(configs); i++ {
		if len(configs[i]) != len(baseConfig) {
			return fmt.Errorf("cluster %d has %d members, but cluster 0 has %d", i, len(configs[i]), len(baseConfig))
		}

		for _, member := range configs[i] {
			baseMember, exists := baseConfigMap[member.ID.String()]
			if !exists {
				return fmt.Errorf("cluster %d has member %s not found in cluster 0", i, member.ID)
			}
			if member.Suffrage != baseMember.Suffrage {
				return fmt.Errorf("cluster %d member %s has suffrage %v, but cluster 0 has %v", i, member.ID, member.Suffrage, baseMember.Suffrage)
			}
		}
	}

	stressLog("verifyConfigurationConsistency: SUCCESS - all %d clusters have consistent configuration (%d members)", len(clusters), len(baseConfig))
	return nil
}

// verifyClusterStability verifies the cluster is stable (leader doesn't change, no state changes)
func verifyClusterStability(t *testing.T, clusters []Cluster, duration time.Duration) error {
	stressLog("verifyClusterStability: Verifying cluster stability for %v", duration)
	start := time.Now()

	// Get initial state
	initialLeader, err := getLeaderID(clusters)
	if err != nil {
		return fmt.Errorf("failed to get initial leader: %w", err)
	}

	initialStates := make([]raft.RaftState, len(clusters))
	for i, cl := range clusters {
		initialStates[i] = cl.State()
	}

	// Monitor for changes
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	checkCount := 0
	for time.Since(start) < duration {
		select {
		case <-ticker.C:
			checkCount++

			// Check leader hasn't changed
			currentLeader, err := getLeaderID(clusters)
			if err != nil {
				return fmt.Errorf("check %d: failed to get leader: %w", checkCount, err)
			}
			if currentLeader != initialLeader {
				return fmt.Errorf("check %d: leader changed from %s to %s", checkCount, initialLeader, currentLeader)
			}

			// Check states haven't changed (at least leader/follower counts)
			leaderCount := 0
			for i, cl := range clusters {
				state := cl.State()
				if state == raft.Leader {
					leaderCount++
					if initialStates[i] != raft.Leader {
						return fmt.Errorf("check %d: cluster %d became leader (was %s)", checkCount, i, initialStates[i])
					}
				}
			}
			if leaderCount != 1 {
				return fmt.Errorf("check %d: expected 1 leader, found %d", checkCount, leaderCount)
			}

			if checkCount%10 == 0 {
				stressLog("verifyClusterStability: Check %d: stable (leader: %s)", checkCount, currentLeader)
			}
		}
	}

	stressLog("verifyClusterStability: SUCCESS - cluster stable for %v (%d checks)", duration, checkCount)
	return nil
}

// verifyFSMCoherence verifies that FSM state is coherent across all nodes
// It writes values on the leader, uses Barrier to ensure replication, then reads from all nodes
func verifyFSMCoherence(t *testing.T, clusters []Cluster, testPrefix string, numKeys int) error {
	stressLog("verifyFSMCoherence: Starting FSM coherence test with %d keys (prefix: %s)", numKeys, testPrefix)

	// Find the leader
	var leaderCluster Cluster
	var leaderIdx int
	for i, cl := range clusters {
		if cl.IsLeader() {
			leaderCluster = cl
			leaderIdx = i
			break
		}
	}
	if leaderCluster == nil {
		return fmt.Errorf("no leader found")
	}
	stressLog("verifyFSMCoherence: Leader is cluster %d", leaderIdx)

	// Write test values on the leader
	testData := make(map[string][]byte)
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("%s/key-%d", testPrefix, i)
		value := []byte(fmt.Sprintf("value-%d-%d", i, time.Now().UnixNano()))
		testData[key] = value

		err := leaderCluster.Set(key, value, 10*time.Second)
		if err != nil {
			return fmt.Errorf("failed to set key %s on leader: %w", key, err)
		}
	}
	stressLog("verifyFSMCoherence: Wrote %d keys on leader", numKeys)

	// Use Barrier to ensure all writes are committed and replicated
	err := leaderCluster.Barrier(10 * time.Second)
	if err != nil {
		return fmt.Errorf("barrier failed: %w", err)
	}
	stressLog("verifyFSMCoherence: Barrier completed successfully")

	// Give a small window for followers to apply the committed logs
	time.Sleep(100 * time.Millisecond)

	// Verify all nodes can read the correct values
	for i, cl := range clusters {
		for key, expectedValue := range testData {
			actualValue, found := cl.Get(key)
			if !found {
				return fmt.Errorf("cluster %d: key %s not found", i, key)
			}
			if string(actualValue) != string(expectedValue) {
				return fmt.Errorf("cluster %d: key %s has wrong value: expected %q, got %q",
					i, key, string(expectedValue), string(actualValue))
			}
		}
	}
	stressLog("verifyFSMCoherence: SUCCESS - all %d clusters have coherent FSM state (%d keys verified)", len(clusters), numKeys)
	return nil
}

// verifyFSMCoherenceWithDelete verifies FSM coherence including delete operations
func verifyFSMCoherenceWithDelete(t *testing.T, clusters []Cluster, testPrefix string) error {
	stressLog("verifyFSMCoherenceWithDelete: Starting FSM coherence test with delete (prefix: %s)", testPrefix)

	// Find the leader
	var leaderCluster Cluster
	for _, cl := range clusters {
		if cl.IsLeader() {
			leaderCluster = cl
			break
		}
	}
	if leaderCluster == nil {
		return fmt.Errorf("no leader found")
	}

	// Write a key
	key := fmt.Sprintf("%s/delete-test", testPrefix)
	value := []byte("to-be-deleted")

	err := leaderCluster.Set(key, value, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to set key: %w", err)
	}

	// Barrier and verify it exists on all nodes
	if err := leaderCluster.Barrier(10 * time.Second); err != nil {
		return fmt.Errorf("barrier after set failed: %w", err)
	}
	time.Sleep(50 * time.Millisecond)

	for i, cl := range clusters {
		if _, found := cl.Get(key); !found {
			return fmt.Errorf("cluster %d: key not found after set", i)
		}
	}
	stressLog("verifyFSMCoherenceWithDelete: Key set and verified on all nodes")

	// Delete the key
	err = leaderCluster.Delete(key, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to delete key: %w", err)
	}

	// Barrier and verify it's deleted on all nodes
	if err := leaderCluster.Barrier(10 * time.Second); err != nil {
		return fmt.Errorf("barrier after delete failed: %w", err)
	}
	time.Sleep(50 * time.Millisecond)

	for i, cl := range clusters {
		if _, found := cl.Get(key); found {
			return fmt.Errorf("cluster %d: key still exists after delete", i)
		}
	}

	stressLog("verifyFSMCoherenceWithDelete: SUCCESS - delete operation replicated correctly")
	return nil
}

// verifyClusterHealth performs comprehensive cluster health checks
func verifyClusterHealth(t *testing.T, clusters []Cluster, nodeIDs []peercore.ID, stabilityDuration time.Duration) error {
	stressLog("verifyClusterHealth: Starting comprehensive health check")

	// 1. Verify exactly one leader
	leaderID, err := verifyLeaderElected(t, clusters, 10*time.Second)
	if err != nil {
		return fmt.Errorf("leader election check failed: %w", err)
	}
	stressLog("verifyClusterHealth: Leader check passed: %s", leaderID)

	// 2. Verify Raft states (1 Leader, rest Followers)
	if err := verifyRaftStates(t, clusters); err != nil {
		return fmt.Errorf("raft states check failed: %w", err)
	}
	stressLog("verifyClusterHealth: Raft states check passed")

	// 3. Verify all nodes are voters
	if err := verifyAllVoters(t, clusters, nodeIDs, 10*time.Second); err != nil {
		return fmt.Errorf("voters check failed: %w", err)
	}
	stressLog("verifyClusterHealth: Voters check passed")

	// 4. Verify configuration consistency
	if err := verifyConfigurationConsistency(t, clusters); err != nil {
		return fmt.Errorf("configuration consistency check failed: %w", err)
	}
	stressLog("verifyClusterHealth: Configuration consistency check passed")

	// 5. Verify cluster stability
	if stabilityDuration > 0 {
		if err := verifyClusterStability(t, clusters, stabilityDuration); err != nil {
			return fmt.Errorf("cluster stability check failed: %w", err)
		}
		stressLog("verifyClusterHealth: Cluster stability check passed")
	}

	// 6. Verify FSM coherence (writes replicate correctly)
	testPrefix := fmt.Sprintf("health-check-%d", time.Now().UnixNano())
	if err := verifyFSMCoherence(t, clusters, testPrefix, 5); err != nil {
		return fmt.Errorf("FSM coherence check failed: %w", err)
	}
	stressLog("verifyClusterHealth: FSM coherence check passed")

	stressLog("verifyClusterHealth: SUCCESS - all health checks passed")
	return nil
}

// TestStressCluster_ConcurrentJoin_5Nodes tests concurrent join with 5 nodes
func TestStressCluster_ConcurrentJoin_5Nodes(t *testing.T) {
	testConcurrentJoin(t, 5, 5)
}

// TestStressCluster_ConcurrentJoin_10Nodes tests concurrent join with 10 nodes
func TestStressCluster_ConcurrentJoin_10Nodes(t *testing.T) {
	testConcurrentJoin(t, 10, 5)
}

// TestStressCluster_ConcurrentJoin_20Nodes tests concurrent join with 20 nodes
func TestStressCluster_ConcurrentJoin_20Nodes(t *testing.T) {
	testConcurrentJoin(t, 20, 5)
}

func testConcurrentJoin(t *testing.T, nodeCount int, iterations int) {
	stressLog("=== testConcurrentJoin START: %d nodes, %d iterations ===", nodeCount, iterations)
	leaderDistribution := make(map[string]int)

	for iter := 0; iter < iterations; iter++ {
		iterStart := time.Now()
		stressLog("--- Iteration %d/%d START: Creating %d nodes ---", iter+1, iterations, nodeCount)
		t.Logf("Iteration %d/%d: Creating %d nodes", iter+1, iterations, nodeCount)

		// Create nodes with full mesh
		nodes := createStressNodes(t, nodeCount)
		nodeIDs := make([]peercore.ID, nodeCount)
		for i, node := range nodes {
			nodeIDs[i] = node.ID()
		}

		// Wait for mesh connectivity
		err := waitAllConnected(t, nodes, 30*time.Second)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: mesh connectivity: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "failed to establish mesh connectivity")
		}

		namespace := fmt.Sprintf("stress-concurrent-%d-%d", nodeCount, iter)
		stressLog("Iteration %d/%d: Creating clusters with namespace: %s", iter+1, iterations, namespace)

		// Create clusters concurrently
		clusters := make([]Cluster, nodeCount)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var createErr error
		clusterStart := time.Now()

		for i := 0; i < nodeCount; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				bootTimeout := bootstrapTimeoutForNodes(nodeCount)
				stressLog("Iteration %d/%d: Creating cluster for node %d", iter+1, iterations, idx)
				cl, err := New(
					nodes[idx],
					namespace,
					WithTimeouts(stressTimeoutConfig()),
					WithBootstrapTimeout(bootTimeout),
				)
				if err != nil {
					stressLog("Iteration %d/%d: ERROR creating cluster for node %d: %v", iter+1, iterations, idx, err)
					mu.Lock()
					if createErr == nil {
						createErr = err
					}
					mu.Unlock()
				} else {
					clusters[idx] = cl
					stressLog("Iteration %d/%d: SUCCESS creating cluster for node %d", iter+1, iterations, idx)
				}
			}(i)
		}

		wg.Wait()
		stressLog("Iteration %d/%d: All clusters created (took %v)", iter+1, iterations, time.Since(clusterStart))
		if createErr != nil {
			stressLog("--- Iteration %d/%d FAILED: cluster creation error: %v ---", iter+1, iterations, createErr)
			assert.NilError(t, createErr, "failed to create clusters")
		}

		// Cleanup: Close clusters first, then nodes (with delay to let Raft finish)
		// Capture nodes and clusters by value to avoid loop variable issues
		iterNodes := nodes
		iterClusters := clusters
		iterNum := iter + 1
		defer func() {
			stressLog("Iteration %d/%d: Starting cleanup", iterNum, iterations)
			// Close all clusters first
			for i, cl := range iterClusters {
				if cl != nil {
					stressLog("Iteration %d/%d: Closing cluster %d", iterNum, iterations, i)
					cl.Close()
				}
			}
			// Give Raft time to finish cleanup
			time.Sleep(200 * time.Millisecond)
			// Close all nodes
			for i, node := range iterNodes {
				if node != nil {
					stressLog("Iteration %d/%d: Closing node %d", iterNum, iterations, i)
					node.Close()
				}
			}
			stressLog("Iteration %d/%d: Cleanup complete", iterNum, iterations)
		}()

		// Verify leader elected - scale timeout with node count
		leaderTimeout := time.Duration(nodeCount) * 500 * time.Millisecond
		if leaderTimeout < 10*time.Second {
			leaderTimeout = 10 * time.Second
		}
		if leaderTimeout > 30*time.Second {
			leaderTimeout = 30 * time.Second
		}
		leaderID, err := verifyLeaderElected(t, clusters, leaderTimeout)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: leader election: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "failed to verify leader election")
		}

		// Verify all nodes are voters - scale timeout with node count
		voterTimeout := time.Duration(nodeCount) * 500 * time.Millisecond
		if voterTimeout < 10*time.Second {
			voterTimeout = 10 * time.Second
		}
		if voterTimeout > 20*time.Second {
			voterTimeout = 20 * time.Second
		}
		err = verifyAllVoters(t, clusters, nodeIDs, voterTimeout)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: voter verification: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "failed to verify all nodes are voters")
		}

		// Stricter health checks
		stressLog("--- Iteration %d/%d: Running strict health checks ---", iter+1, iterations)

		// Verify Raft states (1 Leader, rest Followers)
		err = verifyRaftStates(t, clusters)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: Raft states check: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "raft states check failed")
		}

		// Verify configuration consistency
		err = verifyConfigurationConsistency(t, clusters)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: Configuration consistency: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "configuration consistency check failed")
		}

		// Verify cluster stability (500ms)
		err = verifyClusterStability(t, clusters, 500*time.Millisecond)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: Cluster stability: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "cluster stability check failed")
		}

		// Verify FSM coherence - write values and verify they replicate
		fsmPrefix := fmt.Sprintf("concurrent-%d-%d", nodeCount, iter)
		err = verifyFSMCoherence(t, clusters, fsmPrefix, 10)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: FSM coherence: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "FSM coherence check failed")
		}

		// Track leader distribution
		leaderDistribution[leaderID.String()]++

		stressLog("--- Iteration %d/%d SUCCESS: Leader %s elected, health checks passed, FSM coherent (took %v) ---", iter+1, iterations, leaderID.String(), time.Since(iterStart))
		t.Logf("Iteration %d: Leader elected: %s", iter+1, leaderID)
	}

	// Log leader distribution
	stressLog("=== testConcurrentJoin COMPLETE: Leader distribution across %d iterations ===", iterations)
	t.Logf("Leader distribution across %d iterations:", iterations)
	for leaderID, count := range leaderDistribution {
		stressLog("  Leader %s: %d times (%.1f%%)", leaderID, count, float64(count)/float64(iterations)*100)
		t.Logf("  %s: %d times", leaderID, count)
	}
}

// TestStressCluster_SequentialJoin_5Nodes tests sequential join with 5 nodes
func TestStressCluster_SequentialJoin_5Nodes(t *testing.T) {
	testSequentialJoin(t, 5, 5)
}

// TestStressCluster_SequentialJoin_10Nodes tests sequential join with 10 nodes
func TestStressCluster_SequentialJoin_10Nodes(t *testing.T) {
	testSequentialJoin(t, 10, 5)
}

// TestStressCluster_SequentialJoin_20Nodes tests sequential join with 20 nodes
func TestStressCluster_SequentialJoin_20Nodes(t *testing.T) {
	testSequentialJoin(t, 20, 5)
}

func testSequentialJoin(t *testing.T, nodeCount int, iterations int) {
	stressLog("=== testSequentialJoin START: %d nodes, %d iterations ===", nodeCount, iterations)
	leaderDistribution := make(map[string]int)

	for iter := 0; iter < iterations; iter++ {
		iterStart := time.Now()
		stressLog("--- Iteration %d/%d START: Creating %d nodes ---", iter+1, iterations, nodeCount)
		t.Logf("Iteration %d/%d: Creating %d nodes", iter+1, iterations, nodeCount)

		// Create nodes with full mesh
		nodes := createStressNodes(t, nodeCount)
		nodeIDs := make([]peercore.ID, nodeCount)
		for i, node := range nodes {
			nodeIDs[i] = node.ID()
		}

		// Wait for mesh connectivity
		err := waitAllConnected(t, nodes, 30*time.Second)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: mesh connectivity: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "failed to establish mesh connectivity")
		}

		namespace := fmt.Sprintf("stress-sequential-%d-%d", nodeCount, iter)
		clusters := make([]Cluster, nodeCount)

		// First node starts the cluster (via discovery bootstrap)
		bootTimeout := bootstrapTimeoutForNodes(nodeCount)
		stressLog("Iteration %d/%d: Creating first cluster node", iter+1, iterations)
		clusters[0], err = New(
			nodes[0],
			namespace,
			WithTimeouts(stressTimeoutConfig()),
			WithBootstrapTimeout(bootTimeout),
		)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: first cluster creation: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "failed to create first cluster")
		}

		// Wait for leader to be elected
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		err = clusters[0].WaitForLeader(ctx)
		cancel()
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: wait for leader: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "failed to elect leader")
		}
		stressLog("Iteration %d/%d: Leader elected", iter+1, iterations)

		// Remaining nodes join sequentially
		// Scale timeout based on node count - larger clusters need more time
		joinTimeout := time.Duration(nodeCount) * 500 * time.Millisecond
		if joinTimeout < 10*time.Second {
			joinTimeout = 10 * time.Second
		}
		if joinTimeout > 30*time.Second {
			joinTimeout = 30 * time.Second
		}

		for i := 1; i < nodeCount; i++ {
			joinStart := time.Now()
			stressLog("Iteration %d/%d: Node %d joining cluster (timeout: %v)", iter+1, iterations, i, joinTimeout)
			clusters[i], err = New(
				nodes[i],
				namespace,
				WithTimeouts(stressTimeoutConfig()),
				WithBootstrapTimeout(bootTimeout),
			)
			if err != nil {
				stressLog("Iteration %d/%d: ERROR creating cluster for node %d: %v", iter+1, iterations, i, err)
				assert.NilError(t, err, "failed to create cluster for node %d", i)
			}

			// Wait for node to join - scale timeout with cluster size
			err = waitForMember(t, clusters[0], nodes[i].ID(), raft.Voter, joinTimeout)
			if err != nil {
				// Get current members for debugging
				members, _ := clusters[0].Members()
				stressLog("Iteration %d/%d: ERROR node %d failed to join after %v (current members: %d): %v", iter+1, iterations, i, time.Since(joinStart), len(members), err)
				assert.NilError(t, err, "node %d failed to join as voter", i)
			}

			// Verify cluster size
			members, err := clusters[0].Members()
			if err != nil {
				stressLog("Iteration %d/%d: ERROR getting members: %v", iter+1, iterations, err)
				assert.NilError(t, err, "failed to get members")
			}
			stressLog("Iteration %d/%d: Node %d joined, cluster size: %d (took %v)", iter+1, iterations, i, len(members), time.Since(joinStart))
			assert.Equal(t, len(members), i+1, "cluster should have %d members", i+1)

			// Small delay between joins
			time.Sleep(200 * time.Millisecond)
		}

		// Cleanup: Close clusters first, then nodes (with delay to let Raft finish)
		// Capture nodes and clusters by value to avoid loop variable issues
		iterNodes := nodes
		iterClusters := clusters
		iterNum := iter + 1
		defer func() {
			stressLog("Iteration %d/%d: Starting cleanup", iterNum, iterations)
			// Close all clusters first
			for i, cl := range iterClusters {
				if cl != nil {
					stressLog("Iteration %d/%d: Closing cluster %d", iterNum, iterations, i)
					cl.Close()
				}
			}
			// Give Raft time to finish cleanup
			time.Sleep(200 * time.Millisecond)
			// Close all nodes
			for i, node := range iterNodes {
				if node != nil {
					stressLog("Iteration %d/%d: Closing node %d", iterNum, iterations, i)
					node.Close()
				}
			}
			stressLog("Iteration %d/%d: Cleanup complete", iterNum, iterations)
		}()

		// Verify leader still exists - scale timeout with node count
		leaderTimeout := time.Duration(nodeCount) * 500 * time.Millisecond
		if leaderTimeout < 10*time.Second {
			leaderTimeout = 10 * time.Second
		}
		if leaderTimeout > 30*time.Second {
			leaderTimeout = 30 * time.Second
		}
		leaderID, err := verifyLeaderElected(t, clusters, leaderTimeout)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: leader verification: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "failed to verify leader after sequential joins")
		}

		// Verify all nodes are voters - scale timeout with node count
		voterTimeout := time.Duration(nodeCount) * 500 * time.Millisecond
		if voterTimeout < 10*time.Second {
			voterTimeout = 10 * time.Second
		}
		if voterTimeout > 20*time.Second {
			voterTimeout = 20 * time.Second
		}
		err = verifyAllVoters(t, clusters, nodeIDs, voterTimeout)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: voter verification: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "failed to verify all nodes are voters")
		}

		// Stricter health checks
		stressLog("--- Iteration %d/%d: Running strict health checks ---", iter+1, iterations)

		// Verify Raft states (1 Leader, rest Followers)
		err = verifyRaftStates(t, clusters)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: Raft states check: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "raft states check failed")
		}

		// Verify configuration consistency
		err = verifyConfigurationConsistency(t, clusters)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: Configuration consistency: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "configuration consistency check failed")
		}

		// Verify cluster stability (500ms)
		err = verifyClusterStability(t, clusters, 500*time.Millisecond)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: Cluster stability: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "cluster stability check failed")
		}

		// Verify FSM coherence - write values and verify they replicate
		fsmPrefix := fmt.Sprintf("sequential-%d-%d", nodeCount, iter)
		err = verifyFSMCoherence(t, clusters, fsmPrefix, 10)
		if err != nil {
			stressLog("--- Iteration %d/%d FAILED: FSM coherence: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "FSM coherence check failed")
		}

		// Track leader distribution
		leaderDistribution[leaderID.String()]++

		stressLog("--- Iteration %d/%d SUCCESS: Leader %s, All %d nodes joined, FSM coherent (took %v) ---", iter+1, iterations, leaderID.String(), nodeCount, time.Since(iterStart))
		t.Logf("Iteration %d: Leader: %s, All %d nodes joined", iter+1, leaderID, nodeCount)
	}

	// Log leader distribution
	stressLog("=== testSequentialJoin COMPLETE: Leader distribution across %d iterations ===", iterations)
	t.Logf("Leader distribution across %d iterations:", iterations)
	for leaderID, count := range leaderDistribution {
		stressLog("  Leader %s: %d times (%.1f%%)", leaderID, count, float64(count)/float64(iterations)*100)
		t.Logf("  %s: %d times", leaderID, count)
	}
}

// TestStressCluster_PhasedJoin_5Nodes tests phased join (half + half) with 5 nodes
func TestStressCluster_PhasedJoin_5Nodes(t *testing.T) {
	testPhasedJoin(t, 5)
}

// TestStressCluster_PhasedJoin_10Nodes tests phased join (half + half) with 10 nodes
func TestStressCluster_PhasedJoin_10Nodes(t *testing.T) {
	testPhasedJoin(t, 10)
}

// TestStressCluster_PhasedJoin_20Nodes tests phased join (half + half) with 20 nodes
func TestStressCluster_PhasedJoin_20Nodes(t *testing.T) {
	testPhasedJoin(t, 20)
}

func testPhasedJoin(t *testing.T, nodeCount int) {
	stressLog("=== testPhasedJoin START: %d nodes ===", nodeCount)
	testStart := time.Now()
	// Create nodes with full mesh
	nodes := createStressNodes(t, nodeCount)
	nodeIDs := make([]peercore.ID, nodeCount)
	for i, node := range nodes {
		nodeIDs[i] = node.ID()
	}

	// Wait for mesh connectivity
	err := waitAllConnected(t, nodes, 30*time.Second)
	if err != nil {
		stressLog("testPhasedJoin FAILED: mesh connectivity: %v", err)
		assert.NilError(t, err, "failed to establish mesh connectivity")
	}

	namespace := fmt.Sprintf("stress-phased-%d", nodeCount)
	firstHalf := nodeCount / 2
	secondHalf := nodeCount - firstHalf

	stressLog("testPhasedJoin: Phase 1: Starting first half (%d nodes)", firstHalf)
	t.Logf("Phase 1: Starting first half (%d nodes)", firstHalf)

	// Phase 1: First half starts together
	clusters := make([]Cluster, nodeCount)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var createErr error
	phase1Start := time.Now()
	bootTimeout := bootstrapTimeoutForNodes(nodeCount)

	for i := 0; i < firstHalf; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			stressLog("testPhasedJoin: Phase 1: Creating cluster for node %d", idx)
			cl, err := New(
				nodes[idx],
				namespace,
				WithTimeouts(stressTimeoutConfig()),
				WithBootstrapTimeout(bootTimeout),
			)
			if err != nil {
				stressLog("testPhasedJoin: Phase 1: ERROR creating cluster for node %d: %v", idx, err)
				mu.Lock()
				if createErr == nil {
					createErr = err
				}
				mu.Unlock()
			} else {
				clusters[idx] = cl
			}
		}(i)
	}

	wg.Wait()
	stressLog("testPhasedJoin: Phase 1: All clusters created (took %v)", time.Since(phase1Start))
	if createErr != nil {
		stressLog("testPhasedJoin FAILED: Phase 1 cluster creation: %v", createErr)
		assert.NilError(t, createErr, "failed to create first half clusters")
	}

	// Cleanup: Close clusters first, then nodes (with delay to let Raft finish)
	defer func() {
		stressLog("testPhasedJoin: Starting cleanup")
		// Close all clusters first
		for i, cl := range clusters {
			if cl != nil {
				stressLog("testPhasedJoin: Closing cluster %d", i)
				cl.Close()
			}
		}
		// Give Raft time to finish cleanup
		time.Sleep(500 * time.Millisecond)
		// Close all nodes
		for i, node := range nodes {
			if node != nil {
				stressLog("testPhasedJoin: Closing node %d", i)
				node.Close()
			}
		}
		stressLog("testPhasedJoin: Cleanup complete")
	}()

	// Wait for first half to stabilize - scale timeout with node count
	phase1Timeout := time.Duration(firstHalf) * 500 * time.Millisecond
	if phase1Timeout < 10*time.Second {
		phase1Timeout = 10 * time.Second
	}
	if phase1Timeout > 30*time.Second {
		phase1Timeout = 30 * time.Second
	}
	leaderID, err := verifyLeaderElected(t, clusters[:firstHalf], phase1Timeout)
	if err != nil {
		stressLog("testPhasedJoin FAILED: Phase 1 leader verification: %v", err)
		assert.NilError(t, err, "failed to verify leader in first half")
	}

	voterTimeout1 := time.Duration(firstHalf) * 500 * time.Millisecond
	if voterTimeout1 < 10*time.Second {
		voterTimeout1 = 10 * time.Second
	}
	if voterTimeout1 > 20*time.Second {
		voterTimeout1 = 20 * time.Second
	}
	err = verifyAllVoters(t, clusters[:firstHalf], nodeIDs[:firstHalf], voterTimeout1)
	if err != nil {
		stressLog("testPhasedJoin FAILED: Phase 1 voter verification: %v", err)
		assert.NilError(t, err, "failed to verify first half are all voters")
	}

	// Stricter health checks for Phase 1
	stressLog("testPhasedJoin: Running strict health checks after Phase 1")

	// Verify Raft states (1 Leader, rest Followers)
	err = verifyRaftStates(t, clusters[:firstHalf])
	if err != nil {
		stressLog("testPhasedJoin FAILED: Phase 1 Raft states check: %v", err)
		assert.NilError(t, err, "raft states check failed after Phase 1")
	}

	// Verify configuration consistency
	err = verifyConfigurationConsistency(t, clusters[:firstHalf])
	if err != nil {
		stressLog("testPhasedJoin FAILED: Phase 1 configuration consistency: %v", err)
		assert.NilError(t, err, "configuration consistency check failed after Phase 1")
	}

	stressLog("testPhasedJoin: Phase 1 complete: Leader %s, %d nodes joined, health checks passed (took %v)", leaderID.String(), firstHalf, time.Since(phase1Start))
	t.Logf("Phase 1 complete: Leader %s, %d nodes joined", leaderID, firstHalf)

	// Small delay before second phase
	time.Sleep(500 * time.Millisecond)

	stressLog("testPhasedJoin: Phase 2: Starting second half (%d nodes)", secondHalf)
	t.Logf("Phase 2: Starting second half (%d nodes)", secondHalf)
	phase2Start := time.Now()

	// Phase 2: Second half joins existing cluster
	for i := firstHalf; i < nodeCount; i++ {
		joinStart := time.Now()
		stressLog("testPhasedJoin: Phase 2: Node %d joining cluster", i)
		var err error
		clusters[i], err = New(
			nodes[i],
			namespace,
			WithTimeouts(stressTimeoutConfig()),
			WithBootstrapTimeout(bootTimeout),
		)
		if err != nil {
			stressLog("testPhasedJoin: Phase 2: ERROR creating cluster for node %d: %v", i, err)
			assert.NilError(t, err, "failed to create cluster for node %d", i)
		}

		// Wait for node to join - scale timeout with node count
		joinTimeout := time.Duration(nodeCount) * 500 * time.Millisecond
		if joinTimeout < 10*time.Second {
			joinTimeout = 10 * time.Second
		}
		if joinTimeout > 30*time.Second {
			joinTimeout = 30 * time.Second
		}
		err = waitForMember(t, clusters[0], nodes[i].ID(), raft.Voter, joinTimeout)
		if err != nil {
			stressLog("testPhasedJoin: Phase 2: ERROR node %d failed to join: %v", i, err)
			assert.NilError(t, err, "node %d failed to join as voter", i)
		}
		stressLog("testPhasedJoin: Phase 2: Node %d joined (took %v)", i, time.Since(joinStart))

		// Small delay between joins
		time.Sleep(200 * time.Millisecond)
	}

	// Verify final state - scale timeout with node count
	phase2Timeout := time.Duration(nodeCount) * 500 * time.Millisecond
	if phase2Timeout < 10*time.Second {
		phase2Timeout = 10 * time.Second
	}
	if phase2Timeout > 30*time.Second {
		phase2Timeout = 30 * time.Second
	}
	leaderID, err = verifyLeaderElected(t, clusters, phase2Timeout)
	if err != nil {
		stressLog("testPhasedJoin FAILED: Phase 2 leader verification: %v", err)
		assert.NilError(t, err, "failed to verify leader after second phase")
	}

	voterTimeout2 := time.Duration(nodeCount) * 500 * time.Millisecond
	if voterTimeout2 < 10*time.Second {
		voterTimeout2 = 10 * time.Second
	}
	if voterTimeout2 > 20*time.Second {
		voterTimeout2 = 20 * time.Second
	}
	err = verifyAllVoters(t, clusters, nodeIDs, voterTimeout2)
	if err != nil {
		stressLog("testPhasedJoin FAILED: Phase 2 voter verification: %v", err)
		assert.NilError(t, err, "failed to verify all nodes are voters")
	}

	// Stricter health checks
	stressLog("testPhasedJoin: Running strict health checks after Phase 2")

	// Verify Raft states (1 Leader, rest Followers)
	err = verifyRaftStates(t, clusters)
	if err != nil {
		stressLog("testPhasedJoin FAILED: Raft states check: %v", err)
		assert.NilError(t, err, "raft states check failed")
	}

	// Verify configuration consistency
	err = verifyConfigurationConsistency(t, clusters)
	if err != nil {
		stressLog("testPhasedJoin FAILED: Configuration consistency: %v", err)
		assert.NilError(t, err, "configuration consistency check failed")
	}

	// Verify cluster stability (500ms)
	err = verifyClusterStability(t, clusters, 500*time.Millisecond)
	if err != nil {
		stressLog("testPhasedJoin FAILED: Cluster stability: %v", err)
		assert.NilError(t, err, "cluster stability check failed")
	}

	// Verify FSM coherence - write values and verify they replicate
	fsmPrefix := fmt.Sprintf("phased-%d", nodeCount)
	err = verifyFSMCoherence(t, clusters, fsmPrefix, 10)
	if err != nil {
		stressLog("testPhasedJoin FAILED: FSM coherence: %v", err)
		assert.NilError(t, err, "FSM coherence check failed")
	}

	// Also test delete operation for phased join
	err = verifyFSMCoherenceWithDelete(t, clusters, fsmPrefix)
	if err != nil {
		stressLog("testPhasedJoin FAILED: FSM coherence with delete: %v", err)
		assert.NilError(t, err, "FSM coherence with delete check failed")
	}

	stressLog("testPhasedJoin: Phase 2 complete: Leader %s, all %d nodes joined, FSM coherent (took %v)", leaderID.String(), nodeCount, time.Since(phase2Start))
	stressLog("=== testPhasedJoin COMPLETE: All %d nodes joined (total time: %v) ===", nodeCount, time.Since(testStart))
	t.Logf("Phase 2 complete: Leader %s, all %d nodes joined", leaderID, nodeCount)
}

// TestStressCluster_LeaderDistribution tests that different nodes can become leader
func TestStressCluster_LeaderDistribution(t *testing.T) {
	iterations := 10
	nodeCount := 5
	stressLog("=== TestStressCluster_LeaderDistribution START: %d nodes, %d iterations ===", nodeCount, iterations)
	leaderDistribution := make(map[string]int)

	for iter := 0; iter < iterations; iter++ {
		iterStart := time.Now()
		stressLog("--- Distribution Iteration %d/%d START ---", iter+1, iterations)
		t.Logf("Iteration %d/%d", iter+1, iterations)

		// Create nodes with full mesh
		nodes := createStressNodes(t, nodeCount)
		nodeIDs := make([]peercore.ID, nodeCount)
		for i, node := range nodes {
			nodeIDs[i] = node.ID()
		}

		// Wait for mesh connectivity
		err := waitAllConnected(t, nodes, 30*time.Second)
		if err != nil {
			stressLog("--- Distribution Iteration %d/%d FAILED: mesh connectivity: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "failed to establish mesh connectivity")
		}

		namespace := fmt.Sprintf("stress-distribution-%d", iter)

		// Create clusters concurrently
		clusters := make([]Cluster, nodeCount)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var createErr error
		bootTimeout := bootstrapTimeoutForNodes(nodeCount)

		for i := 0; i < nodeCount; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				cl, err := New(
					nodes[idx],
					namespace,
					WithTimeouts(stressTimeoutConfig()),
					WithBootstrapTimeout(bootTimeout),
				)
				if err != nil {
					stressLog("Distribution Iteration %d/%d: ERROR creating cluster for node %d: %v", iter+1, iterations, idx, err)
					mu.Lock()
					if createErr == nil {
						createErr = err
					}
					mu.Unlock()
				} else {
					clusters[idx] = cl
				}
			}(i)
		}

		wg.Wait()
		if createErr != nil {
			stressLog("--- Distribution Iteration %d/%d FAILED: cluster creation: %v ---", iter+1, iterations, createErr)
			assert.NilError(t, createErr, "failed to create clusters")
		}

		// Cleanup: Close clusters first, then nodes (with delay to let Raft finish)
		// Capture nodes and clusters by value to avoid loop variable issues
		iterNodes := nodes
		iterClusters := clusters
		iterNum := iter + 1
		defer func() {
			stressLog("Distribution Iteration %d/%d: Starting cleanup", iterNum, iterations)
			// Close all clusters first
			for i, cl := range iterClusters {
				if cl != nil {
					stressLog("Distribution Iteration %d/%d: Closing cluster %d", iterNum, iterations, i)
					cl.Close()
				}
			}
			// Give Raft time to finish cleanup
			time.Sleep(200 * time.Millisecond)
			// Close all nodes
			for i, node := range iterNodes {
				if node != nil {
					stressLog("Distribution Iteration %d/%d: Closing node %d", iterNum, iterations, i)
					node.Close()
				}
			}
			stressLog("Distribution Iteration %d/%d: Cleanup complete", iterNum, iterations)
		}()

		// Verify leader elected - scale timeout with node count (5 nodes for distribution test)
		leaderTimeout := 60 * time.Second // Distribution test uses 5 nodes, so 60s is sufficient
		leaderID, err := verifyLeaderElected(t, clusters, leaderTimeout)
		if err != nil {
			stressLog("--- Distribution Iteration %d/%d FAILED: leader election: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "failed to verify leader election")
		}

		// Stricter health checks
		stressLog("--- Distribution Iteration %d/%d: Running strict health checks ---", iter+1, iterations)

		// Verify Raft states (1 Leader, rest Followers)
		err = verifyRaftStates(t, clusters)
		if err != nil {
			stressLog("--- Distribution Iteration %d/%d FAILED: Raft states check: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "raft states check failed")
		}

		// Verify configuration consistency
		err = verifyConfigurationConsistency(t, clusters)
		if err != nil {
			stressLog("--- Distribution Iteration %d/%d FAILED: Configuration consistency: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "configuration consistency check failed")
		}

		// Verify cluster stability (500ms)
		err = verifyClusterStability(t, clusters, 500*time.Millisecond)
		if err != nil {
			stressLog("--- Distribution Iteration %d/%d FAILED: Cluster stability: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "cluster stability check failed")
		}

		// Verify FSM coherence - write values and verify they replicate
		fsmPrefix := fmt.Sprintf("distribution-%d", iter)
		err = verifyFSMCoherence(t, clusters, fsmPrefix, 5)
		if err != nil {
			stressLog("--- Distribution Iteration %d/%d FAILED: FSM coherence: %v ---", iter+1, iterations, err)
			assert.NilError(t, err, "FSM coherence check failed")
		}

		// Track leader distribution
		leaderDistribution[leaderID.String()]++

		stressLog("--- Distribution Iteration %d/%d SUCCESS: Leader %s elected, FSM coherent (took %v) ---", iter+1, iterations, leaderID.String(), time.Since(iterStart))
		t.Logf("Iteration %d: Leader elected: %s", iter+1, leaderID)
	}

	// Log detailed leader distribution
	stressLog("=== TestStressCluster_LeaderDistribution COMPLETE: Leader distribution across %d iterations ===", iterations)
	t.Logf("Leader distribution across %d iterations:", iterations)
	uniqueLeaders := 0
	for leaderID, count := range leaderDistribution {
		percentage := float64(count) / float64(iterations) * 100
		stressLog("  Leader %s: %d times (%.1f%%)", leaderID, count, percentage)
		t.Logf("  %s: %d times (%.1f%%)", leaderID, count, percentage)
		uniqueLeaders++
	}

	// Verify at least 2 different nodes became leader
	if uniqueLeaders < 2 {
		stressLog("=== TestStressCluster_LeaderDistribution FAILED: Only %d unique leader(s), expected at least 2 ===", uniqueLeaders)
	}
	assert.Assert(t, uniqueLeaders >= 2, "expected at least 2 different leaders, got %d", uniqueLeaders)
	stressLog("Total unique leaders: %d", uniqueLeaders)
	t.Logf("Total unique leaders: %d", uniqueLeaders)
}
