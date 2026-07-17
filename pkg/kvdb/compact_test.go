package kvdb

// Tests for spec item G (DAG compaction / generation snapshots) and its
// BenchmarkCompact (Item H).

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// queryAll returns every key/value currently in the datastore, as a map, for
// easy before/after and cross-replica comparisons.
func queryAll(t testing.TB, r *Datastore) map[string][]byte {
	t.Helper()
	ctx := context.Background()
	res, err := r.Query(ctx, query.Query{})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close() //nolint:errcheck
	out := map[string][]byte{}
	for e := range res.Next() {
		if e.Error != nil {
			t.Fatal(e.Error)
		}
		out[e.Key] = append([]byte(nil), e.Value...)
	}
	return out
}

func assertSameKV(t testing.TB, a, b map[string][]byte) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("state mismatch: %d keys vs %d keys (a=%v b=%v)", len(a), len(b), a, b)
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok {
			t.Fatalf("state mismatch: key %s missing on the other side", k)
		}
		if !bytes.Equal(v, bv) {
			t.Fatalf("state mismatch for key %s: %q vs %q", k, v, bv)
		}
	}
}

func headCIDs(heads []Head) []cid.Cid {
	out := make([]cid.Cid, len(heads))
	for i, h := range heads {
		out[i] = h.Cid
	}
	return out
}

func countPrefix(t testing.TB, r *Datastore, prefix ds.Key) int {
	t.Helper()
	ctx := context.Background()
	res, err := r.store.Query(ctx, query.Query{Prefix: prefix.String(), KeysOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close() //nolint:errcheck
	n := 0
	for e := range res.Next() {
		if e.Error != nil {
			t.Fatal(e.Error)
		}
		n++
	}
	return n
}

// syncReplicaHeads processes, on "to", every block reachable from every
// current head of dagName on "from" that "to" does not already know about.
// It mirrors exactly what "to" would do upon receiving a broadcast for
// those heads, without depending on any network/broadcaster timing --
// this is what lets the compaction tests below be fully deterministic.
func syncReplicaHeads(t testing.TB, from, to *Datastore, dagName string) {
	t.Helper()
	ctx := context.Background()
	heads, _, err := from.heads.ListDAG(ctx, dagName)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range heads {
		if err := to.handleBlock(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
}

// deltaTombstonesForKey fetches the delta stored at c and returns the
// tombstone entries in it matching key.
func deltaTombstonesForKey(t testing.TB, r *Datastore, c cid.Cid, key string) []*pb.Element {
	t.Helper()
	ctx := context.Background()
	ng := &crdtNodeGetter{NodeGetter: r.dagService}
	_, deltaBytes, err := ng.GetDelta(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	delta := r.newDelta()
	if err := delta.Unmarshal(deltaBytes); err != nil {
		t.Fatal(err)
	}
	tombs, err := delta.GetTombstones()
	if err != nil {
		t.Fatal(err)
	}
	var out []*pb.Element
	for _, tb := range tombs {
		if tb.GetKey() == key {
			out = append(out, tb)
		}
	}
	return out
}

// TestCompactSingleReplica checks the core single-replica contract: after
// Compact, Query results are unchanged, there is a single head, the purge
// count matches the purged history, purged blocks are actually gone from
// the DAG service, and (unlike PurgeDAG) their processed-block markers are
// kept.
func TestCompactSingleReplica(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	const numKeys = 20
	keys := make([]ds.Key, numKeys)
	for i := range numKeys {
		keys[i] = ds.NewKey(fmt.Sprintf("compact-key-%d", i))
		if err := r.Put(ctx, keys[i], fmt.Appendf(nil, "v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	// a few extra versions (multiple element markers per key)...
	for i := range 5 {
		if err := r.Put(ctx, keys[i], fmt.Appendf(nil, "v%d-updated", i)); err != nil {
			t.Fatal(err)
		}
	}
	// ...and some fully deleted keys.
	for i := 5; i < 10; i++ {
		if err := r.Delete(ctx, keys[i]); err != nil {
			t.Fatal(err)
		}
	}

	before := queryAll(t, r)

	oldHeads, _, err := r.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(oldHeads) == 0 {
		t.Fatal("expected heads before compaction")
	}
	dagCIDSet, _, _, err := r.walkProcessedDAG(ctx, headCIDs(oldHeads))
	if err != nil {
		t.Fatal(err)
	}

	n, err := r.Compact(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected purged blocks > 0")
	}
	if n != len(dagCIDSet) {
		t.Fatalf("expected purge count %d (size of the pre-compaction walk), got %d", len(dagCIDSet), n)
	}

	after := queryAll(t, r)
	assertSameKV(t, before, after)
	if len(after) != 15 {
		t.Fatalf("expected 15 surviving keys, got %d", len(after))
	}

	newHeads, _, err := r.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(newHeads) != 1 {
		t.Fatalf("expected 1 head after compaction, got %d", len(newHeads))
	}

	for c := range dagCIDSet {
		if _, err := r.dagService.Get(ctx, c); err == nil {
			t.Errorf("expected old block %s to have been removed from the DAG service", c)
		}
		processed, err := r.isProcessed(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if !processed {
			t.Errorf("expected the processed-block marker for purged block %s to be kept", c)
		}
	}
}

// TestCompactTwoReplicasInSync checks that a replica that was fully synced
// before Compact converges to the exact same single snapshot head, with
// identical state, once it processes it.
func TestCompactTwoReplicasInSync(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 2, nil)
	defer closeReplicas()
	w, other := replicas[0], replicas[1]
	ctx := context.Background()

	for i := range 10 {
		if err := w.Put(ctx, ds.NewKey(fmt.Sprintf("sync-key-%d", i)), fmt.Appendf(nil, "v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Delete(ctx, ds.NewKey("sync-key-0")); err != nil {
		t.Fatal(err)
	}

	syncReplicaHeads(t, w, other, "")
	assertSameKV(t, queryAll(t, w), queryAll(t, other))

	if _, err := w.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	writerStateAfter := queryAll(t, w)

	syncReplicaHeads(t, w, other, "")
	otherStateAfter := queryAll(t, other)

	assertSameKV(t, writerStateAfter, otherStateAfter)

	otherHeads, _, err := other.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	writerHeads, _, err := w.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(otherHeads) != 1 || len(writerHeads) != 1 {
		t.Fatalf("expected both replicas to have a single head, got writer=%d other=%d", len(writerHeads), len(otherHeads))
	}
	if otherHeads[0].Cid != writerHeads[0].Cid {
		t.Fatalf("expected both replicas to converge on the same snapshot head: %s vs %s", writerHeads[0].Cid, otherHeads[0].Cid)
	}
}

// TestCompactLaggingReplica checks G6's lagging-replica contract: a replica
// that only synced part of the writer's history before being cut off must,
// upon reconnecting (syncing only the post-compaction snapshot head(s)),
// still converge value-wise: keys deleted meanwhile stay deleted (via the
// carried tombstone), new keys/values are resupplied, and priorities match.
func TestCompactLaggingReplica(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 2, nil)
	defer closeReplicas()
	w, lag := replicas[0], replicas[1]
	ctx := context.Background()

	staysKey := ds.NewKey("stays")
	if err := w.Put(ctx, staysKey, []byte("v0")); err != nil {
		t.Fatal(err)
	}
	deletedKey := ds.NewKey("will-be-deleted")
	if err := w.Put(ctx, deletedKey, []byte("dying")); err != nil {
		t.Fatal(err)
	}

	// lag catches up to this point only, then gets "disconnected" (we
	// simply stop syncing it).
	syncReplicaHeads(t, w, lag, "")
	if has, err := lag.Has(ctx, deletedKey); err != nil || !has {
		t.Fatalf("expected lagging replica to have the not-yet-deleted key, has=%v err=%v", has, err)
	}

	// writer continues on its own, unseen by lag: delete a key, add a
	// new one, then compact.
	if err := w.Delete(ctx, deletedKey); err != nil {
		t.Fatal(err)
	}
	newKey := ds.NewKey("new-after-partition")
	if err := w.Put(ctx, newKey, []byte("fresh")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	// lag "reconnects": it only ever syncs the post-compaction snapshot
	// head(s) -- the purged history is gone from the DAG service, so
	// there is nothing else it could fetch even if it tried.
	syncReplicaHeads(t, w, lag, "")

	if has, err := lag.Has(ctx, deletedKey); err != nil || has {
		t.Fatalf("expected deleted key to be gone on the lagging replica, has=%v err=%v", has, err)
	}

	for _, k := range []ds.Key{staysKey, newKey} {
		wv, err := w.Get(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		lv, err := lag.Get(ctx, k)
		if err != nil {
			t.Fatalf("lagging replica missing key %s: %v", k, err)
		}
		if !bytes.Equal(wv, lv) {
			t.Fatalf("value mismatch for %s: writer=%q lag=%q", k, wv, lv)
		}

		wp, err := w.set.getPriority(ctx, k.String())
		if err != nil {
			t.Fatal(err)
		}
		lp, err := lag.set.getPriority(ctx, k.String())
		if err != nil {
			t.Fatal(err)
		}
		if wp != lp {
			t.Fatalf("priority mismatch for %s: writer=%d lag=%d", k, wp, lp)
		}
	}
}

// TestCompactFreshReplica checks G6's fresh-replica contract: a replica
// that joins after Compact and only ever syncs the snapshot head(s)
// reproduces the full live state, and never even attempts to touch the
// purged history (no processed markers for it).
func TestCompactFreshReplica(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 2, nil)
	defer closeReplicas()
	w, fresh := replicas[0], replicas[1]
	ctx := context.Background()

	for i := range 15 {
		if err := w.Put(ctx, ds.NewKey(fmt.Sprintf("fresh-key-%d", i)), fmt.Appendf(nil, "v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 5 {
		if err := w.Delete(ctx, ds.NewKey(fmt.Sprintf("fresh-key-%d", i))); err != nil {
			t.Fatal(err)
		}
	}

	oldHeads, _, err := w.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	purgedCIDSet, _, _, err := w.walkProcessedDAG(ctx, headCIDs(oldHeads))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	// fresh has never synced anything before this.
	syncReplicaHeads(t, w, fresh, "")

	writerState := queryAll(t, w)
	freshState := queryAll(t, fresh)
	assertSameKV(t, writerState, freshState)
	if len(freshState) != 10 {
		t.Fatalf("expected 10 surviving keys, got %d", len(freshState))
	}

	for c := range purgedCIDSet {
		processed, err := fresh.isProcessed(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if processed {
			t.Errorf("fresh replica should never have touched purged history block %s", c)
		}
	}
}

// TestCompactPriorityPreservation checks Item G3: an element's priority
// carried into the snapshot is its own original priority, not the
// snapshot's (much higher) height. A "concurrent-style" merge with a lower
// priority for the same key must therefore still lose against it, and one
// with a higher priority must still win.
func TestCompactPriorityPreservation(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	// Raise the DAG height a bit first so the winning write's priority
	// is comfortably greater than 1, leaving room for a "lower priority"
	// merge below.
	for i := range 5 {
		if err := r.Put(ctx, ds.NewKey(fmt.Sprintf("filler-%d", i)), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	k := ds.NewKey("prio-key")
	if err := r.Put(ctx, k, []byte("winner")); err != nil {
		t.Fatal(err)
	}
	winnerPrio, err := r.set.getPriority(ctx, k.String())
	if err != nil {
		t.Fatal(err)
	}
	if winnerPrio <= 1 {
		t.Fatalf("test setup issue: winnerPrio should be > 1, got %d", winnerPrio)
	}

	if _, err := r.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	gotVal, err := r.Get(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotVal) != "winner" {
		t.Fatalf("unexpected value after compact: %q", gotVal)
	}
	gotPrio, err := r.set.getPriority(ctx, k.String())
	if err != nil {
		t.Fatal(err)
	}
	if gotPrio != winnerPrio {
		t.Fatalf("expected priority to be preserved as the element's original %d after compaction (not inflated to the snapshot's height), got %d", winnerPrio, gotPrio)
	}

	// A lower-priority "concurrent" write for the same key must not win.
	loserDelta := r.newDelta()
	loserDelta.SetElements([]*pb.Element{{Key: k.String(), Value: []byte("loser")}})
	loserDelta.SetPriority(1)
	if err := r.set.Merge(ctx, loserDelta, "fake-loser-block-id"); err != nil {
		t.Fatal(err)
	}
	if v, err := r.Get(ctx, k); err != nil || string(v) != "winner" {
		t.Fatalf("lower-priority write should not have won: value=%q err=%v", v, err)
	}

	// A higher-priority "concurrent" write for the same key must win.
	higherDelta := r.newDelta()
	higherDelta.SetElements([]*pb.Element{{Key: k.String(), Value: []byte("higher-wins")}})
	higherDelta.SetPriority(winnerPrio + 100)
	if err := r.set.Merge(ctx, higherDelta, "fake-higher-block-id"); err != nil {
		t.Fatal(err)
	}
	if v, err := r.Get(ctx, k); err != nil || string(v) != "higher-wins" {
		t.Fatalf("higher-priority write should have won: value=%q err=%v", v, err)
	}
}

// TestCompactTwoGenerationTombstones checks the two-generation carry rule
// directly (spec G5 step 3): a tombstone is embedded in the snapshot delta
// that purges its target, but by the next compaction (with no intervening
// writes) it is inert -- the target's element marker is long gone -- and
// must be dropped from the second-generation snapshot.
//
// The local /t/ tombstone marker itself is a different story from round 3
// onward: it survives every generation's purge (aliased to the snapshot
// that carried it, exactly like a re-homed element marker -- see
// putTombs/S4), including the very generation that carries it, since its
// target is typically being purged in that same run. This is what closes a
// self-purge gap that would otherwise let a late-arriving, divergent write
// for the same (now long-purged) id resurrect the key locally on the
// compacting replica itself -- see compact.go's "Two-generation tombstone
// rule". Once the tombstone becomes inert (this test's second compaction),
// its marker's own path-id CID is never again reachable by any future
// walk, so it is never revisited or deleted either: a tiny, permanent,
// and harmless residue (same trade-off already accepted for the
// processed-block markers Compact keeps -- see the "Purged-block
// bookkeeping trade-off" section of the Compact doc comment).
func TestCompactTwoGenerationTombstones(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	k := ds.NewKey("doomed")
	if err := r.Put(ctx, k, []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}
	// an unrelated live key, so the walked DAG/setKeys are not trivial.
	alive := ds.NewKey("alive")
	if err := r.Put(ctx, alive, []byte("still here")); err != nil {
		t.Fatal(err)
	}

	if n, err := r.Compact(ctx, ""); err != nil || n == 0 {
		t.Fatalf("expected first compaction to purge blocks, n=%d err=%v", n, err)
	}

	heads1, _, err := r.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(heads1) != 1 {
		t.Fatalf("expected 1 head after first compaction, got %d", len(heads1))
	}
	if tombs1 := deltaTombstonesForKey(t, r, heads1[0].Cid, k.String()); len(tombs1) == 0 {
		t.Fatal("expected the first-generation snapshot to carry the tombstone")
	}
	// The local marker survives this same generation's purge, aliased to
	// the gen1 snapshot that just carried it (see this test's doc comment).
	if n := countPrefix(t, r, r.set.tombsPrefix(k.String())); n != 1 {
		t.Fatalf("expected 1 local tomb entry for %s after first compaction, got %d", k, n)
	}
	if aliases := tombAliasesForKey(t, r, k.String()); len(aliases) != 1 || !aliases[0].Equals(heads1[0].Cid) {
		t.Fatalf("expected the local tomb entry aliased to the gen1 snapshot %s, got %v", heads1[0].Cid, aliases)
	}

	n2, err := r.Compact(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 1 {
		t.Fatalf("expected second compaction to purge exactly the superseded first-generation snapshot block, got %d", n2)
	}

	heads2, _, err := r.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(heads2) != 1 {
		t.Fatalf("expected 1 head after second compaction, got %d", len(heads2))
	}
	if tombs2 := deltaTombstonesForKey(t, r, heads2[0].Cid, k.String()); len(tombs2) != 0 {
		t.Fatalf("expected the inert tombstone to be dropped from the second-generation snapshot delta, got %v", tombs2)
	}
	// The marker from the first generation is now permanently inert
	// residue (see this test's doc comment): its own path-id CID is
	// unreachable from any future walk, so it is never revisited, and it
	// carries no more weight since the target element marker is long
	// gone. It is harmless and still there, exactly one entry.
	if n := countPrefix(t, r, r.set.tombsPrefix(k.String())); n != 1 {
		t.Fatalf("expected 1 (permanently inert) local tomb entry for %s after second compaction, got %d", k, n)
	}

	if has, err := r.Has(ctx, k); err != nil || has {
		t.Fatalf("doomed key should still be gone, has=%v err=%v", has, err)
	}
	if v, err := r.Get(ctx, alive); err != nil || string(v) != "still here" {
		t.Fatalf("unrelated live key should survive two compactions unchanged: value=%q err=%v", v, err)
	}
}

// TestCompactSiblingSplit checks that when the live state is too big for a
// single snapshot delta (MaxBatchDeltaSize), Compact greedily splits it
// across sibling snapshot heads that all become heads, and that a replica
// syncing all of them converges to the full state.
func TestCompactSiblingSplit(t *testing.T) {
	opts := DefaultOptions()
	opts.MaxBatchDeltaSize = 200 // bytes: small enough to force several siblings.
	replicas, closeReplicas := makeNReplicasNoBcast(t, 2, opts)
	defer closeReplicas()
	w, other := replicas[0], replicas[1]
	ctx := context.Background()

	const numKeys = 40
	for i := range numKeys {
		k := ds.NewKey(fmt.Sprintf("split-key-%d", i))
		v := fmt.Appendf(nil, "value-%03d-xxxxxxxxxxxxxxxxxxxx", i)
		if err := w.Put(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}

	n, err := w.Compact(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected purged blocks")
	}

	heads, _, err := w.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(heads) < 2 {
		t.Fatalf("expected multiple sibling snapshot heads with a small MaxBatchDeltaSize, got %d", len(heads))
	}

	ng := &crdtNodeGetter{NodeGetter: w.dagService}
	for _, h := range heads {
		_, deltaBytes, err := ng.GetDelta(ctx, h.Cid)
		if err != nil {
			t.Fatal(err)
		}
		delta := w.newDelta()
		if err := delta.Unmarshal(deltaBytes); err != nil {
			t.Fatal(err)
		}
		if !delta.IsSnapshot() {
			t.Errorf("expected head %s to be a snapshot delta", h.Cid)
		}
	}

	syncReplicaHeads(t, w, other, "")

	writerState := queryAll(t, w)
	otherState := queryAll(t, other)
	assertSameKV(t, writerState, otherState)
	if len(writerState) != numKeys {
		t.Fatalf("expected %d keys, got %d", numKeys, len(writerState))
	}
}

// TestCompactEmptyDagName checks that compacting a dagName with no heads is
// a no-op returning (0, nil).
func TestCompactEmptyDagName(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	n, err := r.Compact(ctx, "nonexistent-dag")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 purged blocks for a dagName with no heads, got %d", n)
	}
}

// TestCompactOtherDagNamesUntouched checks that compacting one dagName does
// not affect another dagName's heads or set state, even though they share
// the same underlying elems/tombs/keys namespaces.
func TestCompactOtherDagNamesUntouched(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	// Keys are ds.Key.String()-formatted (leading "/"), exactly as every
	// call through the public Datastore.Put/Delete/Batch API produces --
	// see compactSnapshotState's doc comment for why this convention
	// matters to Compact specifically.
	dag1Key := ds.NewKey("dag1-key").String()
	dag2Key := ds.NewKey("dag2-key").String()

	delta1, err := r.set.Add(ctx, dag1Key, []byte("v1"))
	if err != nil {
		t.Fatal(err)
	}
	delta1.SetDagName("dag1")
	if _, err := r.publish(ctx, delta1); err != nil {
		t.Fatal(err)
	}

	delta2, err := r.set.Add(ctx, dag2Key, []byte("v2"))
	if err != nil {
		t.Fatal(err)
	}
	delta2.SetDagName("dag2")
	if _, err := r.publish(ctx, delta2); err != nil {
		t.Fatal(err)
	}

	dag2HeadsBefore, _, err := r.heads.ListDAG(ctx, "dag2")
	if err != nil {
		t.Fatal(err)
	}

	n, err := r.Compact(ctx, "dag1")
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected dag1 compaction to purge something")
	}

	dag2HeadsAfter, _, err := r.heads.ListDAG(ctx, "dag2")
	if err != nil {
		t.Fatal(err)
	}
	if len(dag2HeadsAfter) != 1 || len(dag2HeadsBefore) != 1 || dag2HeadsAfter[0].Cid != dag2HeadsBefore[0].Cid {
		t.Fatalf("expected dag2 heads to be untouched by compacting dag1: before=%v after=%v", dag2HeadsBefore, dag2HeadsAfter)
	}
	val2, err := r.set.Element(ctx, dag2Key)
	if err != nil {
		t.Fatal(err)
	}
	if string(val2) != "v2" {
		t.Fatalf("dag2 value should be untouched, got %q", val2)
	}

	dag1Heads, _, err := r.heads.ListDAG(ctx, "dag1")
	if err != nil {
		t.Fatal(err)
	}
	if len(dag1Heads) != 1 {
		t.Fatalf("expected 1 dag1 head after compaction, got %d", len(dag1Heads))
	}
	val1, err := r.set.Element(ctx, dag1Key)
	if err != nil {
		t.Fatal(err)
	}
	if string(val1) != "v1" {
		t.Fatalf("dag1 value should survive compaction unchanged, got %q", val1)
	}
}

// TestCompactRepairDAG checks that repairDAG completes and marks the store
// clean when a dirty head happens to be a (already-processed) snapshot
// node: it must reprocess the snapshot node itself without trying to queue
// or fetch its now-purged, unfetchable covered-head links (G4).
func TestCompactRepairDAG(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	for i := range 10 {
		if err := r.Put(ctx, ds.NewKey(fmt.Sprintf("repair-key-%d", i)), fmt.Appendf(nil, "v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Delete(ctx, ds.NewKey("repair-key-0")); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	heads, _, err := r.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(heads) != 1 {
		t.Fatalf("expected a single snapshot head, got %d", len(heads))
	}

	// Force the snapshot head to look unprocessed so repairDAG actually
	// re-walks it instead of trivially skipping an already-processed
	// head -- this is what exercises the "do not queue a snapshot's
	// links" branch: those links point at now-purged, unfetchable
	// blocks, so queuing them would make repairDAG fail forever.
	if err := r.store.Delete(ctx, r.processedBlockKey(heads[0].Cid)); err != nil {
		t.Fatal(err)
	}

	r.MarkDirty(ctx)
	if err := r.Repair(ctx); err != nil {
		t.Fatalf("repair over a compacted store failed: %v", err)
	}
	if r.IsDirty(ctx) {
		t.Fatal("expected the store to be marked clean after repair")
	}

	processed, err := r.isProcessed(ctx, heads[0].Cid)
	if err != nil {
		t.Fatal(err)
	}
	if !processed {
		t.Fatal("expected the snapshot head to be marked processed again after repair")
	}

	if has, err := r.Has(ctx, ds.NewKey("repair-key-0")); err != nil || has {
		t.Fatalf("expected repair-key-0 to remain deleted, has=%v err=%v", has, err)
	}
	if v, err := r.Get(ctx, ds.NewKey("repair-key-1")); err != nil || string(v) != "v1" {
		t.Fatalf("unexpected state after repair: value=%q err=%v", v, err)
	}
}

// BenchmarkCompact measures Compact() over a store with 2000 keys, 50% of
// them deleted. Each iteration gets a fresh replica (rather than reusing one
// across iterations): Compact folds a DAG's *entire* reachable history --
// including any earlier snapshot(s) -- into the new one, so reusing a
// replica would make every successive iteration's Compact call walk and
// re-fold an ever-growing amount of already-compacted history on top of the
// new 2000 keys, which is a real and correctly-handled property of repeated
// compaction (see TestCompactTwoGenerationTombstones) but would confound
// this benchmark's per-call measurement.
func BenchmarkCompact(b *testing.B) {
	const numKeys = 2000
	ctx := context.Background()

	for range b.N {
		b.StopTimer()
		replicas, closeReplicas := makeNReplicasNoBcast(b, 1, nil)
		r := replicas[0]

		crdtBatch, err := r.Batch(ctx)
		if err != nil {
			b.Fatal(err)
		}
		for k := range numKeys {
			key := ds.NewKey(fmt.Sprintf("bench-compact-%d", k))
			if err := crdtBatch.Put(ctx, key, []byte("value")); err != nil {
				b.Fatal(err)
			}
		}
		if err := crdtBatch.Commit(ctx); err != nil {
			b.Fatal(err)
		}

		delBatch, err := r.Batch(ctx)
		if err != nil {
			b.Fatal(err)
		}
		for k := 0; k < numKeys; k += 2 {
			key := ds.NewKey(fmt.Sprintf("bench-compact-%d", k))
			if err := delBatch.Delete(ctx, key); err != nil {
				b.Fatal(err)
			}
		}
		if err := delBatch.Commit(ctx); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		if _, err := r.Compact(ctx, ""); err != nil {
			b.Fatal(err)
		}

		b.StopTimer()
		closeReplicas()
		b.StartTimer()
	}
}
