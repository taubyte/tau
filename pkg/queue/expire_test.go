package queue

import (
	"errors"
	"testing"
	"time"
)

// TestExpireDropsWhatIsOlderThanTheAge. The age is measured from when an item
// became deliverable, which for a buried one is when it was buried.
func TestExpireDropsWhatIsOlderThanTheAge(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "old")
	h.clock.advance(time.Hour)
	h.mustPush(t, "job-2", "new")

	dropped, err := h.Expire(30*time.Minute, 0)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if dropped != 1 {
		t.Fatalf("dropped: got %d, want 1", dropped)
	}
	if ready, _ := h.Depth(); ready != 1 {
		t.Fatalf("depth: got %d ready, want 1", ready)
	}
	if d := h.mustClaim(t); d.ID != "job-2" {
		t.Fatalf("claim: got %q, want job-2", d.ID)
	}
}

// TestExpireKeepsTheNewestItemsThatFitTheSize, dropping from the front — the
// same drop-oldest a log's retention means.
func TestExpireKeepsTheNewestItemsThatFitTheSize(t *testing.T) {
	h := newHarness(t, Config{})
	for _, id := range []string{"job-1", "job-2", "job-3"} {
		h.mustPush(t, id, "0123456789")
		h.clock.advance(time.Second)
	}

	// Twenty bytes holds the two newest payloads exactly.
	if _, err := h.Expire(0, 20); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if ready, _ := h.Depth(); ready != 2 {
		t.Fatalf("depth: got %d ready, want 2", ready)
	}
	if d := h.mustClaim(t); d.ID != "job-2" {
		t.Fatalf("claim: got %q, want job-2 — expiry drops the oldest", d.ID)
	}
}

// TestASizeTooSmallForOneItemStillKeepsTheNewest, for the reason node/log.go
// keeps its newest record: a budget that cannot hold one item is a
// misconfiguration, and emptying the queue over it discards work that was just
// accepted.
func TestASizeTooSmallForOneItemStillKeepsTheNewest(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "0123456789")

	if _, err := h.Expire(0, 1); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if ready, _ := h.Depth(); ready != 1 {
		t.Fatalf("depth: got %d ready, want 1", ready)
	}
}

// TestExpireLeavesAClaimedItemAlone. A claimed item is somebody's work in
// flight; if its consumer is gone the claim expires by itself and the item
// comes back ready, where the next pass can see it.
func TestExpireLeavesAClaimedItemAlone(t *testing.T) {
	h := newHarness(t, Config{VisibilityTimeout: time.Hour})
	h.mustPush(t, "job-1", "one")
	h.mustClaim(t)

	h.clock.advance(24 * time.Hour)
	dropped, err := h.Expire(time.Minute, 0)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("dropped: got %d, want 0", dropped)
	}
	if _, claimed := h.Depth(); claimed != 1 {
		t.Fatalf("depth: got %d claimed, want 1", claimed)
	}
}

// TestAnAbsentBudgetKeepsEverything, which is the vocabulary's own sentence for
// a log and means the same here.
func TestAnAbsentBudgetKeepsEverything(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")
	h.clock.advance(10000 * time.Hour)

	if dropped, err := h.Expire(0, 0); err != nil || dropped != 0 {
		t.Fatalf("Expire: got (%d, %v), want (0, nil)", dropped, err)
	}
	if ready, _ := h.Depth(); ready != 1 {
		t.Fatalf("depth: got %d ready, want 1", ready)
	}
}

// TestExpireRefusesOffTheLeader, like every other write in this package: a
// follower that deleted would be deleting what the leader still holds.
func TestExpireRefusesOffTheLeader(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")
	h.clock.advance(time.Hour)
	h.store.stepDown()

	if _, err := h.Expire(time.Minute, 0); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Expire: got %v, want ErrNotLeader", err)
	}
}

// TestAnExpiredIdMayBePushedAgain, which is what tells a drop from a withdrawal:
// both remove the index key, so nothing is left claiming the id is still held.
func TestAnExpiredIdMayBePushedAgain(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")
	h.clock.advance(time.Hour)

	if _, err := h.Expire(time.Minute, 0); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	h.mustPush(t, "job-1", "two")

	if d := h.mustClaim(t); string(d.Data) != "two" {
		t.Fatalf("claim: got %q, want two", d.Data)
	}
}

// TestDeadNamesTheQueueExhaustedItemsMoveTo, so nothing has to reproduce
// withDefaults' derivation by hand.
func TestDeadNamesTheQueueExhaustedItemsMoveTo(t *testing.T) {
	h := newHarness(t, Config{MaxAttempts: 1, VisibilityTimeout: time.Second})
	h.mustPush(t, "job-1", "doomed")
	h.mustClaim(t)

	h.clock.advance(2 * time.Second)
	if err := h.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	dead, err := h.Dead()
	if err != nil {
		t.Fatalf("Dead: %v", err)
	}
	if dead.Config().Name != h.Config().DeadLetter {
		t.Fatalf("Dead: got %q, want %q", dead.Config().Name, h.Config().DeadLetter)
	}
	if ready, _ := dead.Depth(); ready != 1 {
		t.Fatalf("dead depth: got %d ready, want 1", ready)
	}
}

// TestAnExpirePassThatStopsEarlyReportsNothingDropped. Every drop the pass
// decides on goes into one entry at the end, so a pass that stops before it
// applied nothing — and the count of what it had decided on by then would be a
// number no store ever saw, and a caller logging it would read a queue that
// shrank when nothing moved.
func TestAnExpirePassThatStopsEarlyReportsNothingDropped(t *testing.T) {
	h := newHarness(t, Config{})
	for _, id := range []string{"job-1", "job-2", "job-3"} {
		h.mustPush(t, id, "work")
		h.clock.advance(time.Second)
	}
	h.clock.advance(time.Hour)

	// A key under this queue's ready prefix that this queue did not write is the
	// fault ErrForeignKey names. Its stamp is older than any real one, so the
	// newest-first walk reaches it LAST — after it has already decided to drop
	// all three items, which is the only arrangement that can tell a count of
	// what was applied from a count of what was planned.
	foreign := h.keys.readyPrefix() + "0000000000000000000x/x"
	if err := h.store.Set(foreign, []byte("{}"), time.Second); err != nil {
		t.Fatalf("Set: %v", err)
	}

	dropped, err := h.Expire(30*time.Minute, 0)
	if !errors.Is(err, ErrForeignKey) {
		t.Fatalf("Expire: got %v, want ErrForeignKey", err)
	}
	if dropped != 0 {
		t.Fatalf("dropped: got %d, want 0 — the batch never ran", dropped)
	}
	if got := len(h.store.Keys(h.keys.readyPrefix())); got != 4 {
		t.Fatalf("ready keys: got %d, want 4 — the three items and the foreign key, none of them dropped", got)
	}
}
