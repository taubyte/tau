package hoarder

import (
	"testing"
)

// TestSubscribeReconcile_ResubscribeIsSubscribeOnly pins F3's core invariant:
// the reconcile error-recovery path re-subscribes only. A second
// subscribeReconcile (what the error callback runs on a mid-life drop) must not
// reallocate reconcileTrigger under the running loop nor spawn a second loop.
func TestSubscribeReconcile_ResubscribeIsSubscribeOnly(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()

	// startReconcile owns the queue; subscribeReconcile must never touch it.
	srv.reconcileTrigger = make(chan string, 1024)
	before := srv.reconcileTrigger

	// A buffered item nothing should ever drain: only reconcileLoop consumes the
	// trigger, and subscribeReconcile spawns no loop.
	srv.enqueueReconcile("sentinel")

	if err := srv.subscribeReconcile(ctx); err != nil {
		t.Fatalf("first subscribeReconcile failed: %v", err)
	}
	// The resubscribe the error callback would run.
	if err := srv.subscribeReconcile(ctx); err != nil {
		t.Fatalf("resubscribeReconcile failed: %v", err)
	}

	if srv.reconcileTrigger != before {
		t.Fatal("resubscribing must not reallocate reconcileTrigger (data race + lost queue under the running loop)")
	}
	if len(srv.reconcileTrigger) != 1 {
		t.Fatalf("resubscribing must not spawn a reconcile loop; buffered trigger got drained (len=%d)", len(srv.reconcileTrigger))
	}
}

// TestSubscribeMembership_ResubscribeKeepsIncarnation pins the membership half of
// F3: a second subscribeMembership (the error-recovery path) must not re-stamp
// incarnation — a mid-life resubscribe must not make peers treat us as a new
// incarnation, and must not restart the heartbeat/liveness loops.
func TestSubscribeMembership_ResubscribeKeepsIncarnation(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()

	const marker = int64(1234567890)
	srv.incarnation = marker

	if err := srv.subscribeMembership(ctx); err != nil {
		t.Fatalf("first subscribeMembership failed: %v", err)
	}
	// The resubscribe the error callback would run.
	if err := srv.subscribeMembership(ctx); err != nil {
		t.Fatalf("resubscribeMembership failed: %v", err)
	}

	if srv.incarnation != marker {
		t.Fatalf("resubscribing must not reset incarnation: got %d, want %d", srv.incarnation, marker)
	}
}
