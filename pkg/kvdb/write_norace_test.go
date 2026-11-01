//go:build !race

// sync.Pool drops entries at random under the race detector, so allocations
// are counted without it.

package kvdb

import (
	"context"
	"testing"

	ds "github.com/ipfs/go-datastore"
)

func TestAPutAllocations(t *testing.T) {
	ctx := context.Background()
	check := checkSnapshot
	checkSnapshot = nil
	defer func() { checkSnapshot = check }()
	r := warmPebble(t, 4000)
	keys := make([]ds.Key, 4000)
	values := make([][]byte, 4000)
	for i := range keys {
		keys[i], values[i] = warmKey(i), warmValue(i, 1)
	}
	i := 0
	a := testing.AllocsPerRun(500, func() {
		i++
		k := (i * 7919) % 4000
		if err := r.Put(ctx, keys[k], values[k]); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("a put: %.0f allocations", a)
	if a > 8 {
		t.Errorf("a put: %.0f allocations, want at most 8", a)
	}
}
