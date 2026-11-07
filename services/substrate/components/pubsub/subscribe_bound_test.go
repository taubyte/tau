package pubsub

import (
	"runtime"
	"testing"
	"time"
)

// Production 2026-11-07 (node .169): every message started a goroutine before
// it had a worker slot, and those goroutines then held VM instances. A burst of
// messages to a function whose handler blocks must not grow goroutines past
// the worker count: messages beyond it are dropped.
func TestDispatch_BlockedHandlerStaysBounded(t *testing.T) {
	const workers = 64
	slots := make(chan struct{}, workers)
	release := make(chan struct{})
	defer close(release)

	before := runtime.NumGoroutine()
	var ran int
	for i := 0; i < 1000; i++ {
		if dispatch(slots, func() { <-release }) {
			ran++
		}
	}
	time.Sleep(50 * time.Millisecond)

	if grew := runtime.NumGoroutine() - before; grew > workers+5 {
		t.Fatalf("1000 messages to a blocked handler left %d goroutines, want <= %d", grew, workers)
	}
	if ran != workers {
		t.Fatalf("want %d messages handled and the rest dropped, handled %d", workers, ran)
	}
}
