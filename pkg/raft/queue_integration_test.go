package raft

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"gotest.tools/v3/assert"
)

// TestQueue_Integration_SingleNode runs queue Enqueue/Dequeue against a
// single-node (real) raft cluster.
func TestQueue_Integration_SingleNode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping queue integration test in short mode")
	}

	node := newTestNode(t)
	namespace := "queue-single"

	cluster, err := New(node, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster")
	defer cluster.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = cluster.WaitForLeader(ctx)
	assert.NilError(t, err, "failed to wait for leader")
	assert.Assert(t, cluster.IsLeader(), "single node should be leader")

	qu := NewQueue(cluster, "queue/patrick")
	defer qu.Close()

	id, err := qu.Enqueue([]byte("job1"), 5*time.Second)
	assert.NilError(t, err, "Enqueue failed")
	assert.Assert(t, id != "", "expected non-empty id")

	assert.Equal(t, qu.Len(), 1, "Len() should be 1")
	peekID, peekData, ok := qu.Peek()
	assert.Assert(t, ok, "Peek() should succeed")
	assert.Equal(t, peekID, id)
	assert.Assert(t, bytes.Equal(peekData, []byte("job1")), "Peek data mismatch")

	gotID, gotData, err := qu.Dequeue(5 * time.Second)
	assert.NilError(t, err, "Dequeue failed")
	assert.Equal(t, gotID, id)
	assert.Assert(t, bytes.Equal(gotData, []byte("job1")), "Dequeue data mismatch")

	assert.Equal(t, qu.Len(), 0, "Len() after Dequeue should be 0")
}

// TestQueue_Integration_MultiNode_Replication enqueues on the leader and
// verifies the follower sees the item after replication; then Dequeue
// and verifies follower sees empty queue.
func TestQueue_Integration_MultiNode_Replication(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping queue integration test in short mode")
	}

	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}
	node2 := newTestNode(t, node1Info)

	time.Sleep(2 * time.Second)

	namespace := "queue-replication"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()
	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	prefix := "queue/patrick"
	qu1 := NewQueue(cluster1, prefix)
	defer qu1.Close()
	qu2 := NewQueue(cluster2, prefix)
	defer qu2.Close()

	id, err := qu1.Enqueue([]byte("replicated-job"), 5*time.Second)
	assert.NilError(t, err, "Enqueue on leader failed")
	assert.Assert(t, id != "", "expected non-empty id")

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Follower reads from local FSM state
	assert.Equal(t, qu2.Len(), 1, "follower Len() should be 1 after replication")
	_, data, ok := qu2.Peek()
	assert.Assert(t, ok, "follower Peek() should succeed")
	assert.Assert(t, bytes.Equal(data, []byte("replicated-job")), "follower Peek data mismatch")

	// Leader dequeue (removes item)
	gotID, gotData, err := qu1.Dequeue(5 * time.Second)
	assert.NilError(t, err, "Dequeue failed")
	assert.Equal(t, gotID, id)
	assert.Assert(t, bytes.Equal(gotData, []byte("replicated-job")), "Dequeue data mismatch")

	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, qu2.Len(), 0, "follower Len() should be 0 after dequeue")
}

// TestQueue_Integration_MultiNode_DequeueEmpty verifies Dequeue on empty returns empty.
func TestQueue_Integration_MultiNode_DequeueEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping queue integration test in short mode")
	}

	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}
	node2 := newTestNode(t, node1Info)

	time.Sleep(2 * time.Second)

	namespace := "queue-empty"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")

	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()
	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	qu := NewQueue(cluster1, "queue/empty-test")
	defer qu.Close()

	_, data, err := qu.Dequeue(5 * time.Second)
	assert.NilError(t, err, "Dequeue on empty should not error")
	assert.Assert(t, data == nil, "Dequeue on empty should return nil data")
	assert.Equal(t, qu.Len(), 0, "Len() should be 0")
}

// TestQueue_Integration_MultiNode_Order enqueues multiple items and verifies
// Dequeue returns them in FIFO order.
func TestQueue_Integration_MultiNode_Order(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping queue integration test in short mode")
	}

	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}
	node2 := newTestNode(t, node1Info)

	time.Sleep(2 * time.Second)

	namespace := "queue-order"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")

	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()
	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	qu := NewQueue(cluster1, "queue/order-test")
	defer qu.Close()

	ids := make([]string, 0, 3)
	for _, payload := range [][]byte{[]byte("first"), []byte("second"), []byte("third")} {
		id, err := qu.Enqueue(payload, 5*time.Second)
		assert.NilError(t, err, "Enqueue failed")
		ids = append(ids, id)
	}

	assert.Equal(t, qu.Len(), 3, "Len() should be 3")

	for i, expected := range [][]byte{[]byte("first"), []byte("second"), []byte("third")} {
		gotID, gotData, err := qu.Dequeue(5 * time.Second)
		assert.NilError(t, err, "Dequeue %d failed", i)
		assert.Equal(t, gotID, ids[i], "Dequeue %d id mismatch", i)
		assert.Assert(t, bytes.Equal(gotData, expected), "Dequeue %d data mismatch", i)
	}

	_, data, err := qu.Dequeue(5 * time.Second)
	assert.NilError(t, err, "final Dequeue should not error")
	assert.Assert(t, data == nil, "queue should be empty")
}

// TestQueue_Integration_MultiNode_ManyItems enqueues more than 10 items on the
// leader, verifies replication to follower, then dequeues and acks all in order.
func TestQueue_Integration_MultiNode_ManyItems(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping queue integration test in short mode")
	}

	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}
	node2 := newTestNode(t, node1Info)

	time.Sleep(2 * time.Second)

	namespace := "queue-many"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")
	assert.Assert(t, cluster1.IsLeader(), "cluster1 should be leader")

	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()
	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	const n = 15
	prefix := "queue/many-test"
	qu1 := NewQueue(cluster1, prefix)
	defer qu1.Close()
	qu2 := NewQueue(cluster2, prefix)
	defer qu2.Close()

	var ids []string
	for i := 0; i < n; i++ {
		payload := []byte(string(rune('a' + i)))
		id, err := qu1.Enqueue(payload, 5*time.Second)
		assert.NilError(t, err, "Enqueue %d failed", i)
		ids = append(ids, id)
	}

	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, qu2.Len(), n, "follower Len() should be %d after replication", n)

	for i := 0; i < n; i++ {
		gotID, gotData, err := qu1.Dequeue(5 * time.Second)
		assert.NilError(t, err, "Dequeue %d failed", i)
		assert.Equal(t, gotID, ids[i], "Dequeue %d id mismatch", i)
		expected := []byte(string(rune('a' + i)))
		assert.Assert(t, bytes.Equal(gotData, expected), "Dequeue %d data mismatch", i)
	}

	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, qu2.Len(), 0, "follower Len() should be 0 after all dequeued")
	_, data, err := qu1.Dequeue(5 * time.Second)
	assert.NilError(t, err)
	assert.Assert(t, data == nil, "queue should be empty")
}

// TestQueue_Integration_MultiNode_MultipleConsumers enqueues many items and
// has multiple goroutines dequeue/ack concurrently; asserts no duplicate delivery.
func TestQueue_Integration_MultiNode_MultipleConsumers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping queue integration test in short mode")
	}

	node1 := newTestNode(t)
	node1Info := peercore.AddrInfo{ID: node1.ID(), Addrs: node1.Peer().Addrs()}
	node2 := newTestNode(t, node1Info)

	time.Sleep(2 * time.Second)

	namespace := "queue-multi-consumer"

	cluster1, err := New(node1, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	assert.NilError(t, err, "failed to create cluster1")
	defer cluster1.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = cluster1.WaitForLeader(ctx)
	assert.NilError(t, err, "cluster1 failed to wait for leader")

	cluster2, err := New(node2, namespace, WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(500*time.Millisecond))
	assert.NilError(t, err, "failed to create cluster2")
	defer cluster2.Close()

	err = waitForMember(t, cluster1, node2.ID(), raft.Voter, 20*time.Second)
	assert.NilError(t, err, "node2 failed to join as voter")

	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel2()
	err = cluster2.WaitForLeader(ctx2)
	assert.NilError(t, err, "cluster2 failed to wait for leader")

	const numItems = 15
	const numConsumers = 4

	qu := NewQueue(cluster1, "queue/multiconsumer")
	defer qu.Close()

	for i := 0; i < numItems; i++ {
		_, err := qu.Enqueue([]byte{byte(i)}, 5*time.Second)
		assert.NilError(t, err, "Enqueue %d failed", i)
	}

	var mu sync.Mutex
	seen := make(map[string]bool)
	var collected [][]byte
	var errs []string

	var wg sync.WaitGroup
	wg.Add(numConsumers)
	for c := 0; c < numConsumers; c++ {
		go func() {
			defer wg.Done()
			for {
				id, data, err := qu.Dequeue(5 * time.Second)
				if err != nil {
					return
				}
				if id == "" && data == nil {
					return
				}
				mu.Lock()
				if seen[id] {
					errs = append(errs, "duplicate delivery: id "+id)
				}
				seen[id] = true
				collected = append(collected, data)
				mu.Unlock()
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("timeout waiting for consumers")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, e := range errs {
		t.Error(e)
	}
	assert.Equal(t, len(collected), numItems, "collected item count")
	assert.Equal(t, len(seen), numItems, "unique id count")
	payloadSeen := make(map[byte]bool)
	for _, data := range collected {
		if len(data) != 1 {
			t.Errorf("unexpected payload len %d", len(data))
			continue
		}
		if payloadSeen[data[0]] {
			t.Errorf("duplicate payload %d", data[0])
		}
		payloadSeen[data[0]] = true
	}
	for i := 0; i < numItems; i++ {
		assert.Assert(t, payloadSeen[byte(i)], "missing payload %d", i)
	}
}
