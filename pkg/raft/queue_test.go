package raft

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
	"github.com/libp2p/go-libp2p/core/peer"
)

// mockCluster applies commands directly to an in-memory FSM (no raft).
type mockCluster struct {
	fsm FSM
}

func (m *mockCluster) Apply(cmd []byte, _ time.Duration) (FSMResponse, error) {
	log := &raft.Log{Data: cmd, Index: 1}
	resp := m.fsm.Apply(log)
	if fsmResp, ok := resp.(FSMResponse); ok {
		return fsmResp, nil
	}
	return FSMResponse{}, nil
}

func (m *mockCluster) Get(key string) ([]byte, bool) {
	return m.fsm.Get(key)
}

func (m *mockCluster) Keys(prefix string) []string {
	return m.fsm.Keys(prefix)
}

func (m *mockCluster) Close() error                              { return nil }
func (m *mockCluster) Namespace() string                         { return "test" }
func (m *mockCluster) Set(string, []byte, time.Duration) error   { return nil }
func (m *mockCluster) Delete(string, time.Duration) error        { return nil }
func (m *mockCluster) Barrier(time.Duration) error               { return nil }
func (m *mockCluster) IsLeader() bool                            { return true }
func (m *mockCluster) Leader() (peer.ID, error)                  { return "", nil }
func (m *mockCluster) State() raft.RaftState                     { return raft.Leader }
func (m *mockCluster) WaitForLeader(ctx context.Context) error   { return nil }
func (m *mockCluster) Members() ([]Member, error)                { return nil, nil }
func (m *mockCluster) AddVoter(peer.ID, time.Duration) error     { return nil }
func (m *mockCluster) RemoveServer(peer.ID, time.Duration) error { return nil }
func (m *mockCluster) TransferLeadership() error                 { return nil }

func newMockClusterForQueue(t *testing.T) Cluster {
	store := dsync.MutexWrap(datastore.NewMapDatastore())
	fsm := newKVFSM(store, "/raft/test")
	return &mockCluster{fsm: fsm}
}

func TestQueue_EnqueueDequeue(t *testing.T) {
	cluster := newMockClusterForQueue(t)
	qu := NewQueue(cluster, "q").(*queue)
	defer qu.Close()

	id, err := qu.Enqueue([]byte("job1"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Error("expected non-empty id")
	}

	if qu.Len() != 1 {
		t.Errorf("Len() = %d, want 1", qu.Len())
	}

	peekID, peekData, ok := qu.Peek()
	if !ok || peekID != id || !bytes.Equal(peekData, []byte("job1")) {
		t.Errorf("Peek() = %q, %s, %v, want id, job1, true", peekID, peekData, ok)
	}

	gotID, gotData, err := qu.Dequeue(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if gotID != id || !bytes.Equal(gotData, []byte("job1")) {
		t.Errorf("Dequeue() = %q, %s, want %q, job1", gotID, gotData, id)
	}

	if qu.Len() != 0 {
		t.Errorf("Len() after Dequeue = %d, want 0", qu.Len())
	}
}

func TestQueue_DequeueEmpty(t *testing.T) {
	cluster := newMockClusterForQueue(t)
	qu := NewQueue(cluster, "q").(*queue)
	defer qu.Close()

	_, data, err := qu.Dequeue(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Errorf("Dequeue empty want nil data, got %x", data)
	}
}

func TestQueue_LenPeek(t *testing.T) {
	cluster := newMockClusterForQueue(t)
	qu := NewQueue(cluster, "q").(*queue)
	defer qu.Close()

	qu.Enqueue([]byte("a"), 5*time.Second)
	qu.Enqueue([]byte("b"), 5*time.Second)

	if qu.Len() != 2 {
		t.Errorf("Len() = %d, want 2", qu.Len())
	}

	id1, data1, ok := qu.Peek()
	if !ok || !bytes.Equal(data1, []byte("a")) {
		t.Errorf("Peek() = %q, %s, %v", id1, data1, ok)
	}

	qu.Dequeue(5 * time.Second)
	id2, data2, ok := qu.Peek()
	if !ok || !bytes.Equal(data2, []byte("b")) {
		t.Errorf("second Peek() = %q, %s, %v", id2, data2, ok)
	}
}

// TestQueue_ManyItems enqueues more than 10 items, dequeues all in order, and acks each.
func TestQueue_ManyItems(t *testing.T) {
	cluster := newMockClusterForQueue(t)
	qu := NewQueue(cluster, "q").(*queue)
	defer qu.Close()

	const n = 15
	var ids []string
	for i := 0; i < n; i++ {
		payload := []byte(string(rune('a' + i)))
		id, err := qu.Enqueue(payload, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if id == "" {
			t.Fatalf("item %d: expected non-empty id", i)
		}
		ids = append(ids, id)
	}

	if qu.Len() != n {
		t.Errorf("Len() = %d, want %d", qu.Len(), n)
	}

	for i := 0; i < n; i++ {
		gotID, gotData, err := qu.Dequeue(5 * time.Second)
		if err != nil {
			t.Fatalf("Dequeue %d: %v", i, err)
		}
		if gotID != ids[i] {
			t.Errorf("Dequeue %d: id = %q, want %q", i, gotID, ids[i])
		}
		expected := []byte(string(rune('a' + i)))
		if !bytes.Equal(gotData, expected) {
			t.Errorf("Dequeue %d: data = %x, want %x", i, gotData, expected)
		}
	}

	if qu.Len() != 0 {
		t.Errorf("Len() after all Dequeue = %d, want 0", qu.Len())
	}
	_, data, err := qu.Dequeue(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Errorf("Dequeue empty want nil, got %x", data)
	}
}

// TestQueue_MultipleConsumers enqueues many items and has several goroutines
// dequeue and ack concurrently; asserts every item is delivered exactly once.
func TestQueue_MultipleConsumers(t *testing.T) {
	cluster := newMockClusterForQueue(t)
	qu := NewQueue(cluster, "q").(*queue)
	defer qu.Close()

	const numItems = 15
	const numConsumers = 4

	for i := 0; i < numItems; i++ {
		_, err := qu.Enqueue([]byte{byte(i)}, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
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
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for consumers")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, e := range errs {
		t.Error(e)
	}
	if len(collected) != numItems {
		t.Errorf("collected %d items, want %d", len(collected), numItems)
	}
	if len(seen) != numItems {
		t.Errorf("unique ids = %d, want %d", len(seen), numItems)
	}
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
		if !payloadSeen[byte(i)] {
			t.Errorf("missing payload %d", i)
		}
	}
}
