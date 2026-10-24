package kvdb

import (
	"context"
	"fmt"
	"sync"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
)

// The holes H1-H5 and the counterexamples of
// .notes/v2/kvdb-tombstone-reclaim.md, each driven deterministically.

func onlyHead(t testing.TB, r *Datastore) cid.Cid {
	t.Helper()
	hs, _ := r.heads.List()
	if len(hs) != 1 {
		t.Fatalf("want one head, have %d", len(hs))
	}
	return hs[0].Cid
}

func put(t testing.TB, r *Datastore, k, v string) cid.Cid {
	t.Helper()
	if err := r.Put(context.Background(), ds.NewKey(k), []byte(v)); err != nil {
		t.Fatal(err)
	}
	return onlyHead(t, r)
}

func del(t testing.TB, r *Datastore, k string) cid.Cid {
	t.Helper()
	if err := r.Delete(context.Background(), ds.NewKey(k)); err != nil {
		t.Fatal(err)
	}
	return onlyHead(t, r)
}

func compact(t testing.TB, r *Datastore) {
	t.Helper()
	if _, err := r.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func want(t testing.TB, name string, r *Datastore, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		if got := valueOf(t, r, ds.NewKey(k)); got != v {
			t.Errorf("replica %s: %s = %s, want %s", name, k, got, v)
		}
	}
}

func noAutoCompact() *Options {
	o := DefaultOptions()
	o.CompactAfterBlocks = 0
	return o
}

// H1: a replica that merged (D,7) before (D,6) and compacted must not lose
// (D,6), on itself or on a replica that installs its snapshot.
func TestH1OutOfOrderMergeThenCompactLosesNothing(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 3, noAutoCompact())
	defer done()
	d, a, f := replicas[0], replicas[1], replicas[2]

	b6 := put(t, d, "z6", "6")
	b7 := put(t, d, "z7", "7")

	from(dagsyncs, 1, 0)
	mergeOne(t, a, b7)
	compact(t, a)

	dagsyncs[2].setRemotes(dagsyncs[1].DAGService, dagsyncs[0].DAGService)
	syncReplicaHeads(t, a, f)
	want(t, "F", f, map[string]string{"z6": "6", "z7": "7"})

	mergeOne(t, a, b6)
	want(t, "A", a, map[string]string{"z6": "6", "z7": "7"})
	assertSameState(t, []string{"A", "F"}, a, f)
}

// H2: a tombstone merged before the element it removes rides in the
// snapshot, so a replica that installs the snapshot refuses the element.
func TestH2TombstoneBeforeAddSurvivesCompaction(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 3, noAutoCompact())
	defer done()
	d, a, f := replicas[0], replicas[1], replicas[2]

	put(t, d, "w", "W")
	put(t, d, "other", "o")
	b5 := del(t, d, "w")

	from(dagsyncs, 1, 0)
	mergeOne(t, a, b5)
	compact(t, a)

	dagsyncs[2].setRemotes(dagsyncs[1].DAGService, dagsyncs[0].DAGService)
	syncReplicaHeads(t, a, f)
	syncReplicaHeads(t, d, f)
	want(t, "F", f, map[string]string{"w": "<deleted>", "other": "o"})

	// A's own walk goes on to the parents the delete links.
	if err := a.Repair(context.Background()); err != nil {
		t.Fatal(err)
	}
	want(t, "A", a, map[string]string{"w": "<deleted>", "other": "o"})
	from(dagsyncs, 0, 1)
	syncReplicaHeads(t, a, d)
	assertSameState(t, []string{"D", "A", "F"}, d, a, f)
}

// H3: the removal rule runs on every merge, not only between snapshots. A
// fresh replica that installed A's snapshot (X removed) and then walks B's
// chain, which still links the block that added X, keeps X removed.
func TestH3FreshReplicaWalkingAStaleChainKeepsTheDelete(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 3, noAutoCompact())
	defer done()
	a, b, f := replicas[0], replicas[1], replicas[2]

	put(t, a, "x", "X")
	from(dagsyncs, 1, 0)
	syncReplicaHeads(t, a, b)
	put(t, b, "y", "Y")

	del(t, a, "x")
	compact(t, a)

	from(dagsyncs, 2, 0)
	syncReplicaHeads(t, a, f)
	dagsyncs[2].setRemotes(dagsyncs[1].DAGService)
	syncReplicaHeads(t, b, f)
	want(t, "F", f, map[string]string{"x": "<deleted>", "y": "Y"})

	from(dagsyncs, 0, 1)
	syncReplicaHeads(t, b, a)
	from(dagsyncs, 1, 0)
	syncReplicaHeads(t, a, b)
	want(t, "B", b, map[string]string{"x": "<deleted>", "y": "Y"})
	assertSameState(t, []string{"A", "B", "F"}, a, b, f)
}

// H4: a snapshot whose elements span several chunk blocks is merged whole or
// not at all; a replica that cannot fetch every chunk keeps what it has.
func TestH4ChunkedSnapshotIsMergedWhole(t *testing.T) {
	opts := noAutoCompact()
	opts.MaxBatchDeltaSize = 200
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, opts)
	defer done()
	a, x := replicas[0], replicas[1]
	ctx := context.Background()

	all := map[string]string{}
	for i := range 40 {
		k, v := fmt.Sprintf("key-%02d", i), fmt.Sprintf("value-%02d", i)
		put(t, a, k, v)
		all[k] = v
	}
	syncReplicaHeads(t, a, x)
	compact(t, a)

	root := onlyHead(t, a)
	_, rootDelta, err := (&crdtNodeGetter{a.dagService}).GetDelta(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rootDelta.GetChunks()) < 2 {
		t.Fatalf("test setup: want a snapshot in several chunks, got %d", len(rootDelta.GetChunks()))
	}
	held, err := cid.Cast(rootDelta.GetChunks()[1])
	if err != nil {
		t.Fatal(err)
	}
	nd, err := dagsyncs[0].DAGService.Get(ctx, held)
	if err != nil {
		t.Fatal(err)
	}
	if err := dagsyncs[0].DAGService.Remove(ctx, held); err != nil {
		t.Fatal(err)
	}

	syncHeadsLogged(t, a, x)
	want(t, "X", x, all)
	if x.covered(dotFromPB(rootDelta.GetDot())) {
		t.Fatal("X merged a snapshot it could not fetch whole")
	}

	if err := dagsyncs[0].DAGService.Add(ctx, nd); err != nil {
		t.Fatal(err)
	}
	// The snapshot is heard once; repair is what retries it.
	if err := x.Repair(ctx); err != nil {
		t.Fatal(err)
	}
	if !x.covered(dotFromPB(rootDelta.GetDot())) {
		t.Fatal("repair did not retry the snapshot")
	}
	want(t, "X", x, all)
	assertSameState(t, []string{"A", "X"}, a, x)
}

// droppingDAG ends a GetMany early, with no error, at the first block in
// drop: what a getter does when its context ends mid-fetch, since nothing
// obliges it to report each block it did not send.
type droppingDAG struct {
	*fetchThroughDAGSvc

	mu   sync.Mutex
	drop map[cid.Cid]bool
}

func (d *droppingDAG) dropping(c cid.Cid) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.drop[c]
}

func (d *droppingDAG) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	out := make(chan *ipld.NodeOption, len(cids))
	go func() {
		defer close(out)
		for _, c := range cids {
			if d.dropping(c) {
				return
			}
			nd, err := d.Get(ctx, c)
			out <- &ipld.NodeOption{Node: nd, Err: err}
		}
	}()
	return out
}

// H4, the other way a fetch comes up short: the getter stops before the last
// chunk and reports nothing. The snapshot is still not merged until it is
// whole.
func TestH4AChunkFetchThatEndsEarlyIsNotMerged(t *testing.T) {
	opts := noAutoCompact()
	opts.MaxBatchDeltaSize = 200
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 1, opts)
	defer done()
	a := replicas[0]
	ctx := context.Background()

	all := map[string]string{}
	for i := range 40 {
		k, v := fmt.Sprintf("key-%02d", i), fmt.Sprintf("value-%02d", i)
		put(t, a, k, v)
		all[k] = v
	}
	compact(t, a)
	root := onlyHead(t, a)
	_, rootDelta, err := (&crdtNodeGetter{a.dagService}).GetDelta(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rootDelta.GetChunks()) < 2 {
		t.Fatalf("test setup: want a snapshot in several chunks, got %d", len(rootDelta.GetChunks()))
	}
	last, err := cid.Cast(rootDelta.GetChunks()[len(rootDelta.GetChunks())-1])
	if err != nil {
		t.Fatal(err)
	}

	dag := &droppingDAG{fetchThroughDAGSvc: newFetchThrough(), drop: map[cid.Cid]bool{last: true}}
	dag.setRemotes(dagsyncs[0].DAGService)
	// The test repairs X itself. The repair loop's first pass runs at
	// open, and when it lands after the failed fetch it walks the snapshot
	// while the chunk is still withheld and holds it until the test's
	// repair has already found nothing to do.
	xopts := replicaOptions(opts, "x: ")
	xopts.RepairInterval = 0
	x, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("crdttest"), dag, &nullBroadcaster{}, xopts)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()

	syncHeadsLogged(t, a, x)
	if x.covered(dotFromPB(rootDelta.GetDot())) {
		t.Fatal("X merged a snapshot it fetched only part of")
	}
	if got := queryAll(t, x); len(got) != 0 {
		t.Fatalf("X shows %d keys of a snapshot it did not merge", len(got))
	}

	dag.mu.Lock()
	dag.drop = nil
	dag.mu.Unlock()
	if err := x.Repair(ctx); err != nil {
		t.Fatal(err)
	}
	want(t, "X", x, all)
	assertSameState(t, []string{"A", "X"}, a, x)
}

// H5: one context per store. A delete issued by one writer of an element
// another writer added survives a compaction, against the adder's stale
// chain.
func TestH5ADeleteOfAnotherWritersElementSurvivesCompaction(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 4, noAutoCompact())
	defer done()
	w1, w2, a, f := replicas[0], replicas[1], replicas[2], replicas[3]

	put(t, w1, "k", "v")
	from(dagsyncs, 1, 0)
	syncReplicaHeads(t, w1, w2)
	del(t, w2, "k")

	from(dagsyncs, 2, 1)
	syncReplicaHeads(t, w2, a)
	compact(t, a)

	from(dagsyncs, 3, 2)
	syncReplicaHeads(t, a, f)
	from(dagsyncs, 3, 0)
	syncReplicaHeads(t, w1, f)
	want(t, "F", f, map[string]string{"k": "<deleted>"})
	assertSameState(t, []string{"A", "F"}, a, f)
}

// The generation-window counterexample: B compacts its own stale view as
// many times as A compacts the real one. Generation numbers cannot tell the
// fork apart; dots can.
func TestStaleForkCompactedInStepDoesNotResurrect(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 3, noAutoCompact())
	defer done()
	a, b, r := replicas[0], replicas[1], replicas[2]

	put(t, a, "x", "X")
	from(dagsyncs, 1, 0)
	syncReplicaHeads(t, a, b)

	del(t, a, "x")
	compact(t, a)
	put(t, a, "a2", "1")
	compact(t, a)

	put(t, b, "b1", "1")
	compact(t, b)
	put(t, b, "b2", "1")
	compact(t, b)

	for _, order := range [][2]int{{0, 1}, {1, 0}} {
		fresh, fdag, closeFresh := makeNReplicasSeparateStores(t, 1, noAutoCompact())
		fdag[0].setRemotes(dagsyncs[0].DAGService, dagsyncs[1].DAGService)
		for _, i := range order {
			syncReplicaHeads(t, replicas[i], fresh[0])
		}
		want(t, fmt.Sprintf("fresh%v", order), fresh[0], map[string]string{"x": "<deleted>", "a2": "1", "b1": "1", "b2": "1"})
		closeFresh()
	}

	dagsyncs[2].setRemotes(dagsyncs[0].DAGService, dagsyncs[1].DAGService)
	syncReplicaHeads(t, b, r)
	syncReplicaHeads(t, a, r)
	from(dagsyncs, 0, 1)
	syncReplicaHeads(t, b, a)
	syncReplicaHeads(t, a, b)
	assertSameState(t, []string{"A", "B", "R"}, a, b, r)
	want(t, "R", r, map[string]string{"x": "<deleted>"})
}

// The compaction-as-a-DAG-head counterexample: A deletes X and compacts away
// its history; B, offline, puts Y and compacts {X, Y}. A fresh replica that
// merges both kills X and keeps Y, in either order.
func TestCompactionCounterexampleKillsXKeepsY(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	a, b := replicas[0], replicas[1]

	put(t, a, "x", "X")
	from(dagsyncs, 1, 0)
	syncReplicaHeads(t, a, b)
	del(t, a, "x")
	compact(t, a)
	put(t, b, "y", "Y")
	compact(t, b)

	var states []string
	for _, order := range [][2]int{{0, 1}, {1, 0}} {
		fresh, fdag, closeFresh := makeNReplicasSeparateStores(t, 1, noAutoCompact())
		fdag[0].setRemotes(dagsyncs[0].DAGService, dagsyncs[1].DAGService)
		for _, i := range order {
			syncReplicaHeads(t, replicas[i], fresh[0])
		}
		want(t, fmt.Sprintf("fresh%v", order), fresh[0], map[string]string{"x": "<deleted>", "y": "Y"})
		states = append(states, crdtState(t, fresh[0]))
		closeFresh()
	}
	if states[0] != states[1] {
		t.Fatalf("merge order changed the state:\n%s\n---\n%s", states[0], states[1])
	}
}

// The review's three-way case: A deleted X and compacted; B, offline, put Y
// with X live; C put Z with X live. Every merge order gives {Y, Z}.
func TestThreeWayCompactionsConvergeInEveryOrder(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 3, noAutoCompact())
	defer done()

	put(t, replicas[0], "x", "X")
	for _, i := range []int{1, 2} {
		from(dagsyncs, i, 0)
		syncReplicaHeads(t, replicas[0], replicas[i])
	}
	del(t, replicas[0], "x")
	compact(t, replicas[0])
	put(t, replicas[1], "y", "Y")
	compact(t, replicas[1])
	put(t, replicas[2], "z", "Z")

	var states []string
	for _, order := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		fresh, fdag, closeFresh := makeNReplicasSeparateStores(t, 1, noAutoCompact())
		fdag[0].setRemotes(dagsyncs[0].DAGService, dagsyncs[1].DAGService, dagsyncs[2].DAGService)
		for _, i := range order {
			syncReplicaHeads(t, replicas[i], fresh[0])
		}
		want(t, fmt.Sprintf("fresh%v", order), fresh[0], map[string]string{"x": "<deleted>", "y": "Y", "z": "Z"})
		states = append(states, crdtState(t, fresh[0]))
		closeFresh()
	}
	for i := range states {
		if states[i] != states[0] {
			t.Fatalf("merge order %d changed the state:\n%s\n---\n%s", i, states[0], states[i])
		}
	}
}

// Concurrent compactions of the same view both merge, and every replica ends
// with the same state.
func TestConcurrentCompactionsOfTheSameViewConverge(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 3, noAutoCompact())
	defer done()
	a, b, c := replicas[0], replicas[1], replicas[2]

	for i := range 8 {
		put(t, a, fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}
	put(t, a, "k5", "v5-final")
	del(t, a, "k3")
	syncReplicaHeads(t, a, b)
	syncReplicaHeads(t, a, c)

	compact(t, a)
	compact(t, b)

	from(dagsyncs, 0, 1)
	syncReplicaHeads(t, b, a)
	syncReplicaHeads(t, a, b)
	dagsyncs[2].setRemotes(dagsyncs[0].DAGService, dagsyncs[1].DAGService)
	syncReplicaHeads(t, a, c)
	syncReplicaHeads(t, b, c)
	assertSameState(t, []string{"A", "B", "C"}, a, b, c)
	want(t, "C", c, map[string]string{"k3": "<deleted>", "k5": "v5-final", "k0": "v0"})

	// C held the whole history and has merged both snapshots: all of it
	// is covered, so C keeps only the snapshots it heads.
	for _, r := range []*Datastore{a, b, c} {
		if r.blockCount > 2 {
			t.Errorf("a replica keeps %d blocks after both snapshots", r.blockCount)
		}
	}
}

// TestCompactingAgainFoldsNothing: a replica that holds only snapshots it
// heads, made here or merged, has nothing to fold. Compacting anyway would
// write a snapshot that frees nothing, and replicas that compact after a
// merge would answer every snapshot with another one, forever.
func TestCompactingAgainFoldsNothing(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("concurrent=%t", concurrent), func(t *testing.T) {
			replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
			defer done()
			a, b := replicas[0], replicas[1]
			ctx := context.Background()

			put(t, a, "x", "1")
			put(t, a, "y", "2")
			del(t, a, "x")
			syncReplicaHeads(t, a, b)
			compact(t, a)
			if concurrent {
				compact(t, b)
				from(dagsyncs, 0, 1)
				syncReplicaHeads(t, b, a)
			}
			syncReplicaHeads(t, a, b)

			for name, r := range map[string]*Datastore{"a": a, "b": b} {
				before, _ := r.heads.List()
				counter := r.counter
				n, err := r.Compact(ctx)
				if err != nil {
					t.Fatal(err)
				}
				after, _ := r.heads.List()
				if n != 0 || r.counter != counter || !sameCids(headCIDs(before), headCIDs(after)) {
					t.Errorf("%s compacted with nothing to fold: dropped %d blocks, counter %d -> %d, heads %v -> %v", name, n, counter, r.counter, headCIDs(before), headCIDs(after))
				}
			}
			assertSameState(t, []string{"a", "b"}, a, b)
		})
	}
}

// A delete concurrent with a compaction that still holds the element wins.
func TestCompactConcurrentDeleteWins(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	a, b := replicas[0], replicas[1]

	put(t, a, "contested", "keep-me")
	put(t, a, "bystander", "x")
	syncReplicaHeads(t, a, b)
	del(t, b, "contested")
	compact(t, a)

	from(dagsyncs, 0, 1)
	syncReplicaHeads(t, b, a)
	from(dagsyncs, 1, 0)
	syncReplicaHeads(t, a, b)
	want(t, "A", a, map[string]string{"contested": "<deleted>", "bystander": "x"})
	assertSameState(t, []string{"A", "B"}, a, b)
}

// Heads do not depend on delivery order: a snapshot merged after a delta that
// links it is not a head.
func TestHeadsDoNotDependOnOrder(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 3, noAutoCompact())
	defer done()
	a, r1, r2 := replicas[0], replicas[1], replicas[2]
	put(t, a, "k", "v")
	compact(t, a)
	s := onlyHead(t, a)
	d := put(t, a, "k2", "v2")
	mergeOne(t, r1, d)
	mergeOne(t, r1, s)
	mergeOne(t, r2, s)
	mergeOne(t, r2, d)
	from(dagsyncs, 1, 0)
	from(dagsyncs, 2, 0)
	assertSameState(t, []string{"A", "D-then-S", "S-then-D"}, a, r1, r2)
}
