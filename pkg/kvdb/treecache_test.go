package kvdb

import (
	"context"
	"fmt"
	"testing"

	cid "github.com/ipfs/go-cid"
	"github.com/taubyte/tau/pkg/kvdb/statetree"
)

func TestAMergeReadsItsBaseFromTheCacheOfTreeNodes(t *testing.T) {
	// The oracle reads every record of every merge.
	unchecked := noAutoCompact()
	unchecked.checks = nil
	ctx := context.Background()
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, unchecked)
	defer done()
	a, b := replicas[0], replicas[1]
	seedKeys(t, a, 3000, "k")
	compact(t, a)
	syncReplicaHeads(t, a, b)
	counting := &countingDAG{fetchThroughDAGSvc: dagsyncs[1], fetched: map[cid.Cid]int{}}
	b.dagService = counting
	b.gc.recent.DAGService = counting

	const rounds, changed = 32, 3
	leaves, upper, first, peak := 0, 0, 0, 0
	for round := range rounds {
		for i := range changed {
			put(t, a, fmt.Sprintf("/k%04d", (round*changed+i)*397%3000), fmt.Sprintf("r%d", round))
		}
		compact(t, a)
		base := b.base
		closure, err := statetree.Reachable(ctx, dagsyncs[1], base.elems, base.pending)
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
		if round == 0 {
			first = b.trees.Used()
		}
		peak = max(peak, b.trees.Used())
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
	// The cache holds the base, not every base it had: kept, superseded nodes
	// fill it and push out the live ones a later merge reads.
	if peak > 2*first {
		t.Fatalf("the cache grew from %d bytes after the first merge to %d", first, peak)
	}
	if upper > 0 {
		t.Fatalf("%d merges read the base's upper levels from the store %d times", rounds, upper)
	}
	if got, want := queryAll(t, b), queryAll(t, a); !sameKV(got, want) {
		t.Fatalf("b holds %d keys, want %d", len(got), len(want))
	}
	assertTreesMatch(t, b)
}

func TestTheCacheOfTreeNodesStaysWithinItsBudget(t *testing.T) {
	t.Parallel()
	// The tree's live upper levels alone outgrow the small budget: the cache forgets what
	// no root reaches, so a smaller tree would never press it. At 6000 keys a loaded
	// box pressed it as few as four times in a run; at 12000 never fewer than 2000.
	const small, keys = 16 << 10, 12000
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
	seedKeys(t, a, keys, "k")
	compact(t, a)
	syncReplicaHeads(t, a, b)
	peak := [2]int{}
	for round := range 120 {
		for i := range 10 {
			put(t, a, fmt.Sprintf("/k%04d", (round*10+i)*131%keys), fmt.Sprintf("a%d", round))
		}
		compact(t, a)
		syncReplicaHeads(t, a, b)
		put(t, b, fmt.Sprintf("/b%04d", round%50), fmt.Sprintf("b%d", round))
		del(t, b, fmt.Sprintf("/k%04d", round*7%keys))
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
	t.Logf("peak use: %d of %d, %d of %d; the small one pressed %d times", peak[0], treeCacheBytes, peak[1], small, b.trees.Pressed())
	if b.trees.Pressed() == 0 {
		t.Fatalf("no node ever arrived to the small cache full, peak %d: the run does not test its budget", peak[1])
	}
	if got, want := queryAll(t, b), queryAll(t, a); !sameKV(got, want) {
		t.Fatalf("b holds %d keys, a %d", len(got), len(want))
	}
	assertTreesMatch(t, a)
	assertTreesMatch(t, b)
}

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
