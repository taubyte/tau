package queue

import (
	"sync"
	"testing"
	"time"
)

// The interleaving that matters: a second claimer reads the ready set while the
// first is between its read and its write. There is one item, so the second
// must come away with nothing.
//
// The hook holds the first claimer inside its write and waits for the second to
// have had its chance. It waits on a deadline rather than on a signal from the
// second claimer, because when the queue is correct the second claimer is
// parked and has no signal to send — which is the whole point.
func TestASecondClaimerCannotTakeAClaimedItem(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")

	writing := make(chan struct{})
	trying := make(chan struct{})
	h.store.onBatch(func() {
		h.store.onBatch(nil) // only the claim we are racing
		close(writing)
		select {
		case <-trying:
			// The second claimer is in Claim. Give it time to read.
			time.Sleep(100 * time.Millisecond)
		case <-time.After(time.Second):
			t.Error("the second claimer never started")
		}
	})

	var (
		second    Delivery
		secondErr error
		done      = make(chan struct{})
	)
	go func() {
		defer close(done)
		<-writing
		close(trying)
		second, secondErr = h.Claim()
	}()

	first, err := h.Claim()
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	<-done

	if first.ID != "job-1" {
		t.Fatalf("first claim: got %q, want job-1", first.ID)
	}
	if secondErr != ErrNoItem {
		t.Fatalf("second claim: got (%q, %v), want ErrNoItem", second.ID, secondErr)
	}
}

// Every item is delivered to exactly one consumer, and every consumer's claim is
// its own.
func TestConcurrentClaimersShareNothing(t *testing.T) {
	const (
		items     = 200
		consumers = 8
	)

	h := newHarness(t, Config{VisibilityTimeout: time.Hour})
	for i := range items {
		h.mustPush(t, id(i), "payload")
	}

	var (
		mu   sync.Mutex
		seen = map[string]int{}
		wg   sync.WaitGroup
	)
	for range consumers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				d, err := h.Claim()
				if err == ErrNoItem {
					return
				}
				if err != nil {
					t.Errorf("Claim: %v", err)
					return
				}
				mu.Lock()
				seen[d.ID]++
				mu.Unlock()
				if err := h.Ack(d); err != nil {
					t.Errorf("Ack(%q): %v", d.ID, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if len(seen) != items {
		t.Fatalf("delivered %d distinct items, want %d", len(seen), items)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("%q delivered %d times", id, count)
		}
	}
	if ready, claimed := h.Depth(); ready != 0 || claimed != 0 {
		t.Fatalf("depth: got (%d, %d), want (0, 0)", ready, claimed)
	}
}

// Concurrent producers: nothing is dropped and nothing is doubled.
func TestConcurrentPushesKeepEveryItem(t *testing.T) {
	const (
		producers = 8
		each      = 25
	)

	h := newHarness(t, Config{})
	var wg sync.WaitGroup
	for p := range producers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				if err := h.Push(id(p*each+i), []byte("payload")); err != nil {
					t.Errorf("Push: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if ready, _ := h.Depth(); ready != producers*each {
		t.Fatalf("ready: got %d, want %d", ready, producers*each)
	}
}

func id(i int) string {
	return "job-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
}

// Two Queue values for one name are one writer. Nothing stops a caller from
// opening a queue twice, so the lock cannot be the queue's — this is the same
// interleaving as above with the second claimer holding its own value.
func TestTwoValuesForOneNameAreOneWriter(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustPush(t, "job-1", "one")

	other, err := h.group.Open(h.Config())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	writing := make(chan struct{})
	trying := make(chan struct{})
	h.store.onBatch(func() {
		h.store.onBatch(nil)
		close(writing)
		select {
		case <-trying:
			time.Sleep(100 * time.Millisecond)
		case <-time.After(time.Second):
			t.Error("the second claimer never started")
		}
	})

	var (
		second    Delivery
		secondErr error
		done      = make(chan struct{})
	)
	go func() {
		defer close(done)
		<-writing
		close(trying)
		second, secondErr = other.Claim()
	}()

	if _, err := h.Claim(); err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	<-done

	if secondErr != ErrNoItem {
		t.Fatalf("second claim: got (%q, %v), want ErrNoItem", second.ID, secondErr)
	}
}
