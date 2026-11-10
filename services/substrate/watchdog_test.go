package substrate

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func fastWatchdog(t *testing.T, misses int) chan struct{} {
	oi, od, om, oe := watchdogInterval, watchdogDeadline, watchdogMisses, watchdogExit
	t.Cleanup(func() { watchdogInterval, watchdogDeadline, watchdogMisses, watchdogExit = oi, od, om, oe })
	watchdogInterval, watchdogDeadline, watchdogMisses = 20*time.Millisecond, 30*time.Millisecond, misses
	exited := make(chan struct{}, 1)
	watchdogExit = func() { exited <- struct{}{} }
	return exited
}

func TestWatchdogExitsOnHang(t *testing.T) {
	exited := fastWatchdog(t, 3)
	release := make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startWatchdog(ctx, func() { <-release })
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("hung probe did not cause an exit")
	}
}

func TestWatchdogIgnoresErrors(t *testing.T) {
	exited := fastWatchdog(t, 2)
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startWatchdog(ctx, func() { calls.Add(1); time.Sleep(5 * time.Millisecond) })
	select {
	case <-exited:
		t.Fatal("a probe that returns must never restart the node")
	case <-time.After(500 * time.Millisecond):
	}
	if calls.Load() < 10 {
		t.Fatalf("probe ran only %d times", calls.Load())
	}
}

func TestWatchdogRecoveryResetsCount(t *testing.T) {
	exited := fastWatchdog(t, 4)
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// every 4th probe is slow enough for 1-2 misses, then returns
	startWatchdog(ctx, func() {
		if calls.Add(1)%4 == 0 {
			time.Sleep(70 * time.Millisecond)
		}
	})
	select {
	case <-exited:
		t.Fatal("misses must reset once a probe returns")
	case <-time.After(1500 * time.Millisecond):
	}
}

func fastStuck(t *testing.T) chan struct{} {
	exited := fastWatchdog(t, 1)
	oa, om, ot := stuckAfter, stuckMin, stuckTicks
	t.Cleanup(func() { stuckAfter, stuckMin, stuckTicks = oa, om, ot })
	stuckAfter, stuckMin, stuckTicks = time.Hour, 20, 3
	return exited
}

func runStuck(t *testing.T, n func() int) bool {
	exited := fastStuck(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startStuckWatchdog(ctx, func(time.Duration) int { return n() })
	select {
	case <-exited:
		return true
	case <-time.After(300 * time.Millisecond):
		return false
	}
}

func TestStuckWatchdogFiresAtN(t *testing.T) {
	if !runStuck(t, func() int { return 20 }) {
		t.Fatal("20 stuck requests did not cause an exit")
	}
}

func TestStuckWatchdogIgnoresFewer(t *testing.T) {
	if runStuck(t, func() int { return 19 }) {
		t.Fatal("fewer than N stuck requests caused an exit")
	}
}

func TestStuckWatchdogRecoveryResets(t *testing.T) {
	var calls atomic.Int32
	// stuck for two checks, then clear, repeatedly: never three in a row
	if runStuck(t, func() int {
		if calls.Add(1)%3 == 0 {
			return 0
		}
		return 50
	}) {
		t.Fatal("count must reset when requests complete")
	}
}

// A miss is a check of the same outstanding probe, so one slow lookup spends
// the whole budget by itself; it must outlast the worst legitimate lookup.
func TestWatchdogBudgetOutlastsASlowLookup(t *testing.T) {
	if budget := watchdogDeadline + time.Duration(watchdogMisses-1)*watchdogInterval; budget < stuckAfter {
		t.Fatalf("one probe outstanding for %s exits the node, below %s", budget, stuckAfter)
	}
}
