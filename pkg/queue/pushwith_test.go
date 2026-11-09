package queue

import (
	"errors"
	"testing"

	"github.com/taubyte/tau/pkg/raft"
)

// PushWith's one guarantee over Push: the caller's record commits in the same
// replicated entry as the push, or neither does.

func TestPushWithCommitsTheCallersOperationInTheSameEntry(t *testing.T) {
	h := newHarness(t, Config{})
	h.store.reset()

	err := h.PushWith([]Item{{ID: "job-1", Data: []byte("one")}},
		raft.BatchOp{Set: &raft.SetCommand{Key: "cron/past/nightly", Value: []byte("1757041200")}})
	if err != nil {
		t.Fatalf("PushWith: %v", err)
	}

	// One batch, not two: no crash fits between the record and the work.
	batches := 0
	for _, op := range h.store.trace() {
		if op == "batch" {
			batches++
		}
	}
	if batches != 1 {
		t.Fatalf("batches: got %d, want 1", batches)
	}

	if value, held := h.store.Get("cron/past/nightly"); !held || string(value) != "1757041200" {
		t.Fatalf("the caller's record: got (%q, %v), want (1757041200, true)", value, held)
	}
	if ready, _ := depthOf(t, h); ready != 1 {
		t.Fatalf("ready after PushWith: got %d, want 1", ready)
	}
}

func TestARefusedEntryCommitsNeitherThePushNorTheRecord(t *testing.T) {
	h := newHarness(t, Config{})
	h.store.failBatches(errors.New("the entry did not commit"))

	err := h.PushWith([]Item{{ID: "job-1", Data: []byte("one")}},
		raft.BatchOp{Set: &raft.SetCommand{Key: "cron/past/nightly", Value: []byte("1757041200")}})
	if err == nil {
		t.Fatal("PushWith reported success on an entry that did not commit")
	}

	if _, held := h.store.Get("cron/past/nightly"); held {
		t.Fatal("the caller's record outlived the push it was a record of")
	}
	if ready, _ := depthOf(t, h); ready != 0 {
		t.Fatalf("ready after a refused entry: got %d, want 0", ready)
	}
}

func TestPushWithNoItemsStillCommitsTheOperations(t *testing.T) {
	h := newHarness(t, Config{})

	err := h.PushWith(nil,
		raft.BatchOp{Set: &raft.SetCommand{Key: "cron/past/nightly", Value: []byte("1757041200")}})
	if err != nil {
		t.Fatalf("PushWith: %v", err)
	}
	if _, held := h.store.Get("cron/past/nightly"); !held {
		t.Fatal("the operation was dropped with the empty item list")
	}
}

func TestPushWithADuplicateIdStillCommitsTheRecord(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")

	err := h.PushWith([]Item{{ID: "job-1", Data: []byte("one")}},
		raft.BatchOp{Set: &raft.SetCommand{Key: "cron/past/nightly", Value: []byte("1757041200")}})
	if err != nil {
		t.Fatalf("PushWith: %v", err)
	}
	if _, held := h.store.Get("cron/past/nightly"); !held {
		t.Fatal("the record was dropped with the duplicate item")
	}
	if ready, _ := depthOf(t, h); ready != 1 {
		t.Fatalf("ready: got %d, want 1", ready)
	}
}

func TestPushWithRefusesAnOperationOnThisQueuesKeys(t *testing.T) {
	h := newHarness(t, Config{})

	// Derived, not spelled: the keyspace escapes a name.
	mine := newKeyspace(h.Config().Name)
	dead := newKeyspace(h.Config().DeadLetter)

	for _, key := range []string{mine.prefix, mine.readyPrefix() + "x", dead.index("x")} {
		err := h.PushWith([]Item{{ID: "job-1"}},
			raft.BatchOp{Set: &raft.SetCommand{Key: key, Value: nil}})
		if !errors.Is(err, ErrReservedKey) {
			t.Fatalf("PushWith(%q): got %v, want ErrReservedKey", key, err)
		}
	}

	if ready, _ := depthOf(t, h); ready != 0 {
		t.Fatalf("a refused call pushed anyway: ready %d", ready)
	}
}

func TestPushWithRefusesAMalformedOperation(t *testing.T) {
	h := newHarness(t, Config{})

	both := raft.BatchOp{
		Set:    &raft.SetCommand{Key: "cron/past/nightly"},
		Delete: &raft.DeleteCommand{Key: "cron/held/nightly"},
	}
	for _, op := range []raft.BatchOp{{}, both} {
		if err := h.PushWith([]Item{{ID: "job-1"}}, op); !errors.Is(err, ErrMalformedOp) {
			t.Fatalf("PushWith(%+v): got %v, want ErrMalformedOp", op, err)
		}
	}
}

func TestPushWithOnAFollowerWritesNothing(t *testing.T) {
	h := newHarness(t, Config{})
	h.store.stepDown()

	err := h.PushWith([]Item{{ID: "job-1"}},
		raft.BatchOp{Set: &raft.SetCommand{Key: "cron/past/nightly", Value: nil}})
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("PushWith on a follower: got %v, want ErrNotLeader", err)
	}
	if _, held := h.store.Get("cron/past/nightly"); held {
		t.Fatal("a follower committed the caller's record")
	}
}
