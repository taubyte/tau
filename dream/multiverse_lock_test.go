package dream

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func lockTestMultiverse(t *testing.T, names ...string) *Multiverse {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := &Multiverse{ctx: ctx, ctxC: cancel, universes: make(map[string]*Universe)}
	for _, name := range names {
		u := &Universe{name: name, multiverse: m}
		if err := u.init(); err != nil {
			t.Fatal(err)
		}
		m.universes[name] = u
	}
	return m
}

func finishesWithin(t *testing.T, d time.Duration, what string, run func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { run(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not finish in %s: deadlock", what, d)
	}
}

// Status read-locked a universe twice. With a writer (Register, Stop) queued
// between the two, Go read locks block and Dream stalled for good.
func TestStatusDoesNotStallBesideWriters(t *testing.T) {
	m := lockTestMultiverse(t, "u")
	u := m.universes["u"]
	finishesWithin(t, 10*time.Second, "Status beside Register", func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 20000 {
				m.Status()
			}
		}()
		go func() {
			defer wg.Done()
			for range 20000 {
				u.lock.Lock()
				u.lookups["x"] = nil
				u.lock.Unlock()
			}
		}()
		wg.Wait()
	})
}

// Delete wrote the map under a read lock; run with -race.
func TestDeleteBesideReaders(t *testing.T) {
	names := make([]string, 200)
	for i := range names {
		names[i] = fmt.Sprintf("u%d", i)
	}
	m := lockTestMultiverse(t, names...)
	finishesWithin(t, 10*time.Second, "Delete beside List", func() {
		var wg sync.WaitGroup
		for _, name := range names {
			wg.Add(2)
			go func() { defer wg.Done(); _ = m.Delete(name) }()
			go func() { defer wg.Done(); _, _ = m.List() }()
		}
		wg.Wait()
	})
	if left, _ := m.List(); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

// Stop of a temporary universe took its own lock then the multiverse lock;
// Status takes them the other way round.
func TestStopBesideStatus(t *testing.T) {
	names := make([]string, 500)
	for i := range names {
		names[i] = fmt.Sprintf("u%d", i)
	}
	m := lockTestMultiverse(t, names...)
	for _, u := range m.universes {
		u.state = UniverseStateRunning
	}
	finishesWithin(t, 10*time.Second, "Stop beside Status", func() {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20000 {
				m.Status()
			}
		}()
		for _, name := range names {
			wg.Add(1)
			go func() { defer wg.Done(); m.universes[name].Stop() }()
		}
		wg.Wait()
	})
}
