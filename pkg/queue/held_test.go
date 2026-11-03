package queue

import (
	"errors"
	"testing"
	"time"
)

func mustHold(t *testing.T, q *Queue, want int64) {
	t.Helper()
	if got := q.Held(); got != want {
		t.Fatalf("held: got %d bytes, want %d", got, want)
	}
}

// EVERY WAY AN ITEM ARRIVES OR LEAVES MOVES WHAT THE QUEUE HOLDS, so a bound read
// off the counter is a bound on the items.
func TestWhatAQueueHoldsFollowsItsItems(t *testing.T) {
	h := newHarness(t, Config{MaxAttempts: 1})
	dead := h.deadLetter(t)

	h.mustPush(t, "a", "12345")
	h.mustPush(t, "b", "123")
	h.mustPush(t, "a", "12345")
	mustHold(t, h.Queue, 8)

	// Acked: gone.
	d := h.mustClaim(t)
	if err := h.Ack(d); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	mustHold(t, h.Queue, 3)

	// Removed: gone.
	if err := h.Remove("b"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	mustHold(t, h.Queue, 0)

	// Buried: moved, from this queue to its dead letter.
	h.mustPush(t, "c", "1234")
	d = h.mustClaim(t)
	if err := h.Nack(d); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	mustHold(t, h.Queue, 0)
	mustHold(t, dead, 4)

	// Expired off the dead letter: gone.
	h.clock.advance(time.Second)
	if _, err := dead.Expire(time.Millisecond, 0); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	mustHold(t, dead, 0)
}

func TestAPushPastTheBoundIsRefusedWhole(t *testing.T) {
	h := newHarness(t, Config{MaxBytes: 10})

	h.mustPush(t, "a", "123456")
	err := h.PushWith([]Item{{ID: "b", Data: []byte("12")}, {ID: "c", Data: []byte("123")}})
	if !errors.Is(err, ErrFull) {
		t.Fatalf("PushWith: got %v, want ErrFull", err)
	}
	if ready, _ := h.Depth(); ready != 1 {
		t.Fatalf("depth: got %d ready, want 1 — a refused push adds nothing", ready)
	}
	mustHold(t, h.Queue, 6)

	// Room made, room taken.
	d := h.mustClaim(t)
	if err := h.Ack(d); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	h.mustPush(t, "b", "1234567890")
	mustHold(t, h.Queue, 10)

	// AN EMPTY PAYLOAD HOLDS NOTHING, so a full queue still takes one.
	h.mustPush(t, "empty", "")

	if _, err := newHarness(t, Config{}).group.Open(Config{Name: "q", MaxBytes: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Open: got %v, want ErrInvalidConfig for a negative bound", err)
	}
}

// UNCLAIMED WORK IS NOT FORGOTTEN: past its age it moves to the dead letter,
// which is itself bounded, and what is claimed or still young stays.
func TestRetireMovesUnclaimedWorkPastItsAgeToTheDeadLetter(t *testing.T) {
	h := newHarness(t, Config{})
	dead := h.deadLetter(t)

	h.mustPush(t, "old", "123")
	h.clock.advance(time.Second)
	h.mustPush(t, "held", "45")
	claimed := h.mustClaim(t)
	if claimed.ID != "old" {
		t.Fatalf("claim: got %q, want old", claimed.ID)
	}
	h.mustPush(t, "aged", "6789")
	h.clock.advance(2 * time.Hour)
	h.mustPush(t, "young", "0")

	moved, err := h.Retire(time.Hour)
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if moved != 2 {
		t.Fatalf("retired: got %d, want 2 (held and aged)", moved)
	}
	mustHold(t, h.Queue, 4) // the claimed "old" and the young one
	mustHold(t, dead, 6)
	if ready, _ := dead.Depth(); ready != 2 {
		t.Fatalf("dead letter: got %d ready, want 2", ready)
	}

	if moved, err := h.Retire(0); err != nil || moved != 0 {
		t.Fatalf("Retire(0): got %d, %v, want nothing", moved, err)
	}
	if moved, err := h.Retire(time.Hour); err != nil || moved != 0 {
		t.Fatalf("a second pass: got %d, %v, want nothing", moved, err)
	}

	h.store.stepDown()
	if _, err := h.Retire(time.Hour); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Retire off the leader: got %v, want ErrNotLeader", err)
	}
}
