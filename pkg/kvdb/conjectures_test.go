package kvdb

import (
	"context"
	"fmt"
	"maps"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
)

// TestE1CompactionKeepsEveryLiveVersion: a key with two concurrent versions
// must keep both through a compaction. B deletes the only version it has
// seen; the version C wrote concurrently was never observed by that delete,
// so it survives on every replica that has the same deltas.
func TestE1CompactionKeepsEveryLiveVersion(t *testing.T) {
	replicas, dagsyncs, closeReplicas := makeNReplicasSeparateStores(t, 4, nil)
	defer closeReplicas()
	a, b, c, u := replicas[0], replicas[1], replicas[2], replicas[3]
	ctx := context.Background()
	k := ds.NewKey("k")

	if err := c.Put(ctx, k, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	for _, x := range []string{"x1", "x2"} {
		if err := b.Put(ctx, ds.NewKey(x), []byte(x)); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Put(ctx, k, []byte("v2")); err != nil {
		t.Fatal(err)
	}

	for _, dst := range []int{0, 3} {
		from(dagsyncs, dst, 2)
		syncReplicaHeads(t, c, replicas[dst])
		from(dagsyncs, dst, 1)
		syncReplicaHeads(t, b, replicas[dst])
	}

	if _, err := a.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	from(dagsyncs, 3, 0)
	syncReplicaHeads(t, a, u)

	if err := b.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}
	for _, dst := range []int{0, 3} {
		from(dagsyncs, dst, 1)
		syncReplicaHeads(t, b, replicas[dst])
	}

	if got := valueOf(t, u, k); got != "v1" {
		t.Errorf("replica U: k = %s, want v1", got)
	}
	if got := valueOf(t, a, k); got != "v1" {
		t.Errorf("compactor A: k = %s, want v1 (the delete never observed C's version)", got)
	}
}

// TestE2CompactionDuringAWalkLosesNothing: a merge root becomes a head as soon
// as one of its branches bottoms out. Compacting in that window must not hide
// the other branch from replicas that learn the history from the snapshot.
func TestE2CompactionDuringAWalkLosesNothing(t *testing.T) {
	replicas, dagsyncs, closeReplicas := makeNReplicasSeparateStores(t, 6, nil)
	defer closeReplicas()
	a, w1, w2, w3, x, f := replicas[0], replicas[1], replicas[2], replicas[3], replicas[4], replicas[5]
	ctx := context.Background()

	if err := w1.Put(ctx, ds.NewKey("k1"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := w2.Put(ctx, ds.NewKey("k2"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	from(dagsyncs, 3, 1)
	syncReplicaHeads(t, w1, w3)
	from(dagsyncs, 3, 2)
	syncReplicaHeads(t, w2, w3)
	if err := w3.Put(ctx, ds.NewKey("k3"), []byte("3")); err != nil {
		t.Fatal(err)
	}
	r, _ := w3.heads.List()
	if len(r) != 1 {
		t.Fatalf("w3 has %d heads, want the merge root only", len(r))
	}
	root := r[0]
	p1, _ := w1.heads.List()
	p2, _ := w2.heads.List()

	// X merges everything.
	from(dagsyncs, 4, 3)
	syncReplicaHeads(t, w3, x)

	// A merges the root and the first branch, and is still walking the
	// second one when it compacts.
	from(dagsyncs, 0, 3)
	mergeOne(t, a, root.Cid)
	mergeOne(t, a, p1[0].Cid)
	if _, err := a.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	mergeOne(t, a, p2[0].Cid)

	from(dagsyncs, 4, 0)
	syncHeadsLogged(t, a, x)
	from(dagsyncs, 5, 0)
	syncHeadsLogged(t, a, f)

	for name, rep := range map[string]*Datastore{"A": a, "X": x, "F": f} {
		for _, k := range []string{"k1", "k2", "k3"} {
			if got := valueOf(t, rep, ds.NewKey(k)); got == "<deleted>" {
				t.Errorf("replica %s lost %s", name, k)
			}
		}
	}
}

// mergeOne merges the single block c on r without descending into its
// parents: one step of what dagWorker does.
func mergeOne(t testing.TB, r *Datastore, c cid.Cid) {
	t.Helper()
	ctx := context.Background()
	ng := &crdtNodeGetter{NodeGetter: r.dagService}
	nd, d, err := ng.GetDelta(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.processNode(ctx, ng, d, nd); err != nil {
		t.Fatal(err)
	}
}

// TestE3AFreshReplicaSyncsFromAReclaimingPeer: B wrote offline on top of h; A's
// snapshot covers h and B reclaimed it. A fresh replica syncing from B must
// end up clean, with nobody holding h.
func TestE3AFreshReplicaSyncsFromAReclaimingPeer(t *testing.T) {
	replicas, dagsyncs, closeReplicas := makeNReplicasSeparateStores(t, 3, nil)
	defer closeReplicas()
	a, b, f := replicas[0], replicas[1], replicas[2]
	ctx := context.Background()

	if err := a.Put(ctx, ds.NewKey("h"), []byte("h")); err != nil {
		t.Fatal(err)
	}
	from(dagsyncs, 1, 0)
	syncReplicaHeads(t, a, b)
	if err := b.Put(ctx, ds.NewKey("off"), []byte("off")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	syncReplicaHeads(t, a, b)

	from(dagsyncs, 2, 1)
	dagsyncs[1].setRemotes()
	syncReplicaHeads(t, b, f)
	if err := f.Repair(ctx); err != nil {
		t.Errorf("fresh replica cannot repair: %s", err)
	}
	if f.IsDirty(ctx) {
		t.Errorf("fresh replica is still dirty after a repair")
	}
	for _, k := range []string{"h", "off"} {
		if got := valueOf(t, f, ds.NewKey(k)); got != k {
			t.Errorf("fresh replica: %s = %s", k, got)
		}
	}
	bh, _ := b.heads.List()
	fh, _ := f.heads.List()
	if !sameCids(headCIDs(bh), headCIDs(fh)) {
		t.Errorf("fresh replica's heads %v differ from its source's %v: a branch it merged is not a tip it will build on", headCIDs(fh), headCIDs(bh))
	}
}

// TestACoveredBlockIsNeverFetched: a replica never asks for a block whose dot
// its context covers. A fresh replica syncing from a compacted peer fetches
// the snapshot alone and records nothing it covers as missing; and the walk
// skips a covered link it is handed, as a walk is when a snapshot merged
// meanwhile covers a parent that was missing when it was linked.
func TestACoveredBlockIsNeverFetched(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	a := replicas[0]
	ctx := context.Background()

	var history []link
	for i := range 5 {
		put(t, a, fmt.Sprintf("k%d", i), "v")
		history = append(history, onlyHeadLink(t, a))
	}
	del(t, a, "k0")
	history = append(history, onlyHeadLink(t, a))
	compact(t, a)
	snapshot := onlyHead(t, a)

	dag := newCountingDAG()
	dag.setRemotes(dagsyncs[0].DAGService)
	f, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("crdttest"), dag, &nullBroadcaster{}, replicaOptions(noAutoCompact(), "fresh: "))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	syncReplicaHeads(t, a, f)
	if got := dag.take(); !maps.Equal(got, map[cid.Cid]int{snapshot: 1}) {
		t.Errorf("syncing a snapshot fetched %v, want the snapshot %s alone", got, snapshot)
	}
	if n, err := countPrefix(ctx, f.store, f.missingPrefix()); err != nil || n != 0 {
		t.Errorf("the fresh replica records %d covered parents as missing (%v)", n, err)
	}

	if err := f.handleBranch(ctx, history); err != nil {
		t.Fatal(err)
	}
	if got := dag.take(); len(got) != 0 {
		t.Errorf("the walk fetched blocks it covers: %v", got)
	}
	assertSameState(t, []string{"a", "fresh"}, a, f)
}

func onlyHeadLink(t testing.TB, r *Datastore) link {
	t.Helper()
	hs, _ := r.heads.List()
	if len(hs) != 1 {
		t.Fatalf("want one head, have %d", len(hs))
	}
	return link{cid: hs[0].Cid, dot: hs[0].Dot}
}
