package queue

import (
	"errors"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

func TestOpenRejects(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want error
	}{
		{"no name", Config{}, ErrNoName},
		{"negative visibility timeout", Config{Name: "b", VisibilityTimeout: -time.Second}, ErrInvalidConfig},
		{"negative delay", Config{Name: "b", Delay: -time.Second}, ErrInvalidConfig},
		{"negative retry interval", Config{Name: "b", RetryInterval: -time.Second}, ErrInvalidConfig},
		{"negative retry ceiling", Config{Name: "b", RetryCeiling: -time.Second}, ErrInvalidConfig},
		{"negative apply timeout", Config{Name: "b", ApplyTimeout: -time.Second}, ErrInvalidConfig},
		{"negative retry factor", Config{Name: "b", RetryFactor: -1}, ErrInvalidConfig},
		{"apply timeout above the cluster maximum", Config{Name: "b", ApplyTimeout: raft.MaxApplyTimeout + time.Second}, ErrInvalidConfig},
		{"dead letter is itself", Config{Name: "b", DeadLetter: "b"}, ErrSelfDeadLetter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group, err := NewGroup(newStore())
			if err != nil {
				t.Fatalf("NewGroup: %v", err)
			}
			if _, err := group.Open(tc.cfg); !errors.Is(err, tc.want) {
				t.Fatalf("Open: got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNewGroupRejectsNoStore(t *testing.T) {
	if _, err := NewGroup(nil); !errors.Is(err, ErrNoStore) {
		t.Fatalf("NewGroup: got %v, want ErrNoStore", err)
	}
}

func TestNewGroupRejectsNilClock(t *testing.T) {
	if _, err := NewGroup(newStore(), WithClock(nil)); !errors.Is(err, ErrNoClock) {
		t.Fatalf("NewGroup: got %v, want ErrNoClock", err)
	}
}

func TestOpenFillsDefaults(t *testing.T) {
	group, err := NewGroup(newStore())
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}
	q, err := group.Open(Config{Name: "build"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cfg := q.Config()
	if cfg.VisibilityTimeout != DefaultVisibilityTimeout {
		t.Errorf("visibility timeout: got %s, want %s", cfg.VisibilityTimeout, DefaultVisibilityTimeout)
	}
	if cfg.MaxAttempts != DefaultMaxAttempts {
		t.Errorf("max attempts: got %d, want %d", cfg.MaxAttempts, DefaultMaxAttempts)
	}
	if cfg.RetryFactor != 1 {
		t.Errorf("retry factor: got %v, want 1", cfg.RetryFactor)
	}
	if cfg.ApplyTimeout != DefaultApplyTimeout {
		t.Errorf("apply timeout: got %s, want %s", cfg.ApplyTimeout, DefaultApplyTimeout)
	}
	if cfg.DeadLetter != "build"+DeadLetterSuffix {
		t.Errorf("dead letter: got %q, want %q", cfg.DeadLetter, "build"+DeadLetterSuffix)
	}
}

func TestPushClaimAckRoundTrip(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")

	if ready, claimed := h.Depth(); ready != 1 || claimed != 0 {
		t.Fatalf("depth after push: got (%d, %d), want (1, 0)", ready, claimed)
	}

	d := h.mustClaim(t)
	if d.ID != "job-1" || string(d.Data) != "one" {
		t.Fatalf("claim: got (%q, %q), want (job-1, one)", d.ID, d.Data)
	}
	if d.Attempt != 1 {
		t.Fatalf("attempt: got %d, want 1", d.Attempt)
	}
	if want := h.clock.now().Add(DefaultVisibilityTimeout); !d.Deadline.Equal(want) {
		t.Fatalf("deadline: got %s, want %s", d.Deadline, want)
	}
	if d.Token == "" {
		t.Fatal("claim returned an empty token")
	}
	if ready, claimed := h.Depth(); ready != 0 || claimed != 1 {
		t.Fatalf("depth after claim: got (%d, %d), want (0, 1)", ready, claimed)
	}

	if err := h.Ack(d); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if ready, claimed := h.Depth(); ready != 0 || claimed != 0 {
		t.Fatalf("depth after ack: got (%d, %d), want (0, 0)", ready, claimed)
	}
	h.mustNotClaim(t)
}

func TestClaimIsFirstInFirstOut(t *testing.T) {
	h := newHarness(t, Config{})
	for _, id := range []string{"a", "b", "c"} {
		h.mustPush(t, id, id)
		h.clock.advance(time.Millisecond)
	}
	for _, want := range []string{"a", "b", "c"} {
		if got := h.mustClaim(t); got.ID != want {
			t.Fatalf("claim: got %q, want %q", got.ID, want)
		}
	}
}

func TestPushesInTheSameInstantBothSurvive(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "a", "one")
	h.mustPush(t, "b", "two")

	if ready, _ := h.Depth(); ready != 2 {
		t.Fatalf("ready: got %d, want 2", ready)
	}
	seen := map[string]string{}
	for range 2 {
		d := h.mustClaim(t)
		seen[d.ID] = string(d.Data)
	}
	if seen["a"] != "one" || seen["b"] != "two" {
		t.Fatalf("claimed: %v", seen)
	}
}

func TestPushDeduplicatesById(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")
	h.mustPush(t, "job-1", "again")

	if ready, _ := h.Depth(); ready != 1 {
		t.Fatalf("ready: got %d, want 1", ready)
	}
	if d := h.mustClaim(t); string(d.Data) != "one" {
		t.Fatalf("data: got %q, want the first push", d.Data)
	}
	h.mustNotClaim(t)
}

func TestPushIsANoOpWhileTheItemIsClaimed(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")
	h.mustClaim(t)
	h.mustPush(t, "job-1", "again")

	if ready, claimed := h.Depth(); ready != 0 || claimed != 1 {
		t.Fatalf("depth: got (%d, %d), want (0, 1)", ready, claimed)
	}
}

func TestPushAfterAckIsANewItem(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")
	if err := h.Ack(h.mustClaim(t)); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	h.mustPush(t, "job-1", "two")

	if d := h.mustClaim(t); string(d.Data) != "two" || d.Attempt != 1 {
		t.Fatalf("claim: got (%q, attempt %d), want (two, attempt 1)", d.Data, d.Attempt)
	}
}

func TestPushRejectsAnEmptyId(t *testing.T) {
	h := newHarness(t, Config{})
	if err := h.Push("", nil); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("Push: got %v, want ErrEmptyID", err)
	}
}

func TestClaimOnAnEmptyQueue(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustNotClaim(t)
}

func TestAckIsFinal(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")
	d := h.mustClaim(t)
	if err := h.Ack(d); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	// Nothing brings it back: not time, not the sweep, not another claim.
	h.clock.advance(365 * 24 * time.Hour)
	if err := h.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	h.mustNotClaim(t)
	if ready, claimed := h.Depth(); ready != 0 || claimed != 0 {
		t.Fatalf("depth: got (%d, %d), want (0, 0)", ready, claimed)
	}
	dl := h.deadLetter(t)
	if _, err := dl.Claim(); err != ErrNoItem {
		t.Fatalf("dead letter: got %v, want ErrNoItem", err)
	}
}

func TestAckAfterTheDeadlineWithNoRedelivery(t *testing.T) {
	h := newHarness(t, Config{VisibilityTimeout: time.Second})
	h.mustPush(t, "job-1", "one")
	d := h.mustClaim(t)

	h.clock.advance(time.Hour)
	if err := h.Ack(d); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	h.mustNotClaim(t)
}

func TestAckWithAStaleTokenIsRefused(t *testing.T) {
	h := newHarness(t, Config{VisibilityTimeout: time.Second})
	h.mustPush(t, "job-1", "one")
	first := h.mustClaim(t)

	h.clock.advance(2 * time.Second)
	second := h.mustClaim(t)
	if second.Token == first.Token {
		t.Fatal("redelivery reused the first delivery's token")
	}

	if err := h.Ack(first); !errors.Is(err, ErrStaleDelivery) {
		t.Fatalf("Ack(stale): got %v, want ErrStaleDelivery", err)
	}
	if _, claimed := h.Depth(); claimed != 1 {
		t.Fatal("the stale ack removed the live claim")
	}
	if err := h.Ack(second); err != nil {
		t.Fatalf("Ack(current): %v", err)
	}
}

func TestNackWithAStaleTokenIsRefused(t *testing.T) {
	h := newHarness(t, Config{VisibilityTimeout: time.Second})
	h.mustPush(t, "job-1", "one")
	first := h.mustClaim(t)

	h.clock.advance(2 * time.Second)
	h.mustClaim(t)

	if err := h.Nack(first); !errors.Is(err, ErrStaleDelivery) {
		t.Fatalf("Nack(stale): got %v, want ErrStaleDelivery", err)
	}
}

func TestAckAfterARequeueIsRefused(t *testing.T) {
	h := newHarness(t, Config{VisibilityTimeout: time.Second, RetryInterval: time.Hour})
	h.mustPush(t, "job-1", "one")
	d := h.mustClaim(t)

	h.clock.advance(2 * time.Second)
	if err := h.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if err := h.Ack(d); !errors.Is(err, ErrStaleDelivery) {
		t.Fatalf("Ack: got %v, want ErrStaleDelivery", err)
	}
	if ready, _ := h.Depth(); ready != 1 {
		t.Fatal("the refused ack disturbed the requeued item")
	}
}

func TestAckAndNackUnknownItem(t *testing.T) {
	h := newHarness(t, Config{})
	d := Delivery{ID: "nobody", Token: "t"}
	if err := h.Ack(d); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Ack: got %v, want ErrNotFound", err)
	}
	if err := h.Nack(d); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Nack: got %v, want ErrNotFound", err)
	}
}

func TestAFollowerWritesNothing(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")
	claimed := h.mustClaim(t)
	h.mustPush(t, "job-2", "two")
	h.store.stepDown()

	for name, call := range map[string]func() error{
		"Push":   func() error { return h.Push("job-3", nil) },
		"Claim":  func() error { _, err := h.Claim(); return err },
		"Ack":    func() error { return h.Ack(claimed) },
		"Nack":   func() error { return h.Nack(claimed) },
		"Remove": func() error { return h.Remove("job-2") },
		"Sweep":  h.Sweep,
	} {
		if err := call(); !errors.Is(err, ErrNotLeader) {
			t.Errorf("%s on a follower: got %v, want ErrNotLeader", name, err)
		}
	}

	if ready, inflight := h.Depth(); ready != 1 || inflight != 1 {
		t.Fatalf("depth on a follower: got (%d, %d), want (1, 1)", ready, inflight)
	}
	if !errors.Is(ErrNotLeader, raft.ErrNotLeader) {
		t.Fatal("ErrNotLeader is not the cluster's")
	}
}

func TestAFollowerRefusesBeforeItLooks(t *testing.T) {
	h := newHarness(t, Config{})
	h.store.stepDown()
	h.store.reset()

	if _, err := h.Claim(); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Claim: got %v, want ErrNotLeader", err)
	}
	if trace := h.store.trace(); len(trace) != 0 {
		t.Fatalf("a follower reached the store: %v", trace)
	}

	h.store.reset()
	if err := h.Remove("job-1"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Remove: got %v, want ErrNotLeader", err)
	}
	if trace := h.store.trace(); len(trace) != 0 {
		t.Fatalf("a follower reached the store: %v", trace)
	}
}

func TestEveryMutationBarriersBeforeItReads(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")
	d := h.mustClaim(t)
	// Pushed now and claimed by nothing below, so the last step has an item to
	// withdraw whichever one the Claim step took.
	h.mustPush(t, "job-keep", "kept")

	for _, step := range []struct {
		name string
		call func() error
	}{
		{"Push", func() error { return h.Push("job-2", nil) }},
		{"Sweep", h.Sweep},
		{"Nack", func() error { return h.Nack(d) }},
		{"Claim", func() error {
			claimed, err := h.Claim()
			d = claimed
			return err
		}},
		{"Ack", func() error { return h.Ack(d) }},
		{"Remove", func() error { return h.Remove("job-keep") }},
	} {
		h.store.reset()
		if err := step.call(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		trace := h.store.trace()
		if len(trace) == 0 || trace[0] != "barrier" {
			t.Errorf("%s: got %v, want a barrier first", step.name, trace)
		}
	}
}

func TestStoreFailurePropagates(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")
	d := h.mustClaim(t)

	boom := errors.New("boom")
	h.store.failBatches(boom)

	for name, call := range map[string]func() error{
		"Push": func() error { return h.Push("job-2", nil) },
		"Ack":  func() error { return h.Ack(d) },
		"Nack": func() error { return h.Nack(d) },
	} {
		if err := call(); !errors.Is(err, boom) {
			t.Errorf("%s: got %v, want the store's error", name, err)
		}
	}

	// Once the claim has expired both the sweep and the claim have to write.
	h.clock.advance(time.Hour)
	if err := h.Sweep(); !errors.Is(err, boom) {
		t.Errorf("Sweep over an expired claim: got %v, want the store's error", err)
	}
	if _, err := h.Claim(); !errors.Is(err, boom) {
		t.Errorf("Claim over an expired claim: got %v, want the store's error", err)
	}
	h.store.failBatches(nil)
	h.mustPush(t, "job-2", "two")
	h.store.failBatches(boom)
	if _, err := h.Claim(); !errors.Is(err, boom) {
		t.Errorf("Claim: got %v, want the store's error", err)
	}
}

func TestClaimReportsTheStoreFailingItsWrite(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")

	boom := errors.New("boom")
	h.store.failBatches(boom)
	if _, err := h.Claim(); !errors.Is(err, boom) {
		t.Fatalf("Claim: got %v, want the store's error", err)
	}

	h.store.failBatches(nil)
	if ready, claimed := h.Depth(); ready != 1 || claimed != 0 {
		t.Fatalf("depth after a refused claim: got (%d, %d), want (1, 0)", ready, claimed)
	}
	if d := h.mustClaim(t); d.Attempt != 1 {
		t.Fatalf("attempt: got %d, want 1 — a refused claim is not a delivery", d.Attempt)
	}
}

func TestOnlyAClaimedItemCanBeAcked(t *testing.T) {
	h := newHarness(t, Config{})
	key := "queue/build/ready/00000000000000000001/job"
	value, err := record{ID: "job", Data: []byte("one"), Attempts: 1, Token: "live"}.encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	h.store.put(key, value)
	h.store.put("queue/build/index/job", []byte(key))

	// The token matches; the item is not claimed.
	if err := h.Ack(Delivery{ID: "job", Token: "live"}); !errors.Is(err, ErrStaleDelivery) {
		t.Fatalf("Ack: got %v, want ErrStaleDelivery", err)
	}
	if err := h.Nack(Delivery{ID: "job", Token: "live"}); !errors.Is(err, ErrStaleDelivery) {
		t.Fatalf("Nack: got %v, want ErrStaleDelivery", err)
	}
}

func TestADeliveryWithNoTokenMatchesNothing(t *testing.T) {
	h := newHarness(t, Config{})
	key := "queue/build/claimed/09223372036854775807/job"
	value, err := record{ID: "job", Data: []byte("one"), Attempts: 1}.encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	h.store.put(key, value)
	h.store.put("queue/build/index/job", []byte(key))

	if err := h.Ack(Delivery{ID: "job"}); !errors.Is(err, ErrStaleDelivery) {
		t.Fatalf("Ack: got %v, want ErrStaleDelivery", err)
	}
}

func TestRemoveWithdrawsAnItemNobodyHasClaimed(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")
	h.mustPush(t, "job-2", "two")

	if err := h.Remove("job-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if ready, claimed := h.Depth(); ready != 1 || claimed != 0 {
		t.Fatalf("depth after remove: got (%d, %d), want (1, 0)", ready, claimed)
	}
	if d := h.mustClaim(t); d.ID != "job-2" {
		t.Fatalf("claim: got %q, want job-2 — the withdrawn item was handed out", d.ID)
	}

	// The id is pushable again, and comes back as a first delivery rather than
	// as a continuation of the one that was withdrawn.
	h.mustPush(t, "job-1", "again")
	if d := h.mustClaim(t); d.ID != "job-1" || string(d.Data) != "again" || d.Attempt != 1 {
		t.Fatalf("claim: got (%q, %q, attempt %d), want (job-1, again, attempt 1)", d.ID, d.Data, d.Attempt)
	}
}

func TestRemoveWithdrawsAnItemThatIsStillBackingOff(t *testing.T) {
	h := newHarness(t, Config{VisibilityTimeout: time.Second, RetryInterval: time.Hour})
	h.mustPush(t, "job-1", "one")
	if err := h.Nack(h.mustClaim(t)); err != nil {
		t.Fatalf("Nack: %v", err)
	}

	if err := h.Remove("job-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if ready, claimed := h.Depth(); ready != 0 || claimed != 0 {
		t.Fatalf("depth: got (%d, %d), want (0, 0)", ready, claimed)
	}
}

func TestRemoveRefusesAnItemAConsumerHolds(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")
	d := h.mustClaim(t)

	if err := h.Remove("job-1"); !errors.Is(err, ErrClaimed) {
		t.Fatalf("Remove: got %v, want ErrClaimed", err)
	}
	if ready, claimed := h.Depth(); ready != 0 || claimed != 1 {
		t.Fatalf("depth: got (%d, %d), want (0, 1)", ready, claimed)
	}
	if err := h.Ack(d); err != nil {
		t.Fatalf("Ack after a refused Remove: %v", err)
	}
	if ready, claimed := h.Depth(); ready != 0 || claimed != 0 {
		t.Fatalf("depth after ack: got (%d, %d), want (0, 0)", ready, claimed)
	}
}

func TestRemoveOfAnItemTheQueueDoesNotHold(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")

	if err := h.Remove("job-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := h.Remove("job-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Remove: got %v, want ErrNotFound", err)
	}
	if err := h.Remove("nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Remove of an unknown id: got %v, want ErrNotFound", err)
	}
}

func TestRemoveReportsAnIndexPointingOutsideTheQueue(t *testing.T) {
	h := newHarness(t, Config{})
	h.store.put("queue/build/index/job", []byte("somewhere/else"))

	if err := h.Remove("job"); !errors.Is(err, ErrForeignKey) {
		t.Fatalf("Remove: got %v, want ErrForeignKey", err)
	}
}

func TestRemoveReportsAStoreThatRefusedTheWrite(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")

	boom := errors.New("boom")
	h.store.failBatches(boom)

	if err := h.Remove("job-1"); !errors.Is(err, boom) {
		t.Fatalf("Remove: got %v, want the store's error", err)
	}
	if ready, _ := h.Depth(); ready != 1 {
		t.Fatal("a refused withdrawal took the item anyway")
	}
}
