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
// on the store a node runs, kept in memory: 133 once scans lent the store's
// bytes, 60 once keys and values were built in the txn's storage, contexts
// shared what they did not change, and delta merges reused theirs, 49 once
// a txn wrote pebbleds in batches it keeps, 40 with the node's block store
// over pebbleds behind the replica's DAG instead of the tests' map store, 20
// once blocks were kvdb's own, not dag-pb built through ipld-prime, 16 once
// a link's cid was read over its block's bytes and adding a block took no
// timeout. With the node's identity and cache layers over its block store,
// 24, and 18 once blocks were written past the block service and bitswap
// told of them in batches, 12 once the block store answered from memory in
// place of boxo's cache, 11 once a delta was marshalled with its block, 10
// once a context entry copied for a change kept its few spans in itself, 7
// once a block's cid shared its allocation, a parent that was a head was
// named by the head's own cid, and the heads were listed in storage the
// store keeps. Over the tests' map store it was 250 before its txns, its
// delta and its broadcast were reused or borrowed, and 171 after.
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
