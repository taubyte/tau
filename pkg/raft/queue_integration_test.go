//go:build raft_integration

package raft

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"gotest.tools/v3/assert"
)

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

	qu := NewQueue(cluster, "test")
	defer qu.Close()

	err = qu.Push("job-1", []byte("item1"), 5*time.Second)
	assert.NilError(t, err, "Push failed")

	assert.Equal(t, qu.Len(), 1, "Len() should be 1")
	peekID, peekData, ok := qu.Peek()
	assert.Assert(t, ok, "Peek() should succeed")
	assert.Equal(t, peekID, "job-1")
	assert.Assert(t, bytes.Equal(peekData, []byte("item1")), "Peek data mismatch")

	gotID, gotData, err := qu.Pop(5 * time.Second)
	assert.NilError(t, err, "Pop failed")
	assert.Equal(t, gotID, "job-1")
	assert.Assert(t, bytes.Equal(gotData, []byte("item1")), "Pop data mismatch")

	assert.Equal(t, qu.Len(), 0, "Len() after Pop should be 0")
}

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

	qu1 := NewQueue(cluster1, "repl")
	defer qu1.Close()
	qu2 := NewQueue(cluster2, "repl")
	defer qu2.Close()

	err = qu1.Push("repl-1", []byte("replicated-item"), 5*time.Second)
	assert.NilError(t, err, "Push on leader failed")

	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, qu2.Len(), 1, "follower Len() should be 1 after replication")
	_, peekData, ok := qu2.Peek()
	assert.Assert(t, ok, "follower Peek() should succeed")
	assert.Assert(t, bytes.Equal(peekData, []byte("replicated-item")), "follower Peek data mismatch")

	gotID, gotData, err := qu1.Pop(5 * time.Second)
	assert.NilError(t, err, "Pop failed")
	assert.Equal(t, gotID, "repl-1")
	assert.Assert(t, bytes.Equal(gotData, []byte("replicated-item")), "Pop data mismatch")

	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, qu2.Len(), 0, "follower Len() should be 0 after pop")
}

func TestQueue_Integration_MultiNode_PopEmpty(t *testing.T) {
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

	qu := NewQueue(cluster1, "empty-test")
	defer qu.Close()

	_, _, err = qu.Pop(5 * time.Second)
	assert.Assert(t, errors.Is(err, ErrQueueEmpty), "Pop on empty should return ErrQueueEmpty")
	assert.Equal(t, qu.Len(), 0, "Len() should be 0")
}

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

	qu := NewQueue(cluster1, "order-test")
	defer qu.Close()

	payloads := [][]byte{[]byte("first"), []byte("second"), []byte("third")}
	ids := []string{"order-1", "order-2", "order-3"}
	for i, payload := range payloads {
		err := qu.Push(ids[i], payload, 5*time.Second)
		assert.NilError(t, err, "Push failed")
	}

	assert.Equal(t, qu.Len(), 3, "Len() should be 3")

	for i, expected := range payloads {
		gotID, gotData, err := qu.Pop(5 * time.Second)
		assert.NilError(t, err, "Pop %d failed", i)
		assert.Equal(t, gotID, ids[i], "Pop %d id mismatch", i)
		assert.Assert(t, bytes.Equal(gotData, expected), "Pop %d data mismatch", i)
	}

	_, _, err = qu.Pop(5 * time.Second)
	assert.Assert(t, errors.Is(err, ErrQueueEmpty), "queue should be empty")
}

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
	qu1 := NewQueue(cluster1, "many-test")
	defer qu1.Close()
	qu2 := NewQueue(cluster2, "many-test")
	defer qu2.Close()

	for i := range n {
		err := qu1.Push(fmt.Sprintf("many-%02d", i), []byte{byte(i)}, 5*time.Second)
		assert.NilError(t, err, "Push %d failed", i)
	}

	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, qu2.Len(), n, "follower Len() should be %d after replication", n)

	for i := range n {
		gotID, gotData, err := qu1.Pop(5 * time.Second)
		assert.NilError(t, err, "Pop %d failed", i)
		assert.Equal(t, gotID, fmt.Sprintf("many-%02d", i), "Pop %d id mismatch", i)
		assert.Assert(t, bytes.Equal(gotData, []byte{byte(i)}), "Pop %d data mismatch", i)
	}

	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, qu2.Len(), 0, "follower Len() should be 0 after all popped")
	_, _, err = qu1.Pop(5 * time.Second)
	assert.Assert(t, errors.Is(err, ErrQueueEmpty), "queue should be empty")
}

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

	qu := NewQueue(cluster1, "multiconsumer")
	defer qu.Close()

	for i := range numItems {
		err := qu.Push(fmt.Sprintf("mc-%02d", i), []byte{byte(i)}, 5*time.Second)
		assert.NilError(t, err, "Push %d failed", i)
	}

	var mu sync.Mutex
	seen := make(map[string]bool)
	var collected [][]byte
	var errs []string

	var wg sync.WaitGroup
	wg.Add(numConsumers)
	for range numConsumers {
		go func() {
			defer wg.Done()
			for {
				id, data, err := qu.Pop(5 * time.Second)
				if errors.Is(err, ErrQueueEmpty) {
					return
				}
				if err != nil {
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
	for i := range numItems {
		assert.Assert(t, payloadSeen[byte(i)], "missing payload %d", i)
	}
}
