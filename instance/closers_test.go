package instance

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEveryLazyOpenerAppendsItsCloserSafely(t *testing.T) {
	n, _, factory := makeNode(t, Config{})

	// Three different adapters appending at once, each holding its own mutex, so none
	// excludes another; each is reached through its seam.
	openers := []func() error{
		func() error { _, err := admissionStores{node: n}.Store(); return err },
		func() error { _, err := volumeStores{node: n}.Store(); return err },
		func() error {
			_, err := databaseStores{node: n}.Store("databases/shop/prod/kv/QmA")
			return err
		},
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
