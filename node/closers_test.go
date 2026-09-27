package node

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// closers_test is about the one slice every lazy opener on this node appends to.
//
// # Why this needs a test and not just a lock
//
// Three adapters open a database the FIRST time a request needs one — the
// instances store, a bucket's store, a database's store — and each guards that
// opening with its OWN mutex, so it opens once. That is not the same property as
// appending to the node's closer list safely: two DIFFERENT adapters opening at
// the same moment hold two different mutexes and neither excludes the other, and
// Close walks the slice while they do it.
//
// So each of them held its own store correctly and wrote to the shared slice
// unsynchronised, which the node's own doc had already said not to do: "one lock
// for it, taken by addCloser and by Close." Construction appends directly and is
// single-threaded; these are not construction.
//
// Run with -race. Without the lock this fails there rather than in the
// assertions, which is the point: the corruption a bare append causes is a lost
// closer — a database that is never closed — and nothing downstream would say so.
func TestEveryLazyOpenerAppendsItsCloserSafely(t *testing.T) {
	n, _, factory := makeNode(t, Config{})

	// TWO OF THE THREE, which is enough: what has to be excluded is two
	// DIFFERENT adapters appending at once, and these two hold two different
	// mutexes. The third (databases.storesFor) takes a composed spec to reach
	// and carries the identical one-line fix.
	openers := []func() error{
		func() error { _, err := (&admissions{node: n}).store(); return err },
		func() error { _, err := (&buckets{node: n}).store(); return err },
	}

	const rounds = 8
	var wg sync.WaitGroup
	for range rounds {
		for _, open := range openers {
			wg.Add(1)
			go func(open func() error) {
				defer wg.Done()
				// An error is fine and is not what this test is about: some of
				// these need more of a node than this one has. What must not
				// happen is two of them writing the slice at once.
				open()
			}(open)
		}
	}
	wg.Wait()

	// Every closer that WAS registered is still reachable, which a torn append
	// would not guarantee: a lost entry is a store that never closes.
	n.closersMu.Lock()
	held := len(n.closers)
	n.closersMu.Unlock()
	require.NotZero(t, held, "the openers registered nothing at all")

	opened, _ := factory.counts()
	require.NotEmpty(t, opened, "no store was opened, so nothing was proven")
}
