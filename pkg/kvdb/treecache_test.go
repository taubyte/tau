package kvdb

import (
	"context"
	"fmt"
	"testing"

	cid "github.com/ipfs/go-cid"
	"github.com/taubyte/tau/pkg/kvdb/mst"
)

// TestAMergeReadsItsBaseFromTheCacheOfTreeNodes: a snapshot merge diffs its
// state against the base, and retires the base's paths it replaced. The
// base's upper levels are the nodes the last merge read from the snapshot
// it merged, and they are read from the store's cache of tree nodes: of the
// base's own nodes, a merge reads from the store only nodes of level 0, the
// ones with no subtree, on the paths of the keys that changed. Every merge
// used to read the base's paths, roots included, back from the blockstore
// and decode them again.
func TestAMergeReadsItsBaseFromTheCacheOfTreeNodes(t *testing.T) {
	// The oracle reads every record of every merge.
	check := checkSnapshot
	checkSnapshot = nil
	defer func() { checkSnapshot = check }()
	ctx := context.Background()
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	a, b := replicas[0], replicas[1]
	seedKeys(t, a, 3000, "k")
	compact(t, a)
	syncReplicaHeads(t, a, b)
	counting := &countingDAG{fetchThroughDAGSvc: dagsyncs[1], fetched: map[cid.Cid]int{}}
	b.dagService = counting
	b.gc.recent.DAGService = counting

	const rounds, changed = 8, 3
	leaves, upper := 0, 0
	for round := range rounds {
		for i := range changed {
			put(t, a, fmt.Sprintf("/k%04d", (round*changed+i)*397%3000), fmt.Sprintf("r%d", round))
		}
		compact(t, a)
		base := b.base
		closure, err := mst.Reachable(ctx, dagsyncs[1], base.elems, base.pending)
		if err != nil {
			t.Fatal(err)
		}
		bottom := map[cid.Cid]bool{}
		if err := closure.ForEach(func(c cid.Cid) error {
			nd, err := dagsyncs[1].Get(ctx, c)
			bottom[c] = err == nil && len(blockLinksOf(t, nd)) == 0 && c != base.elems && c != base.pending
			return err
		}); err != nil {
			t.Fatal(err)
		}
		counting.take()
		syncReplicaHeads(t, a, b)
		if b.IsDirty(ctx) {
			t.Fatal("the merge did not finish")
		}
		for c, n := range counting.take() {
			if !closure.Has(c) {
				continue
			}
			if bottom[c] {
				leaves += n
			} else {
				upper += n
			}
		}
	}
	t.Logf("%d merges of %d changed keys: %d reads of the base's nodes of level 0, %d of the others", rounds, changed, leaves, upper)
	if upper > 0 {
		t.Fatalf("%d merges read the base's upper levels from the store %d times", rounds, upper)
	}
	if got, want := queryAll(t, b), queryAll(t, a); !sameKV(got, want) {
		t.Fatalf("b holds %d keys, want %d", len(got), len(want))
	}
	assertTreesMatch(t, b)
}

// TestTheCacheOfTreeNodesStaysWithinItsBudget: through a long run of
// writes, compactions and merges on both sides, the cache of tree nodes
// never holds more than its budget, the default or a small one.
func TestTheCacheOfTreeNodesStaysWithinItsBudget(t *testing.T) {
	const small = 16 << 10
	replicas, dagsyncs, done := makeSeparateStoresWith(t, 2, func(i int) *Options {
		o := noAutoCompact()
		if i == 1 {
			o.treeCache = small
		}
		return o
	})
	defer done()
	a, b := replicas[0], replicas[1]
	from(dagsyncs, 0, 1)
	seedKeys(t, a, 2000, "k")
	compact(t, a)
	syncReplicaHeads(t, a, b)
	peak := [2]int{}
	for round := range 120 {
		for i := range 10 {
			put(t, a, fmt.Sprintf("/k%04d", (round*10+i)*131%2000), fmt.Sprintf("a%d", round))
		}
		compact(t, a)
		syncReplicaHeads(t, a, b)
		put(t, b, fmt.Sprintf("/b%04d", round%50), fmt.Sprintf("b%d", round))
		del(t, b, fmt.Sprintf("/k%04d", round*7%2000))
		compact(t, b)
		syncReplicaHeads(t, b, a)
		for i, r := range []*Datastore{a, b} {
			used, budget := r.trees.Used(), []int{treeCacheBytes, small}[i]
			if used > budget {
				t.Fatalf("round %d: replica %d's cache holds %d bytes, over its budget of %d", round, i, used, budget)
			}
			peak[i] = max(peak[i], used)
		}
	}
	t.Logf("peak use: %d of %d, %d of %d", peak[0], treeCacheBytes, peak[1], small)
	if peak[1] < small/2 {
		t.Fatalf("the small cache never held more than %d bytes: the run does not test its budget", peak[1])
	}
	if got, want := queryAll(t, b), queryAll(t, a); !sameKV(got, want) {
		t.Fatalf("b holds %d keys, a %d", len(got), len(want))
	}
	assertTreesMatch(t, a)
	assertTreesMatch(t, b)
}

// TestACachedNodeIsNoProofItIsHeld: a node the cache keeps may be one the
// collector has since removed. A state that reaches it again has it
// fetched, as any block the store no longer holds: what a store holds is
// what its collector says, never what its cache can decode.
func TestACachedNodeIsNoProofItIsHeld(t *testing.T) {
	ctx := context.Background()
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	a, b := replicas[0], replicas[1]
	seedKeys(t, a, 2000, "k")
	compact(t, a)
	syncReplicaHeads(t, a, b)
	old := b.base.elems
	if b.trees.Used() == 0 {
		t.Fatal("the merge cached no node")
	}

	put(t, a, "/z", "z")
	compact(t, a)
	syncReplicaHeads(t, a, b)
	if err := b.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if dagsyncs[1].holds(old) {
		t.Fatalf("b still holds %s, the root of the elements it no longer has", old)
	}

	del(t, a, "/z")
	compact(t, a)
	syncReplicaHeads(t, a, b)
	if b.IsDirty(ctx) {
		t.Fatal("the catch-up did not finish")
	}
	if b.base.elems != old {
		t.Fatalf("the elements are back to %s, not %s", b.base.elems, old)
	}
	if !dagsyncs[1].holds(old) {
		t.Fatalf("b did not fetch %s again", old)
	}
	assertReach(t, b)
	assertNoLeak(t, b, dagsyncs[1])
}
