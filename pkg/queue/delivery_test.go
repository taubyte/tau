package queue

import (
	"errors"
	"testing"
	"time"
)

func TestAnUnackedClaimComesBackAfterTheVisibilityTimeout(t *testing.T) {
	h := newHarness(t, Config{VisibilityTimeout: 30 * time.Second})
	h.mustPush(t, "job-1", "one")

	first := h.mustClaim(t)
	if first.Attempt != 1 {
		t.Fatalf("attempt: got %d, want 1", first.Attempt)
	}

	// Invisible for as long as the claim is live, to the last nanosecond.
	h.clock.advance(30*time.Second - time.Nanosecond)
	h.mustNotClaim(t)

	h.clock.advance(time.Nanosecond)
	second := h.mustClaim(t)
	if second.ID != "job-1" || string(second.Data) != "one" {
		t.Fatalf("redelivery: got (%q, %q), want (job-1, one)", second.ID, second.Data)
	}
	if second.Attempt != 2 {
		t.Fatalf("attempt: got %d, want 2", second.Attempt)
	}
	if second.Token == first.Token {
		t.Fatal("redelivery reused the token")
	}
}

func TestOnlyExpiredClaimsAreTakenBack(t *testing.T) {
	h := newHarness(t, Config{VisibilityTimeout: 10 * time.Second})
	h.mustPush(t, "job-1", "one")
	h.mustPush(t, "job-2", "two")

	h.mustClaim(t)
	h.clock.advance(5 * time.Second)
	h.mustClaim(t)

	// job-1's claim has expired, job-2's has not.
	h.clock.advance(6 * time.Second)
	if err := h.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if ready, claimed := h.Depth(); ready != 1 || claimed != 1 {
		t.Fatalf("depth: got (%d, %d), want (1, 1)", ready, claimed)
	}
	if d := h.mustClaim(t); d.ID != "job-1" {
		t.Fatalf("claim: got %q, want job-1", d.ID)
	}
}

func TestDelayHoldsAnItemBack(t *testing.T) {
	h := newHarness(t, Config{Delay: time.Minute})
	h.mustPush(t, "job-1", "one")

	if ready, _ := h.Depth(); ready != 1 {
		t.Fatal("a delayed item should still be in the queue")
	}
	h.clock.advance(time.Minute - time.Nanosecond)
	h.mustNotClaim(t)

	h.clock.advance(time.Nanosecond)
	if d := h.mustClaim(t); d.ID != "job-1" {
		t.Fatalf("claim: got %q, want job-1", d.ID)
	}
}

func TestNackReturnsTheItemImmediatelyWhenNoRetryIntervalIsSet(t *testing.T) {
	h := newHarness(t, Config{VisibilityTimeout: time.Hour})
	h.mustPush(t, "job-1", "one")
	d := h.mustClaim(t)

	if err := h.Nack(d); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	again := h.mustClaim(t)
	if again.Attempt != 2 {
		t.Fatalf("attempt: got %d, want 2", again.Attempt)
	}
}

func TestRetryBackoffSchedule(t *testing.T) {
	h := newHarness(t, Config{
		VisibilityTimeout: time.Hour,
		RetryInterval:     time.Second,
		RetryFactor:       2,
		MaxAttempts:       5,
	})
	h.mustPush(t, "job-1", "one")

	for _, step := range []struct {
		attempt uint32
		wait    time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
	} {
		d := h.mustClaim(t)
		if d.Attempt != step.attempt {
			t.Fatalf("attempt: got %d, want %d", d.Attempt, step.attempt)
		}
		if err := h.Nack(d); err != nil {
			t.Fatalf("Nack: %v", err)
		}

		h.clock.advance(step.wait - time.Nanosecond)
		if _, err := h.Claim(); err != ErrNoItem {
			t.Fatalf("attempt %d: claimable %s early", step.attempt, time.Nanosecond)
		}
		h.clock.advance(time.Nanosecond)
	}
	if d := h.mustClaim(t); d.Attempt != 4 {
		t.Fatalf("attempt: got %d, want 4", d.Attempt)
	}
}

func TestRetryCeilingCapsTheBackoff(t *testing.T) {
	h := newHarness(t, Config{
		VisibilityTimeout: time.Hour,
		RetryInterval:     time.Second,
		RetryFactor:       10,
		RetryCeiling:      3 * time.Second,
		MaxAttempts:       5,
	})
	h.mustPush(t, "job-1", "one")

	// Uncapped the second delay would be 10s and the third 100s; the ceiling
	// makes both 3s.
	for _, step := range []struct {
		attempt uint32
		wait    time.Duration
	}{
		{1, time.Second},
		{2, 3 * time.Second},
		{3, 3 * time.Second},
	} {
		d := h.mustClaim(t)
		if d.Attempt != step.attempt {
			t.Fatalf("attempt: got %d, want %d", d.Attempt, step.attempt)
		}
		if err := h.Nack(d); err != nil {
			t.Fatalf("Nack: %v", err)
		}
		h.clock.advance(step.wait - time.Nanosecond)
		h.mustNotClaim(t)
		h.clock.advance(time.Nanosecond)
	}
	if d := h.mustClaim(t); d.Attempt != 4 {
		t.Fatalf("attempt: got %d, want 4", d.Attempt)
	}
}

func TestExpiryBackoffIsMeasuredFromTheDeadline(t *testing.T) {
	h := newHarness(t, Config{VisibilityTimeout: 10 * time.Second, RetryInterval: 5 * time.Second})
	h.mustPush(t, "job-1", "one")
	h.mustClaim(t)

	// Expired at +10s, so it is due at +15s. At +12s the sweep runs and it is
	// still not due.
	h.clock.advance(12 * time.Second)
	h.mustNotClaim(t)
	if ready, claimed := h.Depth(); ready != 1 || claimed != 0 {
		t.Fatalf("depth: got (%d, %d), want (1, 0)", ready, claimed)
	}

	h.clock.advance(3*time.Second - time.Nanosecond)
	h.mustNotClaim(t)
	h.clock.advance(time.Nanosecond)
	if d := h.mustClaim(t); d.Attempt != 2 {
		t.Fatalf("attempt: got %d, want 2", d.Attempt)
	}
}

func TestALateSweepDoesNotStretchTheBackoff(t *testing.T) {
	h := newHarness(t, Config{VisibilityTimeout: 10 * time.Second, RetryInterval: 5 * time.Second})
	h.mustPush(t, "job-1", "one")
	h.mustClaim(t)

	h.clock.advance(time.Hour)
	if d := h.mustClaim(t); d.Attempt != 2 {
		t.Fatalf("attempt: got %d, want 2", d.Attempt)
	}
}

func TestExhaustionThroughNack(t *testing.T) {
	h := newHarness(t, Config{MaxAttempts: 2, VisibilityTimeout: time.Hour})
	h.mustPush(t, "job-1", "one")

	for want := uint32(1); want <= 2; want++ {
		d := h.mustClaim(t)
		if d.Attempt != want {
			t.Fatalf("attempt: got %d, want %d", d.Attempt, want)
		}
		if err := h.Nack(d); err != nil {
			t.Fatalf("Nack: %v", err)
		}
	}

	// Two deliveries were all it had.
	h.mustNotClaim(t)
	if ready, claimed := h.Depth(); ready != 0 || claimed != 0 {
		t.Fatalf("depth: got (%d, %d), want (0, 0)", ready, claimed)
	}

	dl := h.deadLetter(t)
	dead, err := dl.Claim()
	if err != nil {
		t.Fatalf("dead letter Claim: %v", err)
	}
	if dead.ID != "job-1" || string(dead.Data) != "one" {
		t.Fatalf("dead letter: got (%q, %q), want (job-1, one)", dead.ID, dead.Data)
	}
	if dead.Attempt != 1 {
		t.Fatalf("dead letter attempt: got %d, want 1 — the counter starts again there", dead.Attempt)
	}
}

func TestExhaustionThroughExpiry(t *testing.T) {
	h := newHarness(t, Config{MaxAttempts: 2, VisibilityTimeout: time.Second})
	h.mustPush(t, "job-1", "one")

	h.mustClaim(t)
	h.clock.advance(2 * time.Second)
	if d := h.mustClaim(t); d.Attempt != 2 {
		t.Fatalf("attempt: got %d, want 2", d.Attempt)
	}
	h.clock.advance(2 * time.Second)
	h.mustNotClaim(t)

	dl := h.deadLetter(t)
	if _, err := dl.Claim(); err != nil {
		t.Fatalf("dead letter Claim: %v", err)
	}
}

func TestSweepAloneReachesTheDeadLetter(t *testing.T) {
	h := newHarness(t, Config{MaxAttempts: 1, VisibilityTimeout: time.Second})
	h.mustPush(t, "job-1", "one")
	h.mustClaim(t)

	h.clock.advance(2 * time.Second)
	if err := h.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if ready, claimed := h.Depth(); ready != 0 || claimed != 0 {
		t.Fatalf("depth: got (%d, %d), want (0, 0)", ready, claimed)
	}

	dl := h.deadLetter(t)
	if ready, _ := dl.Depth(); ready != 1 {
		t.Fatalf("dead letter ready: got %d, want 1", ready)
	}
}

func TestAnExhaustedItemIsNeverRedelivered(t *testing.T) {
	h := newHarness(t, Config{MaxAttempts: 1, VisibilityTimeout: time.Second})
	h.mustPush(t, "job-1", "one")
	h.mustClaim(t)

	for range 5 {
		h.clock.advance(time.Hour)
		if err := h.Sweep(); err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		h.mustNotClaim(t)
	}
	dl := h.deadLetter(t)
	if ready, _ := dl.Depth(); ready != 1 {
		t.Fatalf("dead letter ready: got %d, want 1 — it must arrive once, not once per sweep", ready)
	}
}

func TestTheDeadLetterQueueDeduplicates(t *testing.T) {
	h := newHarness(t, Config{MaxAttempts: 1, VisibilityTimeout: time.Second})
	for range 2 {
		h.mustPush(t, "job-1", "one")
		h.mustClaim(t)
		h.clock.advance(2 * time.Second)
		if err := h.Sweep(); err != nil {
			t.Fatalf("Sweep: %v", err)
		}
	}

	if ready, claimed := h.Depth(); ready != 0 || claimed != 0 {
		t.Fatalf("source depth: got (%d, %d), want (0, 0)", ready, claimed)
	}
	dl := h.deadLetter(t)
	if ready, _ := dl.Depth(); ready != 1 {
		t.Fatalf("dead letter ready: got %d, want 1", ready)
	}
}

func TestAConfiguredDeadLetterQueue(t *testing.T) {
	h := newHarness(t, Config{Name: "build", DeadLetter: "build/failed", MaxAttempts: 1, VisibilityTimeout: time.Second})
	h.mustPush(t, "job-1", "one")
	h.mustClaim(t)
	h.clock.advance(2 * time.Second)
	if err := h.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	failed, err := h.group.Open(Config{Name: "build/failed"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if d, err := failed.Claim(); err != nil || d.ID != "job-1" {
		t.Fatalf("configured dead letter: got (%v, %v), want job-1", d.ID, err)
	}
	if ready, _ := h.deadLetter(t).Depth(); ready != 0 {
		t.Fatal("the default dead-letter name was written to as well")
	}
}

func TestQueuesDoNotSeeEachOther(t *testing.T) {
	group, err := NewGroup(newStore(), WithClock(newClock().now))
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}

	names := []string{"build", "build/ready", "build/ready/00000000000000000001"}
	queues := make([]*Queue, len(names))
	for i, name := range names {
		q, err := group.Open(Config{Name: name})
		if err != nil {
			t.Fatalf("Open(%q): %v", name, err)
		}
		queues[i] = q
		if err := q.Push("job", []byte(name)); err != nil {
			t.Fatalf("Push: %v", err)
		}
	}

	for i, q := range queues {
		if ready, claimed := q.Depth(); ready != 1 || claimed != 0 {
			t.Fatalf("%q depth: got (%d, %d), want (1, 0)", names[i], ready, claimed)
		}
		d, err := q.Claim()
		if err != nil {
			t.Fatalf("%q Claim: %v", names[i], err)
		}
		if string(d.Data) != names[i] {
			t.Fatalf("%q claimed %q", names[i], d.Data)
		}
		if _, err := q.Claim(); err != ErrNoItem {
			t.Fatalf("%q claimed a neighbour's item", names[i])
		}
	}
}

func TestAwkwardIdsSurviveTheKeyLayout(t *testing.T) {
	ids := []string{
		"a/b", "..", ".", "%2F", "with space", "\x00", "ready", "claimed", "index",
		"00000000000000000001", "é", "a%2Fb",
	}
	h := newHarness(t, Config{})
	for _, id := range ids {
		h.mustPush(t, id, id)
	}
	if ready, _ := h.Depth(); ready != len(ids) {
		t.Fatalf("ready: got %d, want %d", ready, len(ids))
	}

	seen := map[string]bool{}
	for range ids {
		d := h.mustClaim(t)
		if string(d.Data) != d.ID {
			t.Fatalf("%q carries %q", d.ID, d.Data)
		}
		if seen[d.ID] {
			t.Fatalf("%q delivered twice", d.ID)
		}
		seen[d.ID] = true
		if err := h.Ack(d); err != nil {
			t.Fatalf("Ack(%q): %v", d.ID, err)
		}
	}
	if len(seen) != len(ids) {
		t.Fatalf("delivered %d of %d ids", len(seen), len(ids))
	}
}

func TestClaimReportsAForeignKey(t *testing.T) {
	for _, key := range []string{
		"queue/build/ready/notatimestamp/job",
		"queue/build/ready/0000000000000000001/job", // 19 digits
		"queue/build/ready/00000000000000000001/job/extra",
		"queue/build/claimed/notatimestamp/job",
	} {
		t.Run(key, func(t *testing.T) {
			h := newHarness(t, Config{})
			h.store.put(key, []byte("x"))
			if _, err := h.Claim(); !errors.Is(err, ErrForeignKey) {
				t.Fatalf("Claim: got %v, want ErrForeignKey", err)
			}
		})
	}
}

func TestClaimReportsACorruptRecord(t *testing.T) {
	h := newHarness(t, Config{})
	h.store.put("queue/build/ready/00000000000000000001/job", []byte("not a record"))
	if _, err := h.Claim(); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("Claim: got %v, want ErrCorruptRecord", err)
	}
}

func TestARecordWithNoIdIsCorrupt(t *testing.T) {
	h := newHarness(t, Config{})
	value, err := record{Data: []byte("x")}.encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	h.store.put("queue/build/ready/00000000000000000001/job", value)
	if _, err := h.Claim(); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("Claim: got %v, want ErrCorruptRecord", err)
	}
}

func TestAnIndexPointingAtNothingIsCorrupt(t *testing.T) {
	h := newHarness(t, Config{})
	h.store.put("queue/build/index/job", []byte("queue/build/claimed/00000000000000000001/job"))
	if err := h.Ack(Delivery{ID: "job", Token: "t"}); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("Ack: got %v, want ErrCorruptRecord", err)
	}
}

func TestAnIndexPointingOutsideTheQueueIsForeign(t *testing.T) {
	h := newHarness(t, Config{})
	h.store.put("queue/build/index/job", []byte("somewhere/else"))
	if err := h.Ack(Delivery{ID: "job", Token: "t"}); !errors.Is(err, ErrForeignKey) {
		t.Fatalf("Ack: got %v, want ErrForeignKey", err)
	}
}

func TestSweepReportsACorruptClaimedRecord(t *testing.T) {
	h := newHarness(t, Config{})
	h.store.put("queue/build/claimed/00000000000000000001/job", []byte("not a record"))

	if err := h.Sweep(); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("Sweep: got %v, want ErrCorruptRecord", err)
	}
	if _, err := h.Claim(); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("Claim: got %v, want ErrCorruptRecord", err)
	}
}

func TestARequeuedRecordCarriesNoDelivery(t *testing.T) {
	h := newHarness(t, Config{VisibilityTimeout: time.Second})
	h.mustPush(t, "job-1", "one")
	d := h.mustClaim(t)

	h.clock.advance(2 * time.Second)
	if err := h.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	keys := h.store.Keys(h.keys.readyPrefix())
	if len(keys) != 1 {
		t.Fatalf("ready keys: got %v, want one", keys)
	}
	raw, ok := h.store.Get(keys[0])
	if !ok {
		t.Fatalf("%q holds nothing", keys[0])
	}
	rec, err := decodeRecord(keys[0], raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec.Token != "" {
		t.Errorf("token: got %q, want it cleared", rec.Token)
	}
	if rec.Deadline != 0 {
		t.Errorf("deadline: got %d, want it cleared", rec.Deadline)
	}
	if rec.Attempts != d.Attempt {
		t.Errorf("attempts: got %d, want %d", rec.Attempts, d.Attempt)
	}
}
