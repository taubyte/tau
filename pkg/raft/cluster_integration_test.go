//go:build raft_integration

package raft

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/keypair"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/poll"
)

// newTestNode is a real libp2p node connected to nothing: a test links it once
// it serves what the test shares.
func newTestNode(t *testing.T) taupeer.Node {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	dir := t.TempDir()

	node, err := taupeer.New(
		ctx,
		dir,
		keypair.NewRaw(),
		nil,                              // no admission: an open network
		[]string{"/ip4/127.0.0.1/tcp/0"}, // port 0 for dynamic allocation
		nil,                              // swarm announce
		true,                             // notPublic
		false,                            // don't bootstrap to default peers
	)
	assert.NilError(t, err, "failed to create node")

	t.Cleanup(func() {
		node.Close()
	})

	err = node.WaitForSwarm(5 * time.Second)
	assert.NilError(t, err, "failed to wait for swarm")

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

func waitForKeyValue(t *testing.T, cl Cluster, key, want string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if val, found := cl.Get(key); found && string(val) == want {
				return nil
			}
		}
	}
}

func waitForKeyAbsent(t *testing.T, cl Cluster, key string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, found := cl.Get(key); !found {
				return nil
			}
		}
	}
}

func nodeOf(cl Cluster) taupeer.Node { return cl.(*cluster).node }

func followerOf(t *testing.T, clusters ...Cluster) Cluster {
	t.Helper()
	leader := leaderOf(t, clusters...)
	for _, cl := range clusters {
		if cl != leader {
			return cl
		}
	}
	t.Fatal("a group of one has no follower")
	return nil
}

func without(clusters []Cluster, gone Cluster) []Cluster {
	var rest []Cluster
	for _, cl := range clusters {
		if cl != gone {
			rest = append(rest, cl)
		}
	}
	return rest
}

// whileLeadershipMoves retries a call refused because leadership was moving.
// Only for calls that leave the same state however often they land.
func whileLeadershipMoves(t *testing.T, call func() error) error {
	t.Helper()
	deadline := time.Now().Add(leaderWait)
	for {
		err := call()
		if !(leadershipMoved(err) || errors.Is(err, ErrNoLeader)) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCluster_MultiNode_LeaderElection_Integration(t *testing.T) {
	node1 := newTestNode(t)

	node2 := newTestNode(t)
	node3 := newTestNode(t)

	namespace := "multi-node-test"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	linkTo(t, node2, node1)
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	linkTo(t, node3, node1)
	cluster3, err := New(node3, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster3")
	defer cluster3.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")
	err = waitForMember(t, cluster1, node3.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node3 failed to join as voter")

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	clusters := []Cluster{cluster1, cluster2, cluster3}

	for i, cl := range clusters {
		err := cl.WaitForLeader(ctx2)
		assert.NilError(t, err, "cluster%d failed to wait for leader", i+1)
	}

	t.Logf("Leader: %s", idOf(leaderOf(t, clusters...)))

	err = onLeader(t, clusters, func(leader Cluster) error {
		return leader.Set("test-key", []byte("test-value"), 5*time.Second)
	})
	assert.NilError(t, err, "failed to set on leader")

	for i, cl := range clusters {
		err := waitForKeyValue(t, cl, "test-key", "test-value", 5*time.Second)
		assert.NilError(t, err, "cluster%d: key not replicated in time", i+1)
	}
}

func TestCluster_MultiNode_SendToNonLeader_Integration(t *testing.T) {
	node1 := newTestNode(t)
	node2 := newTestNode(t)

	namespace := "forward-test"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	linkTo(t, node2, node1)
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	clusters := []Cluster{cluster1, cluster2}

	// Set directly on a member that is not leading - should return
	// ErrNotLeader. Which member that is, raft decides when the write arrives:
	// one that had come to lead by then took the write, which is not this case,
	// so it is put to whichever member follows next.
	refused := false
	for deadline := time.Now().Add(leaderWait); !refused && time.Now().Before(deadline); {
		follower := followerOf(t, clusters...)
		err = follower.Set("follower-key", []byte("follower-value"), 5*time.Second)
		if err == nil || errors.Is(err, raft.ErrLeadershipLost) {
			continue
		}
		assert.ErrorIs(t, err, ErrNotLeader, "expected ErrNotLeader when calling Set on follower")
		refused = true
	}
	assert.Assert(t, refused, "no member refused a write while following")

	follower := followerOf(t, clusters...)
	t.Logf("Sending Set to follower: %s", idOf(follower))

	client, err := NewClient(nodeOf(without(clusters, follower)[0]), namespace, nil)
	assert.NilError(t, err, "failed to create client")
	defer client.Close()

	err = whileLeadershipMoves(t, func() error {
		return client.Set("client-key", []byte("client-value"), 5*time.Second, idOf(follower))
	})
	assert.NilError(t, err, "client.Set to follower should forward to leader")

	err = waitForKeyValue(t, follower, "client-key", "client-value", 5*time.Second)
	assert.NilError(t, err, "key should exist after forwarded set")
}

func TestCluster_AutoBootstrapThenJoiners_Integration(t *testing.T) {
	namespace := "autobootstrap-joiners"

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

	node2 := newTestNode(t)
	node3 := newTestNode(t)
	linkTo(t, node2, node1)
	linkTo(t, node3, node1)

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

	leaderOf(t, cluster1, cluster2, cluster3)
}

func TestCluster_MultiNode_DiscoverPeers_Integration(t *testing.T) {
	node1 := newTestNode(t)
	node2 := newTestNode(t)

	namespace := "discover-test"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be the leader")

	linkTo(t, node2, node1)
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	t.Logf("Both clusters agree on leader: %s", idOf(leaderOf(t, cluster1, cluster2)))
}

func TestCluster_MultiNode_StreamServiceForwarding_Integration(t *testing.T) {
	node1 := newTestNode(t)
	node2 := newTestNode(t)

	namespace := "stream-forward-test"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	linkTo(t, node2, node1)
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	clusters := []Cluster{cluster1, cluster2}
	followerCluster := followerOf(t, clusters...)
	leaderCluster := without(clusters, followerCluster)[0]

	t.Logf("Follower: %s", idOf(followerCluster))

	client, err := NewClient(nodeOf(leaderCluster), namespace, nil)
	assert.NilError(t, err, "failed to create client")
	defer client.Close()

	err = whileLeadershipMoves(t, func() error {
		return client.Set("forwarded-key", []byte("forwarded-value"), 5*time.Second, idOf(followerCluster))
	})
	assert.NilError(t, err, "Set to follower should forward to leader")

	err = waitForKeyValue(t, leaderCluster, "forwarded-key", "forwarded-value", 5*time.Second)
	assert.NilError(t, err, "key should exist on leader after forwarded set")
	err = waitForKeyValue(t, followerCluster, "forwarded-key", "forwarded-value", 5*time.Second)
	assert.NilError(t, err, "key should be replicated to follower")

	val, found, err := client.Get("forwarded-key", 0, idOf(followerCluster))
	assert.NilError(t, err, "Get from follower should work")
	assert.Assert(t, found, "key should be found via client")
	assert.Equal(t, string(val), "forwarded-value")

	keys, err := client.Keys("forwarded", idOf(followerCluster))
	assert.NilError(t, err, "Keys from follower should work")
	assert.Assert(t, len(keys) >= 1, "should find at least one key with prefix")

	err = whileLeadershipMoves(t, func() error {
		return client.Delete("forwarded-key", 5*time.Second, idOf(followerCluster))
	})
	assert.NilError(t, err, "Delete on follower should forward to leader")

	for _, cl := range clusters {
		err = waitForKeyAbsent(t, cl, "forwarded-key", 5*time.Second)
		assert.NilError(t, err, "key should be deleted after forwarded delete")
	}
}

func TestCluster_MultiNode_ClientOperations_Integration(t *testing.T) {
	node1 := newTestNode(t)
	node2 := newTestNode(t)

	namespace := "client-ops-test"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	linkTo(t, node2, node1)
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	clusters := []Cluster{cluster1, cluster2}

	err = onLeader(t, clusters, func(leader Cluster) error {
		return leader.Set("key1", []byte("value1"), 5*time.Second)
	})
	assert.NilError(t, err, "Set to leader should succeed")

	err = onLeader(t, clusters, func(leader Cluster) error {
		return leader.Set("key2", []byte("value2"), 5*time.Second)
	})
	assert.NilError(t, err, "Set second key should succeed")

	assert.NilError(t, waitForKeyValue(t, cluster1, "key1", "value1", 5*time.Second), "key1 not replicated in time")
	assert.NilError(t, waitForKeyValue(t, cluster1, "key2", "value2", 5*time.Second), "key2 not replicated in time")

	val, found := cluster1.Get("key1")
	assert.Assert(t, found, "key1 should be found")
	assert.Equal(t, string(val), "value1")

	keys := cluster1.Keys("key")
	assert.Equal(t, len(keys), 2, "should find 2 keys with prefix 'key'")

	client, err := NewClient(node2, namespace, nil)
	assert.NilError(t, err, "failed to create client")
	defer client.Close()

	val, found, err = client.Get("key1", 0, node1.ID())
	assert.NilError(t, err, "Get via client should succeed")
	assert.Assert(t, found, "key1 should be found via client")
	assert.Equal(t, string(val), "value1")

	keys, err = client.Keys("key", node1.ID())
	assert.NilError(t, err, "Keys via client should succeed")
	assert.Equal(t, len(keys), 2, "should find 2 keys via client")

	err = onLeader(t, clusters, func(leader Cluster) error {
		return leader.Delete("key1", 5*time.Second)
	})
	assert.NilError(t, err, "Delete should succeed")

	assert.NilError(t, waitForKeyAbsent(t, cluster1, "key1", 5*time.Second), "delete not replicated in time")

	_, found, err = client.Get("key1", 0, node1.ID())
	assert.NilError(t, err, "Get after delete should succeed")
	assert.Assert(t, !found, "key1 should not be found after delete")

	val, found, err = client.Get("key2", 0, node1.ID())
	assert.NilError(t, err, "Get key2 should succeed")
	assert.Assert(t, found, "key2 should still exist")
	assert.Equal(t, string(val), "value2")
}

func TestCluster_MultiNode_ClientGetWithBarrier_Integration(t *testing.T) {
	node1 := newTestNode(t)
	node2 := newTestNode(t)

	namespace := "barrier-test"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	linkTo(t, node2, node1)
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	clusters := []Cluster{cluster1, cluster2}

	reach := map[peercore.ID]Client{}
	for _, from := range []taupeer.Node{node1, node2} {
		cl, err := NewClient(from, namespace, nil)
		assert.NilError(t, err, "failed to create client")
		defer cl.Close()
		for _, to := range []taupeer.Node{node1, node2} {
			if to.ID() != from.ID() {
				reach[to.ID()] = cl
			}
		}
	}
	client := reach[node1.ID()]

	// A barrier is the leader's to take, so a read behind one goes to
	// whichever member leads when it is sent
	readBehindBarrier := func(key string, barrierNs int64) (val []byte, found bool, err error) {
		err = whileLeadershipMoves(t, func() error {
			target := idOf(leaderOf(t, clusters...))
			var err error
			val, found, err = reach[target].Get(key, barrierNs, target)
			return err
		})
		return val, found, err
	}

	err = onLeader(t, clusters, func(leader Cluster) error {
		return leader.Set("barrier-test-key", []byte("barrier-test-value"), 5*time.Second)
	})
	assert.NilError(t, err, "Set to leader should succeed")

	_, _, err = client.Get("barrier-test-key", 0, node1.ID())
	assert.NilError(t, err, "Get without barrier should succeed")

	barrierNs := int64(2 * time.Second.Nanoseconds())
	val, found, err := readBehindBarrier("barrier-test-key", barrierNs)
	assert.NilError(t, err, "Get with barrier should succeed")
	assert.Assert(t, found, "key should be found with barrier")
	assert.Equal(t, string(val), "barrier-test-value", "value should match")

	err = onLeader(t, clusters, func(leader Cluster) error {
		return leader.Set("barrier-test-key2", []byte("barrier-test-value2"), 5*time.Second)
	})
	assert.NilError(t, err, "Set second key should succeed")

	val, found, err = readBehindBarrier("barrier-test-key2", barrierNs)
	assert.NilError(t, err, "Get with barrier should succeed")
	assert.Assert(t, found, "key2 should be found with barrier")
	assert.Equal(t, string(val), "barrier-test-value2", "value2 should match")

	maxBarrierNs := int64(MaxGetHandlerBarrierTimeout.Nanoseconds())
	val, found, err = readBehindBarrier("barrier-test-key", maxBarrierNs)
	assert.NilError(t, err, "Get with max barrier should succeed")
	assert.Assert(t, found, "key should be found with max barrier")
	assert.Equal(t, string(val), "barrier-test-value", "value should match")

	_, _, err = client.Get("barrier-test-key", -1, node1.ID())
	assert.Assert(t, err != nil, "Get with negative barrier should fail")
	assert.ErrorIs(t, err, ErrInvalidBarrier, "should return ErrInvalidBarrier")

	exceedsMaxBarrierNs := int64((MaxGetHandlerBarrierTimeout + time.Second).Nanoseconds())
	_, _, err = client.Get("barrier-test-key", exceedsMaxBarrierNs, node1.ID())
	assert.Assert(t, err != nil, "Get with barrier exceeding max should fail")
	assert.ErrorIs(t, err, ErrInvalidBarrier, "should return ErrInvalidBarrier")
}

func TestCluster_MultiNode_Apply_InvalidTimeout_Integration(t *testing.T) {
	node1 := newTestNode(t)
	node2 := newTestNode(t)

	namespace := "apply-timeout-test"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	linkTo(t, node2, node1)
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "apply-timeout-key", Value: []byte("apply-timeout-value")},
	}
	data, _ := cbor.Marshal(cmd)

	_, err = cluster1.Apply(data, 0)
	assert.Assert(t, err != nil, "Apply with timeout=0 should fail")
	assert.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	_, err = cluster1.Apply(data, -1*time.Second)
	assert.Assert(t, err != nil, "Apply with negative timeout should fail")
	assert.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	_, err = cluster1.Apply(data, MaxApplyTimeout+time.Second)
	assert.Assert(t, err != nil, "Apply with timeout > MaxApplyTimeout should fail")
	assert.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	clusters := []Cluster{cluster1, cluster2}

	cmd2 := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "apply-timeout-key2", Value: []byte("apply-timeout-value2")},
	}
	data2, _ := cbor.Marshal(cmd2)
	err = onLeader(t, clusters, func(leader Cluster) error {
		_, err := leader.Apply(data2, time.Second)
		return err
	})
	assert.NilError(t, err, "Apply with valid timeout should succeed")

	for i, cl := range clusters {
		err = waitForKeyValue(t, cl, "apply-timeout-key2", "apply-timeout-value2", 5*time.Second)
		assert.NilError(t, err, "cluster%d: key not replicated in time", i+1)
	}

	cmd3 := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "apply-timeout-key3", Value: []byte("apply-timeout-value3")},
	}
	data3, _ := cbor.Marshal(cmd3)
	err = onLeader(t, clusters, func(leader Cluster) error {
		_, err := leader.Apply(data3, MaxApplyTimeout)
		return err
	})
	assert.NilError(t, err, "Apply with timeout at MaxApplyTimeout should succeed")

	for i, cl := range clusters {
		err = waitForKeyValue(t, cl, "apply-timeout-key3", "apply-timeout-value3", 5*time.Second)
		assert.NilError(t, err, "cluster%d: key3 not replicated in time", i+1)
	}
}

func TestCluster_MultiCluster_SameNode_Integration(t *testing.T) {
	node := newTestNode(t)

	namespace1 := "cluster-alpha"
	namespace2 := "cluster-beta"
	namespace3 := "cluster-gamma"

	cluster1, err := New(node, namespace1, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	cluster2, err := New(node, namespace2, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	cluster3, err := New(node, namespace3, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster3")
	defer cluster3.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	assert.NilError(t, cluster1.WaitForLeader(ctx), "cluster1 failed to elect leader")
	assert.NilError(t, cluster2.WaitForLeader(ctx), "cluster2 failed to elect leader")
	assert.NilError(t, cluster3.WaitForLeader(ctx), "cluster3 failed to elect leader")

	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")
	assert.Assert(t, cluster2.IsLeader(), "cluster2 should be leader")
	assert.Assert(t, cluster3.IsLeader(), "cluster3 should be leader")

	err = cluster1.Set("shared-key", []byte("cluster1-value"), 5*time.Second)
	assert.NilError(t, err, "cluster1 Set failed")

	err = cluster2.Set("shared-key", []byte("cluster2-value"), 5*time.Second)
	assert.NilError(t, err, "cluster2 Set failed")

	err = cluster3.Set("shared-key", []byte("cluster3-value"), 5*time.Second)
	assert.NilError(t, err, "cluster3 Set failed")

	val1, found1 := cluster1.Get("shared-key")
	assert.Assert(t, found1, "cluster1 should have the key")
	assert.Equal(t, string(val1), "cluster1-value", "cluster1 has wrong value")

	val2, found2 := cluster2.Get("shared-key")
	assert.Assert(t, found2, "cluster2 should have the key")
	assert.Equal(t, string(val2), "cluster2-value", "cluster2 has wrong value")

	val3, found3 := cluster3.Get("shared-key")
	assert.Assert(t, found3, "cluster3 should have the key")
	assert.Equal(t, string(val3), "cluster3-value", "cluster3 has wrong value")

	err = cluster1.Set("alpha-only", []byte("alpha"), 5*time.Second)
	assert.NilError(t, err)

	err = cluster2.Set("beta-only", []byte("beta"), 5*time.Second)
	assert.NilError(t, err)

	err = cluster3.Set("gamma-only", []byte("gamma"), 5*time.Second)
	assert.NilError(t, err)

	_, found := cluster1.Get("beta-only")
	assert.Assert(t, !found, "cluster1 should NOT have beta-only key")

	_, found = cluster2.Get("alpha-only")
	assert.Assert(t, !found, "cluster2 should NOT have alpha-only key")

	_, found = cluster3.Get("alpha-only")
	assert.Assert(t, !found, "cluster3 should NOT have alpha-only key")

	err = cluster1.Delete("shared-key", 5*time.Second)
	assert.NilError(t, err)

	_, found = cluster1.Get("shared-key")
	assert.Assert(t, !found, "cluster1 should NOT have shared-key after delete")

	val2, found2 = cluster2.Get("shared-key")
	assert.Assert(t, found2, "cluster2 should still have shared-key")
	assert.Equal(t, string(val2), "cluster2-value")

	val3, found3 = cluster3.Get("shared-key")
	assert.Assert(t, found3, "cluster3 should still have shared-key")
	assert.Equal(t, string(val3), "cluster3-value")

	t.Logf("Successfully tested 3 isolated clusters on same node")
}

func TestCluster_MultiCluster_MultiNode_Integration(t *testing.T) {
	node1 := newTestNode(t)
	node2 := newTestNode(t)

	namespaceA := "service-A"
	namespaceB := "service-B"

	clusterA1, err := New(node1, namespaceA, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create clusterA1")
	defer clusterA1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	assert.NilError(t, clusterA1.WaitForLeader(ctx), "clusterA1 failed to elect leader")

	clusterB1, err := New(node1, namespaceB, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create clusterB1")
	defer clusterB1.Close()

	ctx2, cancel2 := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel2()
	assert.NilError(t, clusterB1.WaitForLeader(ctx2), "clusterB1 failed to elect leader")

	linkTo(t, node2, node1)

	clusterA2, err := New(node2, namespaceA, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create clusterA2")
	defer clusterA2.Close()

	ctx3, cancel3 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel3()
	err = waitForMember(t, clusterA1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join cluster A as voter")
	assert.NilError(t, clusterA2.WaitForLeader(ctx3), "clusterA2 failed to wait for leader")

	clusterB2, err := New(node2, namespaceB, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create clusterB2")
	defer clusterB2.Close()

	ctx4, cancel4 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel4()
	err = waitForMember(t, clusterB1, node2.ID(), raft.Voter, 30*time.Second)
	assert.NilError(t, err, "node2 failed to join cluster B as voter")
	assert.NilError(t, clusterB2.WaitForLeader(ctx4), "clusterB2 failed to wait for leader")

	err = onLeader(t, []Cluster{clusterA1, clusterA2}, func(leader Cluster) error {
		return leader.Set("service-data", []byte("from-service-A"), 5*time.Second)
	})
	assert.NilError(t, err, "cluster A Set failed")

	err = onLeader(t, []Cluster{clusterB1, clusterB2}, func(leader Cluster) error {
		return leader.Set("service-data", []byte("from-service-B"), 5*time.Second)
	})
	assert.NilError(t, err, "cluster B Set failed")

	err = waitForKeyValue(t, clusterA1, "service-data", "from-service-A", 5*time.Second)
	assert.NilError(t, err, "clusterA1: service-data not replicated in time")
	valA1, foundA1 := clusterA1.Get("service-data")
	assert.Assert(t, foundA1, "clusterA1 should have service-data")
	assert.Equal(t, string(valA1), "from-service-A")

	err = waitForKeyValue(t, clusterA2, "service-data", "from-service-A", 5*time.Second)
	assert.NilError(t, err, "clusterA2: service-data not replicated in time")
	valA2, _ := clusterA2.Get("service-data")
	assert.Equal(t, string(valA2), "from-service-A")

	err = waitForKeyValue(t, clusterB1, "service-data", "from-service-B", 5*time.Second)
	assert.NilError(t, err, "clusterB1: service-data not replicated in time")
	valB1, foundB1 := clusterB1.Get("service-data")
	assert.Assert(t, foundB1, "clusterB1 should have service-data")
	assert.Equal(t, string(valB1), "from-service-B")

	err = waitForKeyValue(t, clusterB2, "service-data", "from-service-B", 5*time.Second)
	assert.NilError(t, err, "clusterB2: service-data not replicated in time")
	valB2, _ := clusterB2.Get("service-data")
	assert.Equal(t, string(valB2), "from-service-B")

	members_A, _ := clusterA1.Members()
	members_B, _ := clusterB1.Members()

	t.Logf("Cluster A members: %d", len(members_A))
	t.Logf("Cluster B members: %d", len(members_B))

	assert.Equal(t, len(members_A), 2, "Cluster A should have 2 members")
	assert.Equal(t, len(members_B), 2, "Cluster B should have 2 members")

	t.Logf("Successfully tested multi-node multi-cluster isolation")
}

func TestCluster_RejoinAfterLeave_Integration(t *testing.T) {
	node1 := newTestNode(t)
	node2 := newTestNode(t)

	namespace := "rejoin-test"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	assert.NilError(t, cluster1.WaitForLeader(ctx), "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	linkTo(t, node2, node1)
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	// Closed here as well, for a failure before the leave below: a raft
	// left running outlives the node's store and panics the test binary.
	defer cluster2.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	err = onLeader(t, []Cluster{cluster1, cluster2}, func(leader Cluster) error {
		return leader.RemoveServer(node2.ID(), 10*time.Second)
	})
	assert.NilError(t, err, "failed to remove node2")
	assert.NilError(t, waitForMemberAbsent(t, cluster1, node2.ID(), 20*time.Second), "node2 still present after removal")
	assert.NilError(t, cluster2.Close(), "failed to close cluster2")

	cluster2b, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to recreate cluster2")
	defer cluster2b.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to rejoin as voter")
}

func TestCluster_DiscoveryBasedBootstrap_Integration(t *testing.T) {
	node1 := newTestNode(t)
	node2 := newTestNode(t)

	namespace := "discovery-bootstrap-test"

	bootstrapTimeout := 3 * time.Second

	clusters := startTogether(t, namespace, []taupeer.Node{node1, node2}, make([]time.Duration, 2),
		WithTimeouts(testTimeoutConfig()),
		WithBootstrapTimeout(bootstrapTimeout),
	)
	cluster1, cluster2 := clusters[0], clusters[1]

	leaderCtx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	err := cluster1.WaitForLeader(leaderCtx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")

	err = cluster2.WaitForLeader(leaderCtx)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	t.Logf("Leader elected: %s", idOf(leaderOf(t, cluster1, cluster2)))
}

func TestCluster_ThreeNodes_SimultaneousStart_Integration(t *testing.T) {
	nodes := []taupeer.Node{newTestNode(t), newTestNode(t), newTestNode(t)}

	namespace := "three-node-simultaneous"
	bootstrapTimeout := 3 * time.Second

	clusters := startTogether(t, namespace, nodes, make([]time.Duration, 3),
		WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(bootstrapTimeout))
	cluster1, cluster2, cluster3 := clusters[0], clusters[1], clusters[2]

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	err := cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	err = cluster2.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster2 failed to wait for leader")
	err = cluster3.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster3 failed to wait for leader")

	t.Logf("Leader elected among 3 simultaneous nodes: %s", idOf(leaderOf(t, cluster1, cluster2, cluster3)))
}

func TestCluster_LeaderCrashAndFailover_Integration(t *testing.T) {
	node1 := newTestNode(t)
	node2 := newTestNode(t)
	node3 := newTestNode(t)

	namespace := "leader-crash-failover"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	// Closed here as well, for a failure before the close below: a raft
	// left running outlives the node's store and panics the test binary.
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 must be leader after force bootstrap")

	linkTo(t, node2, node1)
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	linkTo(t, node3, node1, node2)
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

	clusters := []Cluster{cluster1, cluster2, cluster3}

	err = onLeader(t, clusters, func(leader Cluster) error {
		return leader.Set("before-crash", []byte("important-data"), 5*time.Second)
	})
	assert.NilError(t, err, "failed to set data before crash")

	for i, cl := range clusters {
		err = waitForKeyValue(t, cl, "before-crash", "important-data", 5*time.Second)
		assert.NilError(t, err, "data must be replicated to cluster%d before crash", i+1)
	}

	crashed := leaderOf(t, clusters...)
	survivors := without(clusters, crashed)
	t.Logf("Crashing leader (%s)...", idOf(crashed))

	err = crashed.Close()
	assert.NilError(t, err, "failed to close the leader")

	newLeader := leaderOf(t, survivors...)

	assert.Assert(t, idOf(newLeader) != idOf(crashed), "new leader must not be the crashed node")

	for _, cl := range survivors {
		val, found := cl.Get("before-crash")
		assert.Assert(t, found, "data must persist after leader crash (%s)", idOf(cl))
		assert.Equal(t, string(val), "important-data", "data integrity failure after crash (%s)", idOf(cl))
	}

	t.Logf("New leader after crash: %s", idOf(newLeader))
}

func TestCluster_NodeRebootWithDataPersistence_Integration(t *testing.T) {
	node1 := newTestNode(t)
	node2 := newTestNode(t)
	node3 := newTestNode(t)

	namespace := "node-reboot-persistence"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 must be leader")

	linkTo(t, node2, node1)
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	// Closed here as well, for a failure before the close below: a raft
	// left running outlives the node's store and panics the test binary.
	defer cluster2.Close()

	linkTo(t, node3, node1, node2)
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

	clusters := []Cluster{cluster1, cluster2, cluster3}

	err = onLeader(t, clusters, func(leader Cluster) error {
		return leader.Set("key-before-reboot", []byte("value-before"), 5*time.Second)
	})
	assert.NilError(t, err, "failed to set key-before-reboot")

	for i, cl := range clusters {
		err = waitForKeyValue(t, cl, "key-before-reboot", "value-before", 5*time.Second)
		assert.NilError(t, err, "key-before-reboot must exist on cluster%d before reboot", i+1)
	}

	rebooted := followerOf(t, clusters...)
	survivors := without(clusters, rebooted)
	t.Logf("Rebooting follower (%s)...", idOf(rebooted))

	err = rebooted.Close()
	assert.NilError(t, err, "failed to close the follower")

	err = onLeader(t, survivors, func(leader Cluster) error {
		return leader.Set("key-during-reboot", []byte("value-during"), 5*time.Second)
	})
	assert.NilError(t, err, "failed to set key-during-reboot (should work with quorum from the other two)")

	for _, cl := range survivors {
		err = waitForKeyValue(t, cl, "key-during-reboot", "value-during", 5*time.Second)
		assert.NilError(t, err, "key-during-reboot must be replicated to %s", idOf(cl))
	}

	rebootedB, err := New(nodeOf(rebooted), namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to recreate the follower after reboot")
	defer rebootedB.Close()

	err = waitForMember(t, survivors[0], idOf(rebooted), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "the follower failed to rejoin as voter after reboot")

	ctx3, cancel3 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel3()
	err = rebootedB.WaitForLeader(ctx3)
	assert.NilError(t, err, "the rebooted follower failed to see leader after reboot")

	err = waitForKeyValue(t, rebootedB, "key-before-reboot", "value-before", 5*time.Second)
	assert.NilError(t, err, "key-before-reboot not replicated in time")
	val, found := rebootedB.Get("key-before-reboot")
	assert.Assert(t, found, "key-before-reboot must exist after reboot")
	assert.Equal(t, string(val), "value-before", "value mismatch for key-before-reboot after reboot")

	err = waitForKeyValue(t, rebootedB, "key-during-reboot", "value-during", 5*time.Second)
	assert.NilError(t, err, "key-during-reboot not replicated in time")
	val, found = rebootedB.Get("key-during-reboot")
	assert.Assert(t, found, "key-during-reboot must be replicated after rejoin")
	assert.Equal(t, string(val), "value-during", "value mismatch for key-during-reboot after reboot")

	err = onLeader(t, append(survivors, rebootedB), func(leader Cluster) error {
		return leader.Set("key-after-reboot", []byte("value-after"), 5*time.Second)
	})
	assert.NilError(t, err, "failed to set key-after-reboot")

	err = waitForKeyValue(t, rebootedB, "key-after-reboot", "value-after", 5*time.Second)
	assert.NilError(t, err, "key-after-reboot must be replicated")

	t.Log("Node reboot with data persistence test passed")
}

func TestCluster_StaggeredConcurrentStartup_Integration(t *testing.T) {
	nodes := []taupeer.Node{newTestNode(t), newTestNode(t), newTestNode(t)}

	namespace := "staggered-startup"
	bootstrapTimeout := 3 * time.Second

	clusters := startTogether(t, namespace, nodes, []time.Duration{0, 200 * time.Millisecond, 500 * time.Millisecond},
		WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(bootstrapTimeout))
	cluster1, cluster2, cluster3 := clusters[0], clusters[1], clusters[2]

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	err := cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	err = cluster2.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster2 failed to wait for leader")
	err = cluster3.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster3 failed to wait for leader")

	leader := leaderOf(t, clusters...)

	err = onLeader(t, clusters, func(leader Cluster) error {
		return leader.Set("staggered-key", []byte("staggered-value"), 5*time.Second)
	})
	assert.NilError(t, err, "failed to set on leader")

	for i, cl := range clusters {
		err = waitForKeyValue(t, cl, "staggered-key", "staggered-value", 5*time.Second)
		assert.NilError(t, err, "cluster%d must see staggered-key", i+1)
	}

	t.Logf("Staggered startup test passed, leader: %s", idOf(leader))
}

// leadingNow returns whichever of clusters leads and a term it leads in. The
// term is read before the member is seen still leading, so it led at that term
// or a later one, and a leader elected once it is gone leads at a later one.
func leadingNow(t *testing.T, clusters ...Cluster) (Cluster, uint64) {
	t.Helper()
	deadline := time.Now().Add(leaderWait)
	for {
		leader := leaderOf(t, clusters...)
		term := getClusterInternal(leader).current().raft.CurrentTerm()
		if leader.IsLeader() {
			return leader, term
		}
		if time.Now().After(deadline) {
			t.Fatalf("no member led long enough to read its term within %v", leaderWait)
		}
	}
}

// rejoinSettled is one look at what raft promises once back, a member that
// crashed while it led, is restarted: one leader every member names, at a term
// past crashTerm every member is at; every member a voter to each; back
// following unless it leads, caught up to the commit index and holding writes.
// Raft does not promise back stays a follower.
func rejoinSettled(back Cluster, members []Cluster, crashTerm uint64, writes map[string]string) poll.Result {
	leader := agreedLeader(members)
	if leader == nil {
		return poll.Continue("the members do not all name the one member that leads")
	}
	term := getClusterInternal(leader).current().raft.CurrentTerm()
	if term <= crashTerm {
		return poll.Continue("%s leads at term %d, not past the crash at term %d", idOf(leader), term, crashTerm)
	}
	for _, m := range members {
		if got := getClusterInternal(m).current().raft.CurrentTerm(); got != term {
			return poll.Continue("%s is at term %d, its leader at %d", idOf(m), got, term)
		}
		config, err := m.Members()
		if err != nil {
			return poll.Continue("%s lists no members: %v", idOf(m), err)
		}
		if len(config) != len(members) {
			return poll.Continue("%s holds %d members, not %d", idOf(m), len(config), len(members))
		}
		for _, v := range members {
			if !slices.ContainsFunc(config, func(s Member) bool { return s.ID == idOf(v) && s.Suffrage == raft.Voter }) {
				return poll.Continue("%s does not hold %s as a voter", idOf(m), idOf(v))
			}
		}
	}
	if back != leader && back.State() != raft.Follower {
		return poll.Continue("%s neither leads nor follows: %v", idOf(back), back.State())
	}
	commit := getClusterInternal(leader).current().raft.CommitIndex()
	r := getClusterInternal(back).current().raft
	if last, applied := r.LastIndex(), r.AppliedIndex(); last < commit || applied < commit {
		return poll.Continue("%s logged to %d and applied to %d, short of its leader's commit index %d", idOf(back), last, applied, commit)
	}
	for key, want := range writes {
		if got, found := back.Get(key); !found || string(got) != want {
			return poll.Continue("%s holds %s=%q (found %v), not %q", idOf(back), key, got, found, want)
		}
	}
	return poll.Success()
}

func TestCluster_LeaderRebootAndRejoin_Integration(t *testing.T) {
	node1 := newTestNode(t)
	node2 := newTestNode(t)
	node3 := newTestNode(t)

	namespace := "leader-reboot-rejoin"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	// Closed here as well, for a failure before the close below: a raft
	// left running outlives the node's store and panics the test binary.
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 must be leader")

	linkTo(t, node2, node1)
	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	linkTo(t, node3, node1, node2)
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

	clusters := []Cluster{cluster1, cluster2, cluster3}

	err = onLeader(t, clusters, func(leader Cluster) error {
		return leader.Set("before-leader-crash", []byte("original-value"), 5*time.Second)
	})
	assert.NilError(t, err, "failed to set before-leader-crash")
	for i, cl := range clusters {
		err = waitForKeyValue(t, cl, "before-leader-crash", "original-value", 5*time.Second)
		assert.NilError(t, err, "cluster%d: before-leader-crash not replicated in time", i+1)
	}

	crashed, crashTerm := leadingNow(t, clusters...)
	survivors := without(clusters, crashed)
	t.Logf("Crashing original leader (%s) at term %d...", idOf(crashed), crashTerm)

	err = crashed.Close()
	assert.NilError(t, err, "failed to close the leader")

	newLeader := leaderOf(t, survivors...)
	assert.Assert(t, idOf(newLeader) != idOf(crashed), "new leader must not be crashed node")
	assert.Assert(t, getClusterInternal(newLeader).current().raft.CurrentTerm() > crashTerm,
		"the survivors' leader must lead at a term past the crash")

	err = onLeader(t, survivors, func(leader Cluster) error {
		return leader.Set("during-leader-down", []byte("new-leader-value"), 5*time.Second)
	})
	assert.NilError(t, err, "new leader failed to set data")
	for _, cl := range survivors {
		err = waitForKeyValue(t, cl, "during-leader-down", "new-leader-value", 5*time.Second)
		assert.NilError(t, err, "%s: during-leader-down not replicated in time", idOf(cl))
	}

	t.Logf("Rebooting old leader (%s)...", idOf(crashed))

	rejoined, err := New(nodeOf(crashed), namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to recreate the old leader")
	defer rejoined.Close()

	err = waitForMember(t, survivors[0], idOf(crashed), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "the old leader failed to rejoin after reboot")

	ctx4, cancel4 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel4()
	err = rejoined.WaitForLeader(ctx4)
	assert.NilError(t, err, "the old leader failed to see leader")
	all := append(survivors, rejoined)

	err = waitForKeyValue(t, rejoined, "before-leader-crash", "original-value", 5*time.Second)
	assert.NilError(t, err, "before-leader-crash not replicated in time")
	val, found := rejoined.Get("before-leader-crash")
	assert.Assert(t, found, "old leader must have data from before crash")
	assert.Equal(t, string(val), "original-value")

	err = waitForKeyValue(t, rejoined, "during-leader-down", "new-leader-value", 5*time.Second)
	assert.NilError(t, err, "during-leader-down not replicated in time")
	val, found = rejoined.Get("during-leader-down")
	assert.Assert(t, found, "old leader must have data set during its downtime")
	assert.Equal(t, string(val), "new-leader-value")

	err = onLeader(t, all, func(leader Cluster) error {
		return leader.Set("after-rejoin", []byte("post-rejoin-value"), 5*time.Second)
	})
	assert.NilError(t, err, "failed to set after-rejoin")

	err = waitForKeyValue(t, rejoined, "after-rejoin", "post-rejoin-value", 5*time.Second)
	assert.NilError(t, err, "old leader must receive new data after rejoin")

	poll.WaitOn(t, func(poll.LogT) poll.Result {
		return rejoinSettled(rejoined, all, crashTerm, map[string]string{
			"before-leader-crash": "original-value",
			"during-leader-down":  "new-leader-value",
			"after-rejoin":        "post-rejoin-value",
		})
	}, poll.WithTimeout(leaderWait), poll.WithDelay(10*time.Millisecond))

	t.Logf("Leader reboot and rejoin test passed. New leader: %s, old leader rejoined: %s", idOf(newLeader), idOf(crashed))
}
