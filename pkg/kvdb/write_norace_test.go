//go:build !race

// The race detector makes sync.Pool drop what is put in it at random, so
// what the write path's pools save is counted only without it.

package kvdb

import (
	"context"
	"testing"

	ds "github.com/ipfs/go-datastore"
)

// TestAPutAllocations bounds what an overwrite on a warm replica allocates,
// the bench store's own work included: 250 before its txns, its delta and
// its broadcast were reused or borrowed, 171 after.
func TestAPutAllocations(t *testing.T) {
	ctx := context.Background()
	check := checkSnapshot
	checkSnapshot = nil
	defer func() { checkSnapshot = check }()
	r, _ := warmReplica(t, 4000)
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
	if a > 175 {
		t.Errorf("a put: %.0f allocations, want at most 175", a)
	}
}
