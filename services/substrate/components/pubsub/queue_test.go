package pubsub

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	iface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
)

var runs atomic.Int64

// testFn names a function no other test run has used, so counts start at 0.
func testFn(t *testing.T) Function {
	return Function{Project: "p", App: "a", Function: fmt.Sprint(t.Name(), runs.Add(1))}
}

// (a) A short burst to a handler that blocks briefly loses nothing.
func TestQueue_BurstIsFullyHandled(t *testing.T) {
	var handled atomic.Int64
	fn := testFn(t)
	q := newQueue(context.Background(), fn, func(iface.Message) {
		time.Sleep(5 * time.Millisecond)
		handled.Add(1)
	})
	for i := 0; i < 500; i++ {
		q.push(nil)
	}
	deadline := time.Now().Add(10 * time.Second)
	for handled.Load() < 500 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := handled.Load(); n != 500 {
		t.Fatalf("handled %d of 500", n)
	}
	for _, r := range []string{DropQueueFull, DropTooOld, DropClosing} {
		if d := DroppedMessages(fn, r); d != 0 {
			t.Fatalf("%d dropped (%s)", d, r)
		}
	}
}

// (b) A flood to a handler that never returns keeps the queue, goroutines and
// memory bounded, and counts what it drops.
func TestQueue_FloodStaysBoundedAndCounts(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	before := runtime.NumGoroutine()
	fn := testFn(t)
	q := newQueue(context.Background(), fn, func(iface.Message) { <-release })
	const sent = 5000
	for i := 0; i < sent; i++ {
		q.push(nil)
	}
	time.Sleep(50 * time.Millisecond)
	if n := q.queued(); n > MaxQueuedMessages {
		t.Fatalf("%d queued, want <= %d", n, MaxQueuedMessages)
	}
	if grew := runtime.NumGoroutine() - before; grew > MaxConcurrentMessages+5 {
		t.Fatalf("%d goroutines, want <= %d", grew, MaxConcurrentMessages)
	}
	want := uint64(sent - MaxConcurrentMessages - MaxQueuedMessages)
	if d := DroppedMessages(fn, DropQueueFull); d != want {
		t.Fatalf("dropped %d as queue full, want %d", d, want)
	}
}

// (c) A message that waited longer than MaxQueueWait is dropped, not run.
func TestQueue_OldMessageDropped(t *testing.T) {
	defer func(w time.Duration, c int) { MaxQueueWait, MaxConcurrentMessages = w, c }(MaxQueueWait, MaxConcurrentMessages)
	MaxQueueWait, MaxConcurrentMessages = 20*time.Millisecond, 1

	release := make(chan struct{})
	var handled atomic.Int64
	fn := testFn(t)
	q := newQueue(context.Background(), fn, func(iface.Message) {
		<-release
		handled.Add(1)
	})
	q.push(nil) // runs, blocks
	q.push(nil) // waits
	time.Sleep(50 * time.Millisecond)
	close(release)
	time.Sleep(50 * time.Millisecond)
	if n := handled.Load(); n != 1 {
		t.Fatalf("handled %d, want 1", n)
	}
	if d := DroppedMessages(fn, DropTooOld); d != 1 {
		t.Fatalf("dropped %d as waited too long, want 1", d)
	}
}

// Closing lets queued messages go, counted, without waiting on the handler.
func TestQueue_CloseDropsQueued(t *testing.T) {
	defer func(c int) { MaxConcurrentMessages = c }(MaxConcurrentMessages)
	MaxConcurrentMessages = 1
	release := make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	fn := testFn(t)
	q := newQueue(ctx, fn, func(iface.Message) { <-release })
	for i := 0; i < 4; i++ {
		q.push(nil)
	}
	cancel()
	q.close()
	q.push(nil)
	if d := DroppedMessages(fn, DropClosing); d != 4 {
		t.Fatalf("dropped %d as closing, want 4 (3 queued + 1 after close)", d)
	}
	if n := q.queued(); n != 0 {
		t.Fatalf("%d still queued", n)
	}
}

// (d) The drop counter is visible per function and reason.
func TestQueue_DropCounterVisible(t *testing.T) {
	defer func(c, m int) { MaxConcurrentMessages, MaxQueuedMessages = c, m }(MaxConcurrentMessages, MaxQueuedMessages)
	MaxConcurrentMessages, MaxQueuedMessages = 1, 1
	release := make(chan struct{})
	defer close(release)
	fn := testFn(t)
	q := newQueue(context.Background(), fn, func(iface.Message) { <-release })
	for i := 0; i < 3; i++ {
		q.push(nil)
	}
	var found bool
	for _, d := range DroppedCounts() {
		if d.Function == fn && d.Reason == DropQueueFull && d.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("pubsub_dropped_messages_total{function=%q,reason=%q} not listed", fn.Function, DropQueueFull)
	}
}
