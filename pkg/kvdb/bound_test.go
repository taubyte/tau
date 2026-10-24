package kvdb

import (
	"context"
	"fmt"
	"testing"

	ds "github.com/ipfs/go-datastore"
)

// footprint is everything a replica stores: its datastore entries and the
// blocks in its own blockstore.
func footprint(t testing.TB, r *Datastore, d *fetchThroughDAGSvc) int {
	t.Helper()
	return namespaceKeys(t, r) + d.blockCount(t)
}

// churn runs write cycles round-robin over the replicas, each replica
// syncing from the previous writer before it writes, and returns each
// replica's peak footprint over the first and the second half of the run. A
// cycle puts a key and deletes it, or only puts it when overwrite is set.
func churn(t *testing.T, replicas []*Datastore, dagsyncs []*fetchThroughDAGSvc, cycles int, key func(int) string, overwrite bool) (first, second []int) {
	t.Helper()
	ctx := context.Background()
	n := len(replicas)
	first, second = make([]int, n), make([]int, n)
	for c := range cycles {
		w := c % n
		if n > 1 {
			prev := (w + n - 1) % n
			from(dagsyncs, w, prev)
			syncReplicaHeads(t, replicas[prev], replicas[w])
		}
		k := ds.NewKey(key(c))
		if err := replicas[w].Put(ctx, k, []byte(fmt.Sprintf("value-%d", c))); err != nil {
			t.Fatal(err)
		}
		if !overwrite {
			if err := replicas[w].Delete(ctx, k); err != nil {
				t.Fatal(err)
			}
		}
		for i := range replicas {
			f := footprint(t, replicas[i], dagsyncs[i])
			peak := first
			if c >= cycles/2 {
				peak = second
			}
			if f > peak[i] {
				peak[i] = f
			}
		}
	}
	return first, second
}

func TestStorageIsBoundedUnderChurn(t *testing.T) {
	const cycles = 400
	for _, tc := range []struct {
		name      string
		replicas  int
		key       func(int) string
		overwrite bool
	}{
		{"same key, one replica", 1, func(int) string { return "k" }, false},
		{"unique keys, one replica", 1, func(c int) string { return fmt.Sprintf("k%d", c) }, false},
		{"same key, three replicas", 3, func(int) string { return "k" }, false},
		{"unique keys, three replicas", 3, func(c int) string { return fmt.Sprintf("k%d", c) }, false},
		// Overwrites only: a put removes the versions it observed, so the
		// key keeps one live version however often it is written.
		{"same key overwritten, one replica", 1, func(int) string { return "k" }, true},
		{"same key overwritten, three replicas", 3, func(int) string { return "k" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.CompactAfterBlocks = 16
			replicas, dagsyncs, done := makeNReplicasSeparateStores(t, tc.replicas, opts)
			defer done()

			first, second := churn(t, replicas, dagsyncs, cycles, tc.key, tc.overwrite)
			// At most one key is live at the end of a cycle: what a
			// replica may hold is that key, the blocks since its last
			// snapshot, the snapshot, and one vector entry per
			// incarnation. None of that grows with the number of cycles.
			limit := 4*opts.CompactAfterBlocks + 8*tc.replicas
			for i := range replicas {
				if second[i] > first[i] || second[i] > limit {
					t.Errorf("replica %d grows with churn: peak %d over the first %d cycles, %d over the next %d (limit %d)", i, first[i], cycles/2, second[i], cycles/2, limit)
				}
			}
		})
	}
}
