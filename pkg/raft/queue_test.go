package raft

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestQueue_PushPop(t *testing.T) {
	qu := NewQueue(NewMockCluster(), "test")
	defer qu.Close()

	err := qu.Push("job-1", []byte("item1"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if qu.Len() != 1 {
		t.Errorf("Len() = %d, want 1", qu.Len())
	}

	id, data, ok := qu.Peek()
	if !ok || id != "job-1" || !bytes.Equal(data, []byte("item1")) {
		t.Errorf("Peek() = (%q, %q, %v); want (job-1, item1, true)", id, data, ok)
	}

	gotID, gotData, err := qu.Pop(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if gotID != "job-1" || !bytes.Equal(gotData, []byte("item1")) {
		t.Errorf("Pop() = (%q, %q); want (job-1, item1)", gotID, gotData)
	}

	if qu.Len() != 0 {
		t.Errorf("Len() after Pop = %d, want 0", qu.Len())
	}
}

func TestQueue_PopEmpty(t *testing.T) {
	qu := NewQueue(NewMockCluster(), "test")
	defer qu.Close()

	_, _, err := qu.Pop(5 * time.Second)
	if !errors.Is(err, ErrQueueEmpty) {
		t.Errorf("Pop empty: got %v, want ErrQueueEmpty", err)
	}
}

func TestQueue_Dedup(t *testing.T) {
	qu := NewQueue(NewMockCluster(), "test")
	defer qu.Close()

	err := qu.Push("same-id", []byte("first"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	err = qu.Push("same-id", []byte("second"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if qu.Len() != 1 {
		t.Errorf("Len() after duplicate push = %d, want 1 (dedup)", qu.Len())
	}
	id, data, err := qu.Pop(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if id != "same-id" || !bytes.Equal(data, []byte("first")) {
		t.Errorf("Pop() = (%q, %q); want first item (dedup kept original)", id, data)
	}
}

func TestQueue_FIFOOrder(t *testing.T) {
	qu := NewQueue(NewMockCluster(), "test")
	defer qu.Close()

	payloads := []string{"first", "second", "third"}
	ids := []string{"id-1", "id-2", "id-3"}
	for i, p := range payloads {
		err := qu.Push(ids[i], []byte(p), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
	}

	if qu.Len() != 3 {
		t.Errorf("Len() = %d, want 3", qu.Len())
	}

	for i, expected := range payloads {
		gotID, gotData, err := qu.Pop(5 * time.Second)
		if err != nil {
			t.Fatalf("Pop %d: %v", i, err)
		}
		if gotID != ids[i] {
			t.Errorf("Pop %d: id = %q, want %q", i, gotID, ids[i])
		}
		if string(gotData) != expected {
			t.Errorf("Pop %d: data = %s, want %s", i, gotData, expected)
		}
	}
}

func TestQueue_ManyItems(t *testing.T) {
	qu := NewQueue(NewMockCluster(), "test")
	defer qu.Close()

	const n = 20
	for i := range n {
		err := qu.Push(fmt.Sprintf("id-%02d", i), []byte{byte(i)}, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
	}

	if qu.Len() != n {
		t.Errorf("Len() = %d, want %d", qu.Len(), n)
	}

	for i := range n {
		_, gotData, err := qu.Pop(5 * time.Second)
		if err != nil {
			t.Fatalf("Pop %d: %v", i, err)
		}
		if len(gotData) != 1 || gotData[0] != byte(i) {
			t.Errorf("Pop %d: data = %x, want %x", i, gotData, []byte{byte(i)})
		}
	}

	if qu.Len() != 0 {
		t.Errorf("Len() after all Pop = %d, want 0", qu.Len())
	}
}

func TestQueue_MultipleConsumers(t *testing.T) {
	qu := NewQueue(NewMockCluster(), "test")
	defer qu.Close()

	const numItems = 20
	const numConsumers = 4

	for i := range numItems {
		err := qu.Push(fmt.Sprintf("id-%02d", i), []byte{byte(i)}, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	seen := make(map[string]bool)
	var collected [][]byte

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
					t.Errorf("duplicate delivery: id %s", id)
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
	if len(collected) != numItems {
		t.Errorf("collected %d items, want %d", len(collected), numItems)
	}
	payloadSeen := make(map[byte]bool)
	for _, data := range collected {
		if len(data) != 1 {
			t.Errorf("unexpected payload len %d", len(data))
			continue
		}
		payloadSeen[data[0]] = true
	}
	for i := range numItems {
		if !payloadSeen[byte(i)] {
			t.Errorf("missing payload %d", i)
		}
	}
}

func TestQueue_ClosedOperations(t *testing.T) {
	qu := NewQueue(NewMockCluster(), "test")
	qu.Close()

	err := qu.Push("x", []byte("x"), 5*time.Second)
	if err != ErrShutdown {
		t.Errorf("Push after Close: got %v, want ErrShutdown", err)
	}

	_, _, err = qu.Pop(5 * time.Second)
	if err != ErrShutdown {
		t.Errorf("Pop after Close: got %v, want ErrShutdown", err)
	}

	if qu.Len() != 0 {
		t.Errorf("Len after Close = %d, want 0", qu.Len())
	}
}

func TestQueue_EmptyID(t *testing.T) {
	qu := NewQueue(NewMockCluster(), "test")
	defer qu.Close()

	err := qu.Push("", []byte("x"), 5*time.Second)
	if err == nil {
		t.Error("Push with empty id should fail")
	}
}

func TestQueue_KeyIsolation(t *testing.T) {
	mc := NewMockCluster()
	q1 := NewQueue(mc, "alpha")
	q2 := NewQueue(mc, "beta")
	defer q1.Close()
	defer q2.Close()

	err := q1.Push("a", []byte("a"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	err = q2.Push("b", []byte("b"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if q1.Len() != 1 {
		t.Errorf("q1.Len() = %d, want 1", q1.Len())
	}
	if q2.Len() != 1 {
		t.Errorf("q2.Len() = %d, want 1", q2.Len())
	}

	id1, data1, err := q1.Pop(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != "a" || !bytes.Equal(data1, []byte("a")) {
		t.Errorf("q1 Pop = (%q, %s), want (a, a)", id1, data1)
	}

	id2, data2, err := q2.Pop(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if id2 != "b" || !bytes.Equal(data2, []byte("b")) {
		t.Errorf("q2 Pop = (%q, %s), want (b, b)", id2, data2)
	}
}

func TestQueue_SnapshotRestoreRoundTrip(t *testing.T) {
	mc1 := NewMockCluster()
	q1 := NewQueue(mc1, "test")
	defer q1.Close()

	items := []struct {
		id   string
		data []byte
	}{
		{"job-1", []byte("data-1")},
		{"job-2", []byte("data-2")},
		{"job-3", []byte("data-3")},
	}
	for _, item := range items {
		err := q1.Push(item.id, item.data, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
	}

	mc1.mu.Lock()
	snapshot := make(map[string][]byte, len(mc1.data))
	for k, v := range mc1.data {
		cp := make([]byte, len(v))
		copy(cp, v)
		snapshot[k] = cp
	}
	mc1.mu.Unlock()

	mc2 := NewMockCluster()
	mc2.mu.Lock()
	mc2.data = snapshot
	mc2.mu.Unlock()

	q2 := NewQueue(mc2, "test")
	defer q2.Close()

	for _, want := range items {
		id, data, err := q2.Pop(5 * time.Second)
		if err != nil {
			t.Fatalf("Pop: %v", err)
		}
		if id != want.id {
			t.Errorf("id = %q, want %q", id, want.id)
		}
		if !bytes.Equal(data, want.data) {
			t.Errorf("data = %q, want %q", data, want.data)
		}
	}

	_, _, err := q2.Pop(5 * time.Second)
	if !errors.Is(err, ErrQueueEmpty) {
		t.Errorf("expected ErrQueueEmpty after draining, got %v", err)
	}
}

func TestQueue_SnapshotRestorePartialPop(t *testing.T) {
	mc1 := NewMockCluster()
	q1 := NewQueue(mc1, "test")
	defer q1.Close()

	for i := range 5 {
		err := q1.Push(fmt.Sprintf("job-%d", i), []byte{byte(i)}, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
	}

	for range 2 {
		_, _, err := q1.Pop(5 * time.Second)
		if err != nil {
			t.Fatal(err)
		}
	}

	mc1.mu.Lock()
	snapshot := make(map[string][]byte, len(mc1.data))
	for k, v := range mc1.data {
		cp := make([]byte, len(v))
		copy(cp, v)
		snapshot[k] = cp
	}
	mc1.mu.Unlock()

	mc2 := NewMockCluster()
	mc2.mu.Lock()
	mc2.data = snapshot
	mc2.mu.Unlock()

	q2 := NewQueue(mc2, "test")
	defer q2.Close()

	if q2.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", q2.Len())
	}

	id, data, err := q2.Pop(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if id != "job-2" || data[0] != 2 {
		t.Errorf("expected job-2/2, got %s/%d", id, data[0])
	}
}

func TestQueue_NilData(t *testing.T) {
	qu := NewQueue(NewMockCluster(), "test")
	defer qu.Close()

	err := qu.Push("id-nil", nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	id, data, err := qu.Pop(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if id != "id-nil" {
		t.Errorf("id = %q, want id-nil", id)
	}
	if len(data) != 0 {
		t.Errorf("data = %x, want empty", data)
	}
}
