package http

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/platformerr"
)

func TestMaxInFlightScalesWithMachine(t *testing.T) {
	const GB = 1 << 30
	for _, c := range []struct {
		cpus int
		mem  uint64
		want int
	}{
		{2, 2 * GB, 128}, {4, 2 * GB, 256}, {16, 2 * GB, 256}, {2, 64 * GB, 128}, {2, 512 << 20, 64}, {1, 0, 1},
	} {
		if got := maxInFlight(c.cpus, c.mem); got != c.want {
			t.Errorf("maxInFlight(%d, %d) = %d, want %d", c.cpus, c.mem, got, c.want)
		}
	}
}

func TestLimitsScaleWithRAM(t *testing.T) {
	const GB = 1 << 30
	for _, c := range []struct {
		cpus                  int
		mem                   uint64
		floor, start, ceiling int
	}{
		{2, 2 * GB, 16, 128, 512}, {2, 8 * GB, 16, 128, 2048}, {1, 0, 1, 1, 1},
	} {
		if f, s, m := limits(c.cpus, c.mem); f != c.floor || s != c.start || m != c.ceiling {
			t.Errorf("limits(%d, %d) = %d, %d, %d", c.cpus, c.mem, f, s, m)
		}
	}
}

// feed runs whole windows of request times through the limiter, with the limit
// fully in use, and returns the lowest limit it saw.
func feed(l *limiter, windows int, sample func(i int) time.Duration) int64 {
	lowest := l.limit.Load()
	l.mu.Lock()
	defer l.mu.Unlock()
	for range windows {
		limit := l.limit.Load()
		for i := range max(windowSamples, limit) {
			l.observe(sample(int(i)), false, limit)
		}
		lowest = min(lowest, l.limit.Load())
	}
	return lowest
}

func steady(d time.Duration) func(int) time.Duration {
	return func(int) time.Duration { return d }
}

func TestLimitGrowsToCeilingWhileLatencyStaysLow(t *testing.T) {
	l := newLimiter(16, 128, 256)
	feed(l, 1, steady(10*time.Millisecond))
	if got := l.limit.Load(); got <= 128 {
		t.Fatalf("limit %d did not grow", got)
	}
	feed(l, 100, steady(10*time.Millisecond))
	if got := l.limit.Load(); got != 256 {
		t.Fatalf("limit %d, want the ceiling", got)
	}
	if len(l.tokens) != 256 {
		t.Fatalf("%d free slots for a limit of 256", len(l.tokens))
	}
}

func TestLimitDoesNotClimbWhileIdle(t *testing.T) {
	l := newLimiter(16, 128, 256)
	l.mu.Lock()
	defer l.mu.Unlock()
	for range 10 * 128 {
		l.observe(10*time.Millisecond, false, 10)
	}
	if got := l.limit.Load(); got != 128 {
		t.Fatalf("limit %d moved with only 10 in flight", got)
	}
}

func TestLimitShrinksWhenLatencyInflatesAndStopsAtFloor(t *testing.T) {
	l := newLimiter(16, 128, 256)
	feed(l, 1, steady(10*time.Millisecond))
	before := l.limit.Load()
	feed(l, 1, steady(200*time.Millisecond))
	if got := l.limit.Load(); got >= before {
		t.Fatalf("limit %d not cut from %d", got, before)
	}
	if lowest := feed(l, 200, steady(200*time.Millisecond)); lowest != 16 {
		t.Fatalf("lowest limit %d, want the floor", lowest)
	}
}

func TestLimitRecoversAfterSurge(t *testing.T) {
	l := newLimiter(16, 128, 256)
	feed(l, 1, steady(10*time.Millisecond))
	feed(l, 40, steady(500*time.Millisecond))
	if got := l.limit.Load(); got != 16 {
		t.Fatalf("surge left the limit at %d", got)
	}
	feed(l, 100, steady(10*time.Millisecond))
	if got := l.limit.Load(); got != 256 {
		t.Fatalf("limit %d after the surge, want the ceiling", got)
	}
}

// One request in five is a lookup, the rest slow database calls: the spread is
// not congestion, but a lookup that slows down is.
func TestLimitIgnoresMixedRequestLengths(t *testing.T) {
	mixed := func(fast time.Duration) func(int) time.Duration {
		return func(i int) time.Duration {
			if i%5 == 0 {
				return fast
			}
			return 300 * time.Millisecond
		}
	}
	l := newLimiter(16, 128, 256)
	feed(l, 100, mixed(time.Millisecond))
	if got := l.limit.Load(); got != 256 {
		t.Fatalf("limit %d under a stable mix, want the ceiling", got)
	}
	feed(l, 5, mixed(50*time.Millisecond))
	if got := l.limit.Load(); got >= 256 {
		t.Fatalf("limit %d not cut when the fast requests slowed", got)
	}
}

func TestLimitCutsOnTimeouts(t *testing.T) {
	window := func(l *limiter, timeouts int) {
		l.mu.Lock()
		defer l.mu.Unlock()
		for i := range 128 {
			l.observe(10*time.Millisecond, i < timeouts, 128)
		}
	}
	l := newLimiter(16, 128, 256)
	window(l, 20)
	if got := l.limit.Load(); got >= 128 {
		t.Fatalf("limit %d not cut by timeouts", got)
	}
	l = newLimiter(16, 128, 256)
	window(l, 1)
	if got := l.limit.Load(); got < 128 {
		t.Fatalf("one timeout cut the limit to %d", got)
	}
}

func TestCutWhileSlotsAreHeldSettlesOnRelease(t *testing.T) {
	l := newLimiter(1, 2, 2)
	for range 2 {
		if err := l.acquire(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	l.mu.Lock()
	l.setLimit(1)
	l.mu.Unlock()
	l.release(0, false)
	if len(l.tokens) != 0 {
		t.Fatal("first release went past the lowered limit")
	}
	l.release(0, false)
	if len(l.tokens) != 1 || l.owed != 0 {
		t.Fatalf("%d free, %d owed; want 1 free", len(l.tokens), l.owed)
	}
}

func TestTauMaxInFlightPinsTheLimit(t *testing.T) {
	t.Setenv("TAU_MAX_INFLIGHT", "7")
	l := newAdmission()
	feed(l, 5, steady(time.Millisecond))
	feed(l, 5, steady(time.Second))
	if got := l.limit.Load(); got != 7 || len(l.tokens) != 7 {
		t.Fatalf("limit %d, %d free; want pinned at 7", got, len(l.tokens))
	}
}

// holdOnly takes the single slot of a fresh limiter of one.
func holdOnly(t *testing.T) *limiter {
	t.Helper()
	l := newLimiter(1, 1, 1)
	if err := l.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	return l
}

// untilWaiting fails rather than hangs if the caller never parks.
func untilWaiting(t *testing.T, l *limiter, n int64) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); l.waiting.Load() != n; time.Sleep(50 * time.Microsecond) {
		if time.Now().After(deadline) {
			t.Fatalf("never saw %d waiting", n)
		}
	}
}

func TestWaiterGetsSlotFreedDuringWait(t *testing.T) {
	l := holdOnly(t)
	ran := make(chan error, 1)
	go func() { ran <- admitted(context.Background(), l, func() error { return nil }) }()
	untilWaiting(t, l, 1)
	l.release(0, false)
	if err := <-ran; err != nil {
		t.Fatalf("waiter refused: %v", err)
	}
}

func TestRefusedAfterWait(t *testing.T) {
	l := holdOnly(t)
	start, called := time.Now(), false
	err := admitted(context.Background(), l, func() error { called = true; return nil })
	if platformerr.CodeOf(err) != platformerr.NodeBusy || called {
		t.Fatalf("err %v, ran %v", err, called)
	}
	if took := time.Since(start); took < admissionWait {
		t.Fatalf("refused after %s, before the wait ended", took)
	}
}

func TestContextCancelEndsWait(t *testing.T) {
	l := holdOnly(t)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	go func() { got <- admitted(ctx, l, func() error { return nil }) }()
	untilWaiting(t, l, 1)
	cancel()
	if err := <-got; !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if l.waiting.Load() != 0 {
		t.Fatal("waiter still counted")
	}
}

func TestWaitersAreBounded(t *testing.T) {
	l := holdOnly(t)
	defer l.release(0, false)
	first := make(chan error, 1)
	go func() { first <- admitted(context.Background(), l, func() error { return nil }) }()
	untilWaiting(t, l, 1)

	start := time.Now()
	err := admitted(context.Background(), l, func() error { return nil })
	if platformerr.CodeOf(err) != platformerr.NodeBusy {
		t.Fatalf("err %v", err)
	}
	if took := time.Since(start); took >= admissionWait {
		t.Fatalf("waited %s past a full queue", took)
	}
	<-first
}

func TestNoSlotLeak(t *testing.T) {
	l := newLimiter(1, 2, 2)
	ctx := context.Background()
	check := func(what string) {
		t.Helper()
		if len(l.tokens) != 2 || l.owed != 0 {
			t.Fatalf("after %s: %d free, %d owed", what, len(l.tokens), l.owed)
		}
	}
	if err := admitted(ctx, l, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	check("success")
	if err := admitted(ctx, l, func() error { return errors.New("x") }); err == nil {
		t.Fatal("error lost")
	}
	check("error")
	func() {
		defer func() { recover() }()
		admitted(ctx, l, func() error { panic("boom") })
	}()
	check("panic")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				admitted(ctx, l, func() error { return nil })
			}
		}()
	}
	wg.Wait()
	check("concurrent use")
}

// A fast failure says nothing about load and must not become the baseline.
func TestFailuresAreNotSamples(t *testing.T) {
	l := newLimiter(1, 1, 1)
	for range 2 * windowSamples {
		admitted(context.Background(), l, func() error { return errors.New("x") })
	}
	if l.base != 0 || l.seen != 0 {
		t.Fatalf("failures counted: base %s, seen %d", l.base, l.seen)
	}
}
