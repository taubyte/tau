package hoarder

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
)

// Production 2026-11-07 (node .169): one slow ReplicasOf ran under r.mu, so
// 8k kv ops queued on that mutex with no deadline. Concurrent ops on a handle
// whose replica lookup hangs must share one lookup and give up at their own
// deadline instead of waiting for each other.
func TestRemoteKV_HungReplicaLookup_DoesNotSerializeCallers(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var lookups atomic.Int32
	r := &remoteKV{resolve: func() ([]peerCore.ID, error) {
		lookups.Add(1)
		<-release
		return nil, nil
	}}

	const n = 50
	const bound = 200 * time.Millisecond
	start := time.Now()
	var wg sync.WaitGroup
	var failed atomic.Int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), bound)
			defer cancel()
			if _, err := r.target(ctx); err != nil {
				failed.Add(1)
			}
		}()
	}
	wg.Wait()

	if el := time.Since(start); el > 2*bound {
		t.Fatalf("%d callers took %v; they queued behind the lookup", n, el)
	}
	if failed.Load() != n {
		t.Fatalf("want all %d callers to fail at their deadline, %d did", n, failed.Load())
	}
	if l := lookups.Load(); l != 1 {
		t.Fatalf("want one shared replica lookup, got %d", l)
	}
}

// A database Put (batch commit) on a hung replica must fail within the
// caller's deadline: the batch carries the ctx it was opened with.
func TestRemoteBatch_CommitHonorsBatchContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	r := &remoteKV{resolve: func() ([]peerCore.ID, error) {
		<-release
		return nil, nil
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	b, _ := r.BatchWithMax(ctx, 0)
	b.Put("k", []byte("v"))

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- b.Commit() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("commit on a hung replica returned nil")
		}
		if el := time.Since(start); el > time.Second {
			t.Fatalf("commit took %v, want about the 200ms deadline", el)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("commit on a hung replica never returned")
	}
}
