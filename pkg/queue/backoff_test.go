package queue

import (
	"math"
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	type testCase struct {
		name     string
		cfg      Config
		attempts uint32
		want     time.Duration
	}
	for _, tc := range []testCase{
		{"no interval is no wait", Config{RetryFactor: 2}, 3, 0},
		{"first failure uses the interval", Config{RetryInterval: time.Second, RetryFactor: 2}, 1, time.Second},
		{"a zeroth attempt is the first", Config{RetryInterval: time.Second, RetryFactor: 2}, 0, time.Second},
		{"second failure multiplies once", Config{RetryInterval: time.Second, RetryFactor: 2}, 2, 2 * time.Second},
		{"fourth failure multiplies three times", Config{RetryInterval: time.Second, RetryFactor: 2}, 4, 8 * time.Second},
		{"a factor of one is a fixed interval", Config{RetryInterval: time.Second, RetryFactor: 1}, 9, time.Second},
		{"a factor below one shrinks", Config{RetryInterval: time.Second, RetryFactor: 0.5}, 3, 250 * time.Millisecond},
		{"the ceiling caps it", Config{RetryInterval: time.Second, RetryFactor: 10, RetryCeiling: 5 * time.Second}, 4, 5 * time.Second},
		{"the ceiling does not raise it", Config{RetryInterval: time.Second, RetryFactor: 1, RetryCeiling: time.Hour}, 1, time.Second},
		{"an overflowing factor stops at the ceiling", Config{RetryInterval: time.Second, RetryFactor: 1e300, RetryCeiling: time.Minute}, 5, time.Minute},
		{"an overflowing factor with no ceiling stops at the longest delay", Config{RetryInterval: time.Second, RetryFactor: 1e300, RetryCeiling: 0}, 5, maxDuration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := backoff(tc.cfg, tc.attempts); got != tc.want {
				t.Fatalf("backoff: got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestAddClamped(t *testing.T) {
	base := time.Unix(0, 1000)
	if got := addClamped(base, time.Duration(500)); got.UnixNano() != 1500 {
		t.Fatalf("addClamped: got %d, want 1500", got.UnixNano())
	}
	if got := addClamped(base, 0); got.UnixNano() != 1000 {
		t.Fatalf("addClamped: got %d, want 1000", got.UnixNano())
	}
	if got := addClamped(time.Unix(0, math.MaxInt64-5), time.Hour); got.UnixNano() != math.MaxInt64 {
		t.Fatalf("addClamped: got %d, want %d", got.UnixNano(), int64(math.MaxInt64))
	}
	if got := addClamped(time.Unix(0, math.MaxInt64), maxDuration); got.UnixNano() != math.MaxInt64 {
		t.Fatalf("addClamped: got %d, want %d", got.UnixNano(), int64(math.MaxInt64))
	}
}

func TestAnOverflowingBackoffDoesNotWrap(t *testing.T) {
	h := newHarness(t, Config{
		VisibilityTimeout: time.Second,
		RetryInterval:     time.Hour,
		RetryFactor:       1e30,
		MaxAttempts:       9,
	})
	h.mustPush(t, "job-1", "one")

	// The first failure waits an hour; the second multiplies that by 1e30, which
	// runs off the end of what a time can hold.
	if err := h.Nack(h.mustClaim(t)); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	h.clock.advance(time.Hour)
	if err := h.Nack(h.mustClaim(t)); err != nil {
		t.Fatalf("Nack: %v", err)
	}

	h.clock.advance(100 * 365 * 24 * time.Hour)
	h.mustNotClaim(t)
	if ready, _ := h.Depth(); ready != 1 {
		t.Fatalf("ready: got %d, want 1", ready)
	}

	// Parked at the end of time, not wrapped round to the front of the queue.
	h.mustPush(t, "job-2", "two")
	if d := h.mustClaim(t); d.ID != "job-2" {
		t.Fatalf("claim: got %q, want job-2", d.ID)
	}
}
