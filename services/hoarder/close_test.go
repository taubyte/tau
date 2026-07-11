package hoarder

import (
	"context"
	"testing"
	"time"
)

// TestClose_JoinsLoops pins F5's Close contract: Close cancels the loop context
// and then blocks on loopsWG.Wait() until every tracked loop has returned, so it
// never tears down srv.db / srv.ldr / the clients under a still-running loop.
//
// It starts the real membership+reconcile loops (which exit promptly on cancel)
// plus one stand-in loop registered on loopsWG exactly like them but held open,
// giving a deterministic window to observe that Close does not return early.
func TestClose_JoinsLoops(t *testing.T) {
	srv := newTestService(t)

	ctx, cancel := context.WithCancel(context.Background())
	srv.reconcileCancel = cancel

	if err := srv.startMembership(ctx); err != nil {
		t.Fatalf("startMembership: %v", err)
	}
	if err := srv.startReconcile(ctx); err != nil {
		t.Fatalf("startReconcile: %v", err)
	}

	// A tracked loop that exits on cancel like the real ones, but whose final
	// return we gate on release — so Close's Wait() must block on it.
	started := make(chan struct{})
	release := make(chan struct{})
	srv.loopsWG.Add(1)
	go func() {
		defer srv.loopsWG.Done()
		close(started)
		<-ctx.Done() // Close cancels; a real loop returns here
		<-release    // we delay the actual return to observe the join
	}()
	<-started

	done := make(chan error, 1)
	go func() { done <- srv.Close() }()

	// Close must have cancelled (so ctx.Done fired) yet cannot return while a
	// tracked loop is still running.
	select {
	case <-done:
		t.Fatal("Close returned before the tracked loop exited: it did not join loopsWG")
	case <-time.After(150 * time.Millisecond):
	}

	close(release) // let the loop finish; Close's Wait() should now unblock
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the tracked loop exited")
	}
}
