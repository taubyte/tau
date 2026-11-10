package seer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// At startup no seer may answer yet. A failed announce is retried soon, with
// growing waits, not at the next 10 minute refresh; a success waits the full
// interval.
func TestAnnounceLoopRetriesFailuresSoon(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		lock  sync.Mutex
		calls []time.Time
	)
	unreachable := errors.New("no seer yet")
	announce := func() error {
		lock.Lock()
		defer lock.Unlock()
		calls = append(calls, time.Now())
		if len(calls) <= 3 {
			return unreachable
		}
		return nil
	}
	var failures int
	failed := func(err error) {
		if !errors.Is(err, unreachable) {
			t.Errorf("unexpected error %v", err)
		}
		lock.Lock()
		failures++
		lock.Unlock()
	}

	retry, interval := 20*time.Millisecond, time.Hour
	go announceLoop(ctx, announce, failed, retry, interval)

	deadline := time.Now().Add(2 * time.Second)
	for {
		lock.Lock()
		n := len(calls)
		lock.Unlock()
		if n >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d announces in 2s; failures must be retried soon", n)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// the waits double: 20ms, 40ms, 80ms
	lock.Lock()
	gaps := []time.Duration{calls[1].Sub(calls[0]), calls[2].Sub(calls[1]), calls[3].Sub(calls[2])}
	lock.Unlock()
	for i, want := range []time.Duration{retry, 2 * retry, 4 * retry} {
		if gaps[i] < want {
			t.Fatalf("wait %d was %s, want at least %s", i, gaps[i], want)
		}
	}

	// after the success the loop waits the full interval
	time.Sleep(10 * retry)
	lock.Lock()
	n, f := len(calls), failures
	lock.Unlock()
	if n != 4 || f != 3 {
		t.Fatalf("%d announces, %d failures after the success; want 4 and 3", n, f)
	}
}
